package migration

import (
	"crypto/sha256"
	"strings"
	"testing"
)

func TestStatusPrecedence(t *testing.T) {
	t.Parallel()

	expectedFingerprint := sha256.Sum256([]byte("expected"))
	liveFingerprint := sha256.Sum256([]byte("live"))
	tests := []struct {
		name                string
		ledgerPresent       bool
		checksumMismatch    bool
		appliedVersion      int64
		expectedVersion     int64
		liveFingerprint     [sha256.Size]byte
		expectedFingerprint [sha256.Size]byte
		want                State
	}{
		{
			name:                "absent precedes every other condition",
			checksumMismatch:    true,
			appliedVersion:      1,
			expectedVersion:     2,
			liveFingerprint:     liveFingerprint,
			expectedFingerprint: expectedFingerprint,
			want:                StateAbsent,
		},
		{
			name:                "checksum mismatch precedes pending and drift",
			ledgerPresent:       true,
			checksumMismatch:    true,
			appliedVersion:      1,
			expectedVersion:     2,
			liveFingerprint:     liveFingerprint,
			expectedFingerprint: expectedFingerprint,
			want:                StateChecksumMismatch,
		},
		{
			name:                "pending precedes drift",
			ledgerPresent:       true,
			appliedVersion:      1,
			expectedVersion:     2,
			liveFingerprint:     liveFingerprint,
			expectedFingerprint: expectedFingerprint,
			want:                StatePending,
		},
		{
			name:                "drift follows complete matching ledger",
			ledgerPresent:       true,
			appliedVersion:      2,
			expectedVersion:     2,
			liveFingerprint:     liveFingerprint,
			expectedFingerprint: expectedFingerprint,
			want:                StateSchemaDrift,
		},
		{
			name:                "current requires complete matching ledger and schema",
			ledgerPresent:       true,
			appliedVersion:      2,
			expectedVersion:     2,
			liveFingerprint:     expectedFingerprint,
			expectedFingerprint: expectedFingerprint,
			want:                StateCurrent,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := classifyStatus(
				test.ledgerPresent,
				test.checksumMismatch,
				test.appliedVersion,
				test.expectedVersion,
				test.liveFingerprint,
				test.expectedFingerprint,
			)
			if got != test.want {
				t.Fatalf("classifyStatus() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestInspectorRejectsInvalidConfigurationBeforeConnecting(t *testing.T) {
	t.Parallel()

	manifest := validManifest("core", "core_version")
	manifest.ExpectedFingerprint = sha256.Sum256([]byte("core"))
	tests := []struct {
		name      string
		inspector Inspector
	}{
		{
			name: "blank application URL",
			inspector: Inspector{
				Manifests:  []Manifest{manifest},
				Configured: []Scope{"core"},
			},
		},
		{
			name: "unknown configured scope",
			inspector: Inspector{
				DatabaseURL: "postgres://unreachable.invalid/example",
				Manifests:   []Manifest{manifest},
				Configured:  []Scope{"other"},
			},
		},
		{
			name: "duplicate configured scope",
			inspector: Inspector{
				DatabaseURL: "postgres://unreachable.invalid/example",
				Manifests:   []Manifest{manifest},
				Configured:  []Scope{"core", "core"},
			},
		},
		{
			name: "missing configured core scope",
			inspector: Inspector{
				DatabaseURL: "postgres://unreachable.invalid/example",
				Manifests:   []Manifest{manifest},
				Configured:  nil,
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := test.inspector.Status(t.Context()); err == nil {
				t.Fatal("Inspector.Status() error = nil, want configuration rejection")
			}
		})
	}
}

func TestInspectorRejectsMissingCoreScopeBeforeConnecting(t *testing.T) {
	t.Parallel()

	core := coreManifestForSet()
	_, err := (Inspector{
		DatabaseURL: "postgres://admin:synthetic-secret@%zz/temporary",
		Manifests:   []Manifest{core},
		Configured:  nil,
	}).Status(t.Context())
	if err == nil || !strings.Contains(err.Error(), "configured migration scope core") {
		t.Fatalf("Inspector.Status() error = %v, want missing core scope before URL parsing", err)
	}
	if strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatalf("Inspector.Status() error exposed database credentials: %q", err)
	}
}

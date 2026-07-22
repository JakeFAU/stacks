package storage

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	analysisdomain "stacks/internal/analysis"
	"stacks/internal/modelpolicy"
)

func TestComputeAnalysisDigestIncludesPairVersionsAndOrderedInputs(t *testing.T) {
	firstDigest := sha256.Sum256([]byte("first-input"))
	secondDigest := sha256.Sum256([]byte("second-input"))
	base := AnalysisInput{
		EmployeeEntityID:      "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ManagerEntityID:       "11111111-2222-3333-4444-555555555555",
		AnalysisPromptVersion: "analyze-v1",
		PolicyVersion:         "policy-v1",
		Inputs: []AnalysisInputReference{
			{Kind: AnalysisInputKindSignal, ID: "00000000-0000-0000-0000-000000000003", Digest: firstDigest[:]},
			{Kind: AnalysisInputKindSignal, ID: "00000000-0000-0000-0000-000000000004", Digest: secondDigest[:]},
		},
	}

	baseline, err := ComputeAnalysisDigest(base)
	if err != nil {
		t.Fatalf("compute baseline digest: %v", err)
	}

	tests := []struct {
		name  string
		input AnalysisInput
	}{
		{
			name: "employee changes",
			input: func() AnalysisInput {
				changed := base
				changed.EmployeeEntityID = "00000000-0000-0000-0000-000000000005"
				return changed
			}(),
		},
		{
			name: "prompt version changes",
			input: func() AnalysisInput {
				changed := base
				changed.AnalysisPromptVersion = "analyze-v2"
				return changed
			}(),
		},
		{
			name: "input order changes",
			input: func() AnalysisInput {
				changed := base
				changed.Inputs = []AnalysisInputReference{base.Inputs[1], base.Inputs[0]}
				return changed
			}(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ComputeAnalysisDigest(test.input)
			if err != nil {
				t.Fatalf("compute changed digest: %v", err)
			}
			if got == baseline {
				t.Fatal("changed analysis identity produced the baseline digest")
			}
		})
	}
}

func TestComputeAnalysisDigestCanonicalizesUUIDs(t *testing.T) {
	digest := sha256.Sum256([]byte("input"))
	canonical := AnalysisInput{
		EmployeeEntityID:      "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ManagerEntityID:       "11111111-2222-3333-4444-555555555555",
		AnalysisPromptVersion: "analyze-v1",
		PolicyVersion:         "policy-v1",
		Inputs: []AnalysisInputReference{{
			Kind:   AnalysisInputKindSignal,
			ID:     "99999999-aaaa-bbbb-cccc-dddddddddddd",
			Digest: digest[:],
		}},
	}
	variant := canonical
	variant.EmployeeEntityID = "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE"
	variant.Inputs = append([]AnalysisInputReference(nil), canonical.Inputs...)
	variant.Inputs[0].ID = "99999999-AAAA-BBBB-CCCC-DDDDDDDDDDDD"

	canonicalDigest, err := ComputeAnalysisDigest(canonical)
	if err != nil {
		t.Fatalf("compute canonical digest: %v", err)
	}
	variantDigest, err := ComputeAnalysisDigest(variant)
	if err != nil {
		t.Fatalf("compute variant digest: %v", err)
	}
	if canonicalDigest != variantDigest {
		t.Fatal("equivalent UUID spellings produced distinct analysis digests")
	}
}

func TestComputeAnalysisDigestRejectsInvalidUUID(t *testing.T) {
	digest := sha256.Sum256([]byte("input"))
	_, err := ComputeAnalysisDigest(AnalysisInput{
		EmployeeEntityID:      "not-a-uuid",
		ManagerEntityID:       "11111111-2222-3333-4444-555555555555",
		AnalysisPromptVersion: "analyze-v1",
		PolicyVersion:         "policy-v1",
		Inputs: []AnalysisInputReference{{
			Kind:   AnalysisInputKindSignal,
			ID:     "99999999-aaaa-bbbb-cccc-dddddddddddd",
			Digest: digest[:],
		}},
	})
	if err == nil {
		t.Fatal("invalid employee UUID was accepted")
	}
}

func TestLegacyAndTemporalAnalysisDigestsUseSeparateNamespaces(t *testing.T) {
	inputDigest := sha256.Sum256([]byte("shared-input"))
	legacy, err := ComputeAnalysisDigest(AnalysisInput{
		EmployeeEntityID:      "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ManagerEntityID:       "11111111-2222-3333-4444-555555555555",
		AnalysisPromptVersion: "analyze-v1",
		PolicyVersion:         "policy-v1",
		Inputs: []AnalysisInputReference{{
			Kind: AnalysisInputKindSignal, ID: "99999999-aaaa-bbbb-cccc-dddddddddddd", Digest: inputDigest[:],
		}},
	})
	if err != nil {
		t.Fatalf("ComputeAnalysisDigest() error = %v", err)
	}
	temporal, err := analysisdomain.ComputeInputDigest(analysisdomain.AnalysisIdentity{
		EmployeeEntityID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ManagerEntityID:  "11111111-2222-3333-4444-555555555555",
		PromptVersion:    "analyze-v1",
		PolicyVersion:    "policy-v1",
		Provider:         modelpolicy.ProviderBedrock,
		Region:           "us-east-1",
		ModelID:          "synthetic-model",
		MaxTokens:        256,
		Inputs: []analysisdomain.InputReference{{
			Kind: analysisdomain.InputSignal, ID: "99999999-aaaa-bbbb-cccc-dddddddddddd", Digest: inputDigest,
		}},
	})
	if err != nil {
		t.Fatalf("analysis.ComputeInputDigest() error = %v", err)
	}
	if legacy == temporal {
		t.Fatal("legacy completion can occupy the temporal report digest namespace")
	}
}

func TestMeetingInputReferenceUsesCanonicalSourceDocumentIdentity(t *testing.T) {
	canonical, err := meetingInputReference("99999999-aaaa-bbbb-cccc-dddddddddddd")
	if err != nil {
		t.Fatalf("meetingInputReference() error = %v", err)
	}
	variant, err := meetingInputReference("99999999-AAAA-BBBB-CCCC-DDDDDDDDDDDD")
	if err != nil {
		t.Fatalf("variant meetingInputReference() error = %v", err)
	}
	other, err := meetingInputReference("88888888-aaaa-bbbb-cccc-dddddddddddd")
	if err != nil {
		t.Fatalf("other meetingInputReference() error = %v", err)
	}
	if canonical.Kind != analysisdomain.InputSourceDocument || canonical.ID != "99999999-aaaa-bbbb-cccc-dddddddddddd" {
		t.Fatalf("meeting input = %#v, want canonical source-document reference", canonical)
	}
	if canonical != variant {
		t.Fatal("equivalent source-document UUID spellings produced different meeting inputs")
	}
	if canonical.Digest == other.Digest {
		t.Fatal("different source documents produced the same meeting digest")
	}
}

func TestPairIdentitySnapshotRequiresEffectiveDecisionForEachConfiguredEntity(t *testing.T) {
	employeeID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	managerID := "11111111-2222-3333-4444-555555555555"
	employeeDigest := sha256.Sum256([]byte("employee-decision"))
	managerDigest := sha256.Sum256([]byte("manager-decision"))
	employeeDecision := effectivePairDecision{
		EntityID: employeeID,
		Input: analysisdomain.InputReference{
			Kind: analysisdomain.InputResolutionDecision, ID: "00000000-0000-0000-0000-000000000001", Digest: employeeDigest,
		},
	}
	managerDecision := effectivePairDecision{
		EntityID: managerID,
		Input: analysisdomain.InputReference{
			Kind: analysisdomain.InputResolutionDecision, ID: "00000000-0000-0000-0000-000000000002", Digest: managerDigest,
		},
	}

	for _, test := range []struct {
		name      string
		decisions []effectivePairDecision
		accepted  bool
		inputs    int
	}{
		{name: "pending pair", decisions: nil, accepted: false},
		{name: "only employee accepted", decisions: []effectivePairDecision{employeeDecision}, accepted: false},
		{name: "both identities accepted", decisions: []effectivePairDecision{employeeDecision, managerDecision}, accepted: true, inputs: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot, err := pairIdentitySnapshot(employeeID, managerID, test.decisions)
			if err != nil {
				t.Fatalf("pairIdentitySnapshot() error = %v", err)
			}
			if snapshot.Accepted != test.accepted || len(snapshot.Inputs) != test.inputs {
				t.Fatalf("snapshot = %#v, want accepted=%t inputs=%d", snapshot, test.accepted, test.inputs)
			}
		})
	}
}

func TestLoadPairInputsUsesOneRepeatableReadOnlySnapshot(t *testing.T) {
	transaction := &fakeAnalysisSnapshot{
		identity: analysisdomain.PairSnapshot{Accepted: true},
		signals: analysisdomain.PairSnapshot{
			Accepted: true,
			Signals:  []analysisdomain.Signal{{ID: "signal-1"}},
		},
	}
	var options pgx.TxOptions
	repository := &AnalysisRepository{
		beginSnapshot: func(_ context.Context, got pgx.TxOptions) (analysisSnapshot, error) {
			options = got
			return transaction, nil
		},
	}

	snapshot, err := repository.LoadPairInputs(context.Background(),
		"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		"11111111-2222-3333-4444-555555555555",
	)
	if err != nil {
		t.Fatalf("LoadPairInputs() error = %v", err)
	}
	if options.IsoLevel != pgx.RepeatableRead || options.AccessMode != pgx.ReadOnly {
		t.Fatalf("transaction options = %#v, want repeatable-read read-only", options)
	}
	if !slices.Equal(transaction.calls, []string{"identity", "signals", "commit", "rollback"}) {
		t.Fatalf("snapshot calls = %#v, want one coherent read transaction", transaction.calls)
	}
	if len(snapshot.Signals) != 1 || snapshot.Signals[0].ID != "signal-1" {
		t.Fatalf("snapshot = %#v, want signal result from the same transaction", snapshot)
	}
}

func TestLoadPairInputsDoesNotReadSignalsForPendingPair(t *testing.T) {
	transaction := &fakeAnalysisSnapshot{identity: analysisdomain.PairSnapshot{Accepted: false}}
	repository := &AnalysisRepository{
		beginSnapshot: func(context.Context, pgx.TxOptions) (analysisSnapshot, error) {
			return transaction, nil
		},
	}

	snapshot, err := repository.LoadPairInputs(context.Background(),
		"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		"11111111-2222-3333-4444-555555555555",
	)
	if err != nil {
		t.Fatalf("LoadPairInputs() error = %v", err)
	}
	if snapshot.Accepted || !slices.Equal(transaction.calls, []string{"identity", "commit", "rollback"}) {
		t.Fatalf("pending snapshot/calls = %#v/%#v", snapshot, transaction.calls)
	}
}

func TestLoadPairInputsRollsBackSnapshotWhenSignalReadFails(t *testing.T) {
	transaction := &fakeAnalysisSnapshot{
		identity:  analysisdomain.PairSnapshot{Accepted: true},
		signalErr: errors.New("synthetic query failure with private source material"),
	}
	repository := &AnalysisRepository{
		beginSnapshot: func(context.Context, pgx.TxOptions) (analysisSnapshot, error) {
			return transaction, nil
		},
	}

	_, err := repository.LoadPairInputs(context.Background(),
		"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		"11111111-2222-3333-4444-555555555555",
	)
	if err == nil || !slices.Equal(transaction.calls, []string{"identity", "signals", "rollback"}) {
		t.Fatalf("LoadPairInputs() error/calls = %v/%#v, want bounded failure and rollback", err, transaction.calls)
	}
	if strings.Contains(err.Error(), "private source material") {
		t.Fatalf("LoadPairInputs() error disclosed underlying private text: %v", err)
	}
}

func TestFindCompletedValidatesIdentityAndReadsCacheInOneTransaction(t *testing.T) {
	identity := testCurrentAnalysisIdentity(t)
	transaction := &fakeCompletedAnalysisLookup{
		report: analysisdomain.Report{ID: "cached-run", InputDigest: identity.InputDigest},
		found:  true,
	}
	repository := &AnalysisRepository{
		beginCompletedLookup: func(context.Context) (completedAnalysisLookup, error) {
			return transaction, nil
		},
	}

	report, found, err := repository.FindCompleted(context.Background(), identity)
	if err != nil {
		t.Fatalf("FindCompleted() error = %v", err)
	}
	if !found || report.ID != "cached-run" {
		t.Fatalf("FindCompleted() = (%#v, %t), want cached report", report, found)
	}
	if !slices.Equal(transaction.calls, []string{"validate", "find", "commit", "rollback"}) {
		t.Fatalf("cache transaction calls = %#v, want validation and lookup in one transaction", transaction.calls)
	}
	if transaction.identity.InputDigest != identity.InputDigest ||
		!slices.Equal(referenceIDsForStorageTest(transaction.identity.Inputs), referenceIDsForStorageTest(identity.Inputs)) {
		t.Fatalf("validated cache identity = %#v, want complete snapshot identity", transaction.identity)
	}
}

func TestFindCompletedReturnsStaleBeforeHistoricalCacheLookup(t *testing.T) {
	identity := testCurrentAnalysisIdentity(t)
	transaction := &fakeCompletedAnalysisLookup{validateErr: analysisdomain.ErrStaleAnalysisInput}
	repository := &AnalysisRepository{
		beginCompletedLookup: func(context.Context) (completedAnalysisLookup, error) {
			return transaction, nil
		},
	}

	_, found, err := repository.FindCompleted(context.Background(), identity)
	if !errors.Is(err, analysisdomain.ErrStaleAnalysisInput) {
		t.Fatalf("FindCompleted() error = %v, want retryable stale-input result", err)
	}
	if found || !slices.Equal(transaction.calls, []string{"validate", "rollback"}) {
		t.Fatalf("FindCompleted() found/calls = %t/%#v, want stale result before cache lookup", found, transaction.calls)
	}
}

func TestValidateEffectivePairDecisionsRejectsCorrectedInput(t *testing.T) {
	digest := sha256.Sum256([]byte("superseded-decision"))
	queryer := fakeDecisionQueryer{rows: map[string]fakeDecisionRecord{}}
	identity := analysisdomain.AnalysisIdentity{
		EmployeeEntityID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ManagerEntityID:  "11111111-2222-3333-4444-555555555555",
		Inputs: []analysisdomain.InputReference{{
			Kind: analysisdomain.InputResolutionDecision,
			ID:   "00000000-0000-0000-0000-000000000001", Digest: digest,
		}},
	}

	err := validateEffectivePairDecisions(context.Background(), &queryer, identity)
	if !errors.Is(err, analysisdomain.ErrStaleAnalysisInput) {
		t.Fatalf("validateEffectivePairDecisions() error = %v, want stale-input error", err)
	}
	if strings.Contains(err.Error(), identity.Inputs[0].ID) {
		t.Fatalf("stale-input error disclosed decision ID: %v", err)
	}
}

func TestValidateEffectivePairDecisionsRejectsDecisionOutsideConfiguredPair(t *testing.T) {
	digest := sha256.Sum256([]byte("other-entity-decision"))
	decisionID := "00000000-0000-0000-0000-000000000001"
	queryer := fakeDecisionQueryer{rows: map[string]fakeDecisionRecord{
		decisionID: {digest: digest[:], entityID: "99999999-aaaa-bbbb-cccc-dddddddddddd"},
	}}
	identity := analysisdomain.AnalysisIdentity{
		EmployeeEntityID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ManagerEntityID:  "11111111-2222-3333-4444-555555555555",
		Inputs: []analysisdomain.InputReference{{
			Kind: analysisdomain.InputResolutionDecision, ID: decisionID, Digest: digest,
		}},
	}

	err := validateEffectivePairDecisions(context.Background(), &queryer, identity)
	if !errors.Is(err, analysisdomain.ErrStaleAnalysisInput) {
		t.Fatalf("validateEffectivePairDecisions() error = %v, want stale-input error", err)
	}
}

func TestValidateEffectivePairDecisionsRequiresCurrentDecisionForBothEntities(t *testing.T) {
	employeeDigest := sha256.Sum256([]byte("employee-decision"))
	employeeDecisionID := "00000000-0000-0000-0000-000000000001"
	queryer := fakeDecisionQueryer{rows: map[string]fakeDecisionRecord{
		employeeDecisionID: {digest: employeeDigest[:], entityID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
	}}
	identity := analysisdomain.AnalysisIdentity{
		EmployeeEntityID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ManagerEntityID:  "11111111-2222-3333-4444-555555555555",
		Inputs: []analysisdomain.InputReference{{
			Kind: analysisdomain.InputResolutionDecision, ID: employeeDecisionID, Digest: employeeDigest,
		}},
	}

	err := validateEffectivePairDecisions(context.Background(), &queryer, identity)
	if !errors.Is(err, analysisdomain.ErrStaleAnalysisInput) {
		t.Fatalf("validateEffectivePairDecisions() error = %v, want missing-manager stale-input error", err)
	}
}

func TestValidateEffectivePairDecisionsLocksAndAcceptsCurrentPair(t *testing.T) {
	employeeDigest := sha256.Sum256([]byte("employee-decision"))
	managerDigest := sha256.Sum256([]byte("manager-decision"))
	employeeDecisionID := "00000000-0000-0000-0000-000000000001"
	managerDecisionID := "00000000-0000-0000-0000-000000000002"
	queryer := &fakeDecisionQueryer{rows: map[string]fakeDecisionRecord{
		employeeDecisionID: {digest: employeeDigest[:], entityID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		managerDecisionID:  {digest: managerDigest[:], entityID: "11111111-2222-3333-4444-555555555555"},
	}}
	identity := analysisdomain.AnalysisIdentity{
		EmployeeEntityID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ManagerEntityID:  "11111111-2222-3333-4444-555555555555",
		Inputs: []analysisdomain.InputReference{
			{Kind: analysisdomain.InputResolutionDecision, ID: employeeDecisionID, Digest: employeeDigest},
			{Kind: analysisdomain.InputResolutionDecision, ID: managerDecisionID, Digest: managerDigest},
		},
	}

	if err := validateEffectivePairDecisions(context.Background(), queryer, identity); err != nil {
		t.Fatalf("validateEffectivePairDecisions() error = %v", err)
	}
	if len(queryer.queries) != 2 {
		t.Fatalf("decision validation queries = %d, want 2", len(queryer.queries))
	}
	for _, query := range queryer.queries {
		if !strings.Contains(query, "superseded_by_id IS NULL") ||
			!strings.Contains(query, "outcome IN ('accepted', 'created')") ||
			!strings.Contains(query, "decision.currently_admissible") ||
			!strings.Contains(query, "mention.currently_admissible") ||
			!strings.Contains(query, "extraction_run.currently_admissible") ||
			!strings.Contains(query, "FOR SHARE") {
			t.Fatalf("decision validation query does not filter and lock current accepted input: %s", query)
		}
	}
}

type fakeAnalysisSnapshot struct {
	identity  analysisdomain.PairSnapshot
	signals   analysisdomain.PairSnapshot
	signalErr error
	calls     []string
}

type fakeCompletedAnalysisLookup struct {
	identity    analysisdomain.AnalysisIdentity
	report      analysisdomain.Report
	found       bool
	validateErr error
	calls       []string
}

type fakeDecisionRecord struct {
	digest   []byte
	entityID string
}

type fakeDecisionQueryer struct {
	rows    map[string]fakeDecisionRecord
	queries []string
}

func (queryer *fakeDecisionQueryer) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	queryer.queries = append(queryer.queries, query)
	record, exists := queryer.rows[args[0].(string)]
	return fakeDecisionRow{record: record, exists: exists}
}

type fakeDecisionRow struct {
	record fakeDecisionRecord
	exists bool
}

func (row fakeDecisionRow) Scan(destinations ...any) error {
	if !row.exists {
		return pgx.ErrNoRows
	}
	*destinations[0].(*[]byte) = append([]byte(nil), row.record.digest...)
	*destinations[1].(*string) = row.record.entityID
	return nil
}

func (snapshot *fakeAnalysisSnapshot) LoadPairIdentity(context.Context, string, string) (analysisdomain.PairSnapshot, error) {
	snapshot.calls = append(snapshot.calls, "identity")
	return snapshot.identity, nil
}

func (snapshot *fakeAnalysisSnapshot) LoadPairSignals(context.Context, string, string, analysisdomain.PairSnapshot) (analysisdomain.PairSnapshot, error) {
	snapshot.calls = append(snapshot.calls, "signals")
	return snapshot.signals, snapshot.signalErr
}

func (snapshot *fakeAnalysisSnapshot) Commit(context.Context) error {
	snapshot.calls = append(snapshot.calls, "commit")
	return nil
}

func (snapshot *fakeAnalysisSnapshot) Rollback(context.Context) error {
	snapshot.calls = append(snapshot.calls, "rollback")
	return nil
}

func (lookup *fakeCompletedAnalysisLookup) ValidateEffectivePairDecisions(_ context.Context, identity analysisdomain.AnalysisIdentity) error {
	lookup.calls = append(lookup.calls, "validate")
	lookup.identity = identity
	lookup.identity.Inputs = append([]analysisdomain.InputReference(nil), identity.Inputs...)
	return lookup.validateErr
}

func (lookup *fakeCompletedAnalysisLookup) FindCompleted(context.Context, [sha256.Size]byte) (analysisdomain.Report, bool, error) {
	lookup.calls = append(lookup.calls, "find")
	return lookup.report, lookup.found, nil
}

func (lookup *fakeCompletedAnalysisLookup) Commit(context.Context) error {
	lookup.calls = append(lookup.calls, "commit")
	return nil
}

func (lookup *fakeCompletedAnalysisLookup) Rollback(context.Context) error {
	lookup.calls = append(lookup.calls, "rollback")
	return nil
}

func testCurrentAnalysisIdentity(t *testing.T) analysisdomain.AnalysisIdentity {
	t.Helper()
	employeeDigest := sha256.Sum256([]byte("employee-decision"))
	managerDigest := sha256.Sum256([]byte("manager-decision"))
	identity := analysisdomain.AnalysisIdentity{
		EmployeeEntityID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ManagerEntityID:  "11111111-2222-3333-4444-555555555555",
		PromptVersion:    "analyze-test-v1",
		PolicyVersion:    "policy-test-v1",
		Provider:         modelpolicy.ProviderBedrock,
		Region:           "us-east-1",
		ModelID:          "synthetic-model",
		MaxTokens:        256,
		Inputs: []analysisdomain.InputReference{
			{Kind: analysisdomain.InputResolutionDecision, ID: "00000000-0000-0000-0000-000000000001", Digest: employeeDigest},
			{Kind: analysisdomain.InputResolutionDecision, ID: "00000000-0000-0000-0000-000000000002", Digest: managerDigest},
		},
	}
	var err error
	identity.InputDigest, err = analysisdomain.ComputeInputDigest(identity)
	if err != nil {
		t.Fatalf("ComputeInputDigest() error = %v", err)
	}
	return identity
}

func referenceIDsForStorageTest(inputs []analysisdomain.InputReference) []string {
	ids := make([]string, 0, len(inputs))
	for _, input := range inputs {
		ids = append(ids, input.ID)
	}
	return ids
}

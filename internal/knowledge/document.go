package knowledge

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
	"strings"
	"time"
	"unicode/utf8"

	"stacks/internal/source"
)

// DocumentVersionInput contains source metadata and tab content for one
// immutable source document version.
type DocumentVersionInput struct {
	Provider           string
	ProviderDocumentID string
	Title              string
	Locator            string
	ProviderVersion    string
	ProviderRevision   string
	ModifiedAt         time.Time
	SourceMeetingTime  *time.Time
	RecordedAt         time.Time
	Tabs               []source.Tab
}

// DocumentVersion is an immutable, tab-aware source document version. Its
// digest covers stable source provenance, ordered tab structure, and tab
// content digests while excluding the local recorded time and the provider's
// optional ephemeral revision marker.
type DocumentVersion struct {
	provider           string
	providerDocumentID string
	title              string
	locator            string
	providerVersion    string
	providerRevision   string
	modifiedAt         time.Time
	sourceMeetingTime  *time.Time
	recordedAt         time.Time
	digest             ContentDigest
	legacyDigest       ContentDigest
	tabs               []source.Tab
}

// NewDocumentVersion validates and copies a source document version. Tab
// values and paths are copied so callers cannot mutate recorded evidence.
func NewDocumentVersion(input DocumentVersionInput) (DocumentVersion, error) {
	provider := strings.TrimSpace(input.Provider)
	if provider == "" {
		return DocumentVersion{}, fmt.Errorf("document provider is required")
	}
	providerDocumentID := strings.TrimSpace(input.ProviderDocumentID)
	if providerDocumentID == "" {
		return DocumentVersion{}, fmt.Errorf("provider document ID is required")
	}
	if input.RecordedAt.IsZero() {
		return DocumentVersion{}, fmt.Errorf("document recorded time is required")
	}
	title := strings.TrimSpace(input.Title)
	locator := strings.TrimSpace(input.Locator)
	providerVersion := strings.TrimSpace(input.ProviderVersion)
	providerRevision := strings.TrimSpace(input.ProviderRevision)
	if title == "" || locator == "" || providerVersion == "" || input.ModifiedAt.IsZero() {
		return DocumentVersion{}, fmt.Errorf("document source provenance is required")
	}
	if len(input.Tabs) == 0 {
		return DocumentVersion{}, fmt.Errorf("document tabs are required")
	}

	tabs := make([]source.Tab, len(input.Tabs))
	seenIDs := make(map[string]struct{}, len(input.Tabs))
	for index, tab := range input.Tabs {
		copied, err := copyTab(tab)
		if err != nil {
			return DocumentVersion{}, fmt.Errorf("document tab %d: %w", index, err)
		}
		if _, exists := seenIDs[copied.ID]; exists {
			return DocumentVersion{}, fmt.Errorf("document tab IDs must be unique")
		}
		seenIDs[copied.ID] = struct{}{}
		tabs[index] = copied
	}

	var sourceMeetingTime *time.Time
	if input.SourceMeetingTime != nil {
		if input.SourceMeetingTime.IsZero() {
			return DocumentVersion{}, fmt.Errorf("document source meeting time must not be zero")
		}
		value := input.SourceMeetingTime.UTC()
		sourceMeetingTime = &value
	}
	version := DocumentVersion{
		provider:           provider,
		providerDocumentID: providerDocumentID,
		title:              title,
		locator:            locator,
		providerVersion:    providerVersion,
		providerRevision:   providerRevision,
		modifiedAt:         input.ModifiedAt.UTC(),
		sourceMeetingTime:  sourceMeetingTime,
		recordedAt:         input.RecordedAt.UTC(),
		tabs:               tabs,
	}
	version.digest = digestDocumentVersion(version, false)
	version.legacyDigest = digestDocumentVersion(version, true)
	return version, nil
}

// Provider returns the source provider that owns the document.
func (version DocumentVersion) Provider() string {
	return version.provider
}

// ProviderDocumentID returns the provider's immutable document identifier.
func (version DocumentVersion) ProviderDocumentID() string {
	return version.providerDocumentID
}

// Title returns the source title captured for this immutable version.
func (version DocumentVersion) Title() string { return version.title }

// Locator returns the source locator captured for this immutable version.
func (version DocumentVersion) Locator() string { return version.locator }

// ProviderVersion returns the provider's file-version marker.
func (version DocumentVersion) ProviderVersion() string { return version.providerVersion }

// ProviderRevision returns the provider's document-revision marker.
func (version DocumentVersion) ProviderRevision() string { return version.providerRevision }

// ModifiedAt returns the provider modification timestamp.
func (version DocumentVersion) ModifiedAt() time.Time { return version.modifiedAt }

// SourceMeetingTime returns a copy of the optional explicit source meeting time.
func (version DocumentVersion) SourceMeetingTime() *time.Time {
	if version.sourceMeetingTime == nil {
		return nil
	}
	value := *version.sourceMeetingTime
	return &value
}

// RecordedAt returns when Stacks recorded this immutable document version.
func (version DocumentVersion) RecordedAt() time.Time {
	return version.recordedAt
}

// Digest returns the SHA-256 identity of the ordered tab structure and content.
func (version DocumentVersion) Digest() ContentDigest {
	return version.digest
}

// LegacyRevisionInclusiveDigest returns the exact document identity produced
// before provider revision was correctly treated as optional provenance. It is
// used only to attach the stable content identity to an existing immutable
// version during upgrade; new source versions use Digest.
func (version DocumentVersion) LegacyRevisionInclusiveDigest() ContentDigest {
	return version.legacyDigest
}

// LegacyRevisionInclusiveDigestFor reproduces the former identity using a
// stored immutable revision marker. This lets storage recognize unchanged
// legacy content even when the provider returns a different ephemeral revision
// during the first upgraded sync.
func (version DocumentVersion) LegacyRevisionInclusiveDigestFor(providerRevision string) ContentDigest {
	version.providerRevision = strings.TrimSpace(providerRevision)
	return digestDocumentVersion(version, true)
}

// Tabs returns a deep copy of the document tabs in their user-visible order.
func (version DocumentVersion) Tabs() []source.Tab {
	tabs := make([]source.Tab, len(version.tabs))
	for index, tab := range version.tabs {
		tabs[index] = cloneTab(tab)
	}
	return tabs
}

func (version DocumentVersion) tabByID(id string) (source.Tab, bool) {
	for _, tab := range version.tabs {
		if tab.ID == id {
			return tab, true
		}
	}
	return source.Tab{}, false
}

func copyTab(tab source.Tab) (source.Tab, error) {
	tab.ID = strings.TrimSpace(tab.ID)
	if tab.ID == "" {
		return source.Tab{}, fmt.Errorf("ID is required")
	}
	tab.Title = strings.TrimSpace(tab.Title)
	if tab.Title == "" {
		return source.Tab{}, fmt.Errorf("title is required")
	}
	tab.ParentID = strings.TrimSpace(tab.ParentID)
	if tab.Order < 0 {
		return source.Tab{}, fmt.Errorf("order must not be negative")
	}
	if !validTabRole(tab.Role) {
		return source.Tab{}, fmt.Errorf("role is invalid")
	}
	if !utf8.ValidString(tab.Text) {
		return source.Tab{}, fmt.Errorf("text must be valid UTF-8")
	}

	tab.Path = append([]string(nil), tab.Path...)
	for index, pathTitle := range tab.Path {
		tab.Path[index] = strings.TrimSpace(pathTitle)
		if tab.Path[index] == "" {
			return source.Tab{}, fmt.Errorf("path title is required")
		}
	}
	return tab, nil
}

func cloneTab(tab source.Tab) source.Tab {
	tab.Path = append([]string(nil), tab.Path...)
	return tab
}

func validTabRole(role source.TabRole) bool {
	switch role {
	case source.TabRoleOther, source.TabRoleTranscript, source.TabRoleGeminiNotes:
		return true
	default:
		return false
	}
}

func digestDocumentVersion(version DocumentVersion, includeProviderRevision bool) ContentDigest {
	hasher := sha256.New()
	writeString(hasher, version.title)
	writeString(hasher, version.locator)
	writeString(hasher, version.providerVersion)
	if includeProviderRevision {
		writeString(hasher, version.providerRevision)
	}
	writeString(hasher, version.modifiedAt.UTC().Format(time.RFC3339Nano))
	if version.sourceMeetingTime == nil {
		writeString(hasher, "")
	} else {
		writeString(hasher, version.sourceMeetingTime.UTC().Format(time.RFC3339Nano))
	}
	tabs := version.tabs
	writeLength(hasher, uint64(len(tabs)))
	for _, tab := range tabs {
		writeString(hasher, tab.ID)
		writeString(hasher, tab.Title)
		writeString(hasher, tab.ParentID)
		writeLength(hasher, uint64(len(tab.Path)))
		for _, pathTitle := range tab.Path {
			writeString(hasher, pathTitle)
		}
		writeLength(hasher, uint64(tab.Order))
		writeString(hasher, string(tab.Role))
		contentDigest := DigestContent([]byte(tab.Text))
		writeBytes(hasher, contentDigest[:])
	}

	var digest ContentDigest
	copy(digest[:], hasher.Sum(nil))
	return digest
}

func writeString(hasher hash.Hash, value string) {
	writeBytes(hasher, []byte(value))
}

func writeBytes(hasher hash.Hash, value []byte) {
	writeLength(hasher, uint64(len(value)))
	_, _ = hasher.Write(value)
}

func writeLength(hasher hash.Hash, length uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], length)
	_, _ = hasher.Write(encoded[:])
}

// EvidenceSpanInput contains the local source range required for a citation.
type EvidenceSpanInput struct {
	Document    DocumentVersion
	TabID       string
	StartOffset int
	EndOffset   int
	Quote       string
}

// EvidenceSpan is an immutable, exact citation into one tab of one document
// version. Offsets are UTF-8 byte offsets into the tab's local text.
type EvidenceSpan struct {
	provider           string
	providerDocumentID string
	documentDigest     ContentDigest
	tabID              string
	startOffset        int
	endOffset          int
	text               string
}

// NewEvidenceSpan validates an exact tab-local quote and constructs a citation.
func NewEvidenceSpan(input EvidenceSpanInput) (EvidenceSpan, error) {
	if input.Document.provider == "" || input.Document.providerDocumentID == "" {
		return EvidenceSpan{}, fmt.Errorf("evidence document is required")
	}
	tabID := strings.TrimSpace(input.TabID)
	if tabID == "" {
		return EvidenceSpan{}, fmt.Errorf("evidence tab ID is required")
	}
	tab, exists := input.Document.tabByID(tabID)
	if !exists {
		return EvidenceSpan{}, fmt.Errorf("evidence tab %q does not exist in document", tabID)
	}
	if input.StartOffset < 0 || input.EndOffset <= input.StartOffset || input.EndOffset > len(tab.Text) {
		return EvidenceSpan{}, fmt.Errorf("evidence offsets are outside tab text")
	}
	if !utf8ByteBoundary(tab.Text, input.StartOffset) || !utf8ByteBoundary(tab.Text, input.EndOffset) {
		return EvidenceSpan{}, fmt.Errorf("evidence offsets must align to UTF-8 rune boundaries")
	}
	if tab.Text[input.StartOffset:input.EndOffset] != input.Quote {
		return EvidenceSpan{}, fmt.Errorf("evidence quote does not exactly match tab text")
	}

	return EvidenceSpan{
		provider:           input.Document.provider,
		providerDocumentID: input.Document.providerDocumentID,
		documentDigest:     input.Document.digest,
		tabID:              tabID,
		startOffset:        input.StartOffset,
		endOffset:          input.EndOffset,
		text:               input.Quote,
	}, nil
}

// Provider returns the source provider that owns the cited document.
func (span EvidenceSpan) Provider() string {
	return span.provider
}

// ProviderDocumentID returns the cited provider document identifier.
func (span EvidenceSpan) ProviderDocumentID() string {
	return span.providerDocumentID
}

// DocumentDigest returns the immutable document version identity.
func (span EvidenceSpan) DocumentDigest() ContentDigest {
	return span.documentDigest
}

// TabID returns the immutable provider tab identifier.
func (span EvidenceSpan) TabID() string {
	return span.tabID
}

// StartOffset returns the inclusive UTF-8 byte offset in the cited tab.
func (span EvidenceSpan) StartOffset() int {
	return span.startOffset
}

// EndOffset returns the exclusive UTF-8 byte offset in the cited tab.
func (span EvidenceSpan) EndOffset() int {
	return span.endOffset
}

// Text returns the exact tab-local quote validated at construction.
func (span EvidenceSpan) Text() string {
	return span.text
}

func utf8ByteBoundary(text string, offset int) bool {
	return offset == 0 || offset == len(text) || (offset > 0 && offset < len(text) && text[offset]&0xc0 != 0x80)
}

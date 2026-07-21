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
	RecordedAt         time.Time
	Tabs               []source.Tab
}

// DocumentVersion is an immutable, tab-aware source document version. Its
// digest is derived from the ordered tab structure and tab content digests.
type DocumentVersion struct {
	provider           string
	providerDocumentID string
	recordedAt         time.Time
	digest             ContentDigest
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

	return DocumentVersion{
		provider:           provider,
		providerDocumentID: providerDocumentID,
		recordedAt:         input.RecordedAt.UTC(),
		digest:             digestTabs(tabs),
		tabs:               tabs,
	}, nil
}

// Provider returns the source provider that owns the document.
func (version DocumentVersion) Provider() string {
	return version.provider
}

// ProviderDocumentID returns the provider's immutable document identifier.
func (version DocumentVersion) ProviderDocumentID() string {
	return version.providerDocumentID
}

// RecordedAt returns when Stacks recorded this immutable document version.
func (version DocumentVersion) RecordedAt() time.Time {
	return version.recordedAt
}

// Digest returns the SHA-256 identity of the ordered tab structure and content.
func (version DocumentVersion) Digest() ContentDigest {
	return version.digest
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

func digestTabs(tabs []source.Tab) ContentDigest {
	hasher := sha256.New()
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

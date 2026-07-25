package evidence

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
	"strings"
	"time"
)

// DocumentVersionInput contains source metadata and structured content for one
// immutable source document version.
type DocumentVersionInput struct {
	Provider, ProviderDocumentID, Title, Locator string
	ProviderVersion, ProviderRevision            string
	ModifiedAt, RecordedAt                       time.Time
	SourceTime                                   *time.Time
	Sections                                     []Section
}

// DocumentVersion is immutable source document evidence.
type DocumentVersion struct {
	provider, providerDocumentID, title, locator, providerVersion, providerRevision string
	modifiedAt, recordedAt                                                          time.Time
	sourceTime                                                                      *time.Time
	digest, legacyDigest                                                            ContentDigest
	sections                                                                        []Section
}

// NewDocumentVersion validates and copies immutable source document evidence.
func NewDocumentVersion(input DocumentVersionInput) (DocumentVersion, error) {
	provider, providerDocumentID := strings.TrimSpace(input.Provider), strings.TrimSpace(input.ProviderDocumentID)
	if provider == "" {
		return DocumentVersion{}, fmt.Errorf("document provider is required")
	}
	if providerDocumentID == "" {
		return DocumentVersion{}, fmt.Errorf("provider document ID is required")
	}
	if input.RecordedAt.IsZero() {
		return DocumentVersion{}, fmt.Errorf("document recorded time is required")
	}
	title, locator := strings.TrimSpace(input.Title), strings.TrimSpace(input.Locator)
	providerVersion, providerRevision := strings.TrimSpace(input.ProviderVersion), strings.TrimSpace(input.ProviderRevision)
	if title == "" || locator == "" || providerVersion == "" || input.ModifiedAt.IsZero() {
		return DocumentVersion{}, fmt.Errorf("document source provenance is required")
	}
	if len(input.Sections) == 0 {
		return DocumentVersion{}, fmt.Errorf("document sections are required")
	}
	sections := append([]Section(nil), input.Sections...)
	seenIDs := make(map[string]struct{}, len(sections))
	for _, section := range sections {
		if section.ID() == "" {
			return DocumentVersion{}, fmt.Errorf("document section: ID is required")
		}
		if _, exists := seenIDs[section.ID()]; exists {
			return DocumentVersion{}, fmt.Errorf("document section IDs must be unique")
		}
		seenIDs[section.ID()] = struct{}{}
	}
	var sourceTime *time.Time
	if input.SourceTime != nil {
		if input.SourceTime.IsZero() {
			return DocumentVersion{}, fmt.Errorf("document source time must not be zero")
		}
		value := input.SourceTime.UTC()
		sourceTime = &value
	}
	version := DocumentVersion{provider: provider, providerDocumentID: providerDocumentID, title: title, locator: locator, providerVersion: providerVersion, providerRevision: providerRevision, modifiedAt: input.ModifiedAt.UTC(), sourceTime: sourceTime, recordedAt: input.RecordedAt.UTC(), sections: sections}
	version.digest, version.legacyDigest = digestDocumentVersion(version, false), digestDocumentVersion(version, true)
	return version, nil
}

func (version DocumentVersion) Provider() string           { return version.provider }
func (version DocumentVersion) ProviderDocumentID() string { return version.providerDocumentID }
func (version DocumentVersion) Title() string              { return version.title }
func (version DocumentVersion) Locator() string            { return version.locator }
func (version DocumentVersion) ProviderVersion() string    { return version.providerVersion }
func (version DocumentVersion) ProviderRevision() string   { return version.providerRevision }
func (version DocumentVersion) ModifiedAt() time.Time      { return version.modifiedAt }
func (version DocumentVersion) SourceTime() *time.Time {
	if version.sourceTime == nil {
		return nil
	}
	value := *version.sourceTime
	return &value
}

func (version DocumentVersion) RecordedAt() time.Time { return version.recordedAt }
func (version DocumentVersion) Digest() ContentDigest { return version.digest }
func (version DocumentVersion) LegacyRevisionInclusiveDigest() ContentDigest {
	return version.legacyDigest
}
func (version DocumentVersion) LegacyRevisionInclusiveDigestFor(providerRevision string) ContentDigest {
	version.providerRevision = strings.TrimSpace(providerRevision)
	return digestDocumentVersion(version, true)
}
func (version DocumentVersion) Sections() []Section {
	return append([]Section(nil), version.sections...)
}

func (version DocumentVersion) sectionByID(id string) (Section, bool) {
	for _, section := range version.sections {
		if section.ID() == id {
			return section, true
		}
	}
	return Section{}, false
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
	if version.sourceTime == nil {
		writeString(hasher, "")
	} else {
		writeString(hasher, version.sourceTime.UTC().Format(time.RFC3339Nano))
	}
	writeLength(hasher, uint64(len(version.sections)))
	for _, section := range version.sections {
		writeString(hasher, section.ID())
		writeString(hasher, section.Title())
		writeString(hasher, section.ParentID())
		writeLength(hasher, uint64(len(section.Path())))
		for _, pathTitle := range section.Path() {
			writeString(hasher, pathTitle)
		}
		writeLength(hasher, uint64(section.Order()))
		writeString(hasher, section.Role())
		contentDigest := DigestContent([]byte(section.Text()))
		writeBytes(hasher, contentDigest[:])
	}
	var digest ContentDigest
	copy(digest[:], hasher.Sum(nil))
	return digest
}

func writeString(hasher hash.Hash, value string) { writeBytes(hasher, []byte(value)) }
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
	Document               DocumentVersion
	SectionID              string
	StartOffset, EndOffset int
	Quote                  string
}

// EvidenceSpan is an immutable exact citation into one section of one document version.
type EvidenceSpan struct {
	provider, providerDocumentID string
	documentDigest               ContentDigest
	sectionID                    string
	startOffset, endOffset       int
	text                         string
}

// NewEvidenceSpan validates an exact section-local quote and constructs a citation.
func NewEvidenceSpan(input EvidenceSpanInput) (EvidenceSpan, error) {
	if input.Document.provider == "" || input.Document.providerDocumentID == "" {
		return EvidenceSpan{}, fmt.Errorf("evidence document is required")
	}
	sectionID := strings.TrimSpace(input.SectionID)
	if sectionID == "" {
		return EvidenceSpan{}, fmt.Errorf("evidence section ID is required")
	}
	section, exists := input.Document.sectionByID(sectionID)
	if !exists {
		return EvidenceSpan{}, fmt.Errorf("evidence section %q does not exist in document", sectionID)
	}
	if input.StartOffset < 0 || input.EndOffset <= input.StartOffset || input.EndOffset > len(section.Text()) {
		return EvidenceSpan{}, fmt.Errorf("evidence offsets are outside section text")
	}
	if !utf8ByteBoundary(section.Text(), input.StartOffset) || !utf8ByteBoundary(section.Text(), input.EndOffset) {
		return EvidenceSpan{}, fmt.Errorf("evidence offsets must align to UTF-8 rune boundaries")
	}
	if section.Text()[input.StartOffset:input.EndOffset] != input.Quote {
		return EvidenceSpan{}, fmt.Errorf("evidence quote does not exactly match section text")
	}
	return EvidenceSpan{provider: input.Document.provider, providerDocumentID: input.Document.providerDocumentID, documentDigest: input.Document.digest, sectionID: sectionID, startOffset: input.StartOffset, endOffset: input.EndOffset, text: input.Quote}, nil
}

func (span EvidenceSpan) Provider() string              { return span.provider }
func (span EvidenceSpan) ProviderDocumentID() string    { return span.providerDocumentID }
func (span EvidenceSpan) DocumentDigest() ContentDigest { return span.documentDigest }
func (span EvidenceSpan) SectionID() string             { return span.sectionID }
func (span EvidenceSpan) StartOffset() int              { return span.startOffset }
func (span EvidenceSpan) EndOffset() int                { return span.endOffset }
func (span EvidenceSpan) Text() string                  { return span.text }

func utf8ByteBoundary(text string, offset int) bool {
	return offset == 0 || offset == len(text) || (offset > 0 && offset < len(text) && text[offset]&0xc0 != 0x80)
}

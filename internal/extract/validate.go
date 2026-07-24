package extract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"stacks/internal/entity"
)

type TabRole string

const (
	TabRoleTranscript TabRole = "transcript"
	TabRoleNotes      TabRole = "gemini-notes"
	TabRoleOther      TabRole = "other"

	MentionRoleSpeaker   = "speaker"
	MentionRoleReference = "reference"

	SignalCategoryDelegationAutonomy   = "delegation_autonomy"
	SignalCategoryScrutinyCorrection   = "scrutiny_correction"
	SignalCategoryEndorsementTrust     = "endorsement_trust"
	SignalCategorySupportAdvocacy      = "support_advocacy"
	SignalCategoryFutureResponsibility = "future_responsibility"

	SignalDirectionStrengthening = "strengthening"
	SignalDirectionWeakening     = "weakening"
	SignalDirectionMixed         = "mixed"
	SignalDirectionUnclear       = "unclear"
)

// SubmittedTab is one exact model input segment. Offsets into Text are UTF-8
// byte offsets and Role is assigned deterministically before model invocation.
type SubmittedTab struct {
	ID   string
	Role TabRole
	Text string
}

// SubmittedText contains the exact, separately classified text supplied to a
// model. Notes are never promoted to primary signal evidence.
type SubmittedText struct {
	Tabs              []SubmittedTab
	SourceMeetingTime *time.Time
}

type Citation struct {
	ID          string `json:"id"`
	TabID       string `json:"tab_id"`
	StartOffset int    `json:"start_offset"`
	EndOffset   int    `json:"end_offset"`
	Quote       string `json:"quote"`
}

type PersonMention struct {
	ID          string   `json:"id"`
	Surface     string   `json:"surface"`
	Role        string   `json:"role"`
	Email       string   `json:"email"`
	CitationIDs []string `json:"citation_ids"`
}

// GroundedPersonIdentity is the independently validated evidence retained from
// one model proposal. ProposedEmail is auditable but never establishes that it
// belongs to the named person; callers decide whether they possess a separate,
// deterministic trusted-email source.
type GroundedPersonIdentity struct {
	NameEvidenceCitationID  string
	EmailEvidenceCitationID string
	NormalizedName          string
	ProposedEmail           string
}

type AttributedStatement struct {
	ID               string   `json:"id"`
	SpeakerMentionID string   `json:"speaker_mention_id"`
	SubjectMentionID string   `json:"subject_mention_id"`
	Predicate        string   `json:"predicate"`
	ObjectText       string   `json:"object_text"`
	ValidDate        string   `json:"valid_date"`
	CitationIDs      []string `json:"citation_ids"`
}

type InteractionSignal struct {
	ID                       string   `json:"id"`
	SubjectMentionID         string   `json:"subject_mention_id"`
	ObjectMentionID          string   `json:"object_mention_id"`
	StatementIDs             []string `json:"statement_ids"`
	Category                 string   `json:"category"`
	Direction                string   `json:"direction"`
	Rationale                string   `json:"rationale"`
	Confidence               float64  `json:"confidence"`
	SupportingCitationIDs    []string `json:"supporting_citation_ids"`
	ContradictingCitationIDs []string `json:"contradicting_citation_ids"`
}

// ExtractionOutput is an untrusted structured model proposal. It is not a
// domain object and must pass ValidateExtraction before conversion.
type ExtractionOutput struct {
	MeetingDate string                `json:"meeting_date"`
	Citations   []Citation            `json:"citations"`
	People      []PersonMention       `json:"people"`
	Statements  []AttributedStatement `json:"statements"`
	Signals     []InteractionSignal   `json:"signals"`
}

// DecodeAndValidateExtraction rejects non-schema JSON and validates all
// semantic references and exact source citations before returning output.
func DecodeAndValidateExtraction(submitted SubmittedText, raw []byte) (ExtractionOutput, error) {
	if err := validateRequiredJSONFields(raw); err != nil {
		return ExtractionOutput{}, fmt.Errorf("extraction output failed schema decoding")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var output ExtractionOutput
	if err := decoder.Decode(&output); err != nil {
		return ExtractionOutput{}, fmt.Errorf("extraction output failed schema decoding")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ExtractionOutput{}, fmt.Errorf("extraction output failed schema decoding")
	}
	output = groundUniqueCitationOffsets(submitted, output)
	if err := ValidateExtraction(submitted, output); err != nil {
		return ExtractionOutput{}, err
	}
	output.MeetingDate = groundedMeetingDate(submitted, output)
	return output, nil
}

func groundUniqueCitationOffsets(submitted SubmittedText, output ExtractionOutput) ExtractionOutput {
	tabs := make(map[string]string, len(submitted.Tabs))
	for _, tab := range submitted.Tabs {
		tabs[tab.ID] = tab.Text
	}
	for index := range output.Citations {
		citation := &output.Citations[index]
		text, exists := tabs[citation.TabID]
		if !exists || citation.Quote == "" || citationMatchesText(*citation, text) {
			continue
		}
		start := strings.Index(text, citation.Quote)
		if start < 0 || strings.Contains(text[start+len(citation.Quote):], citation.Quote) {
			continue
		}
		citation.StartOffset = start
		citation.EndOffset = start + len(citation.Quote)
	}
	return output
}

func citationMatchesText(citation Citation, text string) bool {
	return citation.StartOffset >= 0 && citation.EndOffset > citation.StartOffset &&
		citation.EndOffset <= len(text) &&
		text[citation.StartOffset:citation.EndOffset] == citation.Quote
}

type extractionWire struct {
	MeetingDate *json.RawMessage   `json:"meeting_date"`
	Citations   *[]json.RawMessage `json:"citations"`
	People      *[]json.RawMessage `json:"people"`
	Statements  *[]json.RawMessage `json:"statements"`
	Signals     *[]json.RawMessage `json:"signals"`
}

type citationWire struct {
	ID          *json.RawMessage `json:"id"`
	TabID       *json.RawMessage `json:"tab_id"`
	StartOffset *json.RawMessage `json:"start_offset"`
	EndOffset   *json.RawMessage `json:"end_offset"`
	Quote       *json.RawMessage `json:"quote"`
}

type personWire struct {
	ID          *json.RawMessage `json:"id"`
	Surface     *json.RawMessage `json:"surface"`
	Role        *json.RawMessage `json:"role"`
	Email       *json.RawMessage `json:"email"`
	CitationIDs *json.RawMessage `json:"citation_ids"`
}

type statementWire struct {
	ID               *json.RawMessage `json:"id"`
	SpeakerMentionID *json.RawMessage `json:"speaker_mention_id"`
	SubjectMentionID *json.RawMessage `json:"subject_mention_id"`
	Predicate        *json.RawMessage `json:"predicate"`
	ObjectText       *json.RawMessage `json:"object_text"`
	ValidDate        *json.RawMessage `json:"valid_date"`
	CitationIDs      *json.RawMessage `json:"citation_ids"`
}

type signalWire struct {
	ID                       *json.RawMessage `json:"id"`
	SubjectMentionID         *json.RawMessage `json:"subject_mention_id"`
	ObjectMentionID          *json.RawMessage `json:"object_mention_id"`
	StatementIDs             *json.RawMessage `json:"statement_ids"`
	Category                 *json.RawMessage `json:"category"`
	Direction                *json.RawMessage `json:"direction"`
	Rationale                *json.RawMessage `json:"rationale"`
	Confidence               *json.RawMessage `json:"confidence"`
	SupportingCitationIDs    *json.RawMessage `json:"supporting_citation_ids"`
	ContradictingCitationIDs *json.RawMessage `json:"contradicting_citation_ids"`
}

func validateRequiredJSONFields(raw []byte) error {
	var root extractionWire
	if err := json.Unmarshal(raw, &root); err != nil || root.MeetingDate == nil || root.Citations == nil || root.People == nil || root.Statements == nil || root.Signals == nil {
		return errInvalidSchemaShape
	}
	for _, item := range *root.Citations {
		var value citationWire
		if json.Unmarshal(item, &value) != nil || value.ID == nil || value.TabID == nil || value.StartOffset == nil || value.EndOffset == nil || value.Quote == nil {
			return errInvalidSchemaShape
		}
	}
	for _, item := range *root.People {
		var value personWire
		if json.Unmarshal(item, &value) != nil || value.ID == nil || value.Surface == nil || value.Role == nil || value.Email == nil || value.CitationIDs == nil {
			return errInvalidSchemaShape
		}
	}
	for _, item := range *root.Statements {
		var value statementWire
		if json.Unmarshal(item, &value) != nil || value.ID == nil || value.SpeakerMentionID == nil || value.SubjectMentionID == nil || value.Predicate == nil || value.ObjectText == nil || value.ValidDate == nil || value.CitationIDs == nil {
			return errInvalidSchemaShape
		}
	}
	for _, item := range *root.Signals {
		var value signalWire
		if json.Unmarshal(item, &value) != nil || value.ID == nil || value.SubjectMentionID == nil || value.ObjectMentionID == nil || value.StatementIDs == nil || value.Category == nil || value.Direction == nil || value.Rationale == nil || value.Confidence == nil || value.SupportingCitationIDs == nil || value.ContradictingCitationIDs == nil {
			return errInvalidSchemaShape
		}
	}
	return nil
}

var errInvalidSchemaShape = fmt.Errorf("required schema field is missing")

// ValidateExtraction validates an untrusted proposal without exposing private
// model or source values in returned errors.
func ValidateExtraction(submitted SubmittedText, output ExtractionOutput) error {
	tabs, err := validateSubmittedText(submitted)
	if err != nil {
		return err
	}
	if err := validateDate("meeting", output.MeetingDate); err != nil {
		return err
	}

	citations := make(map[string]Citation, len(output.Citations))
	for index, citation := range output.Citations {
		if err := rejectPaddedIdentifiers("citation", index, citation.ID, citation.TabID); err != nil {
			return err
		}
		if strings.TrimSpace(citation.ID) == "" {
			return fmt.Errorf("citation %d ID is required", index)
		}
		if _, exists := citations[citation.ID]; exists {
			return fmt.Errorf("citation %d ID is duplicated", index)
		}
		tab, exists := tabs[citation.TabID]
		if !exists {
			return fmt.Errorf("citation %d has an unknown reference", index)
		}
		if citation.StartOffset < 0 || citation.EndOffset <= citation.StartOffset || citation.EndOffset > len(tab.Text) {
			return fmt.Errorf("citation %d offsets are outside submitted text", index)
		}
		if !utf8Boundary(tab.Text, citation.StartOffset) || !utf8Boundary(tab.Text, citation.EndOffset) {
			return fmt.Errorf("citation %d offsets must align to UTF-8 boundaries", index)
		}
		if tab.Text[citation.StartOffset:citation.EndOffset] != citation.Quote {
			return fmt.Errorf("citation %d does not exactly match submitted text", index)
		}
		citations[citation.ID] = citation
	}

	people := make(map[string]string, len(output.People))
	for index, person := range output.People {
		if err := rejectPaddedIdentifiers("person mention", index, append([]string{person.ID}, person.CitationIDs...)...); err != nil {
			return err
		}
		if strings.TrimSpace(person.ID) == "" || strings.TrimSpace(person.Surface) == "" {
			return fmt.Errorf("person mention %d required fields are missing", index)
		}
		if _, exists := people[person.ID]; exists {
			return fmt.Errorf("person mention %d ID is duplicated", index)
		}
		if person.Role != MentionRoleSpeaker && person.Role != MentionRoleReference {
			return fmt.Errorf("person mention %d role is invalid", index)
		}
		if err := validateReferences("person mention", index, person.CitationIDs, citations); err != nil {
			return err
		}
		if err := validateGroundedPerson(index, person, citations); err != nil {
			return err
		}
		people[person.ID] = person.Role
	}

	statements := make(map[string]struct{}, len(output.Statements))
	for index, statement := range output.Statements {
		identifiers := []string{statement.ID, statement.SpeakerMentionID, statement.SubjectMentionID}
		if err := rejectPaddedIdentifiers("statement", index, append(identifiers, statement.CitationIDs...)...); err != nil {
			return err
		}
		if strings.TrimSpace(statement.ID) == "" || strings.TrimSpace(statement.Predicate) == "" || strings.TrimSpace(statement.ObjectText) == "" {
			return fmt.Errorf("statement %d required fields are missing", index)
		}
		if _, exists := statements[statement.ID]; exists {
			return fmt.Errorf("statement %d ID is duplicated", index)
		}
		role, exists := people[statement.SpeakerMentionID]
		if !exists {
			return fmt.Errorf("statement %d has an unknown reference", index)
		}
		if role != MentionRoleSpeaker {
			return fmt.Errorf("statement %d speaker reference is invalid", index)
		}
		if _, exists := people[statement.SubjectMentionID]; !exists {
			return fmt.Errorf("statement %d has an unknown reference", index)
		}
		if err := validateReferences("statement", index, statement.CitationIDs, citations); err != nil {
			return err
		}
		if err := validateDate("statement", statement.ValidDate); err != nil {
			return err
		}
		statements[statement.ID] = struct{}{}
	}

	signals := make(map[string]struct{}, len(output.Signals))
	for index, signal := range output.Signals {
		identifiers := []string{signal.ID, signal.SubjectMentionID, signal.ObjectMentionID}
		identifiers = append(identifiers, signal.StatementIDs...)
		identifiers = append(identifiers, signal.SupportingCitationIDs...)
		identifiers = append(identifiers, signal.ContradictingCitationIDs...)
		if err := rejectPaddedIdentifiers("signal", index, identifiers...); err != nil {
			return err
		}
		if strings.TrimSpace(signal.ID) == "" || strings.TrimSpace(signal.Rationale) == "" {
			return fmt.Errorf("signal %d required fields are missing", index)
		}
		if _, exists := signals[signal.ID]; exists {
			return fmt.Errorf("signal %d ID is duplicated", index)
		}
		signals[signal.ID] = struct{}{}
		if !validSignalCategory(signal.Category) {
			return fmt.Errorf("signal %d category is invalid", index)
		}
		if !validSignalDirection(signal.Direction) {
			return fmt.Errorf("signal %d direction is invalid", index)
		}
		if math.IsNaN(signal.Confidence) || math.IsInf(signal.Confidence, 0) || signal.Confidence < 0 || signal.Confidence > 1 {
			return fmt.Errorf("signal %d confidence must be finite and between zero and one", index)
		}
		if _, exists := people[signal.SubjectMentionID]; !exists {
			return fmt.Errorf("signal %d has an unknown reference", index)
		}
		if _, exists := people[signal.ObjectMentionID]; !exists {
			return fmt.Errorf("signal %d has an unknown reference", index)
		}
		if len(signal.StatementIDs) == 0 {
			return fmt.Errorf("signal %d statement references are required", index)
		}
		seenStatements := make(map[string]struct{}, len(signal.StatementIDs))
		for _, statementID := range signal.StatementIDs {
			if _, exists := statements[statementID]; !exists {
				return fmt.Errorf("signal %d has an unknown reference", index)
			}
			if _, exists := seenStatements[statementID]; exists {
				return fmt.Errorf("signal %d has a duplicated reference", index)
			}
			seenStatements[statementID] = struct{}{}
		}
		if err := validateReferences("signal", index, signal.SupportingCitationIDs, citations); err != nil {
			return err
		}
		if err := validateOptionalReferences("signal", index, signal.ContradictingCitationIDs, citations); err != nil {
			return err
		}
		if !hasTranscriptCitation(signal.SupportingCitationIDs, citations, tabs) {
			return fmt.Errorf("signal %d requires supporting transcript evidence", index)
		}
	}
	return nil
}

func rejectPaddedIdentifiers(kind string, index int, identifiers ...string) error {
	for _, identifier := range identifiers {
		if strings.TrimSpace(identifier) != identifier {
			return fmt.Errorf("%s %d identifier has leading or trailing whitespace", kind, index)
		}
	}
	return nil
}

func validateSubmittedText(submitted SubmittedText) (map[string]SubmittedTab, error) {
	if len(submitted.Tabs) == 0 {
		return nil, fmt.Errorf("submitted text requires at least one tab")
	}
	tabs := make(map[string]SubmittedTab, len(submitted.Tabs))
	for index, tab := range submitted.Tabs {
		if strings.TrimSpace(tab.ID) == "" || !utf8.ValidString(tab.Text) {
			return nil, fmt.Errorf("submitted tab %d is invalid", index)
		}
		if tab.Role != TabRoleTranscript && tab.Role != TabRoleNotes && tab.Role != TabRoleOther {
			return nil, fmt.Errorf("submitted tab %d role is invalid", index)
		}
		if _, exists := tabs[tab.ID]; exists {
			return nil, fmt.Errorf("submitted tab %d ID is duplicated", index)
		}
		tabs[tab.ID] = tab
	}
	return tabs, nil
}

func validateDate(field, value string) error {
	if value == "" {
		return nil
	}
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil || parsed.Format(time.DateOnly) != value {
		return fmt.Errorf("%s date is invalid", field)
	}
	return nil
}

func groundedMeetingDate(submitted SubmittedText, _ ExtractionOutput) string {
	if submitted.SourceMeetingTime != nil && !submitted.SourceMeetingTime.IsZero() {
		return submitted.SourceMeetingTime.Format(time.DateOnly)
	}
	// No deterministic content-to-meeting-time contract is approved. A date in
	// arbitrary cited text may be a deadline or another event, so model output
	// cannot establish chronology by itself.
	return ""
}

func validateReferences(kind string, index int, references []string, known map[string]Citation) error {
	if len(references) == 0 {
		return fmt.Errorf("%s %d citation references are required", kind, index)
	}
	return validateOptionalReferences(kind, index, references, known)
}

func validateOptionalReferences(kind string, index int, references []string, known map[string]Citation) error {
	seen := make(map[string]struct{}, len(references))
	for _, reference := range references {
		if _, exists := known[reference]; !exists {
			return fmt.Errorf("%s %d has an unknown reference", kind, index)
		}
		if _, exists := seen[reference]; exists {
			return fmt.Errorf("%s %d has a duplicated reference", kind, index)
		}
		seen[reference] = struct{}{}
	}
	return nil
}

func validateGroundedPerson(index int, person PersonMention, citations map[string]Citation) error {
	_, err := groundedPersonIdentity(person, citations)
	if err != nil {
		return fmt.Errorf("person mention %d %w", index, err)
	}
	return nil
}

// GroundPersonIdentity independently validates exact name and optional email
// citations. Callers must pass an already schema-validated proposal; a
// returned proposed email remains non-authoritative.
func GroundPersonIdentity(person PersonMention, citations []Citation) (GroundedPersonIdentity, error) {
	byID := make(map[string]Citation, len(citations))
	for _, citation := range citations {
		byID[citation.ID] = citation
	}
	return groundedPersonIdentity(person, byID)
}

func groundedPersonIdentity(person PersonMention, citations map[string]Citation) (GroundedPersonIdentity, error) {
	normalizedSurface := entity.NormalizeName(person.Surface)
	if normalizedSurface == "" {
		return GroundedPersonIdentity{}, fmt.Errorf("surface is not grounded in cited evidence")
	}
	normalizedEmail := entity.NormalizeEmail(person.Email)
	if normalizedEmail != "" && !entity.ValidEmail(normalizedEmail) {
		return GroundedPersonIdentity{}, fmt.Errorf("email is invalid")
	}

	nameCitationID := ""
	emailCitationID := ""
	for _, citationID := range person.CitationIDs {
		citation, exists := citations[citationID]
		if !exists {
			return GroundedPersonIdentity{}, fmt.Errorf("has an unknown identity evidence reference")
		}
		quoteName := entity.NormalizeName(citation.Quote)
		quoteEmail := entity.NormalizeEmail(citation.Quote)
		nameGrounded := containsGroundedNameIdentity(quoteName, normalizedSurface)
		if nameGrounded && nameCitationID == "" {
			nameCitationID = citationID
		}
		if normalizedEmail != "" && emailCitationID == "" && containsBoundedEmailIdentity(quoteEmail, normalizedEmail) {
			emailCitationID = citationID
		}
	}
	if nameCitationID == "" {
		return GroundedPersonIdentity{}, fmt.Errorf("surface is not grounded in cited evidence")
	}
	if normalizedEmail != "" && emailCitationID == "" {
		return GroundedPersonIdentity{}, fmt.Errorf("email is not grounded in cited evidence")
	}
	return GroundedPersonIdentity{
		NameEvidenceCitationID: nameCitationID, EmailEvidenceCitationID: emailCitationID,
		NormalizedName: normalizedSurface, ProposedEmail: normalizedEmail,
	}, nil
}

func containsGroundedNameIdentity(text, value string) bool {
	if value == "" {
		return false
	}
	for offset := 0; offset <= len(text)-len(value); {
		index := strings.Index(text[offset:], value)
		if index < 0 {
			return false
		}
		index += offset
		end := index + len(value)
		beforeMatches := false
		if index > 0 {
			before, _ := utf8.DecodeLastRuneInString(text[:index])
			beforeMatches = isNameIdentityRune(before)
		}
		afterMatches := false
		if end < len(text) {
			after, _ := utf8.DecodeRuneInString(text[end:])
			afterMatches = isNameIdentityRune(after)
		}
		if !beforeMatches && !afterMatches && !identityOccurrenceInsideEmailToken(text, index, end) {
			return true
		}
		offset = end
	}
	return false
}

func identityOccurrenceInsideEmailToken(text string, start, end int) bool {
	left := start
	for left > 0 {
		value, width := utf8.DecodeLastRuneInString(text[:left])
		if !isEmailRune(value) {
			break
		}
		left -= width
	}
	right := end
	for right < len(text) {
		value, width := utf8.DecodeRuneInString(text[right:])
		if !isEmailRune(value) {
			break
		}
		right += width
	}
	return strings.Contains(text[left:right], "@")
}

func containsBoundedEmailIdentity(text, value string) bool {
	if value == "" {
		return false
	}
	for offset := 0; offset <= len(text)-len(value); {
		index := strings.Index(text[offset:], value)
		if index < 0 {
			return false
		}
		index += offset
		beforeMatches := false
		if index > 0 {
			before, _ := utf8.DecodeLastRuneInString(text[:index])
			beforeMatches = isEmailRune(before)
		}
		afterOffset := index + len(value)
		if !beforeMatches && !emailIdentityContinues(text[afterOffset:]) {
			return true
		}
		offset = afterOffset
	}
	return false
}

func emailIdentityContinues(suffix string) bool {
	if suffix == "" {
		return false
	}
	next, width := utf8.DecodeRuneInString(suffix)
	if !isEmailRune(next) {
		return false
	}
	if next != '.' {
		return true
	}
	if width == len(suffix) {
		return false
	}
	afterPeriod, _ := utf8.DecodeRuneInString(suffix[width:])
	return unicode.IsLetter(afterPeriod) || unicode.IsDigit(afterPeriod)
}

func isEmailRune(value rune) bool {
	return unicode.IsLetter(value) || unicode.IsDigit(value) || strings.ContainsRune(".!#$%&'*+-/=?^_`{|}~@", value)
}

func isNameIdentityRune(value rune) bool {
	return unicode.IsLetter(value) || unicode.IsDigit(value) || unicode.IsMark(value) ||
		unicode.Is(unicode.Pd, value) || unicode.Is(unicode.Pc, value) || value == '\'' || value == '’'
}

func hasTranscriptCitation(references []string, citations map[string]Citation, tabs map[string]SubmittedTab) bool {
	for _, reference := range references {
		if tabs[citations[reference].TabID].Role == TabRoleTranscript {
			return true
		}
	}
	return false
}

func validSignalCategory(category string) bool {
	switch category {
	case SignalCategoryDelegationAutonomy, SignalCategoryScrutinyCorrection, SignalCategoryEndorsementTrust, SignalCategorySupportAdvocacy, SignalCategoryFutureResponsibility:
		return true
	default:
		return false
	}
}

func validSignalDirection(direction string) bool {
	switch direction {
	case SignalDirectionStrengthening, SignalDirectionWeakening, SignalDirectionMixed, SignalDirectionUnclear:
		return true
	default:
		return false
	}
}

func utf8Boundary(text string, offset int) bool {
	return offset == 0 || offset == len(text) || (offset > 0 && offset < len(text) && text[offset]&0xc0 != 0x80)
}

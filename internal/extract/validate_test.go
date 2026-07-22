package extract

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

const submittedTranscript = "speaker-a assigned the follow-up."

func TestDecodeExtractionLeavesAbsentMeetingDateUnknown(t *testing.T) {
	output := validExtraction()
	output.MeetingDate = ""

	got := decodeExtractionForTest(t, validSubmittedText(), output)
	if got.MeetingDate != "" {
		t.Fatalf("MeetingDate = %q, want unknown", got.MeetingDate)
	}
}

func TestDecodeExtractionDoesNotTrustInventedMeetingDate(t *testing.T) {
	output := validExtraction()
	output.MeetingDate = "2026-07-21"

	got := decodeExtractionForTest(t, validSubmittedText(), output)
	if got.MeetingDate != "" {
		t.Fatalf("MeetingDate = %q, want unknown because no exact source citation contains the date", got.MeetingDate)
	}
}

func TestDecodeExtractionAcceptsExactDateBearingCitation(t *testing.T) {
	const citedText = "2026-07-21 speaker-a assigned"
	submitted := SubmittedText{Tabs: []SubmittedTab{{
		ID: "transcript-tab", Role: TabRoleTranscript,
		Text: citedText + " the follow-up.",
	}}}
	output := validExtraction()
	output.Citations[0] = Citation{
		ID: "citation-1", TabID: "transcript-tab", StartOffset: 0,
		EndOffset: len(citedText), Quote: citedText,
	}

	got := decodeExtractionForTest(t, submitted, output)
	if got.MeetingDate != "2026-07-21" {
		t.Fatalf("MeetingDate = %q, want exact cited date", got.MeetingDate)
	}
}

func TestDecodeExtractionDoesNotTreatEmbeddedDateAsExactToken(t *testing.T) {
	const citedText = "speaker-a codeA2026-07-21B assigned"
	submitted := SubmittedText{Tabs: []SubmittedTab{{
		ID: "transcript-tab", Role: TabRoleTranscript,
		Text: citedText + " the follow-up.",
	}}}
	output := validExtraction()
	output.MeetingDate = "2026-07-21"
	output.Citations[0] = Citation{
		ID: "citation-1", TabID: "transcript-tab", StartOffset: 0,
		EndOffset: len(citedText), Quote: citedText,
	}

	got := decodeExtractionForTest(t, submitted, output)
	if got.MeetingDate != "" {
		t.Fatalf("MeetingDate = %q, want unknown for a date embedded in a larger token", got.MeetingDate)
	}
}

func TestDecodeExtractionDerivesMeetingDateFromPersistedSourceMetadata(t *testing.T) {
	sourceMeetingTime := time.Date(2026, time.July, 21, 14, 30, 0, 0, time.FixedZone("synthetic", -4*60*60))
	submitted := validSubmittedText()
	submitted.SourceMeetingTime = &sourceMeetingTime
	output := validExtraction()
	output.MeetingDate = "2026-07-22"

	got := decodeExtractionForTest(t, submitted, output)
	if got.MeetingDate != "2026-07-21" {
		t.Fatalf("MeetingDate = %q, want source metadata date", got.MeetingDate)
	}
}

func decodeExtractionForTest(t *testing.T, submitted SubmittedText, output ExtractionOutput) ExtractionOutput {
	t.Helper()
	for index := range output.Signals {
		if output.Signals[index].ContradictingCitationIDs == nil {
			output.Signals[index].ContradictingCitationIDs = []string{}
		}
	}
	raw, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal extraction: %v", err)
	}
	decoded, err := DecodeAndValidateExtraction(submitted, raw)
	if err != nil {
		t.Fatalf("DecodeAndValidateExtraction() error = %v", err)
	}
	return decoded
}

func TestValidateExtractionAcceptsExactUTF8ByteCitations(t *testing.T) {
	text := SubmittedText{Tabs: []SubmittedTab{
		{ID: "transcript-tab", Role: TabRoleTranscript, Text: "é assigned the follow-up."},
		{ID: "notes-tab", Role: TabRoleNotes, Text: "secondary summary"},
	}}
	output := validExtraction()
	output.Citations[0] = Citation{
		ID: "citation-1", TabID: "transcript-tab", StartOffset: 0,
		EndOffset: len("é assigned"), Quote: "é assigned",
	}
	output.People[0].Surface = "é"

	if err := ValidateExtraction(text, output); err != nil {
		t.Fatalf("ValidateExtraction() error = %v", err)
	}
}

func TestValidateExtractionRejectsInventedCitation(t *testing.T) {
	output := validExtraction()
	output.Citations[0].Quote = "invented text"

	err := ValidateExtraction(validSubmittedText(), output)
	if err == nil || !strings.Contains(err.Error(), "does not exactly match") {
		t.Fatalf("ValidateExtraction() error = %v, want exact citation mismatch", err)
	}
}

func TestValidateExtractionRejectsPersonSurfaceAbsentFromCitedEvidence(t *testing.T) {
	output := validExtraction()
	output.People[0].Surface = "invented-person"

	err := ValidateExtraction(validSubmittedText(), output)
	if err == nil || !strings.Contains(err.Error(), "grounded") {
		t.Fatalf("ValidateExtraction() error = %v, want ungrounded identity rejection", err)
	}
}

func TestValidateExtractionRejectsNamePrefixInsideHyphenatedIdentity(t *testing.T) {
	const citedText = "Ann-Marie assigned the follow-up."
	submitted := SubmittedText{Tabs: []SubmittedTab{{
		ID: "transcript-tab", Role: TabRoleTranscript, Text: citedText,
	}}}
	output := validExtraction()
	output.People[0].Surface = "Ann"
	output.Citations[0] = Citation{
		ID: "citation-1", TabID: "transcript-tab", StartOffset: 0,
		EndOffset: len(citedText), Quote: citedText,
	}

	err := ValidateExtraction(submitted, output)
	if err == nil || !strings.Contains(err.Error(), "grounded") {
		t.Fatalf("ValidateExtraction() error = %v, want partial hyphenated identity rejection", err)
	}
}

func TestValidateExtractionRejectsPersonEmailAbsentFromCitedEvidence(t *testing.T) {
	output := validExtraction()
	output.People[0].Email = "invented@example.test"

	err := ValidateExtraction(validSubmittedText(), output)
	if err == nil || !strings.Contains(err.Error(), "email") {
		t.Fatalf("ValidateExtraction() error = %v, want ungrounded email rejection", err)
	}
}

func TestValidateExtractionAcceptsNormalizedEmailExactlyPresentInCitedEvidence(t *testing.T) {
	const citedText = "speaker-a <speaker@example.test> assigned"
	submitted := SubmittedText{Tabs: []SubmittedTab{{
		ID: "transcript-tab", Role: TabRoleTranscript,
		Text: citedText + " the follow-up.",
	}}}
	output := validExtraction()
	output.People[0].Email = "SPEAKER@EXAMPLE.TEST"
	output.Citations[0] = Citation{
		ID: "citation-1", TabID: "transcript-tab", StartOffset: 0,
		EndOffset: len(citedText), Quote: citedText,
	}

	if err := ValidateExtraction(submitted, output); err != nil {
		t.Fatalf("ValidateExtraction() error = %v", err)
	}
}

func TestValidateExtractionAcceptsExactEmailBeforeSentencePeriod(t *testing.T) {
	const citedText = "speaker-a emailed speaker@example.test."
	submitted := SubmittedText{Tabs: []SubmittedTab{{
		ID: "transcript-tab", Role: TabRoleTranscript, Text: citedText,
	}}}
	output := validExtraction()
	output.People[0].Email = "speaker@example.test"
	output.Citations[0] = Citation{
		ID: "citation-1", TabID: "transcript-tab", StartOffset: 0,
		EndOffset: len(citedText), Quote: citedText,
	}

	if err := ValidateExtraction(submitted, output); err != nil {
		t.Fatalf("ValidateExtraction() error = %v, want exact email token before punctuation", err)
	}
}

func TestValidateExtractionRejectsCitationInsideUTF8CodePoint(t *testing.T) {
	text := SubmittedText{Tabs: []SubmittedTab{{
		ID: "transcript-tab", Role: TabRoleTranscript, Text: "é assigned",
	}}}
	output := validExtraction()
	output.Citations[0] = Citation{
		ID: "citation-1", TabID: "transcript-tab", StartOffset: 1,
		EndOffset: len("é assigned"), Quote: "\xa9 assigned",
	}

	err := ValidateExtraction(text, output)
	if err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("ValidateExtraction() error = %v, want UTF-8 boundary rejection", err)
	}
}

func TestValidateExtractionRejectsNotesOnlySignal(t *testing.T) {
	output := validExtraction()
	output.Citations[0] = Citation{
		ID: "citation-1", TabID: "notes-tab", StartOffset: 0,
		EndOffset: len("secondary"), Quote: "secondary",
	}
	output.People[0].Surface = "secondary"

	err := ValidateExtraction(validSubmittedText(), output)
	if err == nil || !strings.Contains(err.Error(), "transcript") {
		t.Fatalf("ValidateExtraction() error = %v, want notes-only rejection", err)
	}
}

func TestValidateExtractionRejectsUnknownEnums(t *testing.T) {
	tests := map[string]func(*ExtractionOutput){
		"mention role": func(output *ExtractionOutput) { output.People[0].Role = "owner" },
		"category":     func(output *ExtractionOutput) { output.Signals[0].Category = "sentiment" },
		"direction":    func(output *ExtractionOutput) { output.Signals[0].Direction = "negative" },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			output := validExtraction()
			mutate(&output)
			if err := ValidateExtraction(validSubmittedText(), output); err == nil {
				t.Fatal("ValidateExtraction() error = nil, want enum rejection")
			}
		})
	}
}

func TestValidateExtractionRejectsUnknownReferences(t *testing.T) {
	tests := map[string]func(*ExtractionOutput){
		"person citation":    func(output *ExtractionOutput) { output.People[0].CitationIDs[0] = "missing" },
		"statement speaker":  func(output *ExtractionOutput) { output.Statements[0].SpeakerMentionID = "missing" },
		"statement subject":  func(output *ExtractionOutput) { output.Statements[0].SubjectMentionID = "missing" },
		"statement citation": func(output *ExtractionOutput) { output.Statements[0].CitationIDs[0] = "missing" },
		"signal subject":     func(output *ExtractionOutput) { output.Signals[0].SubjectMentionID = "missing" },
		"signal statement":   func(output *ExtractionOutput) { output.Signals[0].StatementIDs[0] = "missing" },
		"signal citation":    func(output *ExtractionOutput) { output.Signals[0].SupportingCitationIDs[0] = "missing" },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			output := validExtraction()
			mutate(&output)
			err := ValidateExtraction(validSubmittedText(), output)
			if err == nil || !strings.Contains(err.Error(), "unknown reference") {
				t.Fatalf("ValidateExtraction() error = %v, want unknown reference rejection", err)
			}
		})
	}
}

func TestValidateExtractionRequiresSpeakerTypedReference(t *testing.T) {
	output := validExtraction()
	output.People[0].Role = MentionRoleReference

	err := ValidateExtraction(validSubmittedText(), output)
	if err == nil || !strings.Contains(err.Error(), "speaker") {
		t.Fatalf("ValidateExtraction() error = %v, want typed speaker reference rejection", err)
	}
}

func TestValidateExtractionRejectsInvalidDates(t *testing.T) {
	tests := map[string]func(*ExtractionOutput){
		"meeting date":   func(output *ExtractionOutput) { output.MeetingDate = "2026-02-30" },
		"statement date": func(output *ExtractionOutput) { output.Statements[0].ValidDate = "21 July 2026" },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			output := validExtraction()
			mutate(&output)
			if err := ValidateExtraction(validSubmittedText(), output); err == nil {
				t.Fatal("ValidateExtraction() error = nil, want invalid date rejection")
			}
		})
	}
}

func TestValidateExtractionRejectsNonFiniteConfidence(t *testing.T) {
	for _, confidence := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		output := validExtraction()
		output.Signals[0].Confidence = confidence
		if err := ValidateExtraction(validSubmittedText(), output); err == nil {
			t.Fatalf("ValidateExtraction() error = nil for confidence %v", confidence)
		}
	}
}

func TestValidateExtractionRejectsDuplicateSignalIDs(t *testing.T) {
	output := validExtraction()
	output.Signals = append(output.Signals, output.Signals[0])
	if err := ValidateExtraction(validSubmittedText(), output); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("ValidateExtraction() error = %v, want duplicate signal ID rejection", err)
	}
}

func TestValidateExtractionRejectsWhitespacePaddedModelLocalIdentifiers(t *testing.T) {
	tests := map[string]func(*ExtractionOutput){
		"citation ID": func(output *ExtractionOutput) {
			output.Citations[0].ID = " citation-1"
			output.People[0].CitationIDs[0] = " citation-1"
			output.Statements[0].CitationIDs[0] = " citation-1"
			output.Signals[0].SupportingCitationIDs[0] = " citation-1"
		},
		"citation tab reference": func(output *ExtractionOutput) { output.Citations[0].TabID = "transcript-tab " },
		"person ID": func(output *ExtractionOutput) {
			output.People[0].ID = " mention-a"
			output.Statements[0].SpeakerMentionID = " mention-a"
			output.Statements[0].SubjectMentionID = " mention-a"
			output.Signals[0].SubjectMentionID = " mention-a"
			output.Signals[0].ObjectMentionID = " mention-a"
		},
		"person citation reference": func(output *ExtractionOutput) { output.People[0].CitationIDs[0] = "citation-1 " },
		"statement ID": func(output *ExtractionOutput) {
			output.Statements[0].ID = "statement-1 "
			output.Signals[0].StatementIDs[0] = "statement-1 "
		},
		"statement speaker reference":  func(output *ExtractionOutput) { output.Statements[0].SpeakerMentionID = " mention-a" },
		"statement subject reference":  func(output *ExtractionOutput) { output.Statements[0].SubjectMentionID = "mention-a " },
		"statement citation reference": func(output *ExtractionOutput) { output.Statements[0].CitationIDs[0] = " citation-1" },
		"signal ID":                    func(output *ExtractionOutput) { output.Signals[0].ID = " signal-1" },
		"signal subject reference":     func(output *ExtractionOutput) { output.Signals[0].SubjectMentionID = "mention-a " },
		"signal object reference":      func(output *ExtractionOutput) { output.Signals[0].ObjectMentionID = " mention-a" },
		"signal statement reference":   func(output *ExtractionOutput) { output.Signals[0].StatementIDs[0] = "statement-1 " },
		"signal supporting evidence":   func(output *ExtractionOutput) { output.Signals[0].SupportingCitationIDs[0] = " citation-1" },
		"signal contradicting evidence": func(output *ExtractionOutput) {
			output.Signals[0].ContradictingCitationIDs = []string{"citation-1 "}
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			output := validExtraction()
			mutate(&output)
			err := ValidateExtraction(validSubmittedText(), output)
			if err == nil || !strings.Contains(err.Error(), "whitespace") {
				t.Fatalf("ValidateExtraction() error = %v, want padded identifier rejection", err)
			}
		})
	}
}

func validSubmittedText() SubmittedText {
	return SubmittedText{Tabs: []SubmittedTab{
		{ID: "transcript-tab", Role: TabRoleTranscript, Text: submittedTranscript},
		{ID: "notes-tab", Role: TabRoleNotes, Text: "secondary summary"},
	}}
}

func validExtraction() ExtractionOutput {
	return ExtractionOutput{
		MeetingDate: "2026-07-21",
		Citations: []Citation{{
			ID: "citation-1", TabID: "transcript-tab", StartOffset: 0,
			EndOffset: len("speaker-a assigned"), Quote: "speaker-a assigned",
		}},
		People: []PersonMention{{
			ID: "mention-a", Surface: "speaker-a", Role: MentionRoleSpeaker,
			CitationIDs: []string{"citation-1"},
		}},
		Statements: []AttributedStatement{{
			ID: "statement-1", SpeakerMentionID: "mention-a", SubjectMentionID: "mention-a",
			Predicate: "assigned", ObjectText: "follow-up", ValidDate: "2026-07-21",
			CitationIDs: []string{"citation-1"},
		}},
		Signals: []InteractionSignal{{
			ID: "signal-1", SubjectMentionID: "mention-a", ObjectMentionID: "mention-a",
			StatementIDs: []string{"statement-1"}, Category: SignalCategoryFutureResponsibility,
			Direction: SignalDirectionStrengthening, Rationale: "assignment is explicit", Confidence: 0.8,
			SupportingCitationIDs: []string{"citation-1"},
		}},
	}
}

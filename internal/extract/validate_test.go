package extract

import (
	"math"
	"strings"
	"testing"
)

const submittedTranscript = "speaker-a assigned the follow-up."

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

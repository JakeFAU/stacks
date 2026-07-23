package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"stacks/internal/analysis"
)

func TestAnalyzeCommandRendersBoundedCitedReport(t *testing.T) {
	earlier := time.Date(2026, time.June, 3, 0, 0, 0, 0, time.UTC)
	later := time.Date(2026, time.July, 8, 0, 0, 0, 0, time.UTC)
	unknown := analysis.Signal{
		ID: "signal-unknown", Category: analysis.CategorySupportAdvocacy,
		Direction: analysis.DirectionUnclear, Rationale: "Source time is unavailable.",
	}
	counter := reportSignal("signal-counter", earlier, analysis.DirectionStrengthening, analysis.CitationContradicting)
	report := analysis.Report{
		Status:      analysis.StatusMixedOrConflicting,
		Rationale:   "Observable changes point in more than one direction.",
		Limitations: []string{"This report does not claim private mental state."},
		Chronology: []analysis.Signal{
			counter,
			reportSignal("signal-later", later, analysis.DirectionWeakening, analysis.CitationSupporting),
		},
		UnknownTime:     []analysis.Signal{unknown},
		Counterevidence: []analysis.Signal{counter},
		Gaps:            []string{"No observations outside scheduled meetings."},
	}
	service := &fakeAnalyzer{report: report}
	var output bytes.Buffer
	command := AnalyzeCommand{
		Service: service, EmployeeID: "employee-id", ManagerID: "manager-id", Output: &output,
	}

	if err := command.Run(context.Background(), nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if service.employeeID != "employee-id" || service.managerID != "manager-id" {
		t.Fatalf("configured pair = %q/%q", service.employeeID, service.managerID)
	}
	rendered := output.String()
	for _, required := range []string{
		"Conclusion: mixed or conflicting signals",
		"Observable changes point in more than one direction.",
		"Limitations:",
		"Chronology:",
		"2026-06-03",
		"2026-07-08",
		"Counterevidence:",
		"Unknown time:",
		"Gaps:",
		"https://docs.google.com/document/d/synthetic/edit?tab=transcript-tab",
		"offsets=4:13",
		`quote="Synthetic"`,
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered report is missing %q:\n%s", required, rendered)
		}
	}
}

func TestAnalyzeCommandRendersExplicitEmptyCounterevidenceAndGaps(t *testing.T) {
	var output bytes.Buffer
	command := AnalyzeCommand{
		Service:    &fakeAnalyzer{report: analysis.Report{Status: analysis.StatusInsufficientEvidence}},
		EmployeeID: "employee-id", ManagerID: "manager-id", Output: &output,
	}

	if err := command.Run(context.Background(), nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := strings.Count(output.String(), "(none)"); got < 2 {
		t.Fatalf("empty sections marked none %d times, want at least counterevidence and gaps:\n%s", got, output.String())
	}
}

func TestAnalyzeCommandRejectsArguments(t *testing.T) {
	command := AnalyzeCommand{Service: &fakeAnalyzer{}, EmployeeID: "employee-id", ManagerID: "manager-id"}
	if err := command.Run(context.Background(), []string{"unexpected"}); err == nil {
		t.Fatal("Run() error = nil, want analyze usage error")
	}
}

func reportSignal(id string, meeting time.Time, direction analysis.Direction, role analysis.CitationRole) analysis.Signal {
	return analysis.Signal{
		ID: id, Category: analysis.CategoryDelegationAutonomy, Direction: direction,
		ValidTime: &meeting, Rationale: "Synthetic observable rationale.", Confidence: 0.5,
		Citations: []analysis.Citation{{
			ID: id + "-citation", Locator: "https://docs.google.com/document/d/synthetic/edit?tab=transcript-tab",
			StartOffset: 4, EndOffset: 13, Quote: "Synthetic", Role: role,
		}},
	}
}

type fakeAnalyzer struct {
	report                analysis.Report
	employeeID, managerID string
}

func (service *fakeAnalyzer) Analyze(_ context.Context, employeeID, managerID string) (analysis.Report, error) {
	service.employeeID = employeeID
	service.managerID = managerID
	return service.report, nil
}

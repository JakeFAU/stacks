package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"stacks/internal/analysis"
)

// Analyzer produces one cited report for the explicitly configured pair.
type Analyzer interface {
	Analyze(context.Context, string, string) (analysis.Report, error)
}

// AnalyzeCommand renders private transcript evidence only to explicit stdout.
type AnalyzeCommand struct {
	Service    Analyzer
	EmployeeID string
	ManagerID  string
	Output     io.Writer
}

// Run executes `analyze` without positional arguments.
func (command AnalyzeCommand) Run(ctx context.Context, _ Invocation) error {
	if command.Service == nil {
		return fmt.Errorf("analyze command: service is not configured")
	}
	report, err := command.Service.Analyze(ctx, command.EmployeeID, command.ManagerID)
	if err != nil {
		return err
	}
	if !validAnalysisStatus(report.Status) {
		return fmt.Errorf("analyze command: report status is invalid")
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "Conclusion: %s\n", report.Status)
	if strings.TrimSpace(report.Rationale) != "" {
		fmt.Fprintf(&rendered, "Rationale: %s\n", report.Rationale)
	}
	renderStrings(&rendered, "Limitations", report.Limitations)
	renderSignals(&rendered, "Chronology", report.Chronology, true)
	renderSignals(&rendered, "Counterevidence", report.Counterevidence, true)
	renderSignals(&rendered, "Unknown time", report.UnknownTime, false)
	renderStrings(&rendered, "Gaps", report.Gaps)
	output := command.Output
	if output == nil {
		output = io.Discard
	}
	if _, err := io.WriteString(output, rendered.String()); err != nil {
		return fmt.Errorf("write analysis report: %w", err)
	}
	return nil
}

func renderStrings(output *strings.Builder, heading string, values []string) {
	fmt.Fprintf(output, "%s:\n", heading)
	if len(values) == 0 {
		output.WriteString("  (none)\n")
		return
	}
	for _, value := range values {
		fmt.Fprintf(output, "  - %s\n", value)
	}
}

func renderSignals(output *strings.Builder, heading string, signals []analysis.Signal, dated bool) {
	fmt.Fprintf(output, "%s:\n", heading)
	if len(signals) == 0 {
		output.WriteString("  (none)\n")
		return
	}
	for _, signal := range signals {
		date := "unknown"
		if dated && signal.ValidTime != nil {
			date = signal.ValidTime.UTC().Format(time.DateOnly)
		}
		fmt.Fprintf(output, "  - %s | %s | %s | confidence=%.3f\n", date, signal.Category, signal.Direction, signal.Confidence)
		if strings.TrimSpace(signal.Rationale) != "" {
			fmt.Fprintf(output, "    rationale: %s\n", signal.Rationale)
		}
		for _, citation := range signal.Citations {
			fmt.Fprintf(output, "    citation: %s offsets=%d:%d role=%s quote=%q\n",
				citation.Locator, citation.StartOffset, citation.EndOffset, citation.Role, citation.Quote)
		}
	}
}

func validAnalysisStatus(status analysis.ReportStatus) bool {
	switch status {
	case analysis.StatusInsufficientEvidence, analysis.StatusNoMaterialChange,
		analysis.StatusMixedOrConflicting, analysis.StatusPossibleDecline:
		return true
	default:
		return false
	}
}

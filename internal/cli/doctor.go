package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"stacks/internal/doctor"
)

// Doctor checks the configured read-only workflow dependencies.
type Doctor interface {
	Check(context.Context) doctor.Report
}

// DoctorCommand renders bounded doctor results without provider error text.
type DoctorCommand struct {
	Service Doctor
	Output  io.Writer
}

// Run executes `stacks doctor` without positional arguments.
func (command DoctorCommand) Run(ctx context.Context, args []string) error {
	if len(args) != 0 {
		return errors.New("doctor command usage: doctor")
	}
	if command.Service == nil {
		return errors.New("doctor command: service is not configured")
	}
	report := command.Service.Check(ctx)
	output := command.Output
	if output == nil {
		output = io.Discard
	}
	for _, check := range report.Checks {
		if _, err := fmt.Fprintf(output, "%s %s %s", check.Name, check.Status, check.Message); err != nil {
			return fmt.Errorf("write doctor check: %w", err)
		}
		if check.Remediation != "" {
			if _, err := fmt.Fprintf(output, " remediation=%s", check.Remediation); err != nil {
				return fmt.Errorf("write doctor remediation: %w", err)
			}
		}
		if _, err := fmt.Fprintln(output); err != nil {
			return fmt.Errorf("write doctor check: %w", err)
		}
	}
	if report.Err != nil {
		return report.Err
	}
	if !report.Healthy() {
		return errors.New("doctor checks failed")
	}
	return nil
}

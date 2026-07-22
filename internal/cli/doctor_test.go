package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"stacks/internal/doctor"
)

func TestDoctorRendersReportAndFailsOnlyForFailedChecks(t *testing.T) {
	tests := []struct {
		name    string
		report  doctor.Report
		wantErr bool
	}{
		{
			name: "warnings remain executable",
			report: doctor.Report{Checks: []doctor.Check{
				{Name: doctor.CheckDatabaseConnectivity, Status: doctor.StatusOK, Message: "PostgreSQL is reachable"},
				{Name: doctor.CheckInvocationLogging, Status: doctor.StatusWarning, Message: "unknown: invocation logging could not be inspected; do not assume it is disabled"},
			}},
		},
		{
			name: "failed checks return failure",
			report: doctor.Report{Checks: []doctor.Check{
				{Name: doctor.CheckGoogleAuthorization, Status: doctor.StatusFailed, Message: "Google authorization is missing, expired, or invalid", Remediation: "run `stacks auth google`"},
			}},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var output bytes.Buffer
			err := (DoctorCommand{Service: fakeDoctor{report: testCase.report}, Output: &output}).Run(context.Background(), nil)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("Run() error = %v, wantErr %t", err, testCase.wantErr)
			}
			for _, check := range testCase.report.Checks {
				if !strings.Contains(output.String(), string(check.Name)+" "+string(check.Status)+" "+check.Message) {
					t.Fatalf("output %q does not render check %#v", output.String(), check)
				}
				if check.Remediation != "" && !strings.Contains(output.String(), "remediation="+check.Remediation) {
					t.Fatalf("output %q does not render remediation", output.String())
				}
			}
		})
	}
}

func TestDoctorRejectsArgumentsAndPreservesCancellation(t *testing.T) {
	if err := (DoctorCommand{Service: fakeDoctor{}}).Run(context.Background(), []string{"unexpected"}); err == nil {
		t.Fatal("Run() error = nil, want usage error")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (DoctorCommand{Service: fakeDoctor{report: doctor.Report{Err: context.Canceled}}}).Run(ctx, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
}

type fakeDoctor struct {
	report doctor.Report
}

func (fake fakeDoctor) Check(context.Context) doctor.Report {
	return fake.report
}

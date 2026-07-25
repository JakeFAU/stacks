package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAuthCommandRunsOnlySelectedGoogleAuthorization(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		argument      string
		wantDrive     int
		wantDirectory int
		driveErr      error
		directoryErr  error
	}{
		{name: "Drive", argument: "google", wantDrive: 1, directoryErr: errors.New("private directory sentinel")},
		{name: "directory", argument: "google-directory", wantDirectory: 1, driveErr: errors.New("private drive sentinel")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			drive := &recordingAuthorizer{err: testCase.driveErr}
			directory := &recordingAuthorizer{err: testCase.directoryErr}
			command := AuthCommand{GoogleDrive: drive, GoogleDirectory: directory}

			err := command.Run(context.Background(), []string{testCase.argument})
			if err != nil {
				t.Fatalf("Run() error = %v, want selected authorizer success", err)
			}
			if drive.calls != testCase.wantDrive || directory.calls != testCase.wantDirectory {
				t.Fatalf("Authorize() calls = drive:%d directory:%d, want drive:%d directory:%d", drive.calls, directory.calls, testCase.wantDrive, testCase.wantDirectory)
			}
		})
	}
}

func TestAuthCommandRejectsInvalidTargetWithoutAuthorizing(t *testing.T) {
	drive := &recordingAuthorizer{err: errors.New("private drive sentinel")}
	directory := &recordingAuthorizer{err: errors.New("private directory sentinel")}
	command := AuthCommand{GoogleDrive: drive, GoogleDirectory: directory}

	err := command.Run(context.Background(), []string{"doctor"})
	if err == nil || err.Error() != "usage: stacks auth google | stacks auth google-directory" {
		t.Fatalf("Run() error = %v, want exact usage error", err)
	}
	if strings.Contains(err.Error(), "private") {
		t.Fatalf("Run() error exposed private sentinel: %v", err)
	}
	if drive.calls != 0 || directory.calls != 0 {
		t.Fatalf("Authorize() calls = drive:%d directory:%d, want neither", drive.calls, directory.calls)
	}
}

type recordingAuthorizer struct {
	calls int
	err   error
}

func (authorizer *recordingAuthorizer) Authorize(context.Context) error {
	authorizer.calls++
	return authorizer.err
}

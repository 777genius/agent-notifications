package main

import (
	"io"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
)

type failedSummaryWriter struct{}

func (failedSummaryWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

type shortSummaryWriter struct{}

func (shortSummaryWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }

func TestWizardReportsFailedTextOutput(t *testing.T) {
	result := setupwizard.Result{Action: "install", Outcome: "completed"}
	for _, writer := range []io.Writer{failedSummaryWriter{}, shortSummaryWriter{}} {
		if got := writeSetupWizardResult(writer, false, result, nil); got != 1 {
			t.Fatalf("output failure reported success: exit=%d", got)
		}
	}
}

func TestWizardEarlyJSONSwitchSurvivesArgumentRejection(t *testing.T) {
	cases := [][]string{
		{"--json", "--agents", strings.Repeat("a", 4097)},
		{"--agents", "\x00", "--json"},
	}
	for _, args := range cases {
		if _, jsonOut, err := parseSetupWizard(args); err == nil || !jsonOut {
			t.Fatalf("early argument rejection lost JSON output: args count %d json=%t err=%v", len(args), jsonOut, err)
		}
	}
	tooMany := make([]string, 65)
	for i := range tooMany {
		tooMany[i] = "--yes"
	}
	tooMany[64] = "--json"
	if _, jsonOut, err := parseSetupWizard(tooMany); err == nil || !jsonOut {
		t.Fatalf("argument limit lost JSON output: json=%t err=%v", jsonOut, err)
	}
}

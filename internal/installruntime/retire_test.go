package installruntime

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
)

func TestRetirementScanRefusesIncompleteEvidence(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		contextErr, commandErr error
		output, diagnostic     string
	}{
		{name: "timeout", contextErr: context.DeadlineExceeded},
		{name: "cancel", contextErr: context.Canceled},
		{name: "scan failure", commandErr: errors.New("scan failed")},
		{name: "empty success"},
		{name: "mapped executable", output: "p123\nftxt\n"},
		{name: "open resource", output: "p123\nf4\n"},
		{name: "partial permissions", diagnostic: "permission denied"},
		{name: "output limit", commandErr: errors.New("native output exceeds limit")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := drainCommandResult(tc.contextErr, tc.commandErr, []byte(tc.output), []byte(tc.diagnostic)); err == nil {
				t.Fatal("incomplete drain proof accepted")
			}
		})
	}
}

func TestRetirementScanExitFixture(t *testing.T) {
	if os.Getenv("INSTALLRUNTIME_DRAIN_EXIT_FIXTURE") == "1" {
		os.Exit(1)
	}
	if os.Getenv("INSTALLRUNTIME_DRAIN_EXIT_FIXTURE") == "2" {
		os.Exit(2)
	}
}
func TestRetirementScanAcceptsOnlyNoMatchExit(t *testing.T) {
	for _, code := range []string{"1", "2"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestRetirementScanExitFixture$")
		cmd.Env = append(os.Environ(), "INSTALLRUNTIME_DRAIN_EXIT_FIXTURE="+code)
		output, err := cmd.CombinedOutput()
		if len(output) != 0 {
			t.Fatalf("unexpected fixture output: %s", output)
		}
		got := drainCommandResult(nil, err, nil, nil)
		if (got == nil) != (code == "1") {
			t.Fatalf("exit %s: %v", code, got)
		}
	}
}

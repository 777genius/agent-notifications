//go:build windows

package notifier

import (
	"bytes"
	"context"
	"encoding/base64"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestTemporaryPowerShellTraceScriptParses(t *testing.T) {
	powershell, err := systemWindowsPowerShell()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// AST construction only: none of the sender script or WinRT calls execute.
	parse := `$tokens=$null; $errors=$null; [Management.Automation.Language.Parser]::ParseInput([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($env:TEST_PI_PARSE_SCRIPT)),[ref]$tokens,[ref]$errors) | Out-Null; if ($errors.Count) { exit 1 }`
	cmd := exec.CommandContext(ctx, powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", parse)
	cmd.Env = append(nativeNotificationEnvironment(), "TEST_PI_PARSE_SCRIPT="+base64.StdEncoding.EncodeToString([]byte(windowsToastPowerShell)))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("temporary sender script failed AST-only parsing: %v %q", err, output)
	}
}

func TestTemporaryPowerShellTraceRejectsRawOrUnrecognizedStderr(t *testing.T) {
	valid := `{"type":"TEST_pi_native_trace","phase":"powershell_entered","channel":"desktop","elapsed_ms":4000,"remaining_ms":11000,"context":"active"}`
	for _, invalid := range []string{
		"PRIVATE_RAW_ERROR",
		strings.Replace(valid, "powershell_entered", "PRIVATE_PHASE", 1),
		strings.Replace(valid, `"desktop"`, `"webhook"`, 1),
		strings.Replace(valid, "4000", `"PRIVATE_TIME"`, 1),
		strings.Replace(valid, "11000", "15001", 1),
		strings.Replace(valid, "11000", "null", 1),
		strings.Replace(valid, "4000", "null", 1),
		strings.Replace(valid, `"context":"active"`, `"headers":"PRIVATE_HEADER"`, 1),
	} {
		trace := &testPowerShellTrace{}
		_, _ = trace.Write([]byte(invalid + "\n" + valid + "\n"))
		var output bytes.Buffer
		trace.forward(&output)
		if output.String() != valid+"\n" {
			t.Fatalf("unrecognized child stderr was forwarded: %q", output.String())
		}
	}
	trace := &testPowerShellTrace{}
	_, _ = trace.Write([]byte(strings.Repeat("PRIVATE", 2000) + "\n" + valid))
	var output bytes.Buffer
	trace.forward(&output)
	if len(trace.data) != 8192 || output.Len() != 0 {
		t.Fatal("child stderr prefix was not bounded")
	}
}

//go:build windows

package notifier

import (
	"bytes"
	"strings"
	"testing"
)

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

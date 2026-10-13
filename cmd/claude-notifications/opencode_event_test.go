package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestOpenCodeCommandFailsClosedWithBoundedReceipt(t *testing.T) {
	const secret = "PRIVATE_SESSION_811"
	frame := `{"version":1,"kind":"turn_idle_verified","sessionID":"` + secret + `","turnID":"t","messageID":"m","rootSession":true}`
	var output bytes.Buffer
	if code := runOpenCodeEvent([]string{"--protocol", "1"}, io.NopCloser(strings.NewReader(frame)), &output); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if got := output.String(); got != "{\"status\":\"suppressed\",\"reason\":\"not_registered\"}\n" || strings.Contains(got, secret) {
		t.Fatalf("unexpected receipt: %q", got)
	}
}

func TestOpenCodeCommandRequiresExactProtocolArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"--protocol", "2"}, {"--protocol", "1", "extra"}, {"--other", "1"}} {
		var output bytes.Buffer
		if code := runOpenCodeEvent(args, strings.NewReader("{}"), &output); code != 0 {
			t.Fatalf("args %v: exit code %d", args, code)
		}
		if got := output.String(); got != "{\"status\":\"rejected\",\"reason\":\"invalid_command\"}\n" {
			t.Fatalf("args %v: receipt %q", args, got)
		}
	}
}

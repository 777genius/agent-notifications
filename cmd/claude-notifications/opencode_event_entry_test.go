package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestOpenCodeCommandRequiresClosableInputBeforeConsumption(t *testing.T) {
	t.Setenv("AGENT_NOTIFICATIONS_CONTROL_ROOT", t.TempDir())
	input := strings.NewReader(`{"protocol":1}`)
	var output bytes.Buffer
	if code := runOpenCodeEvent([]string{"--protocol", "1"}, input, &output); code != 0 {
		t.Fatal("content-free refusal broke command transport", code)
	}
	if output.String() != "{\"status\":\"rejected\",\"reason\":\"invalid_frame\"}\n" || input.Len() != len(`{"protocol":1}`) {
		t.Fatal("unclosable input was consumed or authorized", output.String())
	}
}

func TestOpenCodeCommandEarlyGuardsDoNotConsumeInput(t *testing.T) {
	for _, tc := range []struct {
		name, root, receipt string
		args                []string
	}{
		{"argv", "/", "{\"status\":\"rejected\",\"reason\":\"invalid_command\"}\n", []string{"--protocol", "1", "extra"}},
		{"root", "relative", "{\"status\":\"suppressed\",\"reason\":\"not_registered\"}\n", []string{"--protocol", "1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AGENT_NOTIFICATIONS_CONTROL_ROOT", tc.root)
			input := strings.NewReader("input must remain available")
			var output bytes.Buffer
			if runOpenCodeEvent(tc.args, input, &output) != 0 || output.String() != tc.receipt || input.Len() != len("input must remain available") {
				t.Fatal("early guard read unsafe input or leaked receipt", output.String())
			}
		})
	}
}

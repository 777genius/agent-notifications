// Package qodersource isolates Qoder SDK wire decoding from notification policy.
package qodersource

import (
	"context"
	"fmt"
	pluginkitai "github.com/777genius/plugin-kit-ai/sdk"
	"github.com/777genius/plugin-kit-ai/sdk/qoder"
)

const MaxPayloadBytes = pluginkitai.MaxPayloadBytes

type StopData struct {
	SessionID, CWD, LastAssistantMessage string
	StopHookActive                       bool
}

type capturedIO struct{ payload []byte }

func (b capturedIO) ReadStdin(ctx context.Context) ([]byte, error) {
	return append([]byte(nil), b.payload...), ctx.Err()
}
func (capturedIO) WriteStdout([]byte) error { return nil }
func (capturedIO) WriteStderr(string) error { return nil }

type emptyEnv struct{}

func (emptyEnv) LookupEnv(string) (string, bool) { return "", false }

// Decode accepts only the declared Stop notification route. SDK responses are
// captured: notifications must never inject hook output or block Qoder.
func Decode(ctx context.Context, publicEvent string, payload []byte) (StopData, error) {
	if publicEvent != "Stop" {
		return StopData{}, fmt.Errorf("unsupported qoder event %q", publicEvent)
	}
	if len(payload) > MaxPayloadBytes {
		return StopData{}, fmt.Errorf("qoder payload exceeds limit")
	}
	app := pluginkitai.New(pluginkitai.Config{Name: "agent-notifications", Args: []string{"agent-notifications", "stop"}, IO: capturedIO{payload}, Env: emptyEnv{}})
	var out StopData
	called := false
	app.Qoder().OnStop(func(e *qoder.StopEvent) *qoder.StopResponse {
		if e.HookEventName != "Stop" {
			return qoder.StopAllow()
		}
		called = true
		out = StopData{SessionID: e.SessionID, CWD: e.CWD, LastAssistantMessage: e.LastAssistantMessage, StopHookActive: e.StopHookActive}
		return qoder.StopAllow()
	})
	if code := app.RunContext(ctx); code != 0 || !called {
		return StopData{}, fmt.Errorf("qoder SDK decode failed")
	}
	return out, nil
}

// Package copilotvscodesource is the only Local SDK boundary. It discards all
// native content except validated observation facts; SDK diagnostics stay private.
package copilotvscodesource

import (
	"bytes"
	"context"
	"errors"
	"time"
	"unicode"
	"unicode/utf8"

	pluginkitai "github.com/777genius/plugin-kit-ai/sdk"
	"github.com/777genius/plugin-kit-ai/sdk/vscodelocal"
)

const Stop = "Stop"
const MaxPayloadBytes = pluginkitai.MaxPayloadBytes

// Facts has no prompt, tool, path, transcript, control output or invented turn ID.
// SessionID is optional and is used only inside the private deduplication key.
type Facts struct {
	Event, SessionID, Timestamp         string
	StopHookActiveKnown, StopHookActive bool
}

// Valid allows an absent session, but requires a valid native event/timestamp.
// Boolean presence/value are preserved; the consumer selects explicit false.
func (f Facts) Valid() bool {
	if f.Event != Stop || !validSession(f.SessionID) || len(f.Timestamp) > 128 {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, f.Timestamp)
	return err == nil
}
func validSession(s string) bool {
	if len(s) > 1024 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

type bufferedIO struct {
	payload        []byte
	stdout, stderr bytes.Buffer
}

func (b *bufferedIO) ReadStdin(ctx context.Context) ([]byte, error) {
	return append([]byte(nil), b.payload...), ctx.Err()
}
func (b *bufferedIO) WriteStdout(p []byte) error { _, err := b.stdout.Write(p); return err }
func (b *bufferedIO) WriteStderr(p string) error { _, err := b.stderr.WriteString(p); return err }

type emptyEnv struct{}

func (emptyEnv) LookupEnv(string) (string, bool) { return "", false }

// Decode invokes the public typed observer with bounded in-memory IO and empty
// environment. No SDK type, raw diagnostics or native response leaves this API.
func Decode(ctx context.Context, selector string, payload []byte) (facts Facts, err error) {
	err = errors.New("invalid_frame")
	defer func() {
		if recover() != nil {
			facts = Facts{}
			err = errors.New("invalid_frame")
		}
	}()
	if selector != Stop || ctx.Err() != nil || len(payload) == 0 || len(payload) > MaxPayloadBytes || !utf8.Valid(payload) {
		return facts, err
	}
	input := &bufferedIO{payload: payload}
	app := pluginkitai.New(pluginkitai.Config{Name: "agent-notifications", Args: []string{"agent-notifications", "VSCodeLocalStop"}, IO: input, Env: emptyEnv{}})
	app.VSCodeLocal().OnStop(func(e *vscodelocal.StopEvent) *vscodelocal.StopResponse {
		if e.HookEventName == Stop {
			facts = Facts{Event: Stop, SessionID: e.SessionID, Timestamp: e.Timestamp, StopHookActiveKnown: e.StopHookActive != nil}
			if e.StopHookActive != nil {
				facts.StopHookActive = *e.StopHookActive
			}
		}
		return &vscodelocal.StopResponse{}
	})
	if app.RunContext(ctx) != 0 || ctx.Err() != nil || !facts.Valid() {
		return Facts{}, err
	}
	return facts, nil
}

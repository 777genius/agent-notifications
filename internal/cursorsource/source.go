// Package cursorsource is the Cursor public SDK boundary. Native content and
// SDK diagnostics remain private; only bounded observation facts leave Decode.
package cursorsource

import (
	"bytes"
	"context"
	"errors"
	"unicode"
	"unicode/utf8"

	pluginkitai "github.com/777genius/plugin-kit-ai/sdk"
	"github.com/777genius/plugin-kit-ai/sdk/cursor"
)

const Stop = "stop"
const Selector = "CursorStop"
const MaxPayloadBytes = pluginkitai.MaxPayloadBytes

// Facts carries only private native identity and stopping observations. It has
// no task-success interpretation, timestamp, invented turn ID or native content.
type Facts struct {
	ConversationID, GenerationID, Event, Status string
	LoopCountKnown                              bool
	LoopCount                                   int64
}

// Valid preserves unknown status for the consumer to suppress. Both native IDs
// are mandatory; nil loop count remains distinguishable from explicit zero.
func (f Facts) Valid() bool {
	return f.Event == Stop && validID(f.ConversationID) && validID(f.GenerationID) && f.LoopCount >= 0 && f.LoopCount <= 2147483647
}

func validID(s string) bool {
	if s == "" || len(s) > 256 || !utf8.ValidString(s) {
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

// Decode calls the pinned public typed observer with buffered IO and empty env.
// The observer's neutral exit code alone is not proof of a decoded event.
func Decode(ctx context.Context, selector string, payload []byte) (facts Facts, err error) {
	err = errors.New("invalid_frame")
	defer func() {
		if recover() != nil {
			facts, err = Facts{}, errors.New("invalid_frame")
		}
	}()
	if selector != Selector || ctx.Err() != nil || len(payload) == 0 || len(payload) > MaxPayloadBytes || !utf8.Valid(payload) {
		return facts, err
	}
	input := &bufferedIO{payload: payload}
	app := pluginkitai.New(pluginkitai.Config{Name: "agent-notifications", Args: []string{"agent-notifications", Selector}, IO: input, Env: emptyEnv{}})
	observed := false
	app.Cursor().OnStop(func(e *cursor.StopEvent) *cursor.StopResponse {
		observed = true
		facts = Facts{ConversationID: e.ConversationID, GenerationID: e.GenerationID, Event: e.HookEventName, Status: string(e.Status), LoopCountKnown: e.LoopCount != nil}
		if e.LoopCount != nil {
			facts.LoopCount = int64(*e.LoopCount)
		}
		return &cursor.StopResponse{}
	})
	if app.RunCursorObserver(ctx) != 0 || ctx.Err() != nil || !observed || !facts.Valid() {
		return Facts{}, err
	}
	return facts, nil
}

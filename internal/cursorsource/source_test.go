package cursorsource_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"runtime"
	"strings"
	"testing"
	"unicode/utf16"

	source "github.com/777genius/agent-notifications/internal/cursorsource"
	pluginkitai "github.com/777genius/plugin-kit-ai/sdk"
	"github.com/777genius/plugin-kit-ai/sdk/cursor"
)

const frame = `{"hook_event_name":"stop","conversation_id":"TEST-conversation-秘密","generation_id":"TEST-generation","status":"completed","workspace_roots":["TEST-private-workspace"],"model":"TEST-private-model","model_params":[{"id":"TEST-private-param","value":"TEST-private-value"}],"user_email":"TEST-private-email","transcript_path":"TEST-private-transcript","prompt":"TEST-private-prompt"}`

type sdkIO struct {
	payload        []byte
	stdout, stderr bytes.Buffer
}

func (b *sdkIO) ReadStdin(ctx context.Context) ([]byte, error) { return b.payload, ctx.Err() }
func (b *sdkIO) WriteStdout(p []byte) error                    { _, err := b.stdout.Write(p); return err }
func (b *sdkIO) WriteStderr(p string) error                    { _, err := b.stderr.WriteString(p); return err }

type noEnv struct{}

func (noEnv) LookupEnv(string) (string, bool) { return "", false }

// Failure caught: an SDK API/codec change, status coercion, nil-to-zero collapse,
// or native private fields being copied into business facts.
func TestPublicObserverAndMinimalFactsContract(t *testing.T) {
	for _, status := range []string{"completed", "aborted", "error", "future", "", "missing", "null"} {
		for _, count := range []string{"missing", "null", "0", "2147483647"} {
			t.Run(status+"/"+count, func(t *testing.T) {
				input := strings.Replace(frame, `"completed"`, `"`+status+`"`, 1)
				wantStatus := status
				switch status {
				case "missing":
					input = strings.Replace(input, `"status":"missing",`, "", 1)
					wantStatus = ""
				case "null":
					input = strings.Replace(input, `"status":"null"`, `"status":null`, 1)
					wantStatus = ""
				}
				if count != "missing" {
					input = strings.TrimSuffix(input, "}") + `,"loop_count":` + count + "}"
				}
				io := &sdkIO{payload: []byte(input)}
				app := pluginkitai.New(pluginkitai.Config{Args: []string{"TEST", source.Selector}, IO: io, Env: noEnv{}})
				calls := 0
				app.Cursor().OnStop(func(e *cursor.StopEvent) *cursor.StopResponse {
					calls++
					if e.ConversationID != "TEST-conversation-秘密" || e.GenerationID != "TEST-generation" || e.HookEventName != "stop" || string(e.Status) != wantStatus {
						t.Fatalf("public typed identity/status changed: %+v", e)
					}
					known := count == "0" || count == "2147483647"
					if (e.LoopCount != nil) != known || (known && int64(*e.LoopCount) != expectedCount(count)) {
						t.Fatal("public typed loop presence/value changed")
					}
					if e.UserEmail == nil || *e.UserEmail != "TEST-private-email" || len(e.ModelParams) != 1 {
						t.Fatal("fixture no longer exercises private public DTO fields")
					}
					return &cursor.StopResponse{}
				})
				if app.RunCursorObserver(context.Background()) != 0 || calls != 1 || io.stdout.String() != "{}\n" || io.stderr.Len() != 0 {
					t.Fatal("public observer no longer observes once with neutral output")
				}
				got, err := source.Decode(context.Background(), source.Selector, []byte(input))
				want := source.Facts{ConversationID: "TEST-conversation-秘密", GenerationID: "TEST-generation", Event: "stop", Status: wantStatus, LoopCountKnown: count == "0" || count == "2147483647", LoopCount: expectedCount(count)}
				if err != nil || got != want || !got.Valid() {
					t.Fatalf("minimal facts = %+v/%v, want %+v", got, err, want)
				}
			})
		}
	}
}

func expectedCount(s string) int64 {
	if s == "2147483647" {
		return 2147483647
	}
	return 0
}

// Failure caught: the neutral SDK exit code admits malformed input without a
// typed callback, selectors/events alias, or bounds leak raw SDK diagnostics.
func TestDecodeRejectsInvalidPublicFramesAndCancellation(t *testing.T) {
	inputs := []string{"", "{", frame + "{}", strings.Repeat("x", source.MaxPayloadBytes+1), strings.Replace(frame, "TEST-generation", "\xff", 1)}
	for _, field := range []string{"conversation_id", "generation_id"} {
		for _, value := range []string{`""`, `null`, `7`, `"bad\u0000"`, `"bad\u007f"`, `"` + strings.Repeat("é", 129) + `"`, `"` + strings.Repeat("x", 257) + `"`} {
			old := `"` + field + `":"TEST-generation"`
			if field == "conversation_id" {
				old = `"conversation_id":"TEST-conversation-秘密"`
			}
			inputs = append(inputs, strings.Replace(frame, old, `"`+field+`":`+value, 1), strings.Replace(frame, old+",", "", 1))
		}
	}
	for _, count := range []string{"-1", "2147483648", "0.5", `"0"`, "true"} {
		inputs = append(inputs, strings.TrimSuffix(frame, "}")+`,"loop_count":`+count+"}")
	}
	for _, event := range []string{"Stop", "AfterAgent", "", "Notification"} {
		inputs = append(inputs, strings.Replace(frame, `"stop"`, `"`+event+`"`, 1))
	}
	inputs = append(inputs, strings.Replace(frame, `"status":"completed"`, `"status":7`, 1))
	for i, input := range inputs {
		f, err := source.Decode(context.Background(), source.Selector, []byte(input))
		if err == nil || err.Error() != "invalid_frame" || f != (source.Facts{}) {
			t.Fatalf("invalid case %d admitted or exposed diagnostics: %+v/%v", i, f, err)
		}
	}
	for _, selector := range []string{"stop", "Stop", "VSCodeLocalStop", ""} {
		if f, err := source.Decode(context.Background(), selector, []byte(frame)); err == nil || f != (source.Facts{}) {
			t.Fatalf("selector %q admitted", selector)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if f, err := source.Decode(ctx, source.Selector, []byte(frame)); err == nil || f != (source.Facts{}) {
		t.Fatal("cancelled decode admitted")
	}
	// Bounds count UTF-8 bytes, not runes; the exact 256-byte boundary is valid.
	input := strings.Replace(frame, "TEST-conversation-秘密", strings.Repeat("é", 128), 1)
	if f, err := source.Decode(context.Background(), source.Selector, []byte(input)); err != nil || len(f.ConversationID) != 256 {
		t.Fatalf("exact ID bound rejected: %v", err)
	}
}

// Native Windows hook transport emits UTF-8 BOM; do not accept repeated BOM or UTF-16.
func TestQualifiedWindowsPowerShellBOM(t *testing.T) {
	for _, encoding := range []struct {
		name  string
		order binary.ByteOrder
		bom   []byte
	}{
		{"UTF16LE", binary.LittleEndian, []byte{0xff, 0xfe}},
		{"UTF16BE", binary.BigEndian, []byte{0xfe, 0xff}},
	} {
		t.Run(encoding.name, func(t *testing.T) {
			input := append([]byte(nil), encoding.bom...)
			for _, unit := range utf16.Encode([]rune(frame)) {
				var pair [2]byte
				encoding.order.PutUint16(pair[:], unit)
				input = append(input, pair[:]...)
			}
			if _, err := source.Decode(context.Background(), source.Selector, input); err == nil {
				t.Fatal("UTF16 frame accepted")
			}
		})
	}

	for _, tc := range []struct {
		name     string
		prefix   []byte
		accepted bool
	}{
		{"plain", nil, true},
		{"one UTF8 BOM", []byte{0xef, 0xbb, 0xbf}, runtime.GOOS == "windows"},
		{"two UTF8 BOM", []byte{0xef, 0xbb, 0xbf, 0xef, 0xbb, 0xbf}, false},
		{"UTF16 BOM", []byte{0xff, 0xfe}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := append(append([]byte(nil), tc.prefix...), []byte(frame)...)
			facts, err := source.Decode(context.Background(), source.Selector, input)
			if (err == nil) != tc.accepted {
				t.Fatalf("accepted=%v want=%v error=%v", err == nil, tc.accepted, err)
			}
			if tc.accepted && (facts.ConversationID != "TEST-conversation-秘密" || facts.GenerationID != "TEST-generation") {
				t.Fatal("native IDs changed")
			}
		})
	}
}

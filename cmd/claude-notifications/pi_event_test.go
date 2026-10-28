package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/pievent"
	"github.com/777genius/agent-notifications/internal/pisource"
)

type piTestClock struct{}

func (piTestClock) Now() (string, float64, error) { return "TEST-Pi-boot", 100, nil }
func piInput(t *testing.T, pid int) []byte {
	t.Helper()
	root := t.TempDir()
	frame := map[string]any{"schema_version": 1, "event": "agent_settled", "observer_id": "170ff6e4-aa22-4cad-a5e8-2e5b7415b6bc", "observation_id": "416eb070-d117-4ddb-bd2e-a40fca7821b4", "session_id": "native-session", "cwd": root, "mode": "json", "pi_pid": pid, "loaded_revision": map[string]any{"projection_digest": "sha256:" + strings.Repeat("1", 64), "canonical_digest": "sha256:" + strings.Repeat("2", 64), "bootstrap_version": 1, "binding_id": "binding", "runtime_root": root, "primary_sha256": strings.Repeat("3", 64)}, "settled": map[string]any{"aborted": false, "boundary_outcome": "completed"}}
	data, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func TestPiEventTransportPinsParentAndNeutralOutput(t *testing.T) {
	selector := filepath.Join(t.TempDir(), "agent-notify-"+strings.Repeat("a", 64)+".json")
	for _, pid := range []int{os.Getppid(), os.Getppid() + 100000} {
		t.Run("pid", func(t *testing.T) {
			var output bytes.Buffer
			called := 0
			code := runPiEventWith(context.Background(), piTestClock{}, []string{"--binding", selector}, io.NopCloser(bytes.NewReader(piInput(t, pid))), &output, func(ctx context.Context, got string, f pisource.Facts) (pievent.Consumer, error) {
				called++
				if got != selector || f.Kind != pisource.Completed || ctx.Err() != nil {
					t.Fatal("native facts lost")
				}
				return pievent.Consumer{}, errors.New("test authority denied")
			})
			want := 0
			if pid == os.Getppid() {
				want = 1
			}
			if code != 0 || output.String() != "{}\n" || called != want {
				t.Fatalf("code=%d output=%q called=%d", code, output.String(), called)
			}
		})
	}
}
func TestPiEventTransportRejectsMalformedArgsAndBoundedInput(t *testing.T) {
	for _, args := range [][]string{nil, {"--binding", "relative"}, {"--binding", filepath.Join(t.TempDir(), "wrong.json")}, {"--binding", "x", "--binding", "y"}} {
		var out bytes.Buffer
		runPiEventWith(context.Background(), piTestClock{}, args, io.NopCloser(strings.NewReader("{}")), &out, func(context.Context, string, pisource.Facts) (pievent.Consumer, error) {
			t.Fatal("invalid argv reached authority")
			return pievent.Consumer{}, nil
		})
		if out.String() != "{}\n" {
			t.Fatal(out.String())
		}
	}
	reader, writer := io.Pipe()
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	var out bytes.Buffer
	runPiEventWith(ctx, piTestClock{}, []string{"--binding", filepath.Join(t.TempDir(), "agent-notify-"+strings.Repeat("a", 64)+".json")}, reader, &out, func(context.Context, string, pisource.Facts) (pievent.Consumer, error) {
		t.Fatal("open stdin reached authority")
		return pievent.Consumer{}, nil
	})
	if time.Since(started) > time.Second || out.String() != "{}\n" {
		t.Fatalf("open stdin escaped budget: %s", time.Since(started))
	}
}

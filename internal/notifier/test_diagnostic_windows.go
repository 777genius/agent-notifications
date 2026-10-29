//go:build windows

package notifier

// TEMPORARY DIAGNOSTIC ONLY. Never merge into the final product candidate.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"time"

	"golang.org/x/sys/windows"
)

func testToastDeadline(ctx context.Context) string {
	deadline, ok := ctx.Deadline()
	if !ok {
		return ""
	}
	ticks, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetTickCount64").Call()
	remaining := max(0, time.Until(deadline).Milliseconds())
	return strconv.FormatUint(uint64(uint32(uint64(ticks)+uint64(remaining))), 10)
}

type testPowerShellTrace struct{ data []byte }

func (t *testPowerShellTrace) Write(data []byte) (int, error) {
	retained := min(len(data), 8192-len(t.data))
	t.data = append(t.data, data[:retained]...)
	return len(data), nil
}

func (t *testPowerShellTrace) forward(output io.Writer) {
	for index, line := range bytes.Split(t.data, []byte{'\n'}) {
		if index >= 16 {
			break
		}
		var keys map[string]json.RawMessage
		if json.Unmarshal(line, &keys) != nil || len(keys) != 6 {
			continue
		}
		var row struct {
			Type        string `json:"type"`
			Phase       string `json:"phase"`
			Channel     string `json:"channel"`
			ElapsedMS   int64  `json:"elapsed_ms"`
			RemainingMS int64  `json:"remaining_ms"`
			Context     string `json:"context"`
		}
		complete := true
		for _, key := range []string{"type", "phase", "channel", "elapsed_ms", "remaining_ms", "context"} {
			if _, present := keys[key]; !present {
				complete = false
			}
		}
		if !complete || bytes.Equal(bytes.TrimSpace(keys["elapsed_ms"]), []byte("null")) ||
			bytes.Equal(bytes.TrimSpace(keys["remaining_ms"]), []byte("null")) ||
			json.Unmarshal(line, &row) != nil || row.Type != "TEST_pi_native_trace" || row.Channel != "desktop" ||
			row.ElapsedMS < 0 || row.ElapsedMS > 60000 || row.RemainingMS < 0 || row.RemainingMS > 15000 ||
			(row.Context != "active" && row.Context != "deadline_exceeded") {
			continue
		}
		switch row.Phase {
		case "powershell_entered", "winrt_ready", "xml_loaded", "toast_created", "notifier_created", "toast_shown", "toast_failed":
			_ = json.NewEncoder(output).Encode(row)
		}
	}
}

type testDesktopPhase string

const (
	testNativeStarted testDesktopPhase = "native_started"
	testFocusReady    testDesktopPhase = "focus_ready"
	testToastStarted  testDesktopPhase = "toast_started"
	testToastReturned testDesktopPhase = "toast_returned"
	testSoundStarted  testDesktopPhase = "sound_started"
	testSoundReturned testDesktopPhase = "sound_returned"
)

func traceTestDesktopPhase(ctx context.Context, phase testDesktopPhase) {
	defer func() { _ = recover() }()
	if ctx == nil {
		return
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return
	}
	// The actual Pi operation owns the unchanged 15-second deadline. Use its
	// monotonic context deadline here, without importing the Pi domain adapter.
	remaining := time.Until(deadline).Milliseconds()
	state := "active"
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		state = "deadline_exceeded"
	} else if ctx.Err() != nil {
		state = "cancelled"
	}
	row := struct {
		Type        string           `json:"type"`
		Phase       testDesktopPhase `json:"phase"`
		Channel     string           `json:"channel"`
		ElapsedMS   int64            `json:"elapsed_ms"`
		RemainingMS int64            `json:"remaining_ms"`
		Context     string           `json:"context"`
	}{"TEST_pi_native_trace", phase, "desktop", max(0, 15000-remaining), max(0, remaining), state}
	_ = json.NewEncoder(os.Stderr).Encode(row)
}

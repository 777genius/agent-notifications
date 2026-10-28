package installprogress

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestSkippedCursorKeepsTruthfulStructuredAndTextStatus(t *testing.T) {
	e, stop, err := parse("cursor\tcomplete\tskipped")
	if err != nil || stop || e.outcome != "skipped" {
		t.Fatalf("skipped fact refused: %+v %v", e, err)
	}
	if label := activityLabel(e); !strings.Contains(label, "Skipped") || strings.Contains(label, "restart") || strings.Contains(label, "Installed") {
		t.Fatal("skip became installation", label)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, sessionMode := range []bool{false, true} {
		var out bytes.Buffer
		path := eventFile(t, "cursor\tcomplete\tskipped\nstop\t\t\n")
		if sessionMode {
			path = eventFile(t, "snapshot\tcursor\tcomplete\tskipped\nstop\t\t\t\n")
			err = RunSession(ctx, path, Options{Output: &out})
		} else {
			err = Run(ctx, path, Options{Output: &out})
		}
		text := strings.ToLower(out.String())
		if err != nil || !strings.Contains(text, "skipped") || strings.Contains(text, "restart required") || strings.Contains(text, "installed") {
			t.Fatalf("session=%v skip presentation %q: %v", sessionMode, out.String(), err)
		}
	}
}

//go:build linux || darwin

package main

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestGeminiEventDiagnosticBlockedPipeRetainsAdmissionDeadline(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	// Fill a real pipe without a reader. The existing stdio adapter provides
	// deadline-capable owned I/O; no permanently blocked filler goroutine.
	filler, err := agentNotifyFile(writer)
	if err != nil {
		t.Fatal(err)
	}
	if err := filler.file.SetWriteDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, err = filler.file.Write(make([]byte, 4<<20))
	_ = filler.Close()
	filler.release()
	if err == nil {
		t.Fatal("TEST pipe was not filled")
	}
	flags := agentNotifyFlagsOf(t, writer)
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = (geminiDiagnosticOutput{ctx: ctx, file: writer}).Write([]byte("TEST diagnostic\n"))
	if err == nil || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("diagnostic exceeded admission deadline: %v %v", time.Since(started), err)
	}
	if _, err := writer.Stat(); err != nil {
		t.Fatalf("caller descriptor closed: %v", err)
	}
	if after := agentNotifyFlagsOf(t, writer); after != flags {
		t.Fatalf("caller flags changed: %d != %d", after, flags)
	}
	cancel()
	// A canceled admission drops output immediately, without opening workers.
	started = time.Now()
	_, err = (geminiDiagnosticOutput{ctx: ctx, file: writer}).Write([]byte("TEST diagnostic\n"))
	if err == nil || time.Since(started) > 100*time.Millisecond {
		t.Fatalf("expired diagnostic waited: %v", err)
	}
}

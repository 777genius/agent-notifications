//go:build windows

package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

func TestWindowsAgentNotifyPipeIsPollable(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	in, err := agentNotifyFile(r)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	if !in.pollable {
		t.Fatal("pipe stdio was treated as a local file")
	}
}

func TestWindowsAgentNotifyPipeWritesResponse(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	out, err := agentNotifyFile(w)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()
	want := bytes.Repeat([]byte("response"), 1024)
	read := make(chan []byte, 1)
	go func() {
		got := make([]byte, len(want))
		_, _ = io.ReadFull(r, got)
		read <- got
	}()
	n, err := out.Write(want)
	if err != nil || n != len(want) {
		t.Fatalf("write = %d, %v", n, err)
	}
	if got := <-read; !bytes.Equal(got, want) {
		t.Fatal("response changed")
	}
}

func TestWindowsAgentNotifyPipeWriteIsBounded(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	out, err := agentNotifyFile(w)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()
	started := time.Now()
	_, err = out.Write(bytes.Repeat([]byte("x"), 1<<20))
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatal(err)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("blocked pipe write exceeded bound")
	}
}

func TestWindowsAgentNotifyRegularFileIsLocal(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "stdio")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	in, err := agentNotifyFile(f)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	if in.pollable {
		t.Fatal("regular file stdio used the poller")
	}
}

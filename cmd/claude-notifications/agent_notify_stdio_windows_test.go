//go:build windows

package main

import (
	"os"
	"testing"
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

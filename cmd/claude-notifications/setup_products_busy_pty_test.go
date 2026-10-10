//go:build linux || darwin

package main

import (
	"os"
	"testing"

	"github.com/creack/pty"
)

// Regression: explicit plain/NO_COLOR/dumb or non-terminal output emits rich
// escapes, despite the existing selector's terminal policy.
func TestSetupProductsBusyTerminalFallback(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("native PTY unavailable: %v", err)
	}
	defer func() { _ = master.Close() }()
	defer func() { _ = slave.Close() }()
	if err := pty.Setsize(master, &pty.Winsize{Rows: 30, Cols: 100}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "")
	if !setupProductsBusyRich(slave, slave, "auto") {
		t.Fatal("qualified terminal did not enable busy animation")
	}
	if setupProductsBusyRich(slave, slave, "plain") {
		t.Fatal("plain enabled busy animation")
	}
	t.Setenv("NO_COLOR", "1")
	if setupProductsBusyRich(slave, slave, "rich") {
		t.Fatal("NO_COLOR enabled busy animation")
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")
	if setupProductsBusyRich(slave, slave, "auto") {
		t.Fatal("dumb terminal enabled busy animation")
	}
	t.Setenv("TERM", "xterm-256color")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()
	if setupProductsBusyRich(slave, writer, "rich") || setupProductsBusyRich(reader, slave, "auto") {
		t.Fatal("pipe enabled busy animation")
	}
}

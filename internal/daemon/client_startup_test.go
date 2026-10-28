//go:build linux

package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDaemonStartupHelper(t *testing.T) {
	mode := os.Getenv("TEST_AGENT_NOTIFY_DAEMON_STARTUP")
	if mode == "" {
		return
	}
	if mode == "child" {
		for {
			time.Sleep(time.Second)
		}
	}
	child := exec.Command(os.Args[0], "-test.run=^TestDaemonStartupHelper$")
	child.Env = append(os.Environ(), "TEST_AGENT_NOTIFY_DAEMON_STARTUP=child")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("TEST_AGENT_NOTIFY_DAEMON_PIDS"), []byte(fmt.Sprintf("%d %d", os.Getpid(), child.Process.Pid)), 0600); err != nil {
		t.Fatal(err)
	}
	if mode == "exit" {
		os.Exit(9)
	}
	if mode == "ready" {
		listener, err := net.Listen("unix", GetSocketPath())
		if err != nil {
			t.Fatal(err)
		}
		serveStartupPings(listener)
	}
	for {
		time.Sleep(time.Second)
	}
}

func serveStartupPings(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		var req Request
		if json.NewDecoder(conn).Decode(&req) == nil {
			_ = json.NewEncoder(conn).Encode(Response{Type: req.Type, Ping: &PingResponse{Version: ProtocolVersion}})
		}
		_ = conn.Close()
	}
}

func startupFixture(t *testing.T, mode string) (string, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", root)
	t.Setenv("TEST_AGENT_NOTIFY_DAEMON_STARTUP", mode)
	t.Setenv("TEST_AGENT_NOTIFY_DAEMON_TEST_BINARY", os.Args[0])
	pidPath := filepath.Join(root, "pids")
	t.Setenv("TEST_AGENT_NOTIFY_DAEMON_PIDS", pidPath)
	helper := filepath.Join(root, "TEST-daemon")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nexec \"$TEST_AGENT_NOTIFY_DAEMON_TEST_BINARY\" -test.run=^TestDaemonStartupHelper$\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if data, err := os.ReadFile(pidPath); err == nil {
			fields := strings.Fields(string(data))
			if len(fields) == 0 {
				return
			}
			if pid, err := strconv.Atoi(fields[0]); err == nil {
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		}
	})
	return helper, pidPath
}

func startupPIDs(t *testing.T, path string) (int, int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 {
		t.Fatalf("malformed process fixture %q", data)
	}
	parent, err := strconv.Atoi(fields[0])
	if err != nil {
		t.Fatal(err)
	}
	child, err := strconv.Atoi(fields[1])
	if err != nil {
		t.Fatal(err)
	}
	return parent, child
}

func assertStartupCollected(t *testing.T, parent, child int) {
	t.Helper()
	if err := syscall.Kill(parent, 0); err != syscall.ESRCH {
		t.Fatalf("startup child %d was not joined: %v", parent, err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", child))
		if os.IsNotExist(err) {
			return
		}
		// A killed grandchild can remain a zombie until the system's reaper
		// collects it. It cannot execute; only our direct child is waitable.
		if err == nil {
			end := strings.LastIndexByte(string(data), ')')
			if end >= 0 && strings.HasPrefix(string(data[end+1:]), " Z ") {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("startup descendant %d remains running", child)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDaemonStartupFailureCollectsOwnedProcessGroup(t *testing.T) {
	for _, test := range []struct {
		name, mode string
		deadline   time.Duration
	}{
		{"canceled", "hang", 300 * time.Millisecond},
		{"readiness_timeout", "hang", 6 * time.Second},
		{"exited_before_ready", "exit", time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			helper, pidPath := startupFixture(t, test.mode)
			ctx, cancel := context.WithTimeout(context.Background(), test.deadline)
			defer cancel()
			if startDaemonOnDemand(ctx, helper) {
				t.Fatal("unready helper was accepted")
			}
			parent, child := startupPIDs(t, pidPath)
			assertStartupCollected(t, parent, child)
		})
	}
}

func TestDaemonStartupReadyReleasesPersistentProcess(t *testing.T) {
	helper, pidPath := startupFixture(t, "ready")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	if !startDaemonOnDemand(ctx, helper) {
		cancel()
		t.Fatal("fixture daemon never became ready")
	}
	cancel()
	parent, child := startupPIDs(t, pidPath)
	if syscall.Kill(parent, 0) != nil || syscall.Kill(child, 0) != nil || !isDaemonRunningContext(context.Background()) {
		t.Fatal("cancel after readiness stopped the shared daemon")
	}
}

func TestDaemonStartupPreservesExistingDaemon(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	listener, err := net.Listen("unix", GetSocketPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func(listener net.Listener) { _ = listener.Close() }(listener)
	go serveStartupPings(listener)
	ctx, cancel := context.WithCancel(context.Background())
	if !startDaemonOnDemand(ctx, "/TEST-must-not-start") {
		cancel()
		t.Fatal("existing daemon was not reused")
	}
	cancel()
	if !isDaemonRunningContext(context.Background()) {
		t.Fatal("existing daemon was stopped")
	}
}

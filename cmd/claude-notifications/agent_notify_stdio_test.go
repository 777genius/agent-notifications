//go:build linux || darwin

package main

import (
	"bytes"
	notifycli "github.com/777genius/agent-notifications/internal/agentnotify/cli"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func agentNotifyFlagsOf(t *testing.T, f *os.File) int {
	t.Helper()
	raw, err := f.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var flags int
	var e error
	if err = raw.Control(func(fd uintptr) { flags, e = unix.FcntlInt(fd, unix.F_GETFL, 0) }); err != nil {
		t.Fatal(err)
	}
	if e != nil {
		t.Fatal(e)
	}
	// Darwin adds FWASWRITTEN after ordinary write(2), including writes made
	// without this adapter. It records I/O history, not caller-controlled mode.
	// xnu/bsd/sys/fcntl.h defines it as 0x00010000. Keep every other status bit
	// in the comparison, including O_NONBLOCK and O_APPEND.
	if runtime.GOOS == "darwin" {
		flags &^= 0x00010000
	}
	return flags
}

func TestAgentNotifyProcessFiles(t *testing.T) {
	for _, c := range []struct {
		name, input, output, diag            string
		args                                 []string
		code                                 int
		nullIn, nullOut, nullErr, production bool
	}{
		{name: "receipt", input: `{"title":"T","body":"B","category":"info","navigation":"none"}`, output: `"status":"rejected"`, args: []string{"notify"}, code: 1},
		{name: "oversized", input: strings.Repeat(" ", notifycli.RawLimit+1), diag: "input_too_large\n", args: []string{"notify"}, code: 2},
		{name: "trailing-frame", input: `{"title":"T","body":"B","category":"info","navigation":"none"}{}`, diag: "invalid_content_json\n", args: []string{"notify"}, code: 2},
		{name: "malformed", input: "{", diag: "invalid_content_json\n", args: []string{"notify"}, code: 2},
		{name: "eof", diag: "invalid_content_json\n", args: []string{"notify"}, code: 2},
		{name: "null-input", nullIn: true, diag: "invalid_content_json\n", args: []string{"notify"}, code: 2},
		{name: "null-stderr", input: `{"title":"T","body":"B","category":"info","navigation":"none"}`, output: `"status":"rejected"`, nullErr: true, args: []string{"notify"}, code: 1},
		{name: "null-output", nullOut: true, args: []string{"notify", "--help"}, production: true},
		{name: "help", output: "Usage: notify", args: []string{"notify", "--help"}, production: true},
		{name: "help-null-stderr", output: "Usage: mcp-server", nullErr: true, args: []string{"mcp-server", "--help"}, production: true},
		{name: "invalid-flags", diag: "invalid_flags\n", args: []string{"notify", "--bad"}, code: 2, production: true},
		{name: "mcp-null", nullIn: true, nullErr: true, args: []string{"mcp-server", "--integration", "codex"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			inputPath := filepath.Join(root, "request.json")
			if err := os.WriteFile(inputPath, []byte(c.input), 0600); err != nil {
				t.Fatal(err)
			}
			if c.nullIn {
				inputPath = os.DevNull
			}
			in, err := os.Open(inputPath)
			if err != nil {
				t.Fatal(err)
			}
			defer in.Close()
			outputPath := filepath.Join(root, "receipt.json")
			if c.nullOut {
				outputPath = os.DevNull
			}
			out, err := os.Create(outputPath)
			if err != nil {
				t.Fatal(err)
			}
			defer out.Close()
			diagPath := filepath.Join(root, "diagnostic.log")
			if c.nullErr {
				diagPath = os.DevNull
			}
			diag, err := os.OpenFile(diagPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer diag.Close()
			cmd := agentNotifyTestCommand(t, c.args...)
			if c.production {
				cmd.Env = append(cmd.Env, "AGENT_NOTIFY_TEST_HELPER=production")
			}
			cmd.Stdin = in
			cmd.Stdout = out
			cmd.Stderr = diag
			before := []int{agentNotifyFlagsOf(t, in), agentNotifyFlagsOf(t, out), agentNotifyFlagsOf(t, diag)}
			err = cmd.Run()
			code := 0
			if err != nil {
				e, ok := err.(*exec.ExitError)
				if !ok {
					t.Fatal(err)
				}
				code = e.ExitCode()
			}
			got, _ := os.ReadFile(out.Name())
			diagnostic := ""
			if !c.nullErr {
				b, _ := os.ReadFile(diagPath)
				diagnostic = string(b)
			}
			if code != c.code || !strings.Contains(string(got), c.output) || (c.output == "" && len(got) != 0) || diagnostic != c.diag {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, got, diagnostic)
			}
			for i, f := range []*os.File{in, out, diag} {
				if after := agentNotifyFlagsOf(t, f); after != before[i] {
					t.Fatalf("flags %x -> %x", before[i], after)
				}
			}
		})
	}
}

// The parent retains the same open descriptions across exec, so these checks
// catch flag leaks even when the child exits. Explicitly begin with blocking
// fds, as os.Pipe normally starts with runtime-owned nonblocking descriptors.
func TestAgentNotifyInheritedFlags(t *testing.T) {
	for _, mode := range []string{"normal", "error", "cancel", "backpressure", "setup-error"} {
		for _, nonblock := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/blocking", true: "/nonblocking"}[nonblock], func(t *testing.T) {
				r, w, err := agentNotifyBlockingPipe()
				if err != nil {
					t.Fatal(err)
				}
				defer r.Close()
				defer w.Close()
				ir, iw, err := agentNotifyBlockingPipe()
				if err != nil {
					t.Fatal(err)
				}
				defer ir.Close()
				defer iw.Close()
				// Capture raw numbers before setting flags, never call Fd while leased.
				wfd := int(w.Fd())
				if err = unix.SetNonblock(wfd, nonblock); err != nil {
					t.Fatal(err)
				}
				if err = unix.SetNonblock(int(ir.Fd()), nonblock); err != nil {
					t.Fatal(err)
				}
				args := []string{"notify", "--help"}
				switch mode {
				case "error":
					args = []string{"notify", "--bad"}
				case "cancel", "setup-error":
					args = []string{"notify"}
				}
				if mode == "backpressure" {
					if err = agentNotifyFillPipe(w); err != nil {
						t.Fatal(err)
					}
					if err = unix.SetNonblock(wfd, nonblock); err != nil {
						t.Fatal(err)
					}
				}
				beforeOut, beforeIn := agentNotifyFlagsOf(t, w), agentNotifyFlagsOf(t, ir)
				cmd := agentNotifyTestCommand(t, args...)
				cmd.Stdout = w
				cmd.Stderr = w // aliases of the SAME open description
				cmd.Stdin = ir
				if mode == "setup-error" {
					dir, e := os.Open(t.TempDir())
					if e != nil {
						t.Fatal(e)
					}
					defer dir.Close()
					cmd.Stdin = dir
				}
				if err = cmd.Start(); err != nil {
					t.Fatal(err)
				}
				if mode == "cancel" {
					time.Sleep(150 * time.Millisecond)
					_ = cmd.Process.Signal(syscall.SIGTERM)
				}
				err = cmd.Wait()
				expected := 0
				if mode != "normal" {
					expected = 2
				}
				code := 0
				if err != nil {
					e, ok := err.(*exec.ExitError)
					if !ok {
						t.Fatal(err)
					}
					code = e.ExitCode()
				}
				if code != expected {
					t.Fatalf("code %d expected %d", code, expected)
				}
				if after := agentNotifyFlagsOf(t, w); after != beforeOut {
					t.Fatalf("stdout/stderr flags %x -> %x", beforeOut, after)
				}
				if after := agentNotifyFlagsOf(t, ir); after != beforeIn {
					t.Fatalf("stdin flags %x -> %x", beforeIn, after)
				}
			})
		}
	}
}

func TestAgentNotifyLeasePreservesOtherFlags(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	fd := int(w.Fd())
	if err = unix.SetNonblock(fd, false); err != nil {
		t.Fatal(err)
	}
	s, err := agentNotifyFile(w)
	if err != nil {
		t.Fatal(err)
	}
	flags := agentNotifyFlagsOf(t, w)
	if flags&unix.O_NONBLOCK == 0 {
		t.Fatal("missing poller nonblocking flag")
	}
	if _, err = unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags|unix.O_APPEND); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s.release()
	s.release()
	after := agentNotifyFlagsOf(t, w)
	if after&unix.O_NONBLOCK != 0 || after&unix.O_APPEND == 0 {
		t.Fatalf("restored flags %x", after)
	}
	if _, err = unix.Write(fd, []byte("borrowed still open")); err != nil {
		t.Fatal(err)
	}
}

func TestAgentNotifyProcessIncompleteFrame(t *testing.T) {
	cmd := agentNotifyTestCommand(t, "notify")
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	var out, diag bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &diag
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_, _ = in.Write([]byte(`{"title":"T","body":"B","category":"info","navigation":"none"}`))
	time.Sleep(150 * time.Millisecond)
	_ = cmd.Process.Signal(syscall.SIGTERM)
	_ = cmd.Wait()
	if out.Len() != 0 {
		t.Fatalf("receipt before EOF: %q", out.String())
	}
	for _, entry := range cmd.Env {
		pair := strings.SplitN(entry, "=", 2)
		if pair[0] == "HOME" || strings.HasPrefix(pair[0], "XDG_") || pair[0] == "CODEX_HOME" {
			files, e := os.ReadDir(pair[1])
			if e != nil || len(files) != 0 {
				t.Fatalf("unexpected state %s %v", pair[0], files)
			}
		}
	}
}

// Raw blocking pipes keep exec.Cmd's Fd calls from changing the fixture flags.
func agentNotifyBlockingPipe() (*os.File, *os.File, error) {
	var fds [2]int
	if err := unix.Pipe(fds[:]); err != nil {
		return nil, nil, err
	}
	unix.CloseOnExec(fds[0])
	unix.CloseOnExec(fds[1])
	return os.NewFile(uintptr(fds[0]), "test-read"), os.NewFile(uintptr(fds[1]), "test-write"), nil
}

// A setup failure after changing flags must unwind its own lease even though
// the stream was never registered with the invocation's cleanup.
func TestAgentNotifyNonpollableSetupCleanup(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux epoll rejects /dev/zero")
	}
	f, err := os.Open("/dev/zero")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	before := agentNotifyFlagsOf(t, f)
	for i := 0; i < 20; i++ {
		s, err := agentNotifyFile(f)
		if err == nil {
			s.Close()
			s.release()
			t.Fatal("accepted nonpollable device")
		}
		if after := agentNotifyFlagsOf(t, f); after != before {
			t.Fatalf("flags %x -> %x", before, after)
		}
	}
	b := make([]byte, 1)
	if n, err := f.Read(b); n != 1 || err != nil || b[0] != 0 {
		t.Fatalf("borrowed file unusable: %d %v", n, err)
	}
}

func TestAgentNotifyFileCloseKeepsBorrowedOpen(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	before := agentNotifyFlagsOf(t, f)
	s, err := agentNotifyFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Write([]byte("owned")); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s.release()
	if _, err = s.Write([]byte("closed")); err != os.ErrClosed {
		t.Fatalf("write after close: %v", err)
	}
	if _, err = f.Write([]byte("borrowed")); err != nil {
		t.Fatal(err)
	}
	if after := agentNotifyFlagsOf(t, f); after != before {
		t.Fatalf("flags %x -> %x", before, after)
	}
	b, err := os.ReadFile(f.Name())
	if err != nil || string(b) != "ownedborrowed" {
		t.Fatalf("content %q: %v", b, err)
	}
}

//go:build windows

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type cursorVersionOutput struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *cursorVersionOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.Len()+len(p) > 4096 {
		return 0, errors.New("Cursor version output exceeds limit")
	}
	return b.Buffer.Write(p)
}

// The official .cmd/.ps1 launcher delegates to its sibling node.exe/index.js.
// Run that exact pair directly so user paths never become shell source.
// Suspend the child until its kill-on-close job owns every descendant.
func probeCursorVersion(ctx context.Context, executable string, _ []string) (string, error) {
	if !strings.EqualFold(filepath.Base(executable), "cursor-agent.cmd") {
		return "", errors.New("official Cursor Windows launcher required")
	}
	dir := filepath.Dir(executable)
	node, index := filepath.Join(dir, "node.exe"), filepath.Join(dir, "index.js")
	for _, path := range []string{executable, node, index} {
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", errors.New("regular official Cursor files required")
		}
	}
	root, err := os.MkdirTemp("", "agent-notifications-cursor-version-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(root)
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(job)
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, node, index, "--version")
	cmd.Dir = root
	cmd.Env = []string{"HOME=" + root, "USERPROFILE=" + root, "LOCALAPPDATA=" + root, "APPDATA=" + root, "TEMP=" + root, "TMP=" + root, "CI=1", "AGENT_CLI_CREDENTIAL_STORE=file"}
	for _, key := range []string{"SystemRoot", "WINDIR", "PATH", "PATHEXT"} {
		if value := os.Getenv(key); value != "" {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Stdin = strings.NewReader("")
	output := &cursorVersionOutput{}
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	if err = cmd.Start(); err != nil {
		return "", err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME, false, uint32(cmd.Process.Pid))
	if err == nil {
		defer windows.CloseHandle(process)
		err = windows.AssignProcessToJobObject(job, process)
		if err == nil {
			status, _, _ := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess").Call(uintptr(process))
			if status != 0 {
				err = errors.New("Cursor version process could not resume")
			}
		}
	}
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return "", err
	}
	err = cmd.Wait()
	return output.String(), err
}

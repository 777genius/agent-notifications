package installruntime

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
)

// WriterProtocolMarker is an offline compatibility declaration in the trusted
// package bytes, not publisher authentication. No historical writer is run to
// discover its capabilities. Increment the floor for destructive protocol changes.
const WriterProtocolMarker = "agent-notifications-managed-writer-protocol-v1"
const WriterFloor = 1
const LocalWriterProtocolMarker = "agent-notifications-managed-writer-protocol-v3"

// SupportedWriterFloor is the kernel/reader ceiling, not a replacement for the
// historical v1 marker or reservation-v2 declaration.
const SupportedWriterFloor = OpenCodeWriterFloor

func managedWriter(path string) bool {
	name := filepath.Base(path)
	if name == "install.sh" || name == "claude-notifications" || name == "claude-notifications.exe" {
		return true
	}
	for _, platform := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			candidate := "claude-notifications-" + platform + "-" + arch
			if name == candidate || name == candidate+".exe" {
				return true
			}
		}
	}
	return false
}

func validateWriterFiles(files []File) error { return validateWriterFilesAtFloor(files, WriterFloor) }

func validateWriterFilesAtFloor(files []File, floor int) error {
	for _, file := range files {
		if file.Remove || !managedWriter(file.Path) {
			continue
		}
		if file.Link != "" {
			// Aliases only authorize a regular managed writer in this same
			// transaction. Never follow a filesystem link to infer trust.
			target := file.Link
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(file.Path), target)
			}
			target = filepath.Clean(target)
			validated := false
			for _, candidate := range files {
				if filepath.Clean(candidate.Path) == target && !candidate.Remove && candidate.Link == "" && managedWriter(candidate.Path) && WriterCompatibleAtFloor(candidate.Data, floor) {
					validated = true
				}
			}
			if validated {
				continue
			}
			return fmt.Errorf("managed writer alias %s requires a validated regular managed target in the transaction", file.Path)
		}
		if !WriterCompatibleAtFloor(file.Data, floor) {
			return fmt.Errorf("managed writer %s is below protocol floor %d; use a compatible install kernel/package for rollback", filepath.Base(file.Path), floor)
		}
	}
	return nil
}

// WriterCompatible is available to package adapters before any candidate exec.
// Source authentication remains the adapter's prerequisite.
func WriterCompatible(data []byte) bool { return strings.Contains(string(data), WriterProtocolMarker) }

// Candidate bytes are inspected before exec; no environment or installed marker
// is changed by this compatibility wrapper. Floors 1/2 keep their old meaning.
func WriterCompatibleAtFloor(data []byte, floor int) bool {
	if floor > SupportedWriterFloor {
		return false
	}
	if floor >= OpenCodeWriterFloor {
		return bytes.Contains(data, []byte(OpenCodeWriterProtocolMarker))
	}
	if floor >= LocalPolicyWriterFloor {
		return bytes.Contains(data, []byte(LocalWriterProtocolMarker))
	}
	return WriterCompatible(data)
}

// WindowsLauncherScript is the transaction-owned BAT wrapper. The shell installer
// must emit the same bytes and must not rewrite a committed launcher.
func WindowsLauncherScript(launcher, entry string) []byte {
	return []byte("@echo off\nREM " + launcher + " Windows wrapper\nREM Automatically runs the platform-specific binary\n\nsetlocal\nset SCRIPT_DIR=%~dp0\nset AGENT_NOTIFICATIONS_LAUNCHER=" + launcher + "\n\"%SCRIPT_DIR%" + entry + "\" %*\n")
}

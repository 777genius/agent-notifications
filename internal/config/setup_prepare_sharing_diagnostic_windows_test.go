//go:build windows

package config

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// Temporary native diagnostic only. A real DELETE handle models the sharing
// state during MoveFileEx namespace publication; preparation is unchanged.
// This does not identify the cause of an unrelated stress failure by itself.
func TestPrepareWindowsNativeSharingDiagnostic(t *testing.T) {
	for _, kind := range []string{"parent", "canonical-file"} {
		t.Run(kind, func(t *testing.T) {
			canonical, legacy, defaults := prepPaths(t)
			putPrep(t, defaults, disabledDesktop)
			if err := prepareGlobalConfigParent(canonical); err != nil {
				t.Fatalf("private TEST parent preparation failed: %v", err)
			}
			target := filepath.Dir(canonical)
			flags := uint32(windows.FILE_FLAG_BACKUP_SEMANTICS | windows.FILE_FLAG_OPEN_REPARSE_POINT)
			wantStage := "prepare-open-parent"
			var before []byte
			if kind == "canonical-file" {
				result, err := PrepareGlobalConfig(context.Background(), canonical, legacy, defaults)
				if err != nil || !result.Ready {
					t.Fatalf("private TEST canonical preparation failed: ready=%t err=%v", result.Ready, err)
				}
				before, err = os.ReadFile(canonical)
				if err != nil {
					t.Fatal("private TEST canonical snapshot failed")
				}
				target = canonical
				flags = windows.FILE_ATTRIBUTE_NORMAL | windows.FILE_FLAG_OPEN_REPARSE_POINT
				wantStage = "prepare-prelock-read"
			}
			name, err := windowsStorePath(target)
			if err != nil {
				t.Fatal("native TEST target conversion failed")
			}
			// The handle itself permits sharing. winOpen deliberately omits
			// FILE_SHARE_DELETE, so its read must conflict with our DELETE access.
			handle, err := windows.CreateFile(name, windows.DELETE,
				windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
				nil, windows.OPEN_EXISTING, flags, 0)
			if err != nil {
				t.Fatalf("native TEST DELETE handle acquisition failed: %v", err)
			}
			var result PrepareGlobalResult
			var prepareErr, closeErr error
			func() {
				defer func() { closeErr = windows.CloseHandle(handle) }()
				result, prepareErr = PrepareGlobalConfig(context.Background(), canonical, legacy, defaults)
			}()
			if closeErr != nil {
				t.Fatal("native TEST DELETE handle close failed")
			}
			var ce *Error
			if !errors.As(prepareErr, &ce) {
				t.Fatalf("expected sanitized configuration error: ready=%t errorType=%T", result.Ready, prepareErr)
			}
			t.Logf("native sharing diagnostic: kind=%s code=%s stage=%s win32=%d", kind, ce.Code, ce.causeStage, ce.causeErrno)
			if result.Ready || ce.Code != ConfigInvalid || ce.causeStage != wantStage || ce.causeErrno != uintptr(windows.ERROR_SHARING_VIOLATION) {
				t.Fatalf("unexpected native sharing classification: ready=%t code=%s stage=%s win32=%d", result.Ready, ce.Code, ce.causeStage, ce.causeErrno)
			}
			after, err := os.ReadFile(canonical)
			if kind == "parent" {
				if !os.IsNotExist(err) {
					t.Fatal("rejected parent probe published a canonical file")
				}
			} else if err != nil || !bytes.Equal(before, after) {
				t.Fatal("rejected canonical probe changed existing bytes")
			}
		})
	}
}

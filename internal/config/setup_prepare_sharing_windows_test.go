//go:build windows

package config

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// Hold the same native DELETE access that can conflict with winOpen during
// MoveFileEx publication. The protected target is never replaced or weakened.
func holdPrepareSharingGap(t *testing.T, kind string) (canonical, legacy, defaults string, release func()) {
	t.Helper()
	canonical, legacy, defaults = prepPaths(t)
	putPrep(t, defaults, disabledDesktop)
	if err := prepareGlobalConfigParent(canonical); err != nil {
		t.Fatal(err)
	}
	target := filepath.Dir(canonical)
	flags := uint32(windows.FILE_FLAG_BACKUP_SEMANTICS | windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if kind == "canonical-file" {
		result, err := PrepareGlobalConfig(context.Background(), canonical, legacy, defaults)
		if err != nil || !result.Ready {
			t.Fatalf("private TEST config setup: ready=%t err=%v", result.Ready, err)
		}
		target = canonical
		flags = windows.FILE_ATTRIBUTE_NORMAL | windows.FILE_FLAG_OPEN_REPARSE_POINT
	}
	name, err := windowsStorePath(target)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.DELETE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release = func() {
		once.Do(func() {
			if err := windows.CloseHandle(handle); err != nil {
				t.Error("native TEST sharing handle close failed")
			}
		})
	}
	t.Cleanup(release)
	// Establish the real collision before starting preparation. This prevents
	// an ineffective native fixture from passing without exercising the gap.
	probe, err := winOpen(target, windows.GENERIC_READ, windows.OPEN_EXISTING, flags, false)
	if probe != nil {
		_ = probe.Close()
	}
	if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("native TEST sharing collision not established: errorType=%T", err)
	}
	return canonical, legacy, defaults, release
}

func assertPrepareSharingUnchanged(t *testing.T, canonical, kind string) {
	t.Helper()
	raw, err := os.ReadFile(canonical)
	if kind == "parent" {
		if !os.IsNotExist(err) {
			t.Fatal("blocked preparation published a canonical file")
		}
	} else if err != nil || !bytes.Equal(raw, []byte(disabledDesktop)) {
		t.Fatal("blocked preparation changed existing canonical bytes")
	}
}

// Without the bounded pre-lock retries this fails with ConfigInvalid/errno32
// before the DELETE handle is released. The successful retry must preserve the
// complete canonical user's disabled settings and unknown JSON bytes exactly.
func TestWindowsPrepareWaitsForNativeSharingGap(t *testing.T) {
	for _, kind := range []string{"parent", "canonical-file"} {
		t.Run(kind, func(t *testing.T) {
			canonical, legacy, defaults, release := holdPrepareSharingGap(t, kind)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			type outcome struct {
				result PrepareGlobalResult
				err    error
			}
			done := make(chan outcome, 1)
			started := make(chan struct{})
			go func() {
				close(started)
				result, err := PrepareGlobalConfig(ctx, canonical, legacy, defaults)
				done <- outcome{result, err}
			}()
			<-started
			select {
			case early := <-done:
				var ce *Error
				if errors.As(early.err, &ce) {
					t.Fatalf("preparation did not wait: code=%s stage=%s win32=%d", ce.Code, ce.causeStage, ce.causeErrno)
				}
				t.Fatalf("preparation did not wait: ready=%t errorType=%T", early.result.Ready, early.err)
			case <-time.After(100 * time.Millisecond):
			}
			release()
			select {
			case final := <-done:
				if final.err != nil || !final.result.Ready || final.result.Changed != (kind == "parent") {
					t.Fatalf("released native sharing gap: result=%+v err=%v", final.result, final.err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("preparation did not finish within original deadline")
			}
			raw, err := os.ReadFile(canonical)
			if err != nil || !bytes.Equal(raw, []byte(disabledDesktop)) {
				t.Fatal("successful preparation changed canonical source bytes")
			}
		})
	}
}

func TestWindowsPrepareNativeSharingGapHonorsDeadline(t *testing.T) {
	for _, kind := range []string{"parent", "canonical-file"} {
		t.Run(kind, func(t *testing.T) {
			canonical, legacy, defaults, release := holdPrepareSharingGap(t, kind)
			ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
			defer cancel()
			result, err := PrepareGlobalConfig(ctx, canonical, legacy, defaults)
			release()
			var ce *Error
			wantStage := "prepare-open-parent"
			if kind == "canonical-file" {
				wantStage = "prepare-prelock-read"
			}
			if !errors.As(err, &ce) || ce.Code != ConfigLockTimeout || ce.causeStage != wantStage || result.Ready || result.Changed {
				t.Fatalf("persistent sharing gap: result=%+v err=%v", result, err)
			}
			assertPrepareSharingUnchanged(t, canonical, kind)
		})
	}
}

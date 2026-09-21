package setupwizard

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/portableasset"
)

func TestLocalArchiveReplacementInvalidatesCache(t *testing.T) {
	for _, checked := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchecked", true: "checked"}[checked], func(t *testing.T) {
			base := t.TempDir()
			exe := filepath.Join(base, "fixture")
			if err := os.WriteFile(exe, []byte("synthetic executable"), 0700); err != nil {
				t.Fatal(err)
			}
			archive := filepath.Join(base, "package.zip")
			req := Request{ControlRoot: filepath.Join(base, "control")}
			build := func(version string) string {
				t.Helper()
				_ = os.Remove(archive)
				result, err := portableasset.Build(portableasset.BuildRequest{Version: version, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Executable: exe, OutputRoot: filepath.Join(base, version), Archive: archive})
				if err != nil {
					t.Fatal(err)
				}
				return result.ArchiveSHA256
			}
			firstDigest := build("1.43.0")
			if checked {
				req.PackageSHA256 = firstDigest
			}
			first, _, err := openLocalPackage(req, archive)
			if err != nil {
				t.Fatal(err)
			}
			same, _, err := openLocalPackage(req, archive)
			if err != nil || same != first {
				t.Fatalf("cache reuse: %s %v", same, err)
			}
			info, err := os.Stat(archive)
			if err != nil {
				t.Fatal(err)
			}
			nextDigest := build("1.44.0")
			if err := os.Chtimes(archive, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
			if checked {
				if _, _, err := openLocalPackage(req, archive); !errors.Is(err, portableasset.ErrChecksumMismatch) {
					t.Fatalf("stale checksum accepted: %v", err)
				}
				req.PackageSHA256 = nextDigest
			}
			next, _, err := openLocalPackage(req, archive)
			if err != nil {
				t.Fatal(err)
			}
			if next == first {
				t.Fatal("replaced archive reused stale extraction")
			}
			body, err := os.ReadFile(filepath.Join(next, "plugin.json"))
			if err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(filepath.Join(base, "1.44.0", "plugin.json"))
			if err != nil || string(body) != string(original) {
				t.Fatalf("wrong extracted manifest: %s, %v", body, err)
			}
			if !packageStillUsable(first) {
				t.Fatal("replacement removed a retained source")
			}
		})
	}
}

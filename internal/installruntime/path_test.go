//go:build linux || darwin

package installruntime

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFingerprintLargeFileFreshBytesWithBoundedAllocation(t *testing.T) {
	// Repeated admission checks must hash every byte without retaining a second
	// whole-file body. The limit leaves ample room for descriptors and paths.
	body := make([]byte, 4*1024*1024+17)
	for i := range body {
		body[i] = byte(i)
	}
	path := filepath.Join(t.TempDir(), "primary")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	check := func(mode uint32) uint64 {
		t.Helper()
		want := Identity{Exists: true, SHA256: fmt.Sprintf("%x", sha256.Sum256(body)), Mode: mode}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		got, err := Fingerprint(path)
		runtime.ReadMemStats(&after)
		if err != nil || got != want {
			t.Fatalf("fresh fingerprint = %+v, %v; want %+v", got, err, want)
		}
		return after.TotalAlloc - before.TotalAlloc
	}
	first := check(0600)
	body[0], body[len(body)-1] = 255, 254
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.WriteAt(body[:1], 0); err == nil {
		_, err = file.WriteAt(body[len(body)-1:], int64(len(body)-1))
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("replace bytes: %v; close: %v", err, closeErr)
	}
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	second := check(0700)
	if first+second >= uint64(len(body)) {
		t.Fatalf("two fresh fingerprints allocated %d bytes for a %d-byte file", first+second, len(body))
	}
}

func TestDestinationParentsConfined(t *testing.T) {
	for _, phase := range []string{"stage", "commit", "recovery"} {
		t.Run(phase, func(t *testing.T) {
			ctx, r := request(t)
			source, foreign := t.TempDir(), t.TempDir()
			for _, root := range []string{source, r.RuntimeRoot} {
				if err := os.MkdirAll(filepath.Join(root, "sounds", "nested"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(source, "sounds", "nested", "tone"), []byte("package"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(foreign, "nested"), 0700); err != nil {
				t.Fatal(err)
			}
			victim := filepath.Join(foreign, "nested", "tone")
			if err := os.WriteFile(victim, []byte("foreign"), 0600); err != nil {
				t.Fatal(err)
			}
			substitute := func() {
				t.Helper()
				parent := filepath.Join(r.RuntimeRoot, "sounds")
				if err := os.Rename(parent, parent+"-saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(foreign, parent); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "stage" {
				substitute()
			}
			var err error
			r.Files, err = StageFiles(source, r.RuntimeRoot, func(string) bool { return true })
			if phase == "stage" {
				if err == nil {
					t.Fatal("linked stage accepted")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if phase == "recovery" {
					r.Fault = func(p string) error {
						if p == "transaction" {
							return fmt.Errorf("crash")
						}
						return nil
					}
					if _, err = Commit(ctx, r); err == nil {
						t.Fatal("missing crash")
					}
					r.Fault = nil
					r.Files = nil
				}
				substitute()
				if _, err = Commit(ctx, r); err == nil {
					t.Fatal("substituted parent accepted")
				}
			}
			got, err := os.ReadFile(victim)
			if err != nil || string(got) != "foreign" {
				t.Fatalf("foreign changed: %s %v", got, err)
			}
		})
	}
}

func TestDestinationDirectoryIdentitySubstitution(t *testing.T) {
	ctx, r := request(t)
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "asset"), []byte("package"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(r.RuntimeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	var err error
	r.Files, err = StageFiles(source, r.RuntimeRoot, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(r.RuntimeRoot, r.RuntimeRoot+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(r.RuntimeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = Commit(ctx, r); err == nil {
		t.Fatal("replacement directory accepted")
	}
	if _, err = os.Stat(filepath.Join(r.RuntimeRoot, "asset")); !os.IsNotExist(err) {
		t.Fatal("wrote substituted directory")
	}
}

func TestStageFilesDropsWritableGroupAndWorldBits(t *testing.T) {
	source := t.TempDir()
	destination := filepath.Join(t.TempDir(), "runtime")
	path := filepath.Join(source, "bin")
	if err := os.WriteFile(path, []byte("writer"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0777); err != nil {
		t.Fatal(err)
	}
	files, err := StageFiles(source, destination, func(string) bool { return true })
	if err != nil || len(files) != 1 {
		t.Fatalf("stage files: %v %+v", err, files)
	}
	if files[0].Mode != 0755 {
		t.Fatalf("staged executable mode = %04o, want 0755", files[0].Mode)
	}
}

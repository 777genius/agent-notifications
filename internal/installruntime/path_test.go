//go:build linux || darwin

package installruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

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

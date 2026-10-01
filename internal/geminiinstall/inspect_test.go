package geminiinstall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Bind all fixture paths to bytes, modes, write times and inodes, including
// directories and locks. Windows also captures attributes and owner/group/DACL.
// A status implementation that calls Apply/Recover must fail this check.
type inspectPathSnapshot struct {
	info     fs.FileInfo
	security string
	contents installruntime.Identity
	link     string
}

func inspectTree(t *testing.T, base string) map[string]inspectPathSnapshot {
	t.Helper()
	entries := map[string]inspectPathSnapshot{}
	err := filepath.WalkDir(base, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, security, err := inspectPathInfo(path)
		if err != nil {
			return err
		}
		state := inspectPathSnapshot{info: info, security: security}
		if info.Mode().IsRegular() {
			state.contents, err = installruntime.Fingerprint(path)
		} else if info.Mode()&os.ModeSymlink != 0 {
			state.link, err = os.Readlink(path)
		}
		entries[path] = state
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func inspectTreeDifference(before, after map[string]inspectPathSnapshot) error {
	if len(before) != len(after) {
		return errors.New("inspection created or removed fixture paths")
	}
	for path, want := range before {
		got, ok := after[path]
		if !ok {
			return fmt.Errorf("inspection removed path %s", path)
		}
		if !os.SameFile(want.info, got.info) {
			return fmt.Errorf("inspection replaced path %s", path)
		}
		if want.info.Mode() != got.info.Mode() || want.security != got.security {
			return fmt.Errorf("inspection changed mode, attributes or security of %s", path)
		}
		if !want.info.ModTime().Equal(got.info.ModTime()) {
			return fmt.Errorf("inspection changed write time of %s: %s -> %s", path, want.info.ModTime(), got.info.ModTime())
		}
		if want.contents != got.contents || want.link != got.link {
			return fmt.Errorf("inspection changed contents or link of %s", path)
		}
	}
	return nil
}

func inspectUnchanged(t *testing.T, ctx context.Context, r Request) (Inspection, error) {
	t.Helper()
	base := filepath.Dir(r.ControlRoot)
	before := inspectTree(t, base)
	result, err := Inspect(ctx, r)
	if change := inspectTreeDifference(before, inspectTree(t, base)); change != nil {
		t.Fatal(change)
	}
	return result, err
}

func TestInspectAbsentIsReadOnly(t *testing.T) {
	ctx, r, _ := installFixture(t)
	got, err := inspectUnchanged(t, ctx, r)
	if err != nil || got != (Inspection{Status: "absent"}) {
		t.Fatalf("missing installation: %+v %v", got, err)
	}
	if _, err := os.Lstat(r.ControlRoot); !os.IsNotExist(err) {
		t.Fatal("inspection created a control root")
	}
	r.ControlRoot = "relative"
	if _, err := Inspect(ctx, r); err == nil {
		t.Fatal("inspection accepted a relative control root")
	}
}

func TestInspectInstalledForeignEditAndRevocation(t *testing.T) {
	ctx, r, settings := installFixture(t)
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	edited := bytes.Replace(data, []byte("printf foreign"), []byte("printf foreign-updated"), 1)
	if bytes.Equal(data, edited) {
		t.Fatal("fixture did not change foreign command")
	}
	if err := os.WriteFile(settings, edited, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := inspectUnchanged(t, ctx, r)
	if err != nil || got != (Inspection{Status: "installed", Registered: true, Webhook: true}) {
		t.Fatalf("foreign edit invalidated managed registration: %+v %v", got, err)
	}
	if err := RevokeChannels(ctx, r.ControlRoot, r.RuntimeRoot); err != nil {
		t.Fatal(err)
	}
	got, err = inspectUnchanged(t, ctx, r)
	if err != nil || got != (Inspection{Status: "installed", Registered: true}) {
		t.Fatalf("inspection re-enabled revoked consent: %+v %v", got, err)
	}
}

func TestInspectRejectsDriftWithoutRepair(t *testing.T) {
	for _, damage := range []string{"receipt", "binary", "owned-hook", "missing-lock", "symlink"} {
		t.Run(damage, func(t *testing.T) {
			ctx, r, settings := installFixture(t)
			if err := Apply(ctx, r); err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "receipt":
				if err := os.WriteFile(filepath.Join(r.ControlRoot, receiptName), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "binary":
				name, _ := binaryName(r.GOOS, r.GOARCH)
				if err := os.Remove(filepath.Join(r.RuntimeRoot, name)); err != nil {
					t.Fatal(err)
				}
			case "owned-hook":
				data, err := os.ReadFile(settings)
				if err != nil {
					t.Fatal(err)
				}
				edited := bytes.Replace(data, []byte("agent-notifications-gemini-after-agent"), []byte("foreign-owner"), 1)
				if bytes.Equal(data, edited) {
					t.Fatal("fixture did not change own group")
				}
				if err := os.WriteFile(settings, edited, 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-lock":
				if err := os.Remove(filepath.Join(r.ControlRoot, ".component-install.lock")); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(settings, settings+".foreign"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(settings+".foreign", settings); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}
			got, err := inspectUnchanged(t, ctx, r)
			if err == nil || !reflect.DeepEqual(got, Inspection{Status: "conflict"}) {
				t.Fatalf("damaged installation reported healthy: %+v %v", got, err)
			}
		})
	}
}

func TestInspectPendingRecoveryDoesNotRecover(t *testing.T) {
	ctx, r, settings := installFixture(t)
	r.Fault = func(phase string) error {
		if phase == "promotion:"+settings {
			return errors.New("synthetic interrupted setup")
		}
		return nil
	}
	if err := Apply(ctx, r); err == nil {
		t.Fatal("fixture did not interrupt installation")
	}
	got, err := inspectUnchanged(t, ctx, r)
	if err != nil || got != (Inspection{Status: "recovery-required"}) {
		t.Fatalf("pending recovery: %+v %v", got, err)
	}
}

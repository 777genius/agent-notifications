package cursorinstall

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/pathpolicy"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/nativeconfig"
)

// Constructor refusals must return no adapter and leave every fresh TEST file intact.
func TestNewRefusesMissingAuthorityOrPathPolicyWithoutEffects(t *testing.T) {
	for _, missing := range []string{"fixed-authority", "path-policy"} {
		t.Run(missing, func(t *testing.T) {
			fixed, unchanged := refusalFixture(t)
			var paths = pathpolicy.Policy{}
			var a *Adapter
			var err error
			if missing == "fixed-authority" {
				a, err = New(nativeconfig.New(), paths, nil)
			} else {
				a, err = New(nativeconfig.New(), nil, &fixed)
			}
			if a != nil || err == nil || err.Error() != "cursor selected route requires fixed qualified authority and path policy" {
				t.Fatalf("missing %s granted adapter or wrong refusal: %v, %v", missing, a, err)
			}
			unchanged()
		})
	}
}

// Unsupported runtime tuples must refuse even valid fixed TEST facts; Linux also rejects bad qualification facts.
func TestNewRefusesUnqualifiedTupleWithoutProfileOrFileEffects(t *testing.T) {
	cases := []string{"qualified-facts-on-unsupported-runtime"}
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		cases = []string{"wrong-fixed-version", "empty-qualification"}
	}
	for _, invalid := range cases {
		t.Run(invalid, func(t *testing.T) {
			fixed, unchanged := refusalFixture(t)
			if invalid == "wrong-fixed-version" {
				fixed.CursorVersion = "TEST-unqualified-version"
			}
			if invalid == "empty-qualification" {
				fixed.QualificationID = ""
			}
			a, err := New(nativeconfig.New(), pathpolicy.Policy{}, &fixed)
			if a != nil || err == nil || err.Error() != "cursor selected tuple is unqualified" {
				t.Fatalf("unqualified tuple granted adapter or wrong refusal: %v, %v", a, err)
			}
			unchanged()
		})
	}
}

func refusalFixture(t *testing.T) (Authority, func()) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(root, "TEST-Cursor-profile")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "TEST-observer")
	body := []byte("TEST fixed executable bytes; never executed\n")
	for path, content := range map[string][]byte{executable: body, filepath.Join(profile, "hooks.json"): []byte(`{"version":1,"hooks":{}}`), filepath.Join(root, "TEST-binding.json"): []byte("TEST binding preserved\n")} {
		if err := os.WriteFile(path, content, 0700); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := func() map[string]string {
		t.Helper()
		files := map[string]string{}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			var content []byte
			if !entry.IsDir() {
				content, err = os.ReadFile(path)
				if err != nil {
					return err
				}
			}
			files[path] = fmt.Sprintf("%s:%s", info.Mode(), content)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	before := snapshot()
	fixed := Authority{ProfileRoot: profile, CursorVersion: cursorVersion, QualificationID: "TEST-vendor-mechanics-only", Executable: executable, ExecutableDigest: digest(body), Selector: filepath.Join(root, "TEST-binding.json"), ObjectID: "TEST-owned-Cursor-Stop"}
	return fixed, func() {
		t.Helper()
		if after := snapshot(); !reflect.DeepEqual(before, after) {
			t.Fatalf("constructor refusal changed TEST bytes, modes or files: before=%v after=%v", before, after)
		}
	}
}

//go:build linux || darwin

package portable

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSameInstalledFileRejectsCopyAndAcceptsAlias(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(root, "primary")
	if err := os.WriteFile(installed, []byte("ledger-primary"), 0700); err != nil {
		t.Fatal(err)
	}
	if !SameInstalledFile(installed, installed) {
		t.Fatal("identical path rejected")
	}
	copyPath := filepath.Join(root, "copy")
	body, err := os.ReadFile(installed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyPath, body, 0700); err != nil {
		t.Fatal(err)
	}
	if SameInstalledFile(copyPath, installed) {
		t.Fatal("package copy impersonated installed primary")
	}
	aliased := darwinSystemAlias(installed)
	if aliased == installed {
		return
	}
	if _, err := os.Stat(aliased); err != nil {
		t.Fatal(err)
	}
	if !SameInstalledFile(aliased, installed) {
		t.Fatal("darwin system alias of the installed primary rejected")
	}
}

func darwinSystemAlias(path string) string {
	for _, from := range []string{"/private/var/", "/private/tmp/", "/private/etc/"} {
		if strings.HasPrefix(path, from) {
			return strings.TrimPrefix(path, "/private")
		}
	}
	return path
}

func TestAcquireAcceptsDarwinSystemAlias(t *testing.T) {
	b, name, _ := fixture(t)
	aliased := darwinSystemAlias(b.DataRoot)
	if aliased == b.DataRoot {
		t.Skip("no darwin system alias")
	}
	lease, err := Acquire(testContext(t), aliased, name)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
}

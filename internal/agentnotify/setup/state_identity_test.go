//go:build linux || darwin

package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A reboot can give the volume a new device number while every inode stays,
// so the durable setupState identity must still match its own directory.
func TestOwnedDirectoryIdentitySurvivesRenumberedVolume(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	id, err := directoryID(f)
	if err != nil {
		t.Fatal(err)
	}
	dev, ino, _ := strings.Cut(id, ":")
	devNumber, err := strconv.ParseUint(dev, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	inode, err := strconv.ParseUint(ino, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if err := matches(f, id); err != nil {
		t.Fatalf("exact identity refused: %v", err)
	}
	if err := matches(f, fmt.Sprintf("%d:%d", devNumber+1, inode)); err != nil {
		t.Fatalf("identity recorded before the volume was renumbered refused: %v", err)
	}
	if err := matches(f, fmt.Sprintf("%d:%d", devNumber, inode+1)); err == nil {
		t.Fatal("different directory accepted")
	}
}

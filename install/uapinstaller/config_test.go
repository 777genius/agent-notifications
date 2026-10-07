//go:build legacy_uapinstaller

package uapinstaller

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOverlappingRootsSiblingsDoNotMatch(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	tmp := filepath.Join(base, "tmp")
	if err := os.MkdirAll(pkg, 0700); err != nil {
		t.Fatal(err)
	}
	if overlappingRoots(pkg, tmp) {
		t.Fatal("missing sibling temp treated as overlap")
	}
	if err := os.MkdirAll(tmp, 0700); err != nil {
		t.Fatal(err)
	}
	if overlappingRoots(pkg, tmp) {
		t.Fatal("distinct sibling roots overlapped")
	}
}

func TestOverlappingRootsNestedPath(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	if !overlappingRoots(pkg, filepath.Join(pkg, "tmp")) {
		t.Fatal("nested temp not overlapping")
	}
}

func TestOverlappingRootsUnicodeAlias(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	nfc := filepath.Join(base, "caf\u00e9")
	nfd := filepath.Join(base, "cafe\u0301")
	if err := os.MkdirAll(nfc, 0700); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(nfc)
	if err != nil {
		t.Fatal(err)
	}
	aliasInfo, aliasErr := os.Stat(nfd)
	if aliasErr != nil || !os.SameFile(info, aliasInfo) {
		t.Skip("filesystem does not alias Unicode NFC/NFD names")
	}
	if !overlappingRoots(nfc, nfd) {
		t.Fatal("unicode alias roots not overlapping")
	}
	if !overlappingRoots(nfc, filepath.Join(nfd, "tmp")) {
		t.Fatal("temp nested under unicode alias not overlapping")
	}
}

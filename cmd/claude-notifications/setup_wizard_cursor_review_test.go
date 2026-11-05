package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCursorCallerDeclaredNameRejectsUnsafeDirectoryManifest(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "plugin.json")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0600); err != nil { t.Fatal(err) }
	}
	write(`{"name":"TEST-plugin"}`)
	if got := cursorCallerDeclaredName(root); got != "TEST-plugin" {
		t.Fatalf("valid manifest refused: %q", got)
	}
	write(strings.Repeat(" ", (1<<20)+1))
	if got := cursorCallerDeclaredName(root); got != "" {
		t.Fatalf("oversized manifest accepted: %q", got)
	}
	if err := os.Remove(path); err != nil { t.Fatal(err) }
	if err := os.Mkdir(path, 0700); err != nil { t.Fatal(err) }
	if got := cursorCallerDeclaredName(root); got != "" {
		t.Fatalf("directory manifest accepted: %q", got)
	}
}

func TestCursorScopeRootCaseVariantRequiresSamePhysicalDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "TEST-MixedCase")
	if err := os.Mkdir(root, 0700); err != nil { t.Fatal(err) }
	if !sameCursorScopeRoot(root, root) { t.Fatal("identical scope rejected") }
	variant := strings.ToLower(root)
	if variant == root { variant = strings.ToUpper(root) }
	got := sameCursorScopeRoot(root, variant)
	if runtime.GOOS == "windows" {
		if info, err := os.Stat(variant); err == nil {
			orig, e := os.Stat(root)
			if e == nil && os.SameFile(orig, info) && !got { t.Fatal("physically identical case-only variant rejected") }
		}
	} else if got {
		t.Fatal("non-Windows case variant admitted")
	}
	other := filepath.Join(t.TempDir(), "TEST-MixedCase")
	if sameCursorScopeRoot(root, other) { t.Fatal("distinct directory admitted") }
}

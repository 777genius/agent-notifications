package main

import (
	"os"
	"path/filepath"
	"testing"
)

func setupCommandRoot(t *testing.T) string {
	t.Helper()
	p, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	// Setup fixtures must not inherit group-write from a shared host's umask.
	if e := os.Chmod(p, 0700); e != nil {
		t.Fatal(e)
	}
	return p
}

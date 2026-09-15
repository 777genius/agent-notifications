//go:build !windows

package config

import (
	"os"
	"testing"
)

type crashPermissions os.FileMode

func captureCrashPermissions(t *testing.T, path string, existing bool) crashPermissions {
	t.Helper()
	if !existing {
		return 0600
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return crashPermissions(info.Mode().Perm())
}

func assertCrashPermissions(t *testing.T, path string, before crashPermissions, existing, published bool) {
	t.Helper()
	want := os.FileMode(0600)
	if existing && !published {
		want = os.FileMode(before)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat mode: %v", err)
	}
	if info.Mode().Perm() != want {
		t.Fatalf("mode: got=%v want=%v", info.Mode().Perm(), want)
	}
}

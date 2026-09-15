//go:build windows

package config

import (
	"testing"

	"golang.org/x/sys/windows"
)

type crashPermissions struct {
	dacl windowsDACLSnapshot
}

func captureCrashPermissions(t *testing.T, path string, existing bool) crashPermissions {
	t.Helper()
	result := crashPermissions{}
	if !existing {
		return result
	}
	f, err := winOpen(path, windows.GENERIC_READ, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, false)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	result.dacl, err = windowsDACLEvidence(f)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertCrashPermissions(t *testing.T, path string, before crashPermissions, existing, _ bool) {
	t.Helper()
	f, err := winOpen(path, windows.GENERIC_READ, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, false)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if !existing {
		if err = checkWindowsACL(f, true); err != nil {
			t.Fatalf("fresh published target DACL is not private: %v", err)
		}
		return
	}
	after, err := windowsDACLEvidence(f)
	if err != nil {
		t.Fatal(err)
	}
	if before.dacl.protected != after.protected || !equalWindowsACEs(before.dacl.aces, after.aces) {
		t.Fatalf("crash changed DACL: before SDDL=%q protected=%t ACEs=%x; after SDDL=%q protected=%t ACEs=%x",
			before.dacl.sddl, before.dacl.protected, before.dacl.aces, after.sddl, after.protected, after.aces)
	}
}

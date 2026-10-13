package config

import (
	"errors"
	"io/fs"
	"testing"
)

// These are synthetic Windows paths. Every dependency is injected; the tests
// must never discover a native user's fallback store.
func TestWindowsAutomaticSelection(t *testing.T) {
	const home = `C:\profile`
	const legacy = `C:\profile\.claude\claude-notifications-go\config.json`
	const neutral = `C:\profile\AppData\agent-notifications\config.json`
	const fallback = `C:\ProgramData\agent-notifications-S-1-5-21-123\config.json`
	denied := &Error{Code: ConfigPermissionDenied}
	for _, tc := range []struct {
		name, existing, want string
		preflight            error
		wantCode             Code
	}{
		{name: "safe", want: neutral},
		{name: "denied", want: fallback, preflight: denied},
		{name: "stable-after-repair", existing: fallback, want: fallback},
		{name: "legacy-wins", existing: legacy, want: legacy, preflight: denied},
		{name: "universal-wins", existing: neutral, want: neutral, preflight: denied},
		{name: "unexpected-io", want: neutral, preflight: fs.ErrInvalid, wantCode: ConfigInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := map[string]fs.FileMode{}
			if tc.existing != "" {
				entries[tc.existing] = 0
			}
			e := fakeEnv("windows", map[string]string{"USERPROFILE": home, "APPDATA": `C:\profile\AppData`}, entries)
			e.windowsFallback = fallback
			calls := 0
			e.windowsPreflight = func(string, string) error { calls++; return tc.preflight }
			e.windowsFallbackCheck = func(p string, exists bool) error {
				if p != fallback || exists != (tc.existing == fallback) {
					t.Fatal("invalid fallback validation")
				}
				return nil
			}
			got, err := Resolve(e)
			if got.Path != tc.want {
				t.Fatalf("selected %+v", got)
			}
			var ce *Error
			if tc.wantCode != "" {
				if !errors.As(err, &ce) || ce.Code != tc.wantCode {
					t.Fatalf("error %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if tc.existing != "" && calls != 0 {
				t.Fatal("preflight retargeted existing configuration")
			}
		})
	}
}

func TestWindowsAutomaticDoesNotMaskExistingEntriesOrRecovery(t *testing.T) {
	const legacy = `C:\profile\.claude\claude-notifications-go\config.json`
	const neutral = `C:\profile\AppData\agent-notifications\config.json`
	const fallback = `C:\safe\agent-notifications-SID\config.json`
	for _, target := range []string{legacy, neutral, fallback} {
		for _, mode := range []fs.FileMode{0, fs.ModeDir, fs.ModeSymlink} {
			t.Run(target+mode.String(), func(t *testing.T) {
				e := fakeEnv("windows", map[string]string{"USERPROFILE": `C:\profile`, "APPDATA": `C:\profile\AppData`}, map[string]fs.FileMode{target: mode})
				e.windowsFallback = fallback
				e.windowsPreflight = func(string, string) error { t.Fatal("existing entry was discarded"); return nil }
				got, err := Resolve(e)
				if err != nil || !got.Exists || got.Path != target {
					t.Fatalf("%+v %v", got, err)
				}
			})
		}
		t.Run(target+"-recovery", func(t *testing.T) {
			e := fakeEnv("windows", map[string]string{"USERPROFILE": `C:\profile`, "APPDATA": `C:\profile\AppData`}, nil)
			e.windowsFallback = fallback
			e.windowsPreflight = func(string, string) error { t.Fatal("recovery failure was bypassed"); return nil }
			parent, _ := splitPath("windows", target)
			e.ReadDir = func(dir string) ([]fs.DirEntry, error) {
				if dir == parent {
					return []fs.DirEntry{fakeEntry{name: "config.json.tmp"}}, nil
				}
				return nil, fs.ErrNotExist
			}
			_, err := Resolve(e)
			var ce *Error
			if !errors.As(err, &ce) || ce.Code != ConfigRecoveryRequired {
				t.Fatalf("%v", err)
			}
		})
	}
}

func TestWindowsAutomaticFallbackValidationAndShadowWarning(t *testing.T) {
	const fallback = `C:\safe\config.json`
	e := fakeEnv("windows", map[string]string{"USERPROFILE": `C:\profile`, "APPDATA": `C:\profile\AppData`}, map[string]fs.FileMode{fallback: 0})
	e.windowsFallback = fallback
	e.windowsPreflight = func(string, string) error { return &Error{Code: ConfigPermissionDenied} }
	e.windowsFallbackCheck = func(string, bool) error { return &Error{Code: ConfigPermissionDenied} }
	got, err := Resolve(e)
	var ce *Error
	if got.Path != fallback || !errors.As(err, &ce) || ce.Code != ConfigPermissionDenied {
		t.Fatalf("%+v %v", got, err)
	}
	e = fakeEnv("windows", e.Vars, map[string]fs.FileMode{fallback: 0, `C:\profile\AppData\agent-notifications\config.json`: 0})
	e.windowsFallback = fallback
	e.windowsFallbackCheck = func(string, bool) error { t.Fatal("shadowed fallback checked for mutation"); return nil }
	got, err = Resolve(e)
	if !errors.As(err, &ce) || ce.Code != ConfigConflict || len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != ConfigMultipleCandidates {
		t.Fatalf("%+v %v", got, err)
	}
	e = fakeEnv("windows", e.Vars, nil)
	e.windowsFallbackErr = &Error{Code: ConfigBaseUnavailable}
	e.windowsPreflight = func(string, string) error { return &Error{Code: ConfigPermissionDenied} }
	_, err = Resolve(e)
	if !errors.As(err, &ce) || ce.Code != ConfigBaseUnavailable {
		t.Fatal(err)
	}
}

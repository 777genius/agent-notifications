package config

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoveryArtifactsUseNativeFilenameEquivalence(t *testing.T) {
	for _, goos := range []string{"windows", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			e := fakeEnv(goos, map[string]string{
				"HOME":        "/home/u",
				"USERPROFILE": `C:\Users\u`,
				"APPDATA":     `C:\Users\u\AppData\Roaming`,
			}, nil)
			e.Lstat = func(p string) (fs.FileInfo, error) {
				if strings.HasSuffix(strings.ToLower(p), "config.json.backup-interrupted") {
					return fakeEntry{mode: 0600}, nil
				}
				return nil, fs.ErrNotExist
			}
			e.ReadDir = func(string) ([]fs.DirEntry, error) {
				return []fs.DirEntry{fakeEntry{"CONFIG.JSON.BACKUP-interrupted", 0}}, nil
			}
			_, err := Resolve(e)
			var configErr *Error
			if !errors.As(err, &configErr) || configErr.Code != ConfigRecoveryRequired {
				t.Fatalf("case alias hid recovery evidence: %v", err)
			}
		})
	}
}

func TestRecoveryArtifactsDoNotInventCaseAliases(t *testing.T) {
	e := fakeEnv("darwin", map[string]string{"HOME": "/home/u"}, nil)
	e.ReadDir = func(string) ([]fs.DirEntry, error) {
		return []fs.DirEntry{fakeEntry{"CONFIG.JSON.BACKUP-unrelated", 0}}, nil
	}
	if _, err := Resolve(e); err != nil {
		t.Fatalf("case-sensitive volume alias invented: %v", err)
	}
}

func TestRecoveryArtifactsUseNativeUnicodeNormalization(t *testing.T) {
	e := fakeEnv("darwin", map[string]string{OverrideEnv: "/fixture/caf\u00e9.json"}, nil)
	e.ReadDir = func(string) ([]fs.DirEntry, error) {
		return []fs.DirEntry{fakeEntry{"cafe\u0301.json.backup-interrupted", 0}}, nil
	}
	e.Lstat = func(p string) (fs.FileInfo, error) {
		if filepath.ToSlash(p) == "/fixture/caf\u00e9.json.backup-interrupted" {
			return fakeEntry{name: filepath.Base(p), mode: 0600}, nil
		}
		return nil, fs.ErrNotExist
	}
	_, err := Resolve(e)
	var configErr *Error
	if !errors.As(err, &configErr) || configErr.Code != ConfigRecoveryRequired {
		t.Fatalf("normalization alias hid recovery evidence: %v", err)
	}
}

func TestRecoveryArtifactsUseTerminalSuffixWithMarkerInBasename(t *testing.T) {
	for _, tt := range []struct {
		name, target, entry, probe string
	}{
		{
			"case",
			"/fixture/CONFIG.backup-work.json",
			"config.backup-work.json.TMP-interrupted",
			"/fixture/CONFIG.backup-work.json.TMP-interrupted",
		},
		{
			"normalization",
			"/fixture/caf\u00e9.tmp-work.json",
			"cafe\u0301.tmp-work.json.BACKUP-interrupted",
			"/fixture/caf\u00e9.tmp-work.json.BACKUP-interrupted",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := fakeEnv("darwin", map[string]string{OverrideEnv: tt.target}, nil)
			e.ReadDir = func(string) ([]fs.DirEntry, error) {
				return []fs.DirEntry{fakeEntry{tt.entry, 0}}, nil
			}
			e.Lstat = func(p string) (fs.FileInfo, error) {
				if filepath.ToSlash(p) == filepath.ToSlash(tt.probe) {
					return fakeEntry{name: filepath.Base(p), mode: 0600}, nil
				}
				return nil, fs.ErrNotExist
			}
			suffix, ok := recoverySuffix(tt.entry)
			assembled := filepath.ToSlash(filepath.Join(filepath.Dir(tt.target), filepath.Base(tt.target)+suffix))
			if !ok || assembled != filepath.ToSlash(tt.probe) {
				t.Fatalf("terminal suffix probe mismatch: suffix=%q ok=%v", suffix, ok)
			}
			_, err := Resolve(e)
			var configErr *Error
			if !errors.As(err, &configErr) || configErr.Code != ConfigRecoveryRequired {
				t.Fatalf("marker in basename hid recovery evidence: %v", err)
			}
		})
	}
}

func TestReadDocumentSelectionTracksBytesAfterSelectionChange(t *testing.T) {
	e := fakeEnv("linux", map[string]string{"HOME": "/home/u"}, nil)
	legacyPresent := false
	e.Lstat = func(p string) (fs.FileInfo, error) {
		if legacyPresent && p == "/home/u/.claude/claude-notifications-go/config.json" {
			return fakeEntry{mode: 0600}, nil
		}
		if p == "/home/u/.config/agent-notifications/config.json" {
			return fakeEntry{mode: 0600}, nil
		}
		return nil, fs.ErrNotExist
	}
	reads := 0
	d, _, selected, err := ReadDocumentSelection(ReadRequest{Env: e, ReadSnapshot: func(p string, limit int) (Snapshot, error) {
		reads++
		if reads == 1 {
			legacyPresent = true
		}
		return Snapshot{Bytes: []byte(`{}`), PhysicalPath: p}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if reads != 2 || selected.Source != "legacy" || d.Revision() == "" {
		t.Fatalf("incoherent result: reads=%d selection=%+v", reads, selected)
	}
}

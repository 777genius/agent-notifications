package config

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// All owned keys, including the long-s and Kelvin-sign simple folds used by
// encoding/json. Values deliberately conflict in both textual orders.
func desktopAliasCases() map[string]string {
	cases := map[string]string{}
	full := `{"enabled":false,"sound":false,"clickToFocus":false}`
	for key, aliases := range map[string][]string{
		"notifications": {"Notifications", "notificationſ"},
		"desktop":       {"Desktop", "deſktop", "desKtop"},
		"enabled":       {"Enabled", "ENABLED"},
		"sound":         {"Sound", "ſound"},
		"clickToFocus":  {"ClickToFocus", "clicKToFocus", "clickToFocuſ"},
	} {
		for _, alias := range aliases {
			wrap := func(body string) string { return `{"notifications":{"desktop":{` + body + `}}}` }
			value, canonical := `false`, `true`
			if key == "notifications" {
				wrap = func(body string) string { return `{` + body + `}` }
				value = `{"desktop":` + full + `}`
				canonical = `{"desktop":{"enabled":true,"sound":true,"clickToFocus":true}}`
			} else if key == "desktop" {
				wrap = func(body string) string { return `{"notifications":{` + body + `}}` }
				value = full
				canonical = `{"enabled":true,"sound":true,"clickToFocus":true}`
			}
			a := `"` + alias + `":` + value
			c := `"` + key + `":` + canonical
			extra := ""
			if key != "notifications" && key != "desktop" {
				for _, other := range []string{"enabled", "sound", "clickToFocus"} {
					if other != key {
						extra += `,"` + other + `":false`
					}
				}
			}
			cases[alias+"/alone"] = wrap(a)
			cases[alias+"/canonical-first"] = wrap(c + "," + a + extra)
			cases[alias+"/alias-first"] = wrap(a + "," + c + extra)
		}
	}
	return cases
}

func TestDesktopAliasLegacyDecoder(t *testing.T) {
	for name, raw := range desktopAliasCases() {
		if !strings.HasSuffix(name, "/alone") {
			continue
		}
		t.Run(name, func(t *testing.T) {
			cfg := DefaultConfig()
			if err := json.Unmarshal([]byte(raw), cfg); err != nil {
				t.Fatal(err)
			}
			d := cfg.Notifications.Desktop
			switch {
			case strings.HasPrefix(name, "Enabled/"), strings.HasPrefix(name, "ENABLED/"):
				if d.Enabled {
					t.Fatal("legacy decoder did not read false")
				}
			case strings.HasPrefix(name, "Sound/"), strings.HasPrefix(name, "ſound/"):
				if d.Sound {
					t.Fatal("legacy decoder did not read false")
				}
			case strings.Contains(name, "Focus"), strings.Contains(name, "Focuſ"):
				if d.ClickToFocus {
					t.Fatal("legacy decoder did not read false")
				}
			default:
				if d.Enabled || d.Sound || d.ClickToFocus {
					t.Fatal("legacy container alias did not read false")
				}
			}
		})
	}
}

func TestDesktopAliasPrepareRejectsWithoutWriting(t *testing.T) {
	for name, raw := range desktopAliasCases() {
		for _, source := range []string{"canonical", "legacy", "defaults"} {
			t.Run(name+"/"+source, func(t *testing.T) {
				home := t.TempDir()
				for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "CODEX_HOME"} {
					t.Setenv(key, home)
				}
				c, l, d := prepPaths(t)
				p := map[string]string{"canonical": c, "legacy": l, "defaults": d}[source]
				putPrep(t, p, raw)
				result, err := PrepareGlobalConfig(context.Background(), c, l, d)
				if !errors.Is(err, errGlobalConfig) || result.Ready || result.Changed {
					t.Errorf("accepted alias: %+v, %v", result, err)
				}
				b, err := os.ReadFile(p)
				if err != nil || string(b) != raw {
					t.Error("source changed", err)
				}
				if source != "canonical" {
					if _, err := os.Stat(c); !os.IsNotExist(err) {
						t.Error("canonical config created", err)
					}
				}
				entries, err := os.ReadDir(filepath.Dir(c))
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					if entry.Name() != "config.json.lock" && !(source == "canonical" && entry.Name() == "config.json") {
						t.Errorf("unexpected file: %s", entry.Name())
					}
				}
			})
		}
	}
}

func TestDesktopAliasSharedValidator(t *testing.T) {
	for name, raw := range desktopAliasCases() {
		t.Run(name, func(t *testing.T) {
			if _, _, _, err := ValidateGlobalDesktop([]byte(raw)); !errors.Is(err, errGlobalConfig) {
				t.Fatalf("alias accepted: %v", err)
			}
		})
	}
}

func TestDesktopAliasPreservesForeignAndCanonicalFalse(t *testing.T) {
	raw := `{"NotificationsOther":{"Enabled":false},"notifications":{"DesktopOther":{"Sound":false},"desktop":{"enabled":false,"Foreign":{"Enabled":false},"SOUND_OTHER":9007199254740993}}}`
	out, values, changed, err := completeDesktop([]byte(raw), true)
	if err != nil || !changed || values != [3]bool{false, true, true} {
		t.Fatal(values, changed, err)
	}
	for _, fragment := range []string{`"NotificationsOther":{"Enabled":false}`, `"DesktopOther":{"Sound":false}`, `"Foreign":{"Enabled":false}`, `"SOUND_OTHER":9007199254740993`} {
		if !strings.Contains(string(out), fragment) {
			t.Errorf("foreign data lost: %s", fragment)
		}
	}
	again, values, changed, err := completeDesktop(out, true)
	if err != nil || changed || string(again) != string(out) || values[0] {
		t.Fatal("canonical false/noop changed", err)
	}
}

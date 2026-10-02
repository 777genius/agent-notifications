package geminiinstall

import (
	"os"
	"path/filepath"
	"testing"
)

// Red regression: the UI shows the Gemini parent as the destination, appends
// .gemini twice, or ignores an explicit root that the actual writer uses.
func TestResolvedScopeMatchesInstallerAuthority(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		request Request
		want    string
		invalid bool
	}{
		{"default", Request{HomeDir: home}, filepath.Join(home, ".gemini"), false},
		{"parent", Request{HomeDir: home, GeminiHome: filepath.Join(home, "parent")}, filepath.Join(home, "parent", ".gemini"), false},
		{"explicit", Request{GeminiHome: "relative", ConfigRoot: filepath.Join(home, "selected")}, filepath.Join(home, "selected"), false},
		{"relative-parent", Request{HomeDir: home, GeminiHome: "relative"}, "", true},
		{"relative-root", Request{HomeDir: home, ConfigRoot: "relative"}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveConfigRoot(tc.request)
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid authority fell back")
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("root=%q err=%v want=%q", got, err, tc.want)
			}
			path, err := settingsPath(tc.request)
			if err != nil || path != filepath.Join(tc.want, "settings.json") {
				t.Fatalf("writer path=%q err=%v", path, err)
			}
		})
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("resolver wrote profile: %v %v", entries, err)
	}
}

// Red regression: extracting the resolver changes the writer's canonical
// directory alias handling, so the displayed and committed roots diverge.
func TestGeminiConfigRootCanonicalAlias(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, "target")
	alias := filepath.Join(home, "alias")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	r := Request{ConfigRoot: alias}
	root, err := ResolveConfigRoot(r)
	if err != nil || root != target {
		t.Fatalf("root=%q err=%v", root, err)
	}
	path, err := settingsPath(r)
	if err != nil || path != filepath.Join(target, "settings.json") {
		t.Fatalf("writer=%q err=%v", path, err)
	}
}

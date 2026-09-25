//go:build linux

package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type fakeProcess struct {
	pid  int
	ppid int
	comm string
	exe  string
}

// useFakeProc points process-ancestry lookups at a fake /proc tree whose walk
// starts at startPID, and restores the real one when the test ends.
func useFakeProc(t *testing.T, startPID int, processes ...fakeProcess) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range processes {
		dir := filepath.Join(root, fmt.Sprint(p.pid))
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		stat := fmt.Sprintf("%d (%s) S %d %d %d 0 -1 4194560\n", p.pid, p.comm, p.ppid, p.pid, p.pid)
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "comm"), []byte(p.comm+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if p.exe != "" {
			if err := os.Symlink(p.exe, filepath.Join(dir, "exe")); err != nil {
				t.Fatal(err)
			}
		}
	}

	origRoot, origParent := procRoot, getParentPID
	t.Cleanup(func() { procRoot, getParentPID = origRoot, origParent })
	procRoot = root
	getParentPID = func() int { return startPID }
	return root
}

// writeIDEHome creates an IDE install with the given product-info.json and
// returns its home directory.
func writeIDEHome(t *testing.T, productInfo string) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "apps", "ide")
	if err := os.MkdirAll(home, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "product-info.json"), []byte(productInfo), 0644); err != nil {
		t.Fatal(err)
	}
	return home
}

const phpStormProductInfo = `{"name":"PhpStorm","launch":[{"os":"Linux","arch":"amd64","launcherPath":"bin/phpstorm","startupWmClass":"jetbrains-phpstorm"}]}`

func TestDetectJetBrainsClass(t *testing.T) {
	phpStorm := writeIDEHome(t, phpStormProductInfo)
	ideaCE := writeIDEHome(t, `{"launch":[{"os":"macOS","startupWmClass":"jetbrains-idea"},{"os":"Linux","startupWmClass":"jetbrains-idea-ce"}]}`)
	noLinuxClass := writeIDEHome(t, `{"launch":[{"os":"Windows","startupWmClass":"jetbrains-phpstorm"},{"os":"Linux"}]}`)
	notJetBrains := writeIDEHome(t, `{"launch":[{"os":"Linux","startupWmClass":"other-app"}]}`)
	invalidJSON := writeIDEHome(t, `{"launch":`)

	shell := func(pid, ppid int) fakeProcess {
		return fakeProcess{pid: pid, ppid: ppid, comm: "bash", exe: "/usr/bin/bash"}
	}
	claude := fakeProcess{pid: 100, ppid: 101, comm: "claude", exe: "/home/u/.local/share/claude/versions/2.1.0"}

	tests := []struct {
		name      string
		env       string
		processes []fakeProcess
		want      string
		wantOK    bool
	}{
		{
			name: "native launcher",
			env:  "JetBrains-JediTerm",
			processes: []fakeProcess{claude, shell(101, 102),
				{pid: 102, ppid: 1, comm: "phpstorm", exe: filepath.Join(phpStorm, "bin", "phpstorm")}},
			want: "jetbrains-phpstorm", wantOK: true,
		},
		{
			name: "sh launcher runs bundled jbr java",
			env:  "JetBrains-JediTerm",
			processes: []fakeProcess{claude, shell(101, 102),
				{pid: 102, ppid: 1, comm: "java", exe: filepath.Join(phpStorm, "jbr", "bin", "java")}},
			want: "jetbrains-phpstorm", wantOK: true,
		},
		{
			name: "binary replaced while running",
			env:  "JetBrains-JediTerm",
			processes: []fakeProcess{claude, shell(101, 102),
				{pid: 102, ppid: 1, comm: "phpstorm", exe: filepath.Join(phpStorm, "bin", "phpstorm") + " (deleted)"}},
			want: "jetbrains-phpstorm", wantOK: true,
		},
		{
			name: "edition comes from product-info, not comm",
			env:  "JetBrains-JediTerm",
			processes: []fakeProcess{claude, shell(101, 102),
				{pid: 102, ppid: 1, comm: "idea", exe: filepath.Join(ideaCE, "bin", "idea")}},
			want: "jetbrains-idea-ce", wantOK: true,
		},
		{
			name: "comm with spaces and parens",
			env:  "JetBrains-JediTerm",
			processes: []fakeProcess{{pid: 100, ppid: 101, comm: "my (odd) tool", exe: "/usr/bin/tool"},
				{pid: 101, ppid: 1, comm: "phpstorm", exe: filepath.Join(phpStorm, "bin", "phpstorm")}},
			want: "jetbrains-phpstorm", wantOK: true,
		},
		{
			name: "terminal emulator env missing",
			processes: []fakeProcess{claude, shell(101, 102),
				{pid: 102, ppid: 1, comm: "phpstorm", exe: filepath.Join(phpStorm, "bin", "phpstorm")}},
		},
		{
			name: "standalone terminal started from the IDE terminal",
			env:  "JetBrains-JediTerm",
			processes: []fakeProcess{claude, shell(101, 102),
				{pid: 102, ppid: 103, comm: "konsole", exe: "/usr/bin/konsole"},
				shell(103, 104),
				{pid: 104, ppid: 1, comm: "phpstorm", exe: filepath.Join(phpStorm, "bin", "phpstorm")}},
		},
		{
			name: "VS Code started from the IDE terminal",
			env:  "JetBrains-JediTerm",
			processes: []fakeProcess{claude, shell(101, 102),
				{pid: 102, ppid: 103, comm: "code", exe: "/usr/share/code/code"},
				shell(103, 104),
				{pid: 104, ppid: 1, comm: "phpstorm", exe: filepath.Join(phpStorm, "bin", "phpstorm")}},
		},
		{
			name:      "no IDE ancestor",
			env:       "JetBrains-JediTerm",
			processes: []fakeProcess{claude, shell(101, 102), {pid: 102, ppid: 1, comm: "systemd", exe: "/usr/lib/systemd/systemd"}},
		},
		{
			name: "product-info without a Linux class",
			env:  "JetBrains-JediTerm",
			processes: []fakeProcess{claude,
				{pid: 101, ppid: 1, comm: "phpstorm", exe: filepath.Join(noLinuxClass, "bin", "phpstorm")}},
		},
		{
			name: "product-info class without jetbrains prefix",
			env:  "JetBrains-JediTerm",
			processes: []fakeProcess{claude,
				{pid: 101, ppid: 1, comm: "app", exe: filepath.Join(notJetBrains, "bin", "app")}},
		},
		{
			name: "invalid product-info",
			env:  "JetBrains-JediTerm",
			processes: []fakeProcess{claude,
				{pid: 101, ppid: 1, comm: "phpstorm", exe: filepath.Join(invalidJSON, "bin", "phpstorm")}},
		},
		{
			name:      "ancestor missing from proc",
			env:       "JetBrains-JediTerm",
			processes: []fakeProcess{claude},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TERMINAL_EMULATOR", tt.env)
			useFakeProc(t, 100, tt.processes...)

			got, ok := detectJetBrainsClass()
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("detectJetBrainsClass() = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestDetectJetBrainsClass_BrokenStat(t *testing.T) {
	t.Setenv("TERMINAL_EMULATOR", "JetBrains-JediTerm")
	root := useFakeProc(t, 100, fakeProcess{pid: 100, ppid: 101, comm: "claude", exe: "/usr/bin/claude"})
	if err := os.WriteFile(filepath.Join(root, "100", "stat"), []byte("100 (claude"), 0644); err != nil {
		t.Fatal(err)
	}

	if got, ok := detectJetBrainsClass(); ok {
		t.Errorf("detectJetBrainsClass() = (%q, true), want not detected", got)
	}
}

func TestDetectJetBrainsClass_StopsOnParentCycle(t *testing.T) {
	t.Setenv("TERMINAL_EMULATOR", "JetBrains-JediTerm")
	useFakeProc(t, 100,
		fakeProcess{pid: 100, ppid: 101, comm: "bash", exe: "/usr/bin/bash"},
		fakeProcess{pid: 101, ppid: 100, comm: "bash", exe: "/usr/bin/bash"},
	)

	if got, ok := detectJetBrainsClass(); ok {
		t.Errorf("detectJetBrainsClass() = (%q, true), want not detected", got)
	}
}

func TestGetTerminalName_JetBrains(t *testing.T) {
	phpStorm := writeIDEHome(t, phpStormProductInfo)
	ide := []fakeProcess{
		{pid: 100, ppid: 101, comm: "claude", exe: "/usr/bin/claude"},
		{pid: 101, ppid: 1, comm: "phpstorm", exe: filepath.Join(phpStorm, "bin", "phpstorm")},
	}
	noIDE := []fakeProcess{
		{pid: 100, ppid: 1, comm: "claude", exe: "/usr/bin/claude"},
	}

	tests := []struct {
		name      string
		env       map[string]string
		processes []fakeProcess
		want      string
	}{
		{
			name:      "JediTerm with IDE ancestor",
			env:       map[string]string{"TERMINAL_EMULATOR": "JetBrains-JediTerm"},
			processes: ide,
			want:      "jetbrains-phpstorm",
		},
		{
			name:      "IDE wins over TERM_PROGRAM inherited from the IDE launcher",
			env:       map[string]string{"TERMINAL_EMULATOR": "JetBrains-JediTerm", "TERM_PROGRAM": "kitty"},
			processes: ide,
			want:      "jetbrains-phpstorm",
		},
		{
			name:      "IDE wins over Konsole vars inherited from the IDE launcher",
			env:       map[string]string{"TERMINAL_EMULATOR": "JetBrains-JediTerm", "KONSOLE_VERSION": "250401"},
			processes: ide,
			want:      "jetbrains-phpstorm",
		},
		{
			name:      "JediTerm without IDE ancestor keeps the old order",
			env:       map[string]string{"TERMINAL_EMULATOR": "JetBrains-JediTerm", "KONSOLE_VERSION": "250401"},
			processes: noIDE,
			want:      "konsole",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restore := saveTerminalEnv(t)
			defer restore()
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			useFakeProc(t, 100, tt.processes...)

			if got := GetTerminalName(); got != tt.want {
				t.Errorf("GetTerminalName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestJetBrainsProject(t *testing.T) {
	root := t.TempDir()
	mkdir := func(parts ...string) string {
		dir := filepath.Join(append([]string{root}, parts...)...)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	writeName := func(project, name string) {
		if err := os.WriteFile(filepath.Join(root, project, ".idea", ".name"), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
	}

	mkdir("named", ".idea")
	writeName("named", "Display Name\n")
	mkdir("empty-name", ".idea")
	writeName("empty-name", " \n")
	mkdir("agent-notifications", ".idea")
	nested := mkdir("agent-notifications", "internal", "daemon")
	plain := mkdir("plain", "sub")

	tests := []struct {
		name     string
		cwd      string
		wantName string
		wantRoot string
	}{
		{"idea name file", filepath.Join(root, "named"), "Display Name", filepath.Join(root, "named")},
		{"empty idea name file", filepath.Join(root, "empty-name"), "empty-name", filepath.Join(root, "empty-name")},
		{"idea without name file", filepath.Join(root, "agent-notifications"), "agent-notifications", filepath.Join(root, "agent-notifications")},
		{"idea in a parent dir", nested, "agent-notifications", filepath.Join(root, "agent-notifications")},
		{"no idea dir", plain, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, projectRoot := jetBrainsProject(tt.cwd)
			if name != tt.wantName || projectRoot != tt.wantRoot {
				t.Errorf("jetBrainsProject(%q) = (%q, %q), want (%q, %q)", tt.cwd, name, projectRoot, tt.wantName, tt.wantRoot)
			}
		})
	}
}

func TestGetFocusProjectPath(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "agent-notifications")
	cwd := filepath.Join(project, "internal")
	if err := os.MkdirAll(filepath.Join(project, ".idea"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		terminal string
		cwd      string
		want     string
	}{
		{"JetBrains project root", "jetbrains-goland", cwd, project},
		{"JetBrains without idea dir", "jetbrains-goland", root, ""},
		{"other terminals", "konsole", cwd, ""},
		{"empty cwd", "jetbrains-goland", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetFocusProjectPath(tt.terminal, tt.cwd); got != tt.want {
				t.Errorf("GetFocusProjectPath(%q, %q) = %q, want %q", tt.terminal, tt.cwd, got, tt.want)
			}
		})
	}
}

func TestGetFocusFolderName(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "agent-notifications", "internal")
	if err := os.MkdirAll(filepath.Join(root, "agent-notifications", ".idea"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		terminal string
		cwd      string
		want     string
	}{
		{"JetBrains uses the project root", "jetbrains-goland", cwd, "agent-notifications"},
		{"JetBrains without idea dir uses cwd", "jetbrains-goland", root, filepath.Base(root)},
		{"other terminals use cwd", "konsole", cwd, "internal"},
		{"empty cwd", "jetbrains-goland", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetFocusFolderName(tt.terminal, tt.cwd); got != tt.want {
				t.Errorf("GetFocusFolderName(%q, %q) = %q, want %q", tt.terminal, tt.cwd, got, tt.want)
			}
		})
	}
}

func TestJetBrainsTitleMatches(t *testing.T) {
	t.Setenv("HOME", "/home/u")

	tests := []struct {
		title   string
		project string
		path    string
		want    bool
	}{
		{"agent-notifications", "agent-notifications", "", true},
		{"agent-notifications – README.md", "agent-notifications", "", true},
		{"wvc-image-processing [/var/www/html/wvc-image-processing] – README.md", "wvc-image-processing", "", true},
		{"agent-notifications – README.md", "agent", "", false},
		{"agent-notifications - README.md", "agent-notifications", "", false},
		{"README.md – agent-notifications", "agent-notifications", "", false},
		{"agent-notifications – README.md", "", "", false},
		// With the project path, a [location] must name this project.
		{"agent", "agent", "/srv/agent", true},
		{"agent – main.go", "agent", "/srv/agent", true},
		{"agent [/srv/agent] – main.go", "agent", "/srv/agent", true},
		{"agent [/opt/agent] – main.go", "agent", "/srv/agent", false},
		{"agent [staging] – main.go", "agent", "/srv/agent", false},
		{"agent [/srv/agent-old] – main.go", "agent", "/srv/agent", false},
		// Under the user home JetBrains shows ~/<relative path>.
		{"api [~/src/api] – main.go", "api", "/home/u/src/api", true},
		{"api [/home/u/src/api] – main.go", "api", "/home/u/src/api", true},
		{"api [~/other/api] – main.go", "api", "/home/u/src/api", false},
	}

	for _, tt := range tests {
		if got := jetBrainsTitleMatches(tt.title, tt.project, tt.path); got != tt.want {
			t.Errorf("jetBrainsTitleMatches(%q, %q, %q) = %v, want %v", tt.title, tt.project, tt.path, got, tt.want)
		}
	}
}

// Two open projects with the same name: only the path tells them apart.
func TestTryKdotool_SameNameProjects(t *testing.T) {
	titles := map[string]string{
		"{a}": "api [/srv/a/api] – main.go",
		"{b}": "api [/srv/b/api] – main.go",
	}

	for _, tt := range []struct {
		path string
		want string
	}{
		{"/srv/b/api", "{b}"},
		{"/srv/a/api", "{a}"},
		{"", "{a}"},
	} {
		trace := useFakeWindowTool(t, "kdotool", []string{"{a}", "{b}"}, titles)
		if err := tryKdotool(FocusHints{TerminalName: "jetbrains-goland", FolderName: "api", ProjectPath: tt.path}); err != nil {
			t.Fatalf("tryKdotool() error = %v", err)
		}
		if got := readActivatedWindows(t, trace); !reflect.DeepEqual(got, []string{tt.want}) {
			t.Errorf("path %q: activated %v, want [%s]", tt.path, got, tt.want)
		}
	}
}

func TestTryKdotool_WindowOrder(t *testing.T) {
	titles := map[string]string{
		"{a}": "agent – main.go",
		"{b}": "agent-notifications – README.md",
		"{c}": "agent-notifications-fork – README.md",
	}

	tests := []struct {
		name     string
		terminal string
		folder   string
		ids      []string
		want     []string
	}{
		{"JetBrains activates the project window", "jetbrains-goland", "agent-notifications", []string{"{a}", "{b}"}, []string{"{b}"}},
		{"JetBrains match is strict", "jetbrains-goland", "agent", []string{"{b}", "{c}", "{a}"}, []string{"{a}"}},
		{"no title match keeps the first window", "jetbrains-goland", "other", []string{"{a}", "{b}"}, []string{"{a}"}},
		{"no folder keeps the first window", "jetbrains-goland", "", []string{"{a}", "{b}"}, []string{"{a}"}},
		{"other terminals match by substring", "konsole", "agent-notifications", []string{"{a}", "{c}"}, []string{"{c}"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trace := useFakeWindowTool(t, "kdotool", tt.ids, titles)

			if err := TryKdotool(tt.terminal, tt.folder); err != nil {
				t.Fatalf("TryKdotool() error = %v", err)
			}
			if got := readActivatedWindows(t, trace); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("activated %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTryKdotool_NoWindows(t *testing.T) {
	trace := useFakeWindowTool(t, "kdotool", nil, nil)

	if err := TryKdotool("jetbrains-goland", "agent-notifications"); err == nil {
		t.Fatal("TryKdotool() error = nil, want error")
	}
	if got := readActivatedWindows(t, trace); len(got) != 0 {
		t.Errorf("activated %v, want none", got)
	}
}

// On X11 the strict matcher must beat a top-most window whose title only
// contains the project name.
func TestTryXdotool_JetBrainsStrictTitleMatch(t *testing.T) {
	titles := map[string]string{
		"1": "agent – main.go",
		"2": "agent-notifications – README.md",
	}
	trace := useFakeWindowTool(t, "xdotool", []string{"1", "2"}, titles)

	if err := TryXdotool("jetbrains-goland", "agent"); err != nil {
		t.Fatalf("TryXdotool() error = %v", err)
	}
	if got := readActivatedWindows(t, trace); !reflect.DeepEqual(got, []string{"1"}) {
		t.Errorf("activated %v, want [1]", got)
	}
}

func writeDesktopFile(t *testing.T, dir, name, wmClass string) {
	t.Helper()
	apps := filepath.Join(dir, "applications")
	if err := os.MkdirAll(apps, 0755); err != nil {
		t.Fatal(err)
	}
	content := "[Desktop Entry]\nName=IDE\nIcon=ide\nStartupWMClass=" + wmClass + "\n"
	if err := os.WriteFile(filepath.Join(apps, name+".desktop"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestGetNotificationDesktopEntryID_JetBrains(t *testing.T) {
	const toolbox = "jetbrains-goland-31970c23-ef28-4826-a2ea-51dff0e57df2"

	tests := []struct {
		name  string
		setup func(t *testing.T, home, system string)
		class string
		want  string
	}{
		{
			name:  "Toolbox entry with uuid suffix",
			setup: func(t *testing.T, home, _ string) { writeDesktopFile(t, home, toolbox, "jetbrains-goland") },
			class: "jetbrains-goland", want: toolbox,
		},
		{
			name: "entry in XDG_DATA_DIRS",
			setup: func(t *testing.T, _, system string) {
				writeDesktopFile(t, system, "jetbrains-goland-9999", "jetbrains-goland")
			},
			class: "jetbrains-goland", want: "jetbrains-goland-9999",
		},
		{
			name: "prefix match with another class is skipped",
			setup: func(t *testing.T, home, _ string) {
				writeDesktopFile(t, home, "jetbrains-idea-ce-1234", "jetbrains-idea-ce")
			},
			class: "jetbrains-idea", want: "jetbrains-idea",
		},
		{
			name:  "no entry keeps the class",
			setup: func(*testing.T, string, string) {},
			class: "jetbrains-goland", want: "jetbrains-goland",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home, system := t.TempDir(), t.TempDir()
			t.Setenv("XDG_DATA_HOME", home)
			t.Setenv("XDG_DATA_DIRS", system)
			t.Setenv("XDG_SESSION_TYPE", "wayland")
			t.Setenv("XDG_CURRENT_DESKTOP", "KDE")
			tt.setup(t, home, system)

			if got := GetNotificationDesktopEntryID(tt.class); got != tt.want {
				t.Errorf("GetNotificationDesktopEntryID(%q) = %q, want %q", tt.class, got, tt.want)
			}
		})
	}
}

// GNOME Wayland keeps the plugin's own entry, which avoids a stuck loading cursor.
func TestGetNotificationDesktopEntryID_JetBrainsOnGnomeWayland(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_DATA_HOME", home)
	t.Setenv("XDG_DATA_DIRS", t.TempDir())
	t.Setenv("XDG_SESSION_TYPE", "wayland")
	t.Setenv("XDG_CURRENT_DESKTOP", "GNOME")
	writeDesktopFile(t, home, "jetbrains-goland-1234", "jetbrains-goland")
	if err := os.WriteFile(filepath.Join(home, "applications", claudeNotificationsDesktopEntryID+".desktop"), []byte("[Desktop Entry]\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if got := GetNotificationDesktopEntryID("jetbrains-goland"); got != claudeNotificationsDesktopEntryID {
		t.Errorf("GetNotificationDesktopEntryID() = %q, want %q", got, claudeNotificationsDesktopEntryID)
	}
}

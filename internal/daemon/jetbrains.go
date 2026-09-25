// ABOUTME: Detects JetBrains IDE terminals (JediTerm) and the IDE project they belong to.
// ABOUTME: Only Linux exposes /proc; on other platforms detection finds nothing.
package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// jetBrainsTerminalEmulator is the TERMINAL_EMULATOR value every JetBrains IDE
// terminal sets. It is inherited by anything started from that terminal.
const jetBrainsTerminalEmulator = "JetBrains-JediTerm"

// jetBrainsClassPrefix starts every JetBrains IDE window class.
const jetBrainsClassPrefix = "jetbrains-"

// maxProcessAncestors bounds the /proc walk in case of a parent cycle.
const maxProcessAncestors = 32

var (
	procRoot     = "/proc"
	getParentPID = os.Getppid
)

// standaloneTerminalComms lists /proc/<pid>/comm values of terminals and
// editors that host their own shells. Meeting one before the IDE means the
// session runs in a terminal started from the IDE terminal.
var standaloneTerminalComms = map[string]struct{}{
	"alacritty":       {},
	"code":            {},
	"cursor":          {},
	"foot":            {},
	"ghostty":         {},
	"gnome-terminal-": {}, // gnome-terminal-server, truncated to 15 chars
	"kgx":             {},
	"kitty":           {},
	"konsole":         {},
	"lxterminal":      {},
	"mate-terminal":   {},
	"ptyxis-agent":    {},
	"qterminal":       {},
	"terminator":      {},
	"tilix":           {},
	"urxvt":           {},
	"wezterm-gui":     {},
	"xfce4-terminal":  {},
	"xterm":           {},
	"yakuake":         {},
}

// isJetBrainsTerminalName reports whether terminalName is a JetBrains IDE window class.
func isJetBrainsTerminalName(terminalName string) bool {
	return strings.HasPrefix(strings.ToLower(terminalName), jetBrainsClassPrefix)
}

// detectJetBrainsClass returns the window class of the JetBrains IDE whose
// terminal runs this process, e.g. "jetbrains-phpstorm". The class comes from
// the IDE's product-info.json, so every product and edition is covered.
func detectJetBrainsClass() (string, bool) {
	if os.Getenv("TERMINAL_EMULATOR") != jetBrainsTerminalEmulator {
		return "", false
	}

	pid := getParentPID()
	for i := 0; i < maxProcessAncestors && pid > 1; i++ {
		dir := filepath.Join(procRoot, strconv.Itoa(pid))
		if comm, err := os.ReadFile(filepath.Join(dir, "comm")); err == nil {
			if _, ok := standaloneTerminalComms[strings.TrimSpace(string(comm))]; ok {
				return "", false
			}
		}
		if class := jetBrainsClassFromExe(filepath.Join(dir, "exe")); class != "" {
			return class, true
		}

		ppid, err := readParentPID(filepath.Join(dir, "stat"))
		if err != nil {
			return "", false
		}
		pid = ppid
	}
	return "", false
}

// readParentPID reads the ppid field of /proc/<pid>/stat. It parses after the
// last ')' because comm may contain spaces and parentheses.
func readParentPID(statPath string) (int, error) {
	data, err := os.ReadFile(statPath)
	if err != nil {
		return 0, err
	}
	stat := string(data)
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return 0, errors.New("malformed stat: no comm")
	}
	// Fields after comm: state, ppid, ...
	fields := strings.Fields(stat[end+1:])
	if len(fields) < 2 {
		return 0, errors.New("malformed stat: no ppid")
	}
	return strconv.Atoi(fields[1])
}

// jetBrainsClassFromExe returns the window class from the product-info.json of
// the IDE install that exeLink (/proc/<pid>/exe) points into, or "".
func jetBrainsClassFromExe(exeLink string) string {
	exe, err := os.Readlink(exeLink)
	if err != nil {
		return ""
	}
	// The kernel marks a binary replaced on disk (e.g. by an IDE update) this way.
	exe = strings.TrimSuffix(exe, " (deleted)")

	home := filepath.Dir(filepath.Dir(exe)) // <home>/bin/<product>
	if filepath.Base(exe) == "java" {
		home = filepath.Dir(home) // <home>/jbr/bin/java, used by bin/<product>.sh
	}
	return readStartupWMClass(filepath.Join(home, "product-info.json"))
}

func readStartupWMClass(productInfoPath string) string {
	data, err := os.ReadFile(productInfoPath)
	if err != nil {
		return ""
	}
	var info struct {
		Launch []struct {
			OS             string `json:"os"`
			StartupWMClass string `json:"startupWmClass"`
		} `json:"launch"`
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return ""
	}
	for _, launch := range info.Launch {
		if strings.EqualFold(launch.OS, "Linux") && strings.HasPrefix(launch.StartupWMClass, jetBrainsClassPrefix) {
			return launch.StartupWMClass
		}
	}
	return ""
}

// jetBrainsProjectName returns the name JetBrains shows in the window title for
// the project containing cwd: .idea/.name when set, else the project directory
// name. It returns "" when no enclosing directory has an .idea folder.
func jetBrainsProjectName(cwd string) string {
	dir := filepath.Clean(cwd)
	for {
		if info, err := os.Stat(filepath.Join(dir, ".idea")); err == nil && info.IsDir() {
			if data, err := os.ReadFile(filepath.Join(dir, ".idea", ".name")); err == nil {
				if name := strings.TrimSpace(string(data)); name != "" {
					return name
				}
			}
			return filepath.Base(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// jetBrainsDesktopEntryID returns the ID of the installed .desktop file whose
// StartupWMClass is class, so the notification server can show the IDE's name
// and icon. Toolbox names these files "<class>-<uuid>.desktop", so the class
// alone is not a valid ID. It returns "" when no entry matches.
func jetBrainsDesktopEntryID(class string) string {
	if strings.ContainsAny(class, `*?[\/`) {
		return ""
	}
	want := "StartupWMClass=" + class
	for _, dir := range xdgApplicationDirs() {
		exact := filepath.Join(dir, class+".desktop")
		suffixed, _ := filepath.Glob(filepath.Join(dir, class+"-*.desktop"))
		for _, path := range append([]string{exact}, suffixed...) {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(data), "\n") {
				if strings.TrimSpace(line) == want {
					return strings.TrimSuffix(filepath.Base(path), ".desktop")
				}
			}
		}
	}
	return ""
}

// xdgApplicationDirs lists the applications directories of the XDG base
// directory spec, most specific first.
func xdgApplicationDirs() []string {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dataHome = filepath.Join(home, ".local", "share")
		}
	}
	dataDirs := os.Getenv("XDG_DATA_DIRS")
	if dataDirs == "" {
		dataDirs = "/usr/local/share:/usr/share"
	}

	var dirs []string
	for _, dir := range append([]string{dataHome}, strings.Split(dataDirs, ":")...) {
		if dir != "" {
			dirs = append(dirs, filepath.Join(dir, "applications"))
		}
	}
	return dirs
}

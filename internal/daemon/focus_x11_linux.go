//go:build linux

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type xdotoolSearch struct {
	label string
	args  []string
}

func buildXdotoolSearches(terminalName, folderName string) []xdotoolSearch {
	className := GetXdotoolClass(terminalName)
	searchTerm := GetSearchTermWithFolder(terminalName, folderName)

	searches := []xdotoolSearch{
		{
			label: "visible class search",
			args:  []string{"search", "--onlyvisible", "--class", className},
		},
		{
			label: "class search",
			args:  []string{"search", "--class", className},
		},
	}

	if searchTerm != "" {
		searches = append(searches,
			xdotoolSearch{
				label: "visible name search",
				args:  []string{"search", "--onlyvisible", "--name", searchTerm},
			},
			xdotoolSearch{
				label: "name search",
				args:  []string{"search", "--name", searchTerm},
			},
		)
	}

	return searches
}

func runXdotoolSearch(args ...string) ([]string, error) {
	cmd := exec.Command("xdotool", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%w, output: %s", err, strings.TrimSpace(string(output)))
	}

	outputStr := strings.TrimSpace(string(output))
	if outputStr == "" {
		return nil, nil
	}

	return splitWindowIDs(outputStr), nil
}

func splitWindowIDs(output string) []string {
	lines := strings.Split(output, "\n")
	ids := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		ids = append(ids, line)
	}
	return ids
}

func prioritizeXdotoolCandidates(windowIDs []string, searchLabel string, hints FocusHints) []string {
	if hints.FolderName == "" {
		return windowIDs
	}
	if !strings.Contains(searchLabel, "class search") {
		return windowIDs
	}

	ours, matching, rest := rankWindows(windowIDs, hints, getXdotoolWindowName, getXdotoolWindowPID)
	if len(ours)+len(matching) == 0 {
		return windowIDs
	}

	// TryXdotool tries candidates from the end (xdotool lists bottom-most
	// windows first), so the best group goes last to be tried first, top-most first.
	return append(append(rest, matching...), ours...)
}

// windowTitleMatcher returns the check for whether a window title belongs to
// the session's folder: strict for JetBrains, whose titles start with the
// project name, and a substring match for everything else.
func windowTitleMatcher(hints FocusHints) func(title string) bool {
	if isJetBrainsTerminalName(hints.TerminalName) {
		return func(title string) bool { return JetBrainsTitleMatches(title, hints.FolderName, hints.ProjectPath) }
	}
	return func(title string) bool { return title != "" && strings.Contains(title, hints.FolderName) }
}

// JetBrainsTitleMatches reports whether a JetBrains window title belongs to
// project, whose root is projectPath. Titles are "<project>" or
// "<project> – <file>" (en dash), so a plain substring check would let "agent"
// match "agent-notifications". When another open project has the same name,
// JetBrains adds its location: "<project> [<location>] – <file>"
// (PlatformFrameTitleBuilder). Without projectPath (older hooks) any location
// is accepted.
func JetBrainsTitleMatches(title, project, projectPath string) bool {
	if project == "" {
		return false
	}
	if title == project || strings.HasPrefix(title, project+" – ") {
		return true
	}
	if projectPath == "" {
		return strings.HasPrefix(title, project+" [")
	}
	for _, location := range jetBrainsTitleLocations(projectPath) {
		if strings.HasPrefix(title, project+" ["+location+"]") {
			return true
		}
	}
	return false
}

// jetBrainsTitleLocations returns the ways JetBrains can show projectPath in a
// window title: "~/<relative>" under the user home (FileUtil
// .getLocationRelativeToUserHome), otherwise the path itself. Both forms are
// accepted because the IDE's idea of the home can differ from this process's.
func jetBrainsTitleLocations(projectPath string) []string {
	locations := []string{projectPath}
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, projectPath); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, "../") {
			locations = append(locations, "~/"+rel)
		}
	}
	return locations
}

// rankWindows splits windowIDs, keeping their order within each group: ours
// are title matches owned by the session's IDE process (hints.IDEPID), matching
// are the other title matches, rest are the others. The IDE process matters for
// same-named projects in two processes of one IDE: JetBrains adds a location to
// the title only for same-named projects within one process. windowPID is only
// called for title matches, and only when the IDE process is known.
func rankWindows(windowIDs []string, hints FocusHints, windowName func(windowID string) string, windowPID func(windowID string) int) (ours, matching, rest []string) {
	titleMatches := windowTitleMatcher(hints)
	for _, windowID := range windowIDs {
		switch {
		case !titleMatches(windowName(windowID)):
			rest = append(rest, windowID)
		case hints.IDEPID > 0 && windowPID(windowID) == hints.IDEPID:
			ours = append(ours, windowID)
		default:
			matching = append(matching, windowID)
		}
	}
	return ours, matching, rest
}

func getKdotoolWindowName(windowID string) string {
	output, err := exec.Command("kdotool", "getwindowname", windowID).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func getKdotoolWindowPID(windowID string) int {
	return windowPIDFromTool("kdotool", windowID)
}

func getXdotoolWindowPID(windowID string) int {
	return windowPIDFromTool("xdotool", windowID)
}

// windowPIDFromTool returns the PID owning windowID, or 0 when unknown.
func windowPIDFromTool(tool, windowID string) int {
	output, err := exec.Command(tool, "getwindowpid", windowID).Output()
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		return 0
	}
	return pid
}

func getXdotoolWindowName(windowID string) string {
	cmd := exec.Command("xdotool", "getwindowname", windowID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

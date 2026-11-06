//go:build linux

package daemon

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

func tryX11WindowID(windowID string) error {
	normalizedID, err := normalizeX11WindowID(windowID)
	if err != nil {
		return fmt.Errorf("invalid X11 window id %q: %w", windowID, err)
	}

	var errs []string

	if err := activateWindowIDWithXdotool(normalizedID); err == nil {
		return nil
	} else {
		errs = append(errs, err.Error())
	}

	if err := activateWindowIDWithWmctrl(normalizedID); err == nil {
		return nil
	} else {
		errs = append(errs, err.Error())
	}

	return fmt.Errorf("exact X11 focus failed: %s", strings.Join(errs, "; "))
}

func activateWindowIDWithXdotool(windowID string) error {
	if _, err := exec.LookPath("xdotool"); err != nil {
		return fmt.Errorf("xdotool not installed")
	}

	cmd := exec.Command("xdotool", "windowactivate", "--sync", windowID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("xdotool windowactivate failed: %w, output: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func activateWindowIDWithWmctrl(windowID string) error {
	if _, err := exec.LookPath("wmctrl"); err != nil {
		return fmt.Errorf("wmctrl not installed")
	}

	cmd := exec.Command("wmctrl", "-i", "-a", windowID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wmctrl -i -a failed: %w, output: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func normalizeX11WindowID(windowID string) (string, error) {
	windowID = strings.TrimSpace(windowID)
	if windowID == "" {
		return "", fmt.Errorf("empty window id")
	}

	id, err := strconv.ParseUint(windowID, 0, 64)
	if err != nil {
		return "", err
	}

	return strconv.FormatUint(id, 10), nil
}

func tryWindowTitle(windowTitle string) error {
	windowTitle = strings.TrimSpace(windowTitle)
	if windowTitle == "" {
		return fmt.Errorf("empty window title")
	}

	var errs []string

	if err := activateWindowTitleWithWmctrl(windowTitle); err == nil {
		return nil
	} else {
		errs = append(errs, err.Error())
	}

	if err := activateWindowTitleWithXdotool(windowTitle); err == nil {
		return nil
	} else {
		errs = append(errs, err.Error())
	}

	return fmt.Errorf("exact title focus failed: %s", strings.Join(errs, "; "))
}

func activateWindowTitleWithWmctrl(windowTitle string) error {
	if _, err := exec.LookPath("wmctrl"); err != nil {
		return fmt.Errorf("wmctrl not installed")
	}

	cmd := exec.Command("wmctrl", "-F", "-a", windowTitle)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wmctrl -F -a failed: %w, output: %s", err, strings.TrimSpace(string(output)))
	}

	return nil
}

func activateWindowTitleWithXdotool(windowTitle string) error {
	if _, err := exec.LookPath("xdotool"); err != nil {
		return fmt.Errorf("xdotool not installed")
	}

	exactRegex := "^" + regexp.QuoteMeta(windowTitle) + "$"
	windowIDs, err := runXdotoolSearch("search", "--name", exactRegex)
	if err != nil {
		return err
	}
	if len(windowIDs) == 0 {
		return fmt.Errorf("no windows found for exact title")
	}

	for i := len(windowIDs) - 1; i >= 0; i-- {
		windowID := windowIDs[i]
		if err := activateWindowIDWithXdotool(windowID); err == nil {
			return nil
		}
	}

	return fmt.Errorf("xdotool could not activate any exact-title match")
}

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type pluginManifest struct {
	Version string `json:"version"`
}

func maybeScheduleWindowsLazyUpdate(pluginRoot string) {
	if currentGOOS != "windows" {
		return
	}

	pluginVersion := readPluginManifestVersion(pluginRoot)
	if pluginVersion == "" || pluginVersion == version {
		return
	}

	stampKey := version + "->" + pluginVersion
	stampPath := windowsLazyUpdateStampPath(pluginRoot)
	if windowsLazyUpdateRecentlyScheduled(stampPath, stampKey) {
		return
	}

	stampWritten := writeWindowsLazyUpdateStamp(stampPath, stampKey) == nil
	if err := scheduleWindowsLazyUpdate(pluginRoot); err != nil && stampWritten {
		_ = os.Remove(stampPath)
	}
}

func readPluginManifestVersion(pluginRoot string) string {
	data, err := os.ReadFile(filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"))
	if err != nil {
		return ""
	}

	var manifest pluginManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return ""
	}
	return manifest.Version
}

func windowsLazyUpdateStampPath(pluginRoot string) string {
	cacheDir, err := os.UserCacheDir()
	if err != nil || cacheDir == "" {
		cacheDir = filepath.Join(pluginRoot, ".cache")
	}
	return filepath.Join(cacheDir, "claude-notifications-go", "windows-lazy-update-stamp")
}

func windowsLazyUpdateRecentlyScheduled(stampPath, stampKey string) bool {
	info, err := os.Stat(stampPath)
	if err != nil {
		return false
	}
	if time.Since(info.ModTime()) > windowsLazyUpdateRetryAfter {
		return false
	}

	data, err := os.ReadFile(stampPath)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == stampKey
}

func writeWindowsLazyUpdateStamp(stampPath, stampKey string) error {
	if err := os.MkdirAll(filepath.Dir(stampPath), 0o700); err != nil {
		return err
	}
	return os.WriteFile(stampPath, []byte(stampKey+"\n"), 0o600)
}

func scheduleWindowsLazyUpdateImpl(pluginRoot string) error {
	installScript := filepath.Join(pluginRoot, "bin", "install.sh")
	if err := requireCompatibleUpdateScript(installScript); err != nil {
		return err
	}

	powershellPath, err := findWindowsPowerShell()
	if err != nil {
		return err
	}

	bashPath, err := findWindowsBash()
	if err != nil {
		return err
	}

	targetDir := filepath.ToSlash(filepath.Join(pluginRoot, "bin"))
	installScript = filepath.ToSlash(installScript)
	shCommand := "INSTALL_TARGET_DIR=" + shellSingleQuoted(targetDir) + " " + shellSingleQuoted(installScript) + " --force"
	psCommand := windowsLazyUpdatePowerShellCommand(bashPath, shCommand)

	cmd := exec.Command(powershellPath, "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", psCommand)
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err == nil {
		cmd.Stdin = devNull
		cmd.Stdout = devNull
		cmd.Stderr = devNull
	}

	if err := cmd.Start(); err != nil {
		if devNull != nil {
			_ = devNull.Close()
		}
		return err
	}

	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
	if devNull != nil {
		_ = devNull.Close()
	}
	return nil
}

func windowsLazyUpdatePowerShellCommand(bashPath, shCommand string) string {
	// Thread.Sleep remains reliable in the detached, NUL-backed Windows
	// PowerShell process where Start-Sleep can stall before launching bash.
	return "$ErrorActionPreference = 'SilentlyContinue'; " +
		"[System.Threading.Thread]::Sleep(750); " +
		"for ($i = 0; $i -lt 6; $i++) { " +
		"& " + powershellSingleQuoted(bashPath) + " -lc " + powershellSingleQuoted(shCommand) + "; " +
		"if ($LASTEXITCODE -eq 0) { break }; " +
		"[System.Threading.Thread]::Sleep(5000) }"
}

func findWindowsPowerShell() (string, error) {
	if path, err := exec.LookPath("powershell.exe"); err == nil {
		return path, nil
	}

	if systemRoot := os.Getenv("SystemRoot"); systemRoot != "" {
		candidate := filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("powershell.exe not found")
}

func findWindowsBash() (string, error) {
	if override := os.Getenv("CLAUDE_NOTIFICATIONS_BASH"); override != "" {
		if _, err := os.Stat(override); err == nil {
			return override, nil
		}
		return "", fmt.Errorf("CLAUDE_NOTIFICATIONS_BASH not found: %s", override)
	}

	for _, name := range []string{"bash.exe", "bash"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}

	for _, candidate := range windowsBashCandidates() {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("bash.exe not found")
}

func windowsBashCandidates() []string {
	var candidates []string
	for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")} {
		if root == "" {
			continue
		}
		candidates = append(candidates,
			filepath.Join(root, "Git", "bin", "bash.exe"),
			filepath.Join(root, "Programs", "Git", "bin", "bash.exe"),
		)
	}
	return candidates
}

func shellSingleQuoted(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func powershellSingleQuoted(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

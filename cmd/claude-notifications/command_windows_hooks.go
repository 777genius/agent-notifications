package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type hookSettings struct {
	Hooks map[string][]hookMatcherGroup `json:"hooks"`
}

type hookMatcherGroup struct {
	Matcher string        `json:"matcher,omitempty"`
	Hooks   []hookCommand `json:"hooks"`
}

type hookCommand struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Timeout int      `json:"timeout"`
	Shell   string   `json:"shell,omitempty"`
}

func runWindowsHooks(args []string) {
	exePath, err := parseWindowsHooksExecutable(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "windows-hooks: %v\n", err)
		os.Exit(1)
	}

	settings := newWindowsHookSettings(exePath)

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "windows-hooks: failed to render JSON: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(string(out))
}

func newWindowsHookSettings(exePath string) hookSettings {
	return hookSettings{
		Hooks: map[string][]hookMatcherGroup{
			"PreToolUse": {
				{
					Matcher: "ExitPlanMode|AskUserQuestion",
					Hooks:   []hookCommand{newExecHook(exePath, "PreToolUse")},
				},
			},
			"Notification": {
				{
					Matcher: "permission_prompt",
					Hooks:   []hookCommand{newExecHook(exePath, "Notification")},
				},
			},
			"Stop": {
				{
					Hooks: []hookCommand{newExecHook(exePath, "Stop")},
				},
			},
			"SubagentStop": {
				{
					Hooks: []hookCommand{newExecHook(exePath, "SubagentStop")},
				},
			},
			"TeammateIdle": {
				{
					Hooks: []hookCommand{newExecHook(exePath, "TeammateIdle")},
				},
			},
		},
	}
}

func parseWindowsHooksExecutable(args []string) (string, error) {
	exeOverride := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--exe":
			if i+1 >= len(args) {
				return "", fmt.Errorf("--exe requires a path")
			}
			i++
			exeOverride = args[i]
		default:
			return "", fmt.Errorf("unknown windows-hooks option: %s", args[i])
		}
	}

	if exeOverride != "" {
		return filepath.Abs(exeOverride)
	}

	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to detect executable path: %w", err)
	}

	exePath, err = filepath.Abs(exePath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve executable path: %w", err)
	}

	if strings.EqualFold(filepath.Ext(exePath), ".exe") {
		return exePath, nil
	}

	pluginRoot := getPluginRoot()
	return filepath.Abs(filepath.Join(pluginRoot, "bin", "claude-notifications-windows-amd64.exe"))
}

func newExecHook(exePath, hookName string) hookCommand {
	return hookCommand{
		Type:    "command",
		Command: exePath,
		Args:    []string{"handle-hook", hookName},
		Timeout: 30,
	}
}

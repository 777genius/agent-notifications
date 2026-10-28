package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
)

const installerResultCapability = "installer-progress-result-v1"

// This side channel projects typed results to the shell's finite presentation
// protocol. Diagnostic text, JSON configuration and argv never become events.
func readInstallerResult(path string) (setupwizard.Result, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1024*1024 {
		return setupwizard.Result{}, errors.New("installer result unavailable")
	}
	file, err := openInstallerResult(path)
	if err != nil {
		return setupwizard.Result{}, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return setupwizard.Result{}, errors.New("installer result changed")
	}
	decoder := json.NewDecoder(io.LimitReader(file, 1024*1024+1))
	var result setupwizard.Result
	if err := decoder.Decode(&result); err != nil {
		return setupwizard.Result{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return setupwizard.Result{}, errors.New("invalid installer result")
	}
	switch result.Action {
	case "install", "update", "repair", "uninstall", "inspect":
	default:
		return setupwizard.Result{}, errors.New("invalid installer action")
	}
	switch result.Outcome {
	case "completed", "unchanged", "incomplete", "failed", "conflict", "invalid", "cancelled", "ready":
	default:
		return setupwizard.Result{}, errors.New("invalid installer outcome")
	}
	return result, nil
}

func writeInstallerResult(path string, out io.Writer) error {
	result, err := readInstallerResult(path)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, target := range result.Targets {
		if target.Unit != "agent-notify" && target.Unit != "direct-mcp" {
			continue
		}
		if target.Client != "claude" && target.Client != "codex" && target.Client != "opencode" && target.Client != "gemini" && target.Client != "cursor" && target.Client != "pi" || seen[target.Client] {
			continue
		}
		seen[target.Client] = true
		outcome := "state_requires_inspection"
		switch target.Outcome {
		case "completed", "installed":
			outcome = "completed"
		case "activation_failed", "activation_not_attempted", "activation_timeout", "activation_canceled", "activation_deadline":
			// This finite presentation category includes saved registration.
			// The current attempt alone cannot prove that independent fact.
			if target.Materialization == "materialized" || target.Materialization == "degraded" {
				outcome = target.Outcome
			}
		default:
			if target.Materialization == "materialized" || target.Materialization == "degraded" {
				outcome = "registration_committed"
			}
		}
		if _, err := fmt.Fprintf(out, "%s\t%s\t%s\n", target.Client, installerTargetPhase(target.Client), outcome); err != nil {
			return err
		}
	}
	reason := "setup_failed"
	for _, target := range result.Targets {
		switch target.Outcome {
		case "activation_timeout":
			reason = "probe_timeout"
		case "activation_canceled":
			reason = "canceled"
		case "activation_deadline":
			reason = "deadline"
		case "activation_failed":
			reason = "activation_failed"
		default:
			continue
		}
		break
	}
	next := "unavailable"
	for _, action := range result.NextActions {
		if usableInstallerRecovery(action) {
			next = "activation_available"
			break
		}
	}
	_, err = fmt.Fprintf(out, "reason\tcategory\t%s\nnext\tcategory\t%s\n", reason, next)
	return err
}

// Recovery is emitted only into the shell's private capture. Human defaults
// refer to that capture, preserving an executable argv without flooding TTYs.
func writeInstallerRecovery(path string, out io.Writer) error {
	result, err := readInstallerResult(path)
	if err != nil {
		return err
	}
	for _, next := range result.NextActions {
		if !usableInstallerRecovery(next) {
			continue
		}
		_, err = fmt.Fprintln(out, "Resume: "+setupPrintableCommand(next.Command))
		return err
	}
	return nil
}

func usableInstallerRecovery(next setupwizard.NextAction) bool {
	if next.Kind != "activate" || len(next.Command) < 2 || next.Command[0] != "setup-notifications" || next.Command[1] != "wizard" {
		return false
	}
	req, _, err := parseSetupWizard(next.Command[2:])
	return err == nil && req.Yes && filepath.IsAbs(req.ControlRoot) && (req.Action == setupwizard.ActionInstall || req.Action == setupwizard.ActionUpdate || req.Action == setupwizard.ActionRepair)
}

func installerTargetPhase(client string) string {
	if client == "pi" {
		return "registration"
	}
	return "MCP"
}

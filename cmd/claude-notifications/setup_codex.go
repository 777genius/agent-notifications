package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/777genius/agent-notifications/internal/codexsetup"
)

type setupCodexOptions struct {
	codexHome     string
	pluginRoot    string
	print         bool
	dryRun        bool
	configure     bool
	configureArgs []string
}

// runSetupCodex registers this plugin's hooks with the Codex CLI.
//
// This explicit setup path writes user hooks independently of native plugin
// discovery. Doing it here rather than in shell
// scripts keeps one implementation for macOS, Linux, and Windows and avoids
// depending on tools like jq that are not installed by default anywhere.
func runSetupCodex(args []string) {
	opts, err := parseSetupCodexOptions(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup-codex: %v\n", err)
		os.Exit(1)
	}

	pluginRoot := opts.pluginRoot
	if pluginRoot == "" {
		pluginRoot = getPluginRoot()
	}

	if opts.print {
		codexHome, err := codexsetup.ResolveCodexHome(opts.codexHome)
		if err != nil {
			fmt.Fprintf(os.Stderr, "setup-codex: %v\n", err)
			os.Exit(1)
		}
		out, err := codexsetup.RenderHooksJSON(filepath.Join(codexHome, codexsetup.InstallDirName))
		if err != nil {
			fmt.Fprintf(os.Stderr, "setup-codex: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(out))
		return
	}

	result, err := codexsetup.Run(codexsetup.Options{
		CodexHome:  opts.codexHome,
		PluginRoot: pluginRoot,
		DryRun:     opts.dryRun,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup-codex: %v\n", err)
		var partial *codexsetup.InitializationError
		if errors.As(err, &partial) {
			os.Exit(3)
		}
		os.Exit(1)
	}

	if opts.dryRun {
		fmt.Println("setup-codex (dry run) would:")
		fmt.Printf("  install the plugin copy at %s\n", result.InstallDir)
		fmt.Printf("  register %s in %s\n", strings.Join(result.Events, ", "), result.HooksPath)
		if result.Replaced {
			fmt.Println("  replace the previous claude-notifications registration")
		}
		if result.ForeignKept > 0 {
			fmt.Printf("  keep %d hook handler(s) belonging to other tools\n", result.ForeignKept)
		}
		return
	}

	fmt.Println("Codex notifications registered.")
	fmt.Printf("  plugin copy: %s\n", result.InstallDir)
	fmt.Printf("  hooks file:  %s\n", result.HooksPath)
	fmt.Printf("  events:      %s\n", strings.Join(result.Events, ", "))
	if result.BackupPath != "" {
		fmt.Printf("  backup:      %s\n", result.BackupPath)
	}
	if result.ForeignKept > 0 {
		fmt.Printf("  preserved:   %d hook handler(s) from other tools\n", result.ForeignKept)
	}
	fmt.Println()
	if opts.configure {
		configure := append([]string{"--provider", "codex"}, opts.configureArgs...)
		retryArgs := opts.configureArgs
		if home := filepath.Clean(result.CodexHome); home != "" && filepath.IsAbs(home) {
			configure = append([]string{"--provider", "codex", "--codex-home", home}, opts.configureArgs...)
			retryArgs = append([]string{"--codex-home", home}, opts.configureArgs...)
		}
		code := executeNotificationConfigure(context.Background(), configure, os.Stdout, result.InstallDir)
		if code != 0 {
			reportAgentNotifySetupFailure(os.Stderr, "codex", retryArgs)
		}
	}
	fmt.Println("Next step: start Codex, run /hooks, review the entries and trust them.")
	fmt.Println("Codex asks for this once; the registration keeps working across plugin updates.")
	fmt.Println("After updating the plugin, rerun setup-codex from the updated bundle to refresh the copy.")
	if executable, err := os.Executable(); err == nil {
		fmt.Printf("Setup executable (no PATH entry required): %s\n", executable)
	}
	fmt.Println("You can also run setup-codex from the installed bundle to repair hook registration.")
}

func parseSetupCodexOptions(args []string) (setupCodexOptions, error) {
	var opts setupCodexOptions
	opts.configure = true
	explicit, skip := false, false
	var rest []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--print":
			opts.print = true
		case "--dry-run":
			opts.dryRun = true
		case "--remove":
			return opts, fmt.Errorf("unknown option: --remove")
		case "--agent-notify":
			explicit = true
			opts.configure = true
		case "--skip-agent-notify":
			skip = true
			opts.configure = false
		case "--codex-home":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--codex-home requires a path")
			}
			i++
			opts.codexHome = args[i]
		case "--plugin-root":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--plugin-root requires a path")
			}
			i++
			opts.pluginRoot = args[i]
		default:
			rest = append(rest, args[i])
		}
	}
	if explicit && skip {
		return opts, fmt.Errorf("--agent-notify and --skip-agent-notify are mutually exclusive")
	}
	if opts.print && opts.dryRun {
		return opts, fmt.Errorf("--print and --dry-run are mutually exclusive")
	}
	if opts.configure && (opts.print || opts.dryRun) {
		if explicit {
			return opts, fmt.Errorf("incompatible flags")
		}
		opts.configure = false
	}
	if opts.configure {
		if len(rest) == 0 {
			rest = agentNotifyDefaultNoneArgs()
		}
		opts.configureArgs = rest
		_, _, err := parseNotificationConfigure(append([]string{"--provider", "codex"}, rest...))
		return opts, err
	}
	if len(rest) != 0 {
		return opts, fmt.Errorf("unknown option: %s", rest[0])
	}
	return opts, nil
}

func reportAgentNotifySetupFailure(w io.Writer, provider string, args []string) {
	retry := "claude-notifications"
	if executable, err := os.Executable(); err == nil {
		retry = executable
	}
	fmt.Fprintf(w, "setup-codex: agent-notify setup failed; Codex hooks remain registered.\n")
	fmt.Fprintf(w, "Retry: %s setup-notifications configure --provider %s %s\n", retry, provider, strings.Join(args, " "))
}

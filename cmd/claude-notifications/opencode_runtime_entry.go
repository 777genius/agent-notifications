package main

import (
	"strings"
	"unicode/utf8"
)

// A request is not entry authority. The OS readers classify the held parent's
// complete argv; image/version and independent reader rows qualify it later.
func runtimeEntryRequest(entry string) bool { return entry == "serve" || entry == "native" }
func runtimeEntryMatches(request, entry string) bool {
	return (entry == "serve" || entry == "tui" || entry == "run") && (request == "native" || request == entry)
}

// This closed grammar follows the pinned V1 CLI (51ef4be), not a semver range.
// Unknown options, remote attach and other commands remain unverified. argv is
// consistency evidence only; it never substitutes for a live official image.
func runtimeNativeEntry(argv []string) string {
	if len(argv) == 0 || len(argv) > 4096 {
		return ""
	}
	for _, arg := range argv {
		if !utf8.ValidString(arg) || strings.ContainsRune(arg, 0) {
			return ""
		}
	}
	if argv[0] == "" {
		return ""
	}
	args := argv[1:]
	// yargs permits these pinned global options before the selected command.
	// Stop at the first non-global argument; help/pure/unknown remain denied.
globalPrefix:
	for len(args) > 0 {
		name, value, assigned := strings.Cut(args[0], "=")
		switch name {
		case "--print-logs", "--no-print-logs":
			if assigned && value != "true" && value != "false" {
				return ""
			}
		case "--log-level":
			if !assigned {
				if len(args) < 2 {
					return ""
				}
				value, args = args[1], args[1:]
			}
			if value != "DEBUG" && value != "INFO" && value != "WARN" && value != "ERROR" {
				return ""
			}
		default:
			break globalPrefix
		}
		args = args[1:]
	}
	// Preserve the native serve boundary, including its command options.
	if len(args) > 0 && args[0] == "serve" {
		return "serve"
	}
	entry := "tui"
	if len(args) > 0 && args[0] == "run" {
		entry, args = "run", args[1:]
	}
	commonBool := " print-logs continue c fork auto yolo dangerously-skip-permissions mini replay demo "
	commonValue := " log-level model m session s agent replay-limit "
	boolOptions, valueOptions := commonBool, commonValue
	if entry == "tui" {
		boolOptions += " mdns no-replay "
		valueOptions += " prompt port hostname mdns-domain cors "
	} else {
		boolOptions += " share thinking interactive i "
		valueOptions += " command format file f title password p username u dir port variant "
	}
	positionals := 0
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			// In local run everything after -- is prompt text, including flags.
			if entry == "run" {
				return entry
			}
			return ""
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			if entry == "tui" {
				// yargs selects these commands instead of the default project path.
				commands := " acp mcp attach run generate debug console providers auth agent upgrade uninstall serve web models stats export import github pr session plugin plug db completion help version "
				if strings.Contains(commands, " "+arg+" ") {
					return ""
				}
				positionals++
				if positionals > 1 || arg == "" {
					return ""
				}
			}
			continue
		}
		name := strings.TrimPrefix(arg, "--")
		if name == arg {
			name = strings.TrimPrefix(arg, "-")
			if len(name) != 1 { // No ambiguous short-option bundles.
				return ""
			}
		}
		name, value, assigned := strings.Cut(name, "=")
		if name == "" {
			return ""
		}
		boolean := strings.Contains(boolOptions, " "+name+" ")
		if strings.HasPrefix(name, "no-") && strings.Contains(boolOptions, " "+strings.TrimPrefix(name, "no-")+" ") {
			boolean = true
		}
		if boolean {
			if assigned && value != "true" && value != "false" {
				return ""
			}
			continue
		}
		if !strings.Contains(valueOptions, " "+name+" ") || name == "" {
			return ""
		}
		if !assigned {
			i++
			if i == len(args) || args[i] == "" || strings.HasPrefix(args[i], "-") {
				return ""
			}
		} else if value == "" {
			return ""
		}
	}
	return entry
}

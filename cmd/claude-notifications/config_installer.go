package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/777genius/agent-notifications/internal/config"
)

// installerConfigCommand keeps shell adapters out of JSON parsing and escaping.
// Success is an exit-code contract, not a substring search of JSON output.
func installerConfigCommand(args []string, out, stderr io.Writer) int {
	if len(args) == 3 && args[0] == "marketplace" {
		data, err := os.ReadFile(args[1])
		if err != nil {
			return 1
		}
		var entries []struct {
			Name string `json:"name"`
			Repo string `json:"repo"`
		}
		if json.Unmarshal(data, &entries) != nil {
			return 1
		}
		for _, entry := range entries {
			if entry.Name == args[2] && entry.Repo != "" {
				fmt.Fprintln(out, entry.Repo)
				break
			}
		}
		return 0
	}
	if len(args) == 3 && (args[0] == "root" || args[0] == "version") {
		entries, err := installerRegistry(args[1], args[2])
		if err != nil {
			return 1
		}
		if len(entries) == 0 {
			return 0
		}
		best := entries[0]
		placeholder := func(v string) bool { v = strings.TrimPrefix(strings.ToLower(v), "v"); return v == "" || v == "unknown" }
		greater := func(a, b string) bool {
			x, y := strings.Split(a, "."), strings.Split(b, ".")
			for i := 0; i < 3; i++ {
				av, bv := 0, 0
				if i < len(x) {
					av, _ = strconv.Atoi(x[i])
				}
				if i < len(y) {
					bv, _ = strconv.Atoi(y[i])
				}
				if av != bv {
					return av > bv
				}
			}
			return false
		}
		for _, entry := range entries[1:] {
			if placeholder(entry.Version) || (!placeholder(best.Version) && greater(entry.Version, best.Version)) {
				best = entry
			}
		}
		if args[0] == "root" {
			fmt.Fprintln(out, best.InstallPath)
		} else {
			fmt.Fprintln(out, best.Version)
		}
		return 0
	}
	if len(args) == 1 && args[0] == "capabilities" {
		fmt.Fprintln(out, "installer-v1")
		return 0
	}
	if len(args) > 0 && args[0] == "bootstrap" {
		request, err := installerBootstrapRequest(args[1:])
		if err != nil {
			fmt.Fprintln(stderr, config.ConfigInvalid)
			return 1
		}
		data, err := json.Marshal(request)
		if err != nil {
			return 1
		}
		return configCommand([]string{"preflight-update", "--stdin", "--json"}, bytes.NewReader(data), out, stderr)
	}
	if len(args) == 3 && args[0] == "versions" {
		entries, err := installerRegistry(args[1], args[2])
		if err != nil {
			fmt.Fprintln(stderr, config.ConfigInvalid)
			return 1
		}
		versions := map[string]bool{}
		for _, entry := range entries {
			v := strings.TrimPrefix(entry.Version, "v")
			if installerVersion.MatchString(v) {
				if _, err := os.Lstat(filepath.Join(entry.InstallPath, "config", "config.json")); err == nil {
					versions[v] = true
				} else if !os.IsNotExist(err) {
					return 1
				}
			}
		}
		ordered := []string{}
		for v := range versions {
			ordered = append(ordered, v)
		}
		sort.Strings(ordered)
		for _, v := range ordered {
			fmt.Fprintln(out, v)
		}
		return 0
	}
	if len(args) == 0 || args[0] != "preflight" {
		fmt.Fprintln(stderr, config.ConfigInvalid)
		return 1
	}
	refresh := []string{}
	for _, path := range args[1:] {
		absolute, err := filepath.Abs(path)
		if err != nil {
			fmt.Fprintln(stderr, config.ConfigInvalid)
			return 1
		}
		refresh = append(refresh, absolute)
	}
	data, err := json.Marshal(map[string]any{"refreshDirs": refresh})
	if err != nil {
		fmt.Fprintln(stderr, config.ConfigInvalid)
		return 1
	}
	return configCommand([]string{"preflight-update", "--stdin", "--json"}, bytes.NewReader(data), io.Discard, stderr)
}

var installerVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type installerEntry struct {
	InstallPath string `json:"installPath"`
	Version     string `json:"version"`
}

func installerRegistry(path, key string) ([]installerEntry, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := config.ParseDocument(data, "", false); err != nil {
		return nil, err
	}
	var registry map[string]json.RawMessage
	if err := json.Unmarshal(data, &registry); err != nil {
		return nil, err
	}
	raw, exists := registry["plugins"]
	if !exists {
		return nil, nil
	}
	var plugins map[string]json.RawMessage
	if err := json.Unmarshal(raw, &plugins); err != nil || plugins == nil {
		return nil, fmt.Errorf("invalid plugins")
	}
	raw, exists = plugins[key]
	if !exists {
		return nil, nil
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil || rows == nil {
		return nil, fmt.Errorf("invalid entries")
	}
	entries := make([]installerEntry, 0, len(rows))
	for _, row := range rows {
		var entry installerEntry
		if json.Unmarshal(row["installPath"], &entry.InstallPath) != nil || !filepath.IsAbs(entry.InstallPath) {
			return nil, fmt.Errorf("invalid registry path")
		}
		if raw, ok := row["version"]; ok {
			if json.Unmarshal(raw, &entry.Version) != nil {
				return nil, fmt.Errorf("invalid version")
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// The installer supplies locations, while shared Store retains all config and
// identity decisions. Historical templates are accepted only with a digest.
func installerBootstrapRequest(args []string) (map[string]any, error) {
	if len(args) != 10 {
		return nil, fmt.Errorf("invalid arguments")
	}
	registry, key, claude, cache, market, codex, product, stage, current, venv := args[0], args[1], args[2], args[3], args[4], args[5], args[6], args[7], args[8], args[9]
	if product != "claude" && product != "codex" && product != "both" {
		return nil, fmt.Errorf("invalid product")
	}
	roots, refresh, protected := []string{}, []string{}, []string{}
	history := []config.HistoricalCandidate{}
	if product != "codex" {
		entries, err := installerRegistry(registry, key)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			roots = append(roots, entry.InstallPath)
			candidate := config.HistoricalCandidate{Path: filepath.Join(entry.InstallPath, "config", "config.json")}
			version := strings.TrimPrefix(entry.Version, "v")
			if installerVersion.MatchString(version) {
				baseline := filepath.Join(stage, "baseline-"+version)
				if digest, err := os.ReadFile(filepath.Join(baseline, "verified")); err == nil {
					candidate.BaselinePath = filepath.Join(baseline, "config.json")
					candidate.BaselineSHA256 = strings.TrimSpace(string(digest))
				} else if !os.IsNotExist(err) {
					return nil, err
				}
			}
			history = append(history, candidate)
		}
		refresh = append(refresh, cache, market)
		refresh = append(refresh, roots...)
		entries, err = installerRegistry(current, key)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			refresh = append(refresh, entry.InstallPath)
		}
		protected = append(protected, current, filepath.Join(claude, "plugins", "known_marketplaces.json"), filepath.Join(claude, "settings.json"))
	}
	history = append(history, config.HistoricalCandidate{Path: filepath.Join(claude, "claude-notifications-go", "config.json")})
	if product != "claude" {
		dest := filepath.Join(codex, "claude-notifications-go")
		refresh = append(refresh, dest)
		history = append(history, config.HistoricalCandidate{Path: filepath.Join(dest, "config", "config.json")})
	}
	if venv != "" {
		refresh = append(refresh, venv)
	}
	for _, path := range refresh {
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("invalid refresh path")
		}
	}
	// Stage is not a mutation target, but cannot live inside one: deleting a
	// refresh root would otherwise remove the very helper enforcing protection.
	stagePath, err := filepath.EvalSymlinks(stage)
	if err != nil {
		return nil, err
	}
	for _, root := range refresh {
		resolved := root
		// Resolve the nearest existing ancestor, including symlinked parents.
		tail := []string{}
		for {
			p, e := filepath.EvalSymlinks(resolved)
			if e == nil {
				resolved = p
				break
			}
			if !os.IsNotExist(e) {
				return nil, e
			}
			parent := filepath.Dir(resolved)
			if parent == resolved {
				return nil, e
			}
			tail = append(tail, filepath.Base(resolved))
			resolved = parent
		}
		for i := len(tail) - 1; i >= 0; i-- {
			resolved = filepath.Join(resolved, tail[i])
		}
		rel, e := filepath.Rel(resolved, stagePath)
		if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("stage overlaps refresh root")
		}
	}
	return map[string]any{"activeBundleRoots": roots, "refreshDirs": refresh, "protectedPaths": protected, "historicalCandidates": history}, nil
}

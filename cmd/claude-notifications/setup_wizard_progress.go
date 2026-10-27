package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

func stripBootstrapProgressFile(args []string) ([]string, string, error) {
	var out []string
	path := ""
	for n := 0; n < len(args); n++ {
		key, value, inline := strings.Cut(args[n], "=")
		if key != "--bootstrap-progress-file" {
			out = append(out, args[n])
			continue
		}
		if path != "" {
			return out, "", errors.New("duplicate bootstrap progress file")
		}
		if !inline {
			n++
			if n >= len(args) {
				return out, "", errors.New("missing bootstrap progress file")
			}
			value = args[n]
		}
		if !validProductPath(value) {
			return out, "", errors.New("invalid bootstrap progress file")
		}
		path = value
	}
	return out, path, nil
}

// The only writable destination is the coordinator's fixed journal below the
// already validated private intent stage. Never parse raw provider diagnostics
// as progress, and never accept a general-purpose output path from this flag.
func openBootstrapProgress(intent, path string) (func(string, string, string), func(), error) {
	stage := filepath.Dir(filepath.Clean(intent))
	path = filepath.Clean(path)
	if path != filepath.Join(stage, "parallel", "progress.events") {
		return nil, nil, errors.New("progress file must be the bootstrap coordinator journal")
	}
	parent, err := os.OpenRoot(stage)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = parent.Close() }()
	dir, err := parent.Lstat("parallel")
	if err != nil || !dir.IsDir() || dir.Mode()&os.ModeSymlink != 0 {
		return nil, nil, errors.New("invalid progress directory")
	}
	root, err := parent.OpenRoot("parallel")
	if err != nil {
		return nil, nil, err
	}
	failed := true
	defer func() {
		if failed {
			_ = root.Close()
		}
	}()
	if err := validateBootstrapProgressLeaf(filepath.Dir(path), root); err != nil {
		return nil, nil, err
	}
	before, err := root.Lstat("progress.events")
	if err != nil {
		return nil, nil, err
	}
	file, err := root.OpenFile("progress.events", os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return nil, nil, err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		_ = file.Close()
		return nil, nil, errors.New("progress journal changed")
	}
	// Private Windows validation deliberately denies concurrent writers. Keep
	// only the validated identity between events; each finite append opens and
	// closes its writer after security validation, before the next validation.
	if err := file.Close(); err != nil {
		return nil, nil, err
	}
	var mu sync.Mutex
	closed := false
	closeProgress := func() {
		mu.Lock()
		defer mu.Unlock()
		if !closed {
			closed = true
			_ = root.Close()
		}
	}
	write := func(client, phase, outcome string) {
		if !bootstrapProgressEventAllowed(client, phase, outcome) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return
		}
		line := fmt.Sprintf("transition\t%s\t%s\t%s\n", client, phase, outcome)
		current, err := root.Lstat("progress.events")
		if err != nil || !os.SameFile(current, opened) || current.Size()+int64(len(line)) > maxBootstrapIntent || validateBootstrapProgressLeaf(filepath.Dir(path), root) != nil {
			return // Presentation failure cannot authorize or abort setup effects.
		}
		file, err := root.OpenFile("progress.events", os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			return
		}
		defer func() { _ = file.Close() }()
		actual, err := file.Stat()
		if err != nil || !os.SameFile(actual, opened) || actual.Size()+int64(len(line)) > maxBootstrapIntent {
			return
		}
		_, _ = file.WriteString(line)
	}
	failed = false
	return write, closeProgress, nil
}

func bootstrapProgressEventAllowed(client, phase, outcome string) bool {
	if client != "claude" && client != "codex" {
		return false
	}
	if phase == "verification" {
		return outcome == "running" || outcome == "done"
	}
	if phase == "MCP" {
		switch outcome {
		case "activation_failed", "activation_timeout", "activation_canceled", "activation_deadline":
			return true
		}
	}
	return false
}

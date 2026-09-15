package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/config"
)

// configCommand has no notification or agent dependencies. All failures cross
// a content-free boundary, including flag names and decoder errors.
func configCommand(args []string, in io.Reader, out, stderr io.Writer) int {
	fail := func(err error) int {
		code := config.ConfigInvalid
		var ce *config.Error
		if errors.As(err, &ce) {
			code = ce.Code
		}
		_, _ = fmt.Fprintln(stderr, code)
		return 1
	}
	invalid := func() int { return fail(&config.Error{Code: config.ConfigInvalid}) }
	if len(args) == 0 {
		return invalid()
	}
	op := args[0]
	flags := map[string]string{}
	for i := 1; i < len(args); i++ {
		flag := args[i]
		if _, ok := flags[flag]; ok {
			return invalid()
		}
		switch flag {
		case "--json", "--stdin":
			flags[flag] = "true"
		case "--from", "--expect-revision":
			i++
			if i == len(args) {
				return invalid()
			}
			flags[flag] = args[i]
		default:
			return invalid()
		}
	}
	allowed := map[string]map[string]bool{
		"path": {"--json": true}, "inspect": {"--json": true},
		"init":             {"--json": true, "--from": true},
		"edit":             {"--stdin": true, "--expect-revision": true},
		"preflight-update": {"--stdin": true, "--json": true},
	}
	set, ok := allowed[op]
	if !ok {
		return invalid()
	}
	for k := range flags {
		if !set[k] {
			return invalid()
		}
	}
	if (op == "inspect" || op == "preflight-update") && flags["--json"] == "" {
		return invalid()
	}
	if (op == "edit" || op == "preflight-update") && flags["--stdin"] == "" {
		return invalid()
	}
	if op == "edit" && flags["--expect-revision"] == "" {
		return fail(&config.Error{Code: config.Code("ConfigConflict")})
	}
	if from, ok := flags["--from"]; ok && !filepath.IsAbs(from) {
		return invalid()
	}
	env := config.SnapshotEnv()
	root := os.Getenv("PLUGIN_ROOT")
	if root == "" {
		root = getPluginRoot()
	}
	_, legacy := config.ConsumerContext(root)
	assets := config.ValidationAssets(root)
	emit := func(v any) int {
		if err := json.NewEncoder(out).Encode(v); err != nil {
			return fail(err)
		}
		return 0
	}
	switch op {
	case "path":
		s, err := config.Resolve(env)
		if err != nil {
			return fail(err)
		}
		s.Diagnostics = append(s.Diagnostics, config.ConsumerDiagnostics()...)
		if flags["--json"] != "" {
			return emit(s)
		}
		if _, err := fmt.Fprintln(out, s.Path); err != nil {
			return fail(err)
		}
		for _, d := range s.Diagnostics {
			_, _ = fmt.Fprintln(stderr, d.Code)
		}
		return 0
	case "inspect":
		d, _, s, err := config.ReadDocumentSelection(config.ReadRequest{Env: env, Assets: assets, Legacy: legacy, ReadSnapshot: config.ReadFileSnapshot})
		s.Diagnostics = append(s.Diagnostics, config.ConsumerDiagnostics()...)
		if err != nil {
			var ce *config.Error
			code := config.ConfigInvalid
			if errors.As(err, &ce) {
				code = ce.Code
			}
			emit(config.Inspection{Selection: s, ErrorCode: code})
			return fail(err)
		}
		inspection := config.InspectDocument(s, s.Path, d.Bytes())
		inspection.Revision = d.Revision()
		if emit(inspection) != 0 {
			return 1
		}
		if !inspection.Valid {
			return fail(&config.Error{Code: inspection.ErrorCode})
		}
		return 0
	case "init":
		result, err := config.EnsureInitialized(context.Background(), config.InitRequest{Env: env, Assets: assets, Legacy: legacy, From: flags["--from"]})
		if err != nil {
			return fail(err)
		}
		if flags["--json"] != "" {
			return emit(result)
		}
		var writeErr error
		if flags["--from"] != "" {
			_, writeErr = fmt.Fprintf(out, "Imported from %q to %q (changed=%t)\n", flags["--from"], result.Selection.Path, result.Changed)
		} else {
			_, writeErr = fmt.Fprintln(out, result.Selection.Path)
		}
		if writeErr != nil {
			return fail(writeErr)
		}
		return 0
	case "edit", "preflight-update":
		data, err := io.ReadAll(io.LimitReader(in, config.MaxDocumentBytes+1))
		if err != nil {
			return invalid()
		}
		// The raw document validator rejects duplicates recursively before decoding
		// the operation envelope; decoder errors are never returned to the caller.
		envelope, parseErr := config.ParseDocument(data, "", false)
		if parseErr != nil {
			return invalid()
		}
		keys := envelope.Raw()
		allowedKeys := map[string]bool{"set": true, "remove": true}
		if op == "preflight-update" {
			allowedKeys = map[string]bool{"activeBundleRoots": true, "refreshDirs": true, "protectedPaths": true, "historicalCandidates": true}
		}
		for key, value := range keys {
			if !allowedKeys[key] || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return invalid()
			}
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if op == "edit" {
			var edits config.Edits
			if dec.Decode(&edits) != nil {
				return invalid()
			}
			// Pure validation precedes Store lock/directory creation.
			current, _, err := config.ReadDocument(config.ReadRequest{Env: env, Assets: assets, Legacy: legacy, ReadSnapshot: config.ReadFileSnapshot})
			if err != nil {
				return fail(err)
			}
			if _, err = config.ApplyRawEdits(current, edits, assets); err != nil {
				return fail(err)
			}
			result, err := config.ApplyEdits(context.Background(), config.EditRequest{Env: env, Assets: assets, ExpectRevision: flags["--expect-revision"], Edits: edits})
			if err != nil {
				return fail(err)
			}
			return emit(result)
		}
		var request config.UpdatePreflightRequest
		if dec.Decode(&request) != nil {
			return invalid()
		}
		// Enforce exact service field names; encoding/json otherwise accepts
		// case variants and null object entries as zero-valued candidates.
		if raw, ok := keys["historicalCandidates"]; ok {
			var candidates []map[string]json.RawMessage
			if json.Unmarshal(raw, &candidates) != nil {
				return invalid()
			}
			for _, candidate := range candidates {
				if candidate == nil {
					return invalid()
				}
				for key, value := range candidate {
					if key != "path" && key != "baselinePath" && key != "baselineSHA256" {
						return invalid()
					}
					if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
						return invalid()
					}
				}
			}
		}
		for _, candidate := range request.HistoricalCandidates {
			if !filepath.IsAbs(candidate.Path) || (candidate.BaselinePath != "" && !filepath.IsAbs(candidate.BaselinePath)) {
				return invalid()
			}
		}
		request.Env = env
		request.Assets = assets
		for _, historical := range legacy.Candidates {
			supplied := false
			for _, candidate := range request.HistoricalCandidates {
				if filepath.Clean(candidate.Path) == filepath.Clean(historical.Path) {
					supplied = true
					break
				}
			}
			if !supplied {
				request.HistoricalCandidates = append(request.HistoricalCandidates, historical)
			}
		}
		result, err := config.PreflightUpdate(request)
		if emit(result) != 0 {
			return 1
		}
		if err != nil {
			return fail(err)
		}
		if result.Status != "safe" {
			return 1
		}
		return 0
	}
	return invalid()
}

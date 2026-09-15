package config

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestPreflightHistoryAndOverlap(t *testing.T) {
	root := t.TempDir()
	setTestHome(t, root)
	bundle := filepath.Join(root, "bundle")
	if err := os.MkdirAll(filepath.Join(bundle, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(bundle, "config", "config.json")
	raw := []byte(`{"future":"secret-canary"}`)
	if err := os.WriteFile(p, raw, 0600); err != nil {
		t.Fatal(err)
	}
	r := UpdatePreflightRequest{Env: SnapshotEnv(), ActiveBundleRoots: []string{bundle}, RefreshDirs: []string{bundle}}
	out, err := PreflightUpdate(r)
	if err == nil || out.Status != "import-required" {
		t.Fatalf("unknown history: %+v %v", out, err)
	}
	baseline := filepath.Join(root, "baseline.json")
	if err := os.WriteFile(baseline, raw, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	r.HistoricalCandidates = []HistoricalCandidate{{Path: p, BaselinePath: baseline, BaselineSHA256: hex.EncodeToString(sum[:])}}
	out, err = PreflightUpdate(r)
	if err != nil || out.Status != "safe" {
		t.Fatalf("verified history: %+v %v", out, err)
	}
	r.HistoricalCandidates[0].BaselineSHA256 = "bad"
	if out, err = PreflightUpdate(r); err == nil || out.Status != "import-required" {
		t.Fatal("accepted bad digest")
	}
	r.Env.Vars[OverrideEnv] = p
	if out, err = PreflightUpdate(r); err == nil || out.Status != "unsafe-target" {
		t.Fatal("explicit bypassed overlap")
	}
	r.RefreshDirs = nil
	if out, err = PreflightUpdate(r); err != nil || out.Status != "safe" {
		t.Fatalf("existing should bypass history: %v", err)
	}
	if err := os.WriteFile(p, []byte(`null`), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err = PreflightUpdate(r); err == nil || out.Status != "invalid-config" {
		t.Fatal("invalid canonical accepted")
	}
}

func TestPreflightMissingExplicitOverlapAndCanonicalHistoryBypass(t *testing.T) {
	env := storeEnv(t)
	bundle := t.TempDir()
	env.Vars[OverrideEnv] = filepath.Join(bundle, "missing.json")
	request := UpdatePreflightRequest{Env: env, RefreshDirs: []string{bundle}, HistoricalCandidates: []HistoricalCandidate{{Path: filepath.Join(bundle, "unknown.json")}}}
	out, err := PreflightUpdate(request)
	if err == nil || out.Status != "unsafe-target" {
		t.Fatalf("missing explicit overlap: %v", err)
	}
	entries, err := os.ReadDir(bundle)
	if err != nil || len(entries) != 0 {
		t.Fatal("preflight wrote bundle")
	}
	request.RefreshDirs = nil
	out, err = PreflightUpdate(request)
	if err != nil || out.Status != "safe" || out.Selection.Exists {
		t.Fatalf("missing explicit without overlap: %v", err)
	}
	delete(env.Vars, OverrideEnv)
	r, err := EnsureInitialized(context.Background(), InitRequest{Env: env})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(r.Selection.Path)
	info, _ := os.Stat(r.Selection.Path)
	// An unreadable/non-file unrelated historical candidate must not be consulted
	// once automatic selection has a valid canonical configuration.
	request.Env = env
	request.HistoricalCandidates = []HistoricalCandidate{{Path: bundle}}
	out, err = PreflightUpdate(request)
	if err != nil || out.Status != "safe" {
		t.Fatalf("canonical did not bypass unrelated history: %v", err)
	}
	after, _ := os.ReadFile(r.Selection.Path)
	afterInfo, _ := os.Stat(r.Selection.Path)
	if !bytes.Equal(before, after) || info.Mode() != afterInfo.Mode() || !info.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatal("preflight touched canonical")
	}
	request.RefreshDirs = []string{filepath.Dir(r.Selection.Path)}
	out, err = PreflightUpdate(request)
	if err == nil || out.Status != "unsafe-target" {
		t.Fatalf("canonical bypassed overlap: %v", err)
	}
}

func TestPreflightUsesNativeCaseAliases(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "bundle")
	if err := os.Mkdir(bundle, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "BUNDLE")
	if _, err := os.Stat(alias); err != nil {
		t.Skip("filesystem is case-sensitive")
	}
	env := storeEnv(t)
	env.Vars[OverrideEnv] = filepath.Join(alias, "config", "config.json")
	out, err := PreflightUpdate(UpdatePreflightRequest{Env: env, RefreshDirs: []string{bundle}})
	if err == nil || out.Status != "unsafe-target" {
		t.Fatalf("native alias escaped refresh protection: %+v %v", out, err)
	}
}

func TestPreflightDoesNotFoldDistinctNativePaths(t *testing.T) {
	root := t.TempDir()
	lower := filepath.Join(root, "bundle")
	upper := filepath.Join(root, "BUNDLE")
	if err := os.Mkdir(lower, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(upper, 0700); err != nil {
		t.Skip("filesystem is case-insensitive")
	}
	env := storeEnv(t)
	env.Vars[OverrideEnv] = filepath.Join(upper, "config.json")
	out, err := PreflightUpdate(UpdatePreflightRequest{Env: env, RefreshDirs: []string{lower}})
	if err != nil || out.Status != "safe" {
		t.Fatalf("distinct native path was folded: %+v %v", out, err)
	}
}

func TestPreflightProtectsAbsentNativeCaseAlias(t *testing.T) {
	root := t.TempDir()
	probe := filepath.Join(root, "case-probe")
	if err := os.WriteFile(probe, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "CASE-PROBE")); err != nil {
		t.Skip("filesystem is case-sensitive")
	}
	if err := os.Remove(probe); err != nil {
		t.Fatal(err)
	}
	env := storeEnv(t)
	env.Vars[OverrideEnv] = filepath.Join(root, "hooks.json")
	out, err := PreflightUpdate(UpdatePreflightRequest{Env: env, ProtectedPaths: []string{filepath.Join(root, "HOOKS.JSON")}})
	if err == nil || out.Status != "unsafe-target" {
		t.Fatalf("absent native alias escaped protection: %+v %v", out, err)
	}
}

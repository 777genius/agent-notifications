package codexsetup

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/777genius/agent-notifications/internal/config"
)

// preflightConfig runs against the old destination before bundle staging can
// replace it. Source and destination remain historical inputs, never selectors.
func preflightConfig(source, destination string, refresh bool, protectedPaths ...string) error {
	_, legacy := config.ConsumerContext(source)
	assets := config.ValidationAssets(source)
	_, previous := config.ConsumerContext(destination)
	for _, candidate := range previous.Candidates {
		found := false
		for _, existing := range legacy.Candidates {
			if existing.Path == candidate.Path {
				found = true
				break
			}
		}
		if !found {
			legacy.Candidates = append(legacy.Candidates, candidate)
		}
	}
	request := config.UpdatePreflightRequest{Env: config.SnapshotEnv(), Assets: assets, HistoricalCandidates: legacy.Candidates}
	request.ProtectedPaths = append(request.ProtectedPaths, protectedPaths...)
	request.ActiveBundleRoots = []string{destination}
	if refresh {
		request.RefreshDirs = []string{destination}
	}
	result, err := config.PreflightUpdate(request)
	if err != nil {
		return err
	}
	if result.Status != "safe" {
		return &config.Error{Code: config.ConfigLegacyImportRequired}
	}
	return nil
}

// InitializationError means registration committed but configuration needs a retry.
// Callers must retain installed assets and may retry config init alone.
type InitializationError struct{ Err error }

func (e *InitializationError) Error() string { return e.Err.Error() }
func (e *InitializationError) Unwrap() error { return e.Err }

// initializeConfig runs only after registration has committed. An error here
// must not roll back working assets, hooks, or a concurrently created config.
func initializeConfig(source string, retryExecutable ...string) error {
	_, legacy := config.ConsumerContext(source)
	assets := config.ValidationAssets(source)
	result, err := config.EnsureInitialized(context.Background(), config.InitRequest{Env: config.SnapshotEnv(), Assets: assets, Legacy: legacy})
	if err == nil {
		return nil
	}
	path := result.Selection.Path
	if path == "" {
		if selected, e := config.Resolve(config.SnapshotEnv()); e == nil {
			path = selected.Path
		}
	}
	executable := filepath.Join(source, "bin", "claude-notifications-"+runtime.GOOS+"-"+runtime.GOARCH)
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	if len(retryExecutable) > 0 {
		executable = retryExecutable[0]
	}
	retry := posixQuote(executable) + " config init"
	if runtime.GOOS == "windows" {
		retry = powershellCommand(executable)
	}
	return &InitializationError{Err: fmt.Errorf("registration succeeded; configuration initialization failed at %q: %w; retry only: %s", path, err, retry)}
}

func powershellCommand(executable string) string {
	return "& '" + strings.ReplaceAll(executable, "'", "''") + "' config init"
}

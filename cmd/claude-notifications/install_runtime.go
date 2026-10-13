package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/skills"
)

var reconcileRuntimeNativeRegistration = installruntime.ReconcileNativeRegistration

// The verified staged executable is the sole shell installer mutation adapter.
// There is no notification delivery, application launch or feature activation.
func installRuntime(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("internal-install-runtime", flag.ContinueOnError)
	flags.SetOutput(output)
	source := flags.String("stage", "", "verified staging directory")
	entry := flags.String("entry", "", "platform binary basename for stable launcher and hooks")
	target := flags.String("target", "", "existing stable bin directory")
	control := flags.String("control-root", "", "shared control root (test override)")
	remove := flags.Bool("remove", false, "remove this managed consumer")
	requireNative := flags.Bool("require-native", false, "require an attested compatible native reader")
	refresh := flags.Bool("refresh", false, "refresh files for existing consumers without adding a registration")
	relocateCache := flags.Bool("relocate-versioned-cache", false, "move Claude hooks between versioned plugin caches")
	purge := flags.Bool("purge-native", false, "explicitly remove retained callback on final uninstall")
	printNativePath := flags.Bool("print-native-path", false, "print only the committed durable native generation path")
	consumer := flags.String("consumer", "claude-hooks", "managed consumer identity")
	orphan := flags.Bool("recover-orphan-consumer", false, "recover exactly one entirely absent consumer")
	redo := flags.Bool("recover-pending", false, "replay the existing pending transaction")
	rollback := flags.Bool("rollback-pending", false, "reverse the existing pending transaction (does not restore payload)")
	dryRun := flags.Bool("dry-run", false, "read-only orphan recovery preview")
	jsonOutput := flags.Bool("json", false, "bounded recovery result as JSON")
	runtimeRoot := flags.String("runtime-root", "", "exact recorded orphan runtime root")
	installationID := flags.String("expected-installation-id", "", "exact observed installation ID")
	generation := flags.Uint64("expected-generation", 0, "positive observed orphan generation")
	if err := flags.Parse(args); err != nil {
		return err
	}
	seen := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { seen[f.Name] = true })
	if *orphan || *redo || *rollback {
		modes := 0
		for _, enabled := range []bool{*orphan, *redo, *rollback} {
			if enabled {
				modes++
			}
		}
		if modes != 1 || flags.NArg() != 0 {
			return fmt.Errorf("select exactly one recovery mode without positional arguments")
		}
		allowed := map[string]bool{"control-root": true, "json": true}
		if *orphan {
			for _, key := range []string{"recover-orphan-consumer", "consumer", "runtime-root", "expected-installation-id", "expected-generation", "dry-run"} {
				allowed[key] = true
			}
		} else if *redo {
			allowed["recover-pending"] = true
		} else {
			allowed["rollback-pending"] = true
		}
		for key := range seen {
			if !allowed[key] {
				return fmt.Errorf("--%s is incompatible with this recovery mode", key)
			}
		}
		if err := recoveryCLIPath(*control); err != nil {
			return fmt.Errorf("explicit control-root required: %w", err)
		}
		if *orphan {
			if !seen["consumer"] || !recoveryCLIText(*consumer) || !recoveryCLIText(*installationID) || *generation == 0 {
				return fmt.Errorf("orphan recovery requires explicit consumer, expected-installation-id and positive expected-generation")
			}
			if err := recoveryCLIPath(*runtimeRoot); err != nil {
				return fmt.Errorf("explicit runtime-root required: %w", err)
			}
		}
		return runRuntimeRecovery(output, *control, *consumer, *runtimeRoot, *installationID, *generation, *orphan, *redo, *dryRun, *jsonOutput)
	}
	for _, key := range []string{"recover-orphan-consumer", "recover-pending", "rollback-pending", "dry-run", "json", "runtime-root", "expected-installation-id", "expected-generation"} {
		if seen[key] {
			return fmt.Errorf("--%s requires a recovery mode", key)
		}
	}
	if strings.HasPrefix(*entry, "claude-notifications-darwin-") && !*remove {
		*requireNative = true
	}
	if *refresh && *remove {
		return fmt.Errorf("refresh cannot remove a consumer")
	}
	if *relocateCache && (*refresh || *remove || *consumer != "claude-hooks") {
		return fmt.Errorf("versioned cache relocation requires a Claude hooks install")
	}
	if *purge && !*remove {
		return fmt.Errorf("purge requires consumer removal")
	}
	if flags.NArg() != 0 || (!*remove && *source == "") || *target == "" {
		return fmt.Errorf("stage and target are required")
	}
	stage, err := filepath.Abs(*source)
	if err != nil {
		return err
	}
	destination, err := installruntime.CanonicalPath(*target)
	if err != nil {
		return err
	}
	var files []installruntime.File
	if !*remove {
		files, err = installruntime.StageFiles(stage, destination, func(rel string) bool {
			if strings.ContainsAny(rel, "/\\") {
				return false
			}
			for _, utility := range []string{"sound-preview", "list-devices", "list-sounds"} {
				if rel == utility || rel == utility+".bat" {
					return true
				}
				for _, osName := range []string{"linux", "darwin", "windows"} {
					for _, arch := range []string{"amd64", "arm64"} {
						name := utility + "-" + osName + "-" + arch
						if osName == "windows" {
							name += ".exe"
						}
						if rel == name {
							return true
						}
					}
				}
			}
			for _, osName := range []string{"linux", "darwin", "windows"} {
				for _, arch := range []string{"amd64", "arm64"} {
					name := "claude-notifications-" + osName + "-" + arch
					if osName == "windows" {
						if rel == name+"-focus.exe" {
							return true
						}
						name += ".exe"
					}
					if rel == name {
						return true
					}
				}
			}
			return false
		})
	}
	if err != nil && !*remove {
		return err
	}
	if *remove {
		files = nil
	}
	if *refresh && *entry == "" {
		changed := files[:0]
		for _, file := range files {
			sourceIdentity, err := installruntime.Fingerprint(filepath.Join(stage, filepath.Base(file.Path)))
			if err != nil {
				return err
			}
			// An unchanged, previously unmanaged utility is not implicitly adopted.
			if sourceIdentity != file.Before {
				changed = append(changed, file)
			}
		}
		files = changed
		if len(files) == 0 {
			if *printNativePath {
				root := *control
				if root == "" {
					root, err = installruntime.ControlRoot()
					if err != nil {
						return err
					}
				}
				// Observe retained ownership without committing or adopting utilities.
				snapshot, err := installruntime.ReadInstalledSnapshot(root)
				if err != nil {
					return err
				}
				if snapshot.Recovery {
					return installruntime.ErrPolicyRecovery
				}
				ledger := snapshot.Ledger
				if ledger.ID != "" && ledger.Owner != "existing-installer" {
					return fmt.Errorf("component owned by %s at %s; explicit takeover required", ledger.Owner, ledger.RuntimeRoot)
				}
				if ledger.Native != nil {
					root, err = installruntime.CanonicalPath(root)
					if err != nil {
						return err
					}
					if ledger.Native.Path != filepath.Clean(ledger.Native.Path) || filepath.Dir(ledger.Native.Path) != filepath.Join(root, "native") {
						return fmt.Errorf("native owner outside persistent directory")
					}
				}
				return printRuntimeNativePath(output, ledger)
			}
			return nil
		}
	}
	if len(files) == 0 && !*remove {
		return fmt.Errorf("no staged runtime binaries")
	}
	if *remove && *entry == "" && runtime.GOOS == "windows" {
		*entry = "claude-notifications-windows-" + runtime.GOARCH + ".exe"
	}
	if *entry != "" {
		valid := false
		for _, osName := range []string{"linux", "darwin", "windows"} {
			for _, arch := range []string{"amd64", "arm64"} {
				name := "claude-notifications-" + osName + "-" + arch
				if osName == "windows" {
					name += ".exe"
				}
				if *entry == name {
					valid = true
				}
			}
		}
		if !valid {
			return fmt.Errorf("invalid managed entry binary")
		}
		if !*remove {
			staged := false
			for _, file := range files {
				if filepath.Base(file.Path) == *entry {
					staged = true
				}
			}
			if !staged {
				return fmt.Errorf("entry binary missing from stage")
			}
			for _, launcher := range []string{"claude-notifications", "agent-notifications"} {
				name := launcher
				if strings.HasSuffix(*entry, ".exe") {
					name += ".bat"
				}
				path := filepath.Join(destination, name)
				before, err := installruntime.Fingerprint(path)
				if err != nil {
					return err
				}
				alias := installruntime.File{Path: path, Before: before, Link: *entry}
				if strings.HasSuffix(*entry, ".exe") {
					alias.Link = ""
					alias.Mode = 0755
					alias.Data = installruntime.WindowsLauncherScript(launcher, *entry)
				}
				files = append(files, alias)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// A broken concrete modern bundle shadows legacy discovery. Preserve that
	// foreign path and refuse rather than silently deleting it during repair.
	modern := filepath.Join(destination, "ClaudeNotifier.app", "Contents", "MacOS", "terminal-notifier-modern")
	if info, e := os.Stat(modern); e == nil && info.Mode().Perm()&0111 == 0 && !*remove {
		return fmt.Errorf("unusable concrete modern callback requires explicit repair; preserving existing bundle")
	}
	inPlace := false
	if sourceInfo, e := os.Stat(stage); e == nil {
		if targetInfo, e := os.Stat(destination); e == nil {
			inPlace = os.SameFile(sourceInfo, targetInfo)
		}
	}
	var native *installruntime.NativeChange
	hasSender := false
	darwinSender := false
	for _, file := range files {
		base := filepath.Base(file.Path)
		if strings.HasPrefix(base, "claude-notifications-darwin-") {
			darwinSender = true
			hasSender = true
		} else if strings.HasPrefix(base, "claude-notifications-") {
			hasSender = true
		}
	}
	if (darwinSender || strings.HasPrefix(*entry, "claude-notifications-darwin-")) && !*remove {
		*requireNative = true
	}
	if !*remove && hasSender {
		// Exhaust the supplied release before considering any retained helper.
		// A managed alias in destination must not pin subsequent updates to A.
		for _, location := range []struct {
			root     string
			retained bool
		}{{stage, inPlace}, {destination, true}} {
			for _, name := range []string{"AgentNotifications.app", "ClaudeNotifier.app", "terminal-notifier.app"} {
				candidate := filepath.Join(location.root, name)
				if _, e := os.Stat(candidate); os.IsNotExist(e) {
					continue
				} else if e != nil {
					return e
				}
				if location.retained {
					native, err = installruntime.StageRetainedNative(ctx, *control, candidate)
				} else {
					native, err = installruntime.StageNative(ctx, *control, candidate)
				}
				if err != nil {
					return fmt.Errorf("native package verification failed; obtain the current authenticated release and external attestation before retrying: %w", err)
				}
				break
			}
			if native != nil {
				break
			}
		}
		if native != nil {
			defer func() {
				cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = installruntime.DiscardNative(cleanup, *control, native)
			}()
		}
		if *requireNative && (native == nil || native.After.DecoderFloor < 1 || len(native.After.Attestation) == 0) {
			return fmt.Errorf("required compatible native release unavailable or unverified; obtain the current authenticated package and its external attestation, then retry (existing callbacks preserved)")
		}
		aliases, e := installruntime.NativeAlias(native, destination)
		if e != nil {
			return e
		}
		files = append(files, aliases...)
	}
	req := installruntime.Request{RefreshOnly: *refresh, RelocateVersionedCache: *relocateCache, ControlRoot: *control, Owner: "existing-installer", RuntimeRoot: filepath.Dir(destination), ConsumerID: *consumer, Files: files, Native: native, RemoveConsumer: *remove, PurgeNative: *purge}
	if strings.HasSuffix(*entry, ".exe") {
		hooks := filepath.Join(filepath.Dir(destination), "hooks", "hooks.json")
		exe := filepath.Join(destination, *entry)
		req.ConfigPaths = []string{hooks}
		req.Consumer = installruntime.Consumer{Registration: hooks, Commands: []string{exe}}
		req.Prepare = func() ([]installruntime.File, error) { return prepareRuntimeHooks(hooks, exe, *remove) }
	}
	if !*remove && *entry != "" {
		prepare := req.Prepare
		req.Prepare = func() ([]installruntime.File, error) {
			// Recheck ownership under the component lock before any promotion.
			path := filepath.Join(req.RuntimeRoot, "skills", "agent-notifications", "SKILL.md")
			before, err := installruntime.Fingerprint(path)
			if err != nil {
				return nil, err
			}
			if before.Exists {
				if before.Link != "" {
					return nil, fmt.Errorf("canonical skill is not an unchanged owned regular file: %s", path)
				}
				ledger, recovery, err := installruntime.ReadOwnership(req.ControlRoot)
				if err != nil {
					return nil, err
				}
				if recovery {
					return nil, fmt.Errorf("canonical skill is not an unchanged owned regular file: %s", path)
				}
				owned, ok := installruntime.OwnedFile(ledger, path)
				if ok {
					if owned != before {
						return nil, fmt.Errorf("canonical skill is not an unchanged owned regular file: %s", path)
					}
				} else {
					current, err := os.ReadFile(path)
					if err != nil {
						return nil, err
					}
					if !bytes.Equal(current, skills.AgentNotify()) {
						return nil, fmt.Errorf("canonical skill is not an unchanged owned regular file: %s", path)
					}
				}
			}
			var extra []installruntime.File
			if prepare != nil {
				extra, err = prepare()
				if err != nil {
					return nil, err
				}
			}
			legacy := filepath.Join(req.RuntimeRoot, "skills", "agent-notify", "SKILL.md")
			legacyBefore, err := installruntime.Fingerprint(legacy)
			if err != nil {
				return nil, err
			}
			if legacyBefore.Exists {
				ledger, recovery, err := installruntime.ReadOwnership(req.ControlRoot)
				if err != nil {
					return nil, err
				}
				owned, ok := installruntime.OwnedFile(ledger, legacy)
				if recovery || !ok || legacyBefore.Link != "" || owned != legacyBefore {
					return nil, fmt.Errorf("legacy skill is not an unchanged owned regular file: %s", legacy)
				}
				extra = append(extra, installruntime.File{Path: legacy, Before: legacyBefore, Remove: true})
			}
			return append(extra, installruntime.File{Path: path, Before: before, Data: skills.AgentNotify(), Mode: 0600}), nil
		}
	}
	ledger, err := installruntime.Commit(ctx, req)
	if err == nil {
		if !*printNativePath {
			_, _ = fmt.Fprintf(output, "managed-runtime committed generation=%d\n", ledger.Generation)
		}
		if !*remove {
			regCtx, regCancel := context.WithTimeout(context.Background(), 10*time.Second)
			warning := reconcileRuntimeNativeRegistration(regCtx, *control)
			regCancel()
			if warning != nil {
				warningOutput := output
				if *printNativePath {
					warningOutput = os.Stderr
				}
				_, _ = fmt.Fprintf(warningOutput, "warning: runtime committed; native registration reconciliation incomplete: %v\n", warning)
			}
		}
		if *printNativePath {
			return printRuntimeNativePath(output, ledger)
		}
		if *purge {
			_, _ = fmt.Fprintln(output, "Callback entrypoint purge completed; pending notifications may no longer open targets. Running callbacks are not stopped.")
		}
	}
	return err
}

func printRuntimeNativePath(output io.Writer, ledger installruntime.Ledger) error {
	path := ""
	if ledger.Native != nil {
		path = ledger.Native.Path
	}
	_, err := fmt.Fprintln(output, path)
	return err
}

// Project only bounded diagnostics. Ownership records, policy preimages and
// opaque command strings are never part of the public recovery response.
type runtimeRecoveryResult struct {
	InstallationID        string   `json:"installation_id,omitempty"`
	Generation            uint64   `json:"generation,omitempty"`
	ConsumerID            string   `json:"consumer,omitempty"`
	RuntimeRoot           string   `json:"runtime_root,omitempty"`
	SelectedPaths         []string `json:"selected_paths,omitempty"`
	SelectedCount         int      `json:"selected_count"`
	PathsTruncated        bool     `json:"paths_truncated,omitempty"`
	Admissible            bool     `json:"admissible"`
	ConflictCode          string   `json:"conflict_code,omitempty"`
	ConflictPath          string   `json:"conflict_path,omitempty"`
	NativeValidated       bool     `json:"native_validated"`
	NativeIdentityRefresh bool     `json:"native_identity_refresh"`
	Mode                  string   `json:"mode"`
}

func recoveryCLIText(s string) bool {
	return s != "" && len(s) <= 4096 && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}

func recoveryCLIPath(path string) error {
	if !recoveryCLIText(path) || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("requires a clean absolute path")
	}
	physical, err := installruntime.CanonicalPath(path)
	if err != nil {
		return err
	}
	if physical != path {
		return fmt.Errorf("requires a physical path without symlink aliases")
	}
	return nil
}

func boundedRecoveryText(s string) string {
	if !recoveryCLIText(s) {
		return ""
	}
	return s
}

// Keep the original typed cause available to callers without exposing ledger or
// journal contents through main's stderr error rendering.
type runtimeRecoveryPublicError struct {
	code  string
	cause error
}

func (e *runtimeRecoveryPublicError) Error() string { return "runtime recovery refused: " + e.code }
func (e *runtimeRecoveryPublicError) Unwrap() error { return e.cause }

// Pending replay must never bootstrap an installation or create missing locks.
// ReadOwnership validates the private physical directory without requiring the
// selected consumer or its payload to survive the interrupted transaction.
func pendingRecoveryControl(control string) (installruntime.Ledger, error) {
	var empty installruntime.Ledger
	if err := recoveryCLIPath(control); err != nil {
		return empty, err
	}
	ledger, pending, err := installruntime.ReadOwnership(control)
	if err != nil {
		return empty, err
	}
	if !recoveryCLIText(ledger.ID) || ledger.Generation == 0 || ledger.PolicyGeneration == 0 || ledger.Owner != "existing-installer" || ledger.WriterFloor < 0 || ledger.WriterFloor > installruntime.SupportedWriterFloor {
		return empty, fmt.Errorf("existing managed ownership required")
	}
	// Schema5 replay validates strict live ownership through protected kernel reads.
	if !pending {
		return empty, fmt.Errorf("no pending transaction")
	}
	for _, name := range []string{"transaction.json", ".component-install.lock", "agent-notifications.json.lock"} {
		identity, err := installruntime.Fingerprint(filepath.Join(control, name))
		if err != nil {
			return empty, err
		}
		if !identity.Exists || identity.Link != "" || identity.Mode != installruntime.IdentityMode(0600) {
			return empty, fmt.Errorf("existing private journal and permanent locks required")
		}
	}
	return ledger, nil
}

func runRuntimeRecovery(out io.Writer, control, consumer, runtimeRoot, id string, generation uint64, orphan, redo, preview, asJSON bool) error {
	r := runtimeRecoveryResult{Mode: "rollback-pending"}
	if redo {
		r.Mode = "recover-pending"
	}
	var operationErr error
	if orphan {
		r.Mode, r.ConsumerID, r.RuntimeRoot = "recover-orphan-consumer", consumer, runtimeRoot
		if preview {
			r.Mode = "preview"
		}
		// Read ownership directly: a healthy snapshot is impossible for an orphan.
		ledger, _, err := installruntime.ReadOwnership(control)
		operationErr = err
		r.Admissible = err == nil
		if err == nil {
			r.InstallationID, r.Generation = boundedRecoveryText(ledger.ID), ledger.Generation
			observed, exists := ledger.Consumers[consumer]
			if !exists {
				operationErr = fmt.Errorf("selected consumer is absent; reread ownership and generation")
			} else {
				req := installruntime.OrphanRecoveryRequest{InstallationID: id, ExpectedGeneration: generation, ConsumerID: consumer, RuntimeRoot: runtimeRoot, Consumer: observed}
				p, err := installruntime.PreviewOrphanConsumer(control, req)
				operationErr = err
				r.SelectedCount, r.Admissible = p.SelectedCount, p.Admissible
				r.ConflictCode, r.ConflictPath = boundedRecoveryText(p.ConflictCode), boundedRecoveryText(p.ConflictPath)
				r.NativeValidated, r.NativeIdentityRefresh = p.NativeValidated, p.NativeIdentityRefresh
				for i, path := range p.SelectedPaths {
					if i == 256 {
						r.PathsTruncated = true
						break
					}
					if !recoveryCLIText(path) {
						operationErr = fmt.Errorf("selected path exceeds diagnostic bounds")
						r.Admissible = false
						break
					}
					r.SelectedPaths = append(r.SelectedPaths, path)
				}
				if operationErr == nil && !preview {
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
					after, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, OrphanRecovery: &req, ExpectedPolicy: &p.ExpectedPolicy})
					cancel()
					operationErr = err
					if err == nil {
						r.Generation = after.Generation
					}
				}
			}
		}
	} else {
		// Pending replay is standalone: its durable after-image may already lack
		// the selected consumer. The kernel fences its own before/after images.
		ledger, err := pendingRecoveryControl(control)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			ledger, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, RecoverOnly: redo, RollbackPending: !redo})
			cancel()
		}
		operationErr = err
		r.Admissible = err == nil
		if err == nil {
			r.InstallationID, r.Generation = boundedRecoveryText(ledger.ID), ledger.Generation
		}
	}
	if operationErr != nil {
		r.Admissible = false
		var conflict *installruntime.OrphanRecoveryConflict
		if errors.As(operationErr, &conflict) {
			r.ConflictCode, r.ConflictPath = boundedRecoveryText(conflict.Code), boundedRecoveryText(conflict.Path)
		}
		if r.ConflictCode == "" {
			r.ConflictCode = "recovery_conflict"
		}
	}
	var writeErr error
	if asJSON {
		writeErr = json.NewEncoder(out).Encode(r)
	} else {
		_, writeErr = fmt.Fprintf(out, "%s installation=%q generation=%d consumer=%q runtime=%q admissible=%t selected=%d native-validated=%t native-identity-refresh=%t conflict=%s path=%q\n", r.Mode, r.InstallationID, r.Generation, r.ConsumerID, r.RuntimeRoot, r.Admissible, r.SelectedCount, r.NativeValidated, r.NativeIdentityRefresh, r.ConflictCode, r.ConflictPath)
		for _, path := range r.SelectedPaths {
			if writeErr != nil {
				break
			}
			_, writeErr = fmt.Fprintf(out, "  %q\n", path)
		}
		if writeErr == nil && r.PathsTruncated {
			_, writeErr = fmt.Fprintln(out, "  (remaining paths omitted)")
		}
	}
	if writeErr != nil {
		return writeErr
	}
	if operationErr != nil {
		return &runtimeRecoveryPublicError{code: "recovery_conflict", cause: operationErr}
	}
	return nil
}

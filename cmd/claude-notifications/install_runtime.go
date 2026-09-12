package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/skills"
)

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
	purge := flags.Bool("purge-native", false, "explicitly remove retained callback on final uninstall")
	consumer := flags.String("consumer", "claude-hooks", "managed consumer identity")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.HasPrefix(*entry, "claude-notifications-darwin-") && !*remove {
		*requireNative = true
	}
	if *refresh && *remove {
		return fmt.Errorf("refresh cannot remove a consumer")
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
		for _, name := range []string{"ClaudeNotifier.app", "terminal-notifier.app"} {
			candidate := filepath.Join(stage, name)
			retained := inPlace
			if _, e := os.Stat(candidate); os.IsNotExist(e) {
				candidate = filepath.Join(destination, name)
				retained = true
			}
			if _, e := os.Stat(candidate); os.IsNotExist(e) {
				continue
			} else if e != nil {
				return e
			}
			// A required release update needs the selected package identity. A
			// decoder floor alone cannot prove a retained artifact is current.
			if retained && *requireNative {
				return fmt.Errorf("required compatible native release must be supplied in a separate authenticated stage; cached helper presence cannot establish release freshness")
			}
			if retained {
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
	req := installruntime.Request{RefreshOnly: *refresh, ControlRoot: *control, Owner: "existing-installer", RuntimeRoot: filepath.Dir(destination), ConsumerID: *consumer, Files: files, Native: native, RemoveConsumer: *remove, PurgeNative: *purge}
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
			path := filepath.Join(req.RuntimeRoot, "skills", "agent-notify", "SKILL.md")
			before, err := installruntime.Fingerprint(path)
			if err != nil {
				return nil, err
			}
			if before.Exists {
				if before.Link != "" {
					return nil, fmt.Errorf("canonical skill is not an unchanged owned regular file: %s", path)
				}
				snapshot, err := installruntime.ReadInstalledSnapshot(req.ControlRoot)
				if err != nil {
					return nil, err
				}
				if snapshot.Recovery {
					return nil, fmt.Errorf("canonical skill is not an unchanged owned regular file: %s", path)
				}
				owned, ok := snapshot.Ledger.Files[path]
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
			return append(extra, installruntime.File{Path: path, Before: before, Data: skills.AgentNotify(), Mode: 0600}), nil
		}
	}
	ledger, err := installruntime.Commit(ctx, req)
	if err == nil {
		fmt.Fprintf(output, "managed-runtime committed generation=%d\n", ledger.Generation)
		if *purge {
			fmt.Fprintln(output, "Callback entrypoint purge completed; pending notifications may no longer open targets. Running callbacks are not stopped.")
		}
	}
	return err
}

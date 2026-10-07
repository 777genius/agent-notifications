//go:build linux

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/linuxcallback"
	"github.com/777genius/agent-notifications/internal/notification"
)

func linuxCallbackMain(command string, args []string) int {
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	path := f.String("snapshot", "", "immutable callback snapshot")
	hash := f.String("sha256", "", "immutable snapshot digest")
	selected := f.String("installation-root", "", "selected supported vendor installation")
	if e := f.Parse(args); e != nil || len(f.Args()) != 0 {
		return 2
	}
	if command == "internal-linux-callback" {
		if *selected != "" {
			return 2
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if e := linuxcallback.Serve(ctx, notification.LinuxBinding{SnapshotPath: *path, SHA256: *hash}); e != nil {
			fmt.Fprintln(os.Stderr, "linux callback unavailable")
			return 1
		}
		return 0
	}
	if *path != "" || *hash != "" || !filepath.IsAbs(*selected) {
		return 2
	}
	root, e := installruntime.ControlRoot()
	if e != nil {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	observed, e := installruntime.ReadPolicySnapshot(ctx, root)
	if e != nil || observed.Installation.Recovery || observed.Installation.Ledger.ID == "" {
		return 1
	}
	s, e := linuxcallback.SelectedSnapshot(*selected)
	if e != nil {
		fmt.Fprintln(os.Stderr, "unsupported selected vendor installation")
		return 1
	}
	var nonce [32]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		return 1
	}
	s.InstallationID = hex.EncodeToString(nonce[:])
	s.ApplicationID = "org.agentnotifications.Callback" + s.InstallationID
	s.Records = filepath.Join(root, "linux-callback", s.InstallationID, "records")
	reader, e := os.Executable()
	if e != nil {
		return 1
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, e := os.UserHomeDir()
		if e != nil {
			return 1
		}
		data = filepath.Join(home, ".local", "share")
	}
	files, binding, e := installruntime.StageLinuxCallback(ctx, root, data, reader, s)
	if e != nil {
		fmt.Fprintln(os.Stderr, fmt.Errorf("linux callback stage: %w", e))
		return 1
	}
	patch, _ := json.Marshal(map[string]any{"linuxCallbackSnapshot": binding})
	l := observed.Installation.Ledger
	_, e = installruntime.Commit(ctx, installruntime.Request{ControlRoot: root, Owner: l.Owner, RuntimeRoot: l.RuntimeRoot, ConsumerID: "linux-callback-setup", RefreshOnly: true, ExpectedGeneration: &l.Generation, ExpectedPolicy: &observed.Preimage, Files: files, PolicyFields: map[string]json.RawMessage{"route": patch}})
	if e != nil {
		fmt.Fprintln(os.Stderr, fmt.Errorf("linux callback commit: %w", e))
		return 1
	}
	if _, e = fmt.Fprintln(os.Stdout, "Linux callback installed; local routing policy remains explicit"); e != nil {
		return 1
	}
	return 0
}

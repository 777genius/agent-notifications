package installruntime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCommitProcess(t *testing.T) {
	if root := os.Getenv("PR2_COMMIT_ROOT"); root != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		action := os.Getenv("PR2_COMMIT_ACTION")
		r := Request{ControlRoot: filepath.Join(root, "control"), RuntimeRoot: filepath.Join(root, "bin"), Owner: "existing-installer", ConsumerID: os.Getenv("PR2_COMMIT_CONSUMER"), RemoveConsumer: action == "remove"}
		if action == "takeover" {
			r.Owner = "foreign-manager"
			if _, err := Commit(ctx, r); err == nil {
				t.Fatal("foreign process took ownership")
			}
			return
		}
		for attempt := 0; attempt < 4; attempt++ {
			if action == "install" {
				path := filepath.Join(r.RuntimeRoot, "sender")
				before, err := Fingerprint(path)
				if err != nil {
					t.Fatal(err)
				}
				r.Files = []File{{Path: path, Before: before, Data: []byte("sender"), Mode: 0755}}
			}
			if _, err := Commit(ctx, r); err == nil {
				return
			} else if attempt == 3 {
				t.Fatal(err)
			}
		}
		return
	}
	for _, scenario := range []string{"install-install", "add-final-remove", "takeover-install"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			r := Request{ControlRoot: filepath.Join(root, "control"), RuntimeRoot: filepath.Join(root, "bin"), Owner: "existing-installer", ConsumerID: "codex"}
			if scenario != "install-install" {
				if _, err := Commit(ctx, r); err != nil {
					t.Fatal(err)
				}
			}
			actions := []string{"install", "install"}
			if scenario == "add-final-remove" {
				actions = []string{"remove", "add"}
			}
			if scenario == "takeover-install" {
				actions = []string{"takeover", "install"}
			}
			consumers := []string{"codex", "claude"}
			commands := []*exec.Cmd{}
			for i, action := range actions {
				cmd := exec.Command(os.Args[0], "-test.run=^TestCommitProcess$")
				cmd.Env = append(os.Environ(), "PR2_COMMIT_ROOT="+root, "PR2_COMMIT_ACTION="+action, "PR2_COMMIT_CONSUMER="+consumers[i])
				cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				commands = append(commands, cmd)
			}
			for _, cmd := range commands {
				if err := cmd.Wait(); err != nil {
					t.Fatal(err)
				}
			}
			l, err := readLedger(r.ControlRoot)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := l.Consumers["claude"]; !ok {
				t.Fatal("lost concurrent admission")
			}
			if scenario != "add-final-remove" && (len(l.Consumers) != 2 || l.Owner != "existing-installer") {
				t.Fatal("lost install consumer")
			}
			if scenario == "add-final-remove" && len(l.Consumers) != 1 {
				t.Fatal("removed consumer survived")
			}
		})
	}
}

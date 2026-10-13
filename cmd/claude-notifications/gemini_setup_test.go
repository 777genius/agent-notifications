package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Argument/channel errors must leave even nonexistent TEST roots untouched.
func TestGeminiSetupPreflightRejectsWithoutWrites(t *testing.T) {
	base, err := installruntime.CanonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "control")
	for _, args := range [][]string{
		{"install", "--control-root", root, "--runtime-root", filepath.Join(base, "runtime")},
		{"install", "--control-root", root, "--desktop", "--unknown"},
		{"install", "--control-root", root, "--webhook", "positional"},
		{"unsupported", "--control-root", root},
	} {
		var output bytes.Buffer
		if runGeminiSetup(args, &output) == 0 {
			t.Fatalf("accepted %v", args)
		}
		if _, err := os.Lstat(root); !os.IsNotExist(err) {
			t.Fatalf("argument failure wrote root: %v", err)
		}
	}
}

func TestGeminiSetupUsesActualFlagRootsAndSeparateConsent(t *testing.T) {
	base, err := installruntime.CanonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, runtimeRoot, profile := filepath.Join(base, "control"), filepath.Join(base, "runtime"), filepath.Join(base, "TEST-profile")
	executable := managedFixtureExecutable(t)
	var output bytes.Buffer
	args := []string{"install", "--control-root", root, "--runtime-root", runtimeRoot, "--home", profile, "--config-root", filepath.Join(profile, "native"), "--binary", executable, "--webhook"}
	if runGeminiSetup(args, &output) != 0 {
		t.Fatal(output.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := installruntime.ReadPolicySnapshot(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Installation.Ledger.Consumers["gemini-notifications"]; !ok {
		t.Fatal("CLI did not register Gemini")
	}
	if !bytes.Contains(s.Fields["route"], []byte(`"geminiNotifications"`)) || bytes.Contains(s.Fields["route"], []byte(`"openCodeNotifications"`)) {
		t.Fatal("CLI mixed observer consent")
	}
	if _, err := os.Stat(filepath.Join(profile, "native", "settings.json")); err != nil {
		t.Fatal("explicit config-root was ignored", err)
	}
	for _, action := range []string{"inspect", "status"} {
		output.Reset()
		if runGeminiSetup([]string{action, "--control-root", root}, &output) != 0 {
			t.Fatal(output.String())
		}
		var got struct {
			Status                       string
			Registered, Desktop, Webhook bool
		}
		d := json.NewDecoder(&output)
		if err := d.Decode(&got); err != nil || got.Status != "installed" || !got.Registered || got.Desktop || !got.Webhook {
			t.Fatalf("%s did not inspect actual managed registration: %+v %v", action, got, err)
		}
		var extra any
		if err := d.Decode(&extra); err != io.EOF {
			t.Fatal("inspection printed more than one JSON object")
		}
		current, recovery, err := installruntime.ReadOwnership(root)
		if err != nil || recovery || current.Generation != s.Installation.Ledger.Generation {
			t.Fatalf("inspection mutated registration: %+v %v", current, err)
		}
	}
	output.Reset()
	if runGeminiSetup([]string{"remove", "--control-root", root, "--home", profile}, &output) != 0 {
		t.Fatal(output.String())
	}
}

func TestGeminiSetupInspectAbsentAndConflictJSON(t *testing.T) {
	base, err := installruntime.CanonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "control")
	var output bytes.Buffer
	if code := runGeminiSetup([]string{"inspect", "--control-root", root}, &output); code != 0 || output.String() != "{\"status\":\"absent\",\"registered\":false,\"desktop\":false,\"webhook\":false}\n" {
		t.Fatalf("absent status: %d %q", code, output.String())
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatal("read-only CLI created missing control root")
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "ownership.json")
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if code := runGeminiSetup([]string{"status", "--control-root", root}, &output); code != 1 || output.String() != "{\"status\":\"conflict\",\"registered\":false,\"desktop\":false,\"webhook\":false}\n" {
		t.Fatalf("conflict status: %d %q", code, output.String())
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "broken" {
		t.Fatal("status repaired corrupt ownership")
	}
}

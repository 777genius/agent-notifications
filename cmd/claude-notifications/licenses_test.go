package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/testenv"
)

// The actual binary must retain readable notices and bypass configuration/effects.
func TestLicensesCommandHasNoEffects(t *testing.T) {
	binary := buildCLIBinary(t)
	home := t.TempDir()
	config := filepath.Join(home, "invalid.json")
	const original = "deliberately invalid configuration"
	if err := os.WriteFile(config, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	env := append(testenv.Env(t, home), "AGENT_NOTIFICATIONS_CONFIG="+config)
	snapshot := func() map[string]string {
		t.Helper()
		files := map[string]string{}
		err := filepath.WalkDir(home, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			value := info.Mode().String()
			if info.Mode().IsRegular() {
				body, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				value += fmt.Sprintf(":%x", sha256.Sum256(body))
			}
			files[path] = value
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	before := snapshot()
	for _, extra := range []bool{false, true} {
		args := []string{"licenses"}
		if extra {
			args = append(args, "unexpected")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.WaitDelay = time.Second
		cmd.Env = env
		output, err := cmd.CombinedOutput()
		cancel()
		if extra {
			if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 {
				t.Fatalf("unexpected arguments: %v %s", err, output)
			}
		} else {
			if err != nil {
				t.Fatalf("licenses: %v %s", err, output)
			}
			for _, required := range []string{"charm.land/huh/v2", "charm.land/bubbletea/v2", "Permission is hereby granted", "Redistribution and use in source and binary forms"} {
				if !strings.Contains(string(output), required) {
					t.Fatalf("binary notices omit %q", required)
				}
			}
		}
		if !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("licenses changed profile paths, permissions or contents")
		}
	}
}

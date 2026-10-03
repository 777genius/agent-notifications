package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

var managedFixture struct {
	once            sync.Once
	dir, executable string
	cwd             string
	compilerEnv     []string
	err             error
}

func TestMain(m *testing.M) {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "locate TEST package source:", err)
		os.Exit(1)
	}
	managedFixture.cwd = cwd
	managedFixture.compilerEnv = os.Environ()
	code := m.Run()
	if managedFixture.dir != "" {
		if err := os.RemoveAll(managedFixture.dir); err != nil {
			fmt.Fprintln(os.Stderr, "remove TEST managed executable:", err)
			code = 1
		}
	}
	os.Exit(code)
}

// Only installation fixture children need this bounded image. The parent test
// binary keeps race/coverage instrumentation; child bytes retain the same real
// package and portable TEST init dispatch, without copying that instrumentation
// into the production managed-file payload. This command only compiles tests.
func managedFixtureExecutable(t *testing.T) string {
	t.Helper()
	managedFixture.once.Do(func() {
		managedFixture.dir, managedFixture.err = os.MkdirTemp("", "TEST-managed-executable-")
		if managedFixture.err != nil {
			return
		}
		name := "fixture"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		managedFixture.executable = filepath.Join(managedFixture.dir, name)
		compiler, err := exec.LookPath("go")
		if err != nil {
			managedFixture.err = fmt.Errorf("locate TEST Go compiler: %w", err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		build := exec.CommandContext(ctx, compiler, "test", "-c", "-ldflags=-s -w", "-o", managedFixture.executable, ".")
		build.Dir = managedFixture.cwd
		build.Env = append(managedFixture.compilerEnv, "GOFLAGS=-mod=readonly")
		if out, err := build.CombinedOutput(); err != nil {
			managedFixture.err = fmt.Errorf("compile TEST managed executable: %w: %s", err, out)
			return
		}
		info, err := os.Stat(managedFixture.executable)
		if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0) || info.Size() <= 0 || info.Size() > 32<<20 {
			managedFixture.err = fmt.Errorf("TEST executable must be regular, executable and within 32MiB: %v", err)
		}
	})
	if managedFixture.err != nil {
		t.Fatal(managedFixture.err)
	}
	return managedFixture.executable
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/777genius/agent-notifications/internal/testenv"
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
	if err := testenv.CleanupFixtures(); err != nil {
		fmt.Fprintln(os.Stderr, "remove TEST fixture compiler cache:", err)
		code = 1
	}
	os.Exit(code)
}

// Only installation fixture children need this bounded image. The parent test
// binary keeps race/coverage instrumentation; child bytes retain the same real
// package and portable TEST init dispatch, without copying that instrumentation
// or unrelated test bodies into the production managed-file payload.
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
		overlay, err := managedFixtureOverlay(ctx, compiler)
		if err != nil {
			managedFixture.err = err
			return
		}
		build := exec.CommandContext(ctx, compiler, "test", "-c", "-trimpath", "-ldflags=-s -w", "-overlay="+overlay, "-o", managedFixture.executable, ".")
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

// Build the canonical package, including C, assembly and embedded inputs, while
// replacing only unrelated test bodies in this child image. The parent suite
// still compiles and executes every real test with its normal instrumentation.
func managedFixtureOverlay(ctx context.Context, compiler string) (string, error) {
	list := exec.CommandContext(ctx, compiler, "list", "-json", ".")
	list.Dir = managedFixture.cwd
	list.Env = append(managedFixture.compilerEnv, "GOFLAGS=-mod=readonly")
	out, err := list.Output()
	if err != nil {
		return "", fmt.Errorf("locate TEST package source files: %w", err)
	}
	var source struct {
		Name                      string
		TestGoFiles, XTestGoFiles []string
	}
	if err := json.Unmarshal(out, &source); err != nil {
		return "", fmt.Errorf("decode TEST package source files: %w", err)
	}
	replacements := map[string]string{}
	for _, group := range []struct {
		name  string
		files []string
	}{{source.Name, source.TestGoFiles}, {source.Name + "_test", source.XTestGoFiles}} {
		stub := filepath.Join(managedFixture.dir, group.name+"-stub.go")
		if err := os.WriteFile(stub, []byte("package "+group.name+"\n"), 0600); err != nil {
			return "", err
		}
		for _, file := range group.files {
			if file != "managed_portable_fixture_test.go" {
				replacements[filepath.Join(managedFixture.cwd, file)] = stub
			}
		}
	}
	body, err := json.Marshal(struct{ Replace map[string]string }{replacements})
	if err != nil {
		return "", err
	}
	path := filepath.Join(managedFixture.dir, "overlay.json")
	return path, os.WriteFile(path, body, 0600)
}

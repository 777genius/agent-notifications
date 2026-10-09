package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// A test image can retain marker literals from fixtures and hide this defect.
// Inspect the actual stripped production CLI, then its readonly declaration.
func TestManagedWriterProtocolProductionDeclarations(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "writer-cli")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	compiler, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, compiler, "build", "-ldflags=-s -w", "-trimpath", "-o", binary, ".")
	build.Dir = managedFixture.cwd
	build.Env = append(managedFixture.compilerEnv, "GOFLAGS=-mod=readonly")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build owned production CLI: %v: %s", err, out)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	for _, floor := range []int{1, 2, 3, 4, 5} {
		if !installruntime.WriterCompatibleAtFloor(data, floor) {
			t.Fatalf("production CLI bytes fail supported writer floor %d", floor)
		}
	}
	if installruntime.WriterCompatibleAtFloor(data, 6) {
		t.Fatal("production CLI granted unknown writer floor")
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"internal-writer-protocol"}, "agent-notifications-managed-writer-protocol-v1\n"},
		{[]string{"internal-writer-protocol", "--all"}, "agent-notifications-managed-writer-protocol-v1\nagent-notifications-managed-writer-protocol-v3\nagent-notifications-managed-writer-protocol-v4\nagent-notifications-managed-writer-protocol-v5\n"},
	} {
		cmd := exec.CommandContext(ctx, binary, tc.args...)
		cmd.Env = managedFixture.compilerEnv
		out, err := cmd.Output()
		if err != nil || strings.ReplaceAll(string(out), "\r\n", "\n") != tc.want {
			t.Fatalf("readonly declaration %v = %q/%v", tc.args, out, err)
		}
	}
}

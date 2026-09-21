//go:build darwin

package setup

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReviewDarwinSystemAliasLostPolicyLock(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("requires actual Darwin system aliases")
	}
	o, r := fixture(t)
	physical := o.ControlRoot
	if !strings.HasPrefix(physical, "/private/var/") && !strings.HasPrefix(physical, "/private/tmp/") {
		t.Skip("fixture is not below a qualified Darwin alias")
	}
	o.ControlRoot = strings.TrimPrefix(physical, "/private")
	lock := filepath.Join(physical, "agent-notifications.json.lock")
	if e := os.Remove(lock); e != nil {
		t.Fatal(e)
	}
	result, e := Apply(contextFor(t), o, Request{ExpectedGeneration: r.ExpectedGeneration, Enabled: ptr(false)})
	_, lockErr := os.Lstat(lock)
	t.Logf("error=%v generation=%d->%d policy_lock_recreated=%v", e, r.ExpectedGeneration, result.Generation, lockErr == nil)
	if e == nil || !os.IsNotExist(lockErr) {
		t.Fatal("disable through a qualified system alias recreated a missing permanent policy lock")
	}
}

package installruntime

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/sys/unix"
)

// The provider maps only UID 0. A subprocess drops DAC bypass capabilities
// instead of weakening the inode-owner prerequisite or chowning an unmapped UID.
// Under host-native nonroot CI the same assertions run without capability edits.
func TestOrphanPermissionDenialIsConflictNotAbsence(t *testing.T) {
	if os.Geteuid() == 0 && os.Getenv("ORPHAN_DAC_CHILD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestOrphanPermissionDenialIsConflictNotAbsence$", "-test.v")
		cmd.Env = append(os.Environ(), "ORPHAN_DAC_CHILD=1")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("unprivileged DAC child: %v\n%s", err, output)
		}
		t.Log(string(output))
		return
	}
	for _, replay := range []bool{false, true} {
		t.Run(map[bool]string{false: "admission", true: "replay"}[replay], func(t *testing.T) {
			if os.Geteuid() == 0 {
				// Capabilities are per-thread. Keep fixture operations and cleanup
				// on this thread; never unlock it after removing permitted bits.
				// Go destroys the locked thread when this child subtest exits.
				runtime.LockOSThread()
				header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
				caps := [2]unix.CapUserData{}
				if err := unix.Capget(&header, &caps[0]); err != nil {
					t.Fatal(err)
				}
				mask := uint32(1<<unix.CAP_DAC_OVERRIDE | 1<<unix.CAP_DAC_READ_SEARCH)
				caps[0].Effective &^= mask
				caps[0].Permitted &^= mask
				caps[0].Inheritable &^= mask
				if err := unix.Capset(&header, &caps[0]); err != nil {
					t.Fatal(err)
				}
				if err := unix.Capget(&header, &caps[0]); err != nil {
					t.Fatal(err)
				}
				if (caps[0].Effective|caps[0].Permitted|caps[0].Inheritable)&mask != 0 {
					t.Fatal("permission fixture thread still has DAC bypass capabilities")
				}
			}
			f := newOrphanFixture(t)
			f.abandon(t, false)
			if replay {
				f.interrupt(t, "transaction")
			}
			parent := filepath.Join(f.runtime, "bin")
			if err := os.Chmod(parent, 0000); err != nil {
				t.Fatal(err)
			}
			defer os.Chmod(parent, 0700)
			if _, err := os.Open(parent); !errors.Is(err, os.ErrPermission) {
				t.Fatalf("permission fixture still has DAC bypass: %v", err)
			}
			images := orphanControlImages(t, f.control)
			var err error
			if replay {
				_, err = Recover(f.ctx, f.control)
			} else {
				_, err = RecoverOrphanConsumer(f.ctx, f.control, f.recoveryRequest())
			}
			if err == nil || !errors.Is(err, os.ErrPermission) {
				t.Fatalf("permission denial treated as absence: %v", err)
			}
			assertOrphanImages(t, images)
		})
	}
}

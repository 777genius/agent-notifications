//go:build linux || darwin

package installruntime

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestOrphanNoFollowAdmissionAndReplayRejectLinksAndSpecialFiles(t *testing.T) {
	for _, replay := range []bool{false, true} {
		for _, damage := range []string{"payload-link", "ancestor-link", "registration-link", "registration-ancestor-link", "payload-fifo", "registration-fifo"} {
			name := "admission/" + damage
			if replay {
				name = "replay/" + damage
			}
			t.Run(name, func(t *testing.T) {
				f := newOrphanFixture(t)
				f.abandon(t, true)
				if replay {
					f.interrupt(t, "transaction")
				}
				outside := filepath.Join(f.root, "outside")
				if err := os.Mkdir(outside, 0700); err != nil {
					t.Fatal(err)
				}
				foreign := filepath.Join(outside, "foreign")
				orphanWrite(t, foreign, "foreign bytes")
				switch damage {
				case "payload-link":
					if err := os.MkdirAll(filepath.Dir(f.orphanPaths[0]), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(foreign, f.orphanPaths[0]); err != nil {
						t.Fatal(err)
					}
				case "ancestor-link":
					if err := os.Symlink(outside, f.runtime); err != nil {
						t.Fatal(err)
					}
				case "registration-link":
					if err := os.Symlink(foreign, f.registration); err != nil {
						t.Fatal(err)
					}
				case "registration-ancestor-link":
					parent := filepath.Dir(f.runtime)
					if err := os.Rename(parent, parent+"-old"); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(outside, parent); err != nil {
						t.Fatal(err)
					}
				case "payload-fifo":
					path := f.orphanPaths[0]
					if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
						t.Fatal(err)
					}
					if err := unix.Mkfifo(path, 0600); err != nil {
						t.Fatal(err)
					}
				case "registration-fifo":
					if err := unix.Mkfifo(f.registration, 0600); err != nil {
						t.Fatal(err)
					}
				}
				images := orphanControlImages(t, f.control)
				var err error
				if replay {
					_, err = Recover(f.ctx, f.control)
				} else {
					_, err = RecoverOrphanConsumer(f.ctx, f.control, f.recoveryRequest())
				}
				if err == nil {
					t.Fatal("no-follow observation admitted a link/special node")
				}
				assertOrphanImages(t, images)
				got, err := os.ReadFile(foreign)
				if err != nil || string(got) != "foreign bytes" {
					t.Fatal("followed link into foreign namespace", err)
				}
			})
		}
	}
}

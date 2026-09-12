//go:build linux

package journal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlatformClockInjectedBootFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "boot_id")
	c := PlatformClock{BootIDPath: p}
	if c.Sample().Available {
		t.Fatal("missing boot identity")
	}
	if e := os.WriteFile(p, []byte("test-boot\n"), 0600); e != nil {
		t.Fatal(e)
	}
	a, b := c.Sample(), c.Sample()
	if !a.Available || a.Boot != "test-boot" || b.Seconds < a.Seconds {
		t.Fatal(a, b)
	}
	if (PlatformClock{}).Sample().Available {
		t.Fatal("implicit filesystem root")
	}
}

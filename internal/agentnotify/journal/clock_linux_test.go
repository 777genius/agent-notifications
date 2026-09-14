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
	if _, _, _, ok := LinuxBootSample(""); ok {
		t.Fatal("empty path")
	}
}

func TestDefaultClockUsesTrustedBootID(t *testing.T) {
	c, ok := DefaultClock().(PlatformClock)
	if !ok || c.BootIDPath != TrustedBootIDPath {
		t.Fatal(c, ok)
	}
	a, b := c.Sample(), c.Sample()
	if !a.Available || a.Boot == "" || b.Boot != a.Boot || b.Seconds < a.Seconds {
		t.Fatal(a, b)
	}
}

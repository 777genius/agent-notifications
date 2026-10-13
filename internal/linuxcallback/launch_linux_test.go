//go:build linux

package linuxcallback

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Red if the actual process receives another argv shape/platform, loses the
// click token, or inherits loader/Electron injection. The only executable is a
// new private TEST helper; no vendor client or user project is launched.
func TestSelectedLauncherProcessContract(t *testing.T) {
	root := t.TempDir()
	launcher := filepath.Join(root, "TEST-launcher")
	receipt := filepath.Join(root, "TEST-receipt")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	script := "#!/bin/sh\nset -eu\numask 077\n{\n" +
		"printf '%s\\000' \"$#\"\n" +
		"for argument in \"$@\"; do printf '%s\\000' \"$argument\"; done\n" +
		"printf '%s\\000' \"$XDG_ACTIVATION_TOKEN\" \"$DESKTOP_STARTUP_ID\" \"${LD_PRELOAD-unset}\" \"${NODE_OPTIONS-unset}\" \"${ELECTRON_OZONE_PLATFORM_HINT-unset}\" \"$PATH\" \"$PWD\"\n" +
		"} > " + quote(receipt+".pending") + "\n/bin/mv " + quote(receipt+".pending") + " " + quote(receipt) + "\n"
	if err := os.WriteFile(launcher, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LD_PRELOAD", "TEST-must-not-load.so")
	t.Setenv("NODE_OPTIONS", "--require /TEST-must-not-load.js")
	t.Setenv("ELECTRON_OZONE_PLATFORM_HINT", "x11")
	t.Setenv("XDG_ACTIVATION_TOKEN", "TEST-stale-token")
	t.Setenv("DESKTOP_STARTUP_ID", "TEST-stale-startup")
	uri, err := ThreadURI("TEST/opaque id%?#")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err = launch(ctx, Snapshot{Launcher: launcher, Executable: filepath.Join(root, "TEST-selected-executable")}, uri, "TEST-native-click-token"); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		raw, err = os.ReadFile(receipt)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("TEST helper receipt missing", ctx.Err())
		case <-ticker.C:
		}
	}
	fields := strings.Split(string(raw), "\x00")
	want := []string{"2", "--ozone-platform=wayland", "codex://threads/TEST%2Fopaque%20id%25%3F%23", "TEST-native-click-token", "TEST-native-click-token", "unset", "unset", "unset", "/usr/bin:/bin", root, ""}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("selected process contract: got %q, want %q", fields, want)
	}
}

package codexcommand

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFrozenHookCommandBytesWithSpacesAndApostrophes(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "test o'brien space", InstallDirName)
	var expected []string
	for _, event := range []string{"PreToolUse", "Stop", "SubagentStop", "PermissionRequest"} {
		// Frozen production quoting, independently spelled out to detect drift
		// in trust-hashed bytes rather than comparing two delegates.
		launcher := filepath.ToSlash(filepath.Join(root, "bin", "codex-hook-wrapper.sh"))
		p := fmt.Sprintf("sh '%s' handle-hook %s --product codex", strings.ReplaceAll(launcher, "'", `'\''`), event)
		w := fmt.Sprintf(`cmd.exe /d /v:off /s /c ""%s" handle-hook %s --product codex"`, filepath.FromSlash(filepath.Join(root, "bin", "codex-hook-wrapper.cmd")), event)
		posix, windows := HookCommands(root, event)
		if posix != p || windows != w {
			t.Fatalf("trust hash command drift: %q / %q", posix, windows)
		}
		expected = append(expected, p, w)
	}
	if !reflect.DeepEqual(Commands(root), expected) {
		t.Fatal("registration set/order drift")
	}
	events := Events()
	events[0] = "mutation"
	if Events()[0] != "PreToolUse" {
		t.Fatal("caller mutated event contract")
	}
}

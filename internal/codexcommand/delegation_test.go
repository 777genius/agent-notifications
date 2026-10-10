package codexcommand_test

import (
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/777genius/agent-notifications/internal/codexcommand"
	"github.com/777genius/agent-notifications/internal/codexsetup"
)

func TestCodecDelegationPreservesSetupBytesAndEventContract(t *testing.T) {
	events := codexcommand.Events()
	sorted := append([]string(nil), events...)
	sort.Strings(sorted)
	if !reflect.DeepEqual(sorted, codexsetup.SortedEvents()) {
		t.Fatal("setup and recovery event contracts diverged")
	}
	for _, event := range events {
		root := filepath.Join(string(filepath.Separator), "o'brien space", codexsetup.InstallDirName)
		p, w := codexsetup.HookCommands(root, event)
		cp, cw := codexcommand.HookCommands(root, event)
		if p != cp || w != cw {
			t.Fatal("setup delegation changed trust-hashed command bytes")
		}
	}
}

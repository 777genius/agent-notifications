//go:build linux || darwin

package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestPersistedIdentitySetupOwnership(t *testing.T) {
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rootPath := filepath.Join(path, "control")
	if err = os.Mkdir(rootPath, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := openRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	state, err := mkdir(root, "state")
	if err != nil {
		t.Fatal(err)
	}
	id, err := directoryID(state)
	if err != nil {
		t.Fatal(err)
	}
	_, ino, _ := strings.Cut(id, ":")
	stored := "999999:" + ino
	if strings.HasPrefix(id, "999999:") {
		stored = "888888:" + ino
	}
	err = matches(state, root, stored)
	if (err == nil) != (runtime.GOOS == "darwin") {
		t.Fatalf("%s persisted setup identity: %v", runtime.GOOS, err)
	}
	if err = matches(state, root, id); err != nil {
		t.Fatal(err)
	}
	// Build only the structural evidence consumed by checkProvisioned. No
	// notification delivery or user namespace is involved.
	for _, name := range []string{"journal", "native-spool"} {
		child, err := mkdir(state, name)
		if err != nil {
			t.Fatal(err)
		}
		if name == "native-spool" {
			if err = createLock(child); err != nil {
				t.Fatal(err)
			}
		}
		if err = child.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"namespace", "journal.json", "lock"} {
		data := []byte("fixture")
		if name == "namespace" {
			data = []byte("namespace\n")
		}
		if err = os.WriteFile(filepath.Join(rootPath, "state", "journal", name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	owner, _ := json.Marshal(ownership{Phase: "ready", DirectoryID: stored, Namespace: "namespace"})
	snapshot := installruntime.PolicySnapshot{Fields: map[string]json.RawMessage{"setupState": owner}}
	err = checkProvisioned(Options{ControlRoot: rootPath}, snapshot)
	if (err == nil) != (runtime.GOOS == "darwin") {
		t.Fatalf("%s ready marker: %v", runtime.GOOS, err)
	}
	if err = state.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(filepath.Join(rootPath, "state"), filepath.Join(rootPath, "saved")); err != nil {
		t.Fatal(err)
	}
	replacement, err := mkdir(root, "state")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = replacement.Close() }()
	if matches(replacement, root, stored) == nil {
		t.Fatal("replacement setup state accepted")
	}
}

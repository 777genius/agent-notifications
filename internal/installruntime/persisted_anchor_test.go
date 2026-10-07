//go:build linux || darwin

package installruntime

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPersistedIdentityFileRecoveryAnchors(t *testing.T) {
	ctx, r := request(t)
	target := filepath.Join(r.RuntimeRoot, "nested", "config")
	r.Files = []File{{Path: target, Data: []byte("fixture publication"), Mode: 0600}}
	r.Fault = func(phase string) error {
		if phase == "transaction" {
			return fmt.Errorf("fixture crash")
		}
		return nil
	}
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("journal fault missing")
	}
	marker := filepath.Join(r.ControlRoot, "transaction.json")
	tx, err := readTransactionFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	mapping := map[string]string{}
	for i := range tx.Files {
		for j := range tx.Files[i].Parents {
			a := &tx.Files[i].Parents[j]
			dev, ino, ok := splitObjectID(a.Identity)
			if !ok {
				t.Fatal("not a Unix identity", a.Identity)
			}
			if mapping[dev] == "" {
				mapping[dev] = fmt.Sprint(90000000 + len(mapping))
			}
			a.Identity = mapping[dev] + ":" + ino
		}
	}
	if err = writeTransaction(marker, tx); err != nil {
		t.Fatal(err)
	}
	journal, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	r.Fault = nil
	r.Files = nil
	r.RecoverOnly = true
	_, err = Commit(ctx, r)
	if runtime.GOOS == "darwin" {
		if err != nil {
			t.Fatal("persisted device renumber recovery rejected", err)
		}
		got, err := os.ReadFile(target)
		if err != nil || string(got) != "fixture publication" {
			t.Fatal("publication not recovered", err)
		}
		if _, err = os.Stat(marker); !os.IsNotExist(err) {
			t.Fatal("journal remains", err)
		}
	} else {
		if err == nil {
			t.Fatal("Linux relaxed persisted identity")
		}
		if _, err = os.Stat(target); !os.IsNotExist(err) {
			t.Fatal("rejected recovery published a file", err)
		}
		got, err := os.ReadFile(marker)
		if err != nil || !bytes.Equal(got, journal) {
			t.Fatal("refusal rewrote journal", err)
		}
	}
}

func TestPersistedIdentityObservedDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "generation.app")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	id, err := nativeDirectoryID(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = checkNativeDirectoryID(path, id); err != nil {
		t.Fatal(err)
	}
	dev, ino, ok := splitObjectID(id)
	if !ok {
		t.Fatal(id)
	}
	replacementDev := "999999"
	if dev == replacementDev {
		replacementDev = "888888"
	}
	err = checkNativeDirectoryID(path, replacementDev+":"+ino)
	if (err == nil) != (runtime.GOOS == "darwin") {
		t.Fatalf("%s observed identity policy: %v", runtime.GOOS, err)
	}
	// Same bytes cannot rescue an inode replacement on either platform.
	saved := filepath.Join(filepath.Dir(path), "saved.app")
	if err = os.Rename(path, saved); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if checkNativeDirectoryID(path, replacementDev+":"+ino) == nil {
		t.Fatal("replacement directory accepted")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(saved, path); err != nil {
		t.Fatal(err)
	}
	if checkNativeDirectoryID(path, replacementDev+":"+ino) == nil {
		t.Fatal("symlinked generation accepted")
	}
}

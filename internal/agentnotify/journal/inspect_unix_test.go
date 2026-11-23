//go:build linux || darwin

package journal

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestInspectReadOnlyValidatesCanonicalSnapshotWithoutLock(t *testing.T) {
	s, clock := fixture(t, Limits{})
	opts := Options{Root: s.root, Clock: clock, Limits: s.limits}
	journalPath := filepath.Join(s.root, "journal.json")
	original, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	// Exclusive lock held by a different fd: preflight's independent
	// canonical snapshot read must not try to take this journal lock.
	lock, err := os.OpenFile(filepath.Join(s.root, "lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Flock(int(lock.Fd()), unix.LOCK_UN) }()
	namespace, err := InspectReadOnly(testContext(t), opts)
	if err != nil || namespace != s.Namespace() {
		t.Fatalf("read-only journal inspect: namespace=%q err=%v", namespace, err)
	}
	after, err := os.ReadFile(journalPath)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("read-only journal check changed snapshot", err)
	}
}

func TestInspectReadOnlyRejectsCorruptOrNonCanonicalJournal(t *testing.T) {
	for _, mutation := range []struct {
		name   string
		change func([]byte) []byte
	}{
		{"corrupt json", func([]byte) []byte { return []byte("{bad\n") }},
		{"noncanonical json key order", func(data []byte) []byte {
			var keys map[string]json.RawMessage
			if err := json.Unmarshal(data, &keys); err != nil {
				panic(err)
			}
			// Encoding a map sorts keys alphabetically, unlike the defined
			// journal disk struct. It remains valid JSON but changes canonical
			// field order, which journal.Open must reject identically.
			out, err := json.Marshal(keys)
			if err != nil {
				panic(err)
			}
			return out
		}},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			s, clock := fixture(t, Limits{})
			path := filepath.Join(s.root, "journal.json")
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			damaged := mutation.change(original)
			if err := os.WriteFile(path, damaged, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := InspectReadOnly(testContext(t), Options{Root: s.root, Clock: clock}); err == nil {
				t.Fatal("corrupt or noncanonical journal was accepted")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(damaged, after) {
				t.Fatal("inspector modified invalid journal", err)
			}
		})
	}
}

func TestInspectReadOnlyNeverAdoptsHardlinkedSnapshotAcrossReplacement(t *testing.T) {
	s, clock := fixture(t, Limits{})
	path := filepath.Join(s.root, "journal.json")
	linked := path + ".hardlink"
	if err := os.Link(path, linked); err != nil {
		t.Fatal(err)
	}
	opts := Options{Root: s.root, Clock: clock}
	if _, err := InspectReadOnly(testContext(t), opts); err == nil {
		t.Fatal("hardlinked journal passed read-only admission")
	}
	if err := os.Remove(linked); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	next := filepath.Join(s.root, "replacement-snapshot.json")
	if err := os.WriteFile(next, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, path); err != nil {
		t.Fatal(err)
	}
	namespace, err := InspectReadOnly(testContext(t), opts)
	if err != nil || namespace != s.Namespace() {
		t.Fatalf("valid atomically replaced snapshot rejected: %q %v", namespace, err)
	}
}

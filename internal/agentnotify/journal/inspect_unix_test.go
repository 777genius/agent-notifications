//go:build linux || darwin

package journal

import (
	"bytes"
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
		name string
		change func([]byte) []byte
	}{
		{"corrupt json", func([]byte) []byte { return []byte("{bad\n") }},
		{"noncanonical json", func(data []byte) []byte { return append(append([]byte{}, data...), ' ') }},
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

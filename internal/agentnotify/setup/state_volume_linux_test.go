//go:build linux

package setup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenumberedOnVolumeRequiresRecordedSameVolumeAndInode(t *testing.T) {
	const vol = "btrfs:ad316eced7cc2670"
	for _, tc := range []struct {
		name, want, current, volume, currentVolume string
		ok                                         bool
	}{
		{"renumbered same volume", "56:43346541", "64:43346541", vol, vol, true},
		{"other subvolume or snapshot", "56:43346541", "64:43346541", vol, "btrfs:ad316eced7cc2677", false},
		{"legacy marker without volume", "56:43346541", "64:43346541", "", vol, false},
		{"not btrfs now", "56:43346541", "64:43346541", vol, "", false},
		{"replaced inode", "56:43346541", "64:43346542", vol, vol, false},
		{"malformed stored", "56:x", "64:43346541", vol, vol, false},
		{"extra colon", "56:1:43346541", "64:43346541", vol, vol, false},
		{"signed dev", "-56:43346541", "64:43346541", vol, vol, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := renumberedOnVolume(tc.want, tc.current, tc.volume, tc.currentVolume); got != tc.ok {
				t.Fatalf("renumberedOnVolume=%v want %v", got, tc.ok)
			}
		})
	}
}

func TestBtrfsVolumeIDFormat(t *testing.T) {
	var fsid [16]byte
	for i := range fsid {
		fsid[i] = byte(i)
	}
	if got, want := btrfsVolumeID(fsid, 257), "btrfs:000102030405060708090a0b0c0d0e0f:257"; got != want {
		t.Fatalf("btrfsVolumeID=%q want %q", got, want)
	}
	if btrfsVolumeID(fsid, 258) == btrfsVolumeID(fsid, 257) {
		t.Fatal("different Btrfs subvolume IDs must not share durable identity")
	}
	other := fsid
	other[0] ^= 0xff
	if btrfsVolumeID(other, 257) == btrfsVolumeID(fsid, 257) {
		t.Fatal("different Btrfs filesystem UUIDs must not share durable identity")
	}
}

// Needs a btrfs TMPDIR (e.g. TMPDIR=$HOME/.cache/go-btrfs-tmp); skipped elsewhere.
func TestSetupStateSurvivesBtrfsRenumberOnlyOnRecordedVolume(t *testing.T) {
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
	defer func() { _ = state.Close() }()
	volume, err := volumeID(state)
	if err != nil {
		t.Fatal(err)
	}
	if volume == "" {
		t.Skip("TMPDIR is not on btrfs")
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
	if err = matches(state, root, stored, volume); err != nil {
		t.Fatalf("renumbered device on recorded volume refused: %v", err)
	}
	if matches(state, root, stored, "") == nil {
		t.Fatal("legacy marker accepted a renumbered device")
	}
	if matches(state, root, stored, "btrfs:0000000000000000") == nil {
		t.Fatal("other volume accepted")
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
	if matches(replacement, root, stored, volume) == nil {
		t.Fatal("replacement inode accepted on recorded volume")
	}
}

// The #693 review case: a snapshot keeps inode numbers and only changes st_dev.
// Needs a btrfs TMPDIR and the btrfs CLI (unprivileged subvolume/snapshot).
func TestSetupStateRefusesBtrfsSnapshotWithSameInode(t *testing.T) {
	btrfs, err := exec.LookPath("btrfs")
	if err != nil {
		t.Skip("btrfs CLI not installed")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	original, snapshot := filepath.Join(base, "sub"), filepath.Join(base, "snap")
	if out, err := exec.Command(btrfs, "subvolume", "create", original).CombinedOutput(); err != nil {
		t.Skipf("cannot create btrfs subvolume here: %v %s", err, out)
	}
	if err = os.MkdirAll(filepath.Join(original, "control", "state"), 0700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(btrfs, "subvolume", "snapshot", original, snapshot).CombinedOutput(); err != nil {
		t.Skipf("cannot snapshot btrfs subvolume here: %v %s", err, out)
	}
	observe := func(dir string) (root, state *os.File, id, volume string) {
		t.Helper()
		root, err := openRoot(filepath.Join(dir, "control"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = root.Close() })
		state, err = openChild(root, "state")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = state.Close() })
		if id, err = directoryID(state); err != nil {
			t.Fatal(err)
		}
		if volume, err = volumeID(state); err != nil {
			t.Fatal(err)
		}
		return root, state, id, volume
	}
	_, _, storedID, storedVolume := observe(original)
	root, state, snapID, snapVolume := observe(snapshot)
	if wantIno, _ := objectInode(storedID); wantIno == "" {
		t.Fatalf("bad stored id %q", storedID)
	} else if snapIno, _ := objectInode(snapID); snapIno != wantIno || snapID == storedID {
		t.Skipf("snapshot did not reproduce same inode/new device: %s vs %s", storedID, snapID)
	}
	if storedVolume == "" || storedVolume == snapVolume {
		t.Fatalf("volume ids do not separate subvolumes: %q %q", storedVolume, snapVolume)
	}
	if matches(state, root, storedID, storedVolume) == nil {
		t.Fatal("snapshot with the same inode accepted as the recorded setup state")
	}
}

//go:build linux

package setup

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestRenumberedOnVolumeRequiresRecordedSameVolumeAndInode(t *testing.T) {
	const vol = "btrfs-subvol:000102030405060708090a0b0c0d0e0f:257"
	for _, tc := range []struct {
		name, want, current, volume, currentVolume string
		ok                                         bool
	}{
		{"renumbered same volume", "56:43346541", "64:43346541", vol, vol, true},
		{"other subvolume or snapshot", "56:43346541", "64:43346541", vol, "btrfs-subvol:000102030405060708090a0b0c0d0e0f:258", false},
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
	var uuid [16]byte
	for i := range uuid {
		uuid[i] = byte(i)
	}
	if got, want := btrfsVolumeID(uuid, 257), "btrfs-subvol:000102030405060708090a0b0c0d0e0f:257"; got != want {
		t.Fatalf("btrfsVolumeID=%q want %q", got, want)
	}
	if btrfsVolumeID(uuid, 258) == btrfsVolumeID(uuid, 257) {
		t.Fatal("different Btrfs subvolume IDs must not share durable identity")
	}
	other := uuid
	other[0] ^= 0xff
	if btrfsVolumeID(other, 257) == btrfsVolumeID(uuid, 257) {
		t.Fatal("different Btrfs subvolume UUIDs must not share durable identity")
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
	if err := matches(state, root, id, volume); err != nil {
		t.Fatalf("same directory and recorded volume refused: %v", err)
	}
	if matches(state, root, id, "btrfs-subvol:00000000000000000000000000000000:0") == nil {
		t.Fatal("matching device and inode accepted a mismatched recorded volume")
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
	if err := matches(state, root, id, ""); err != nil {
		t.Fatalf("legacy exact device/inode marker should remain valid: %v", err)
	}
	if matches(state, root, stored, "btrfs-subvol:00000000000000000000000000000000:0") == nil {
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
	// TempDir's RemoveAll cannot rmdir a Btrfs subvolume. Register explicit
	// deletion now; snapshots are registered later and therefore deleted first.
	t.Cleanup(func() {
		if out, err := exec.Command(btrfs, "subvolume", "delete", original).CombinedOutput(); err != nil {
			t.Errorf("delete test Btrfs subvolume: %v %s", err, out)
		}
	})
	if err = os.MkdirAll(filepath.Join(original, "control", "state"), 0700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(btrfs, "subvolume", "snapshot", original, snapshot).CombinedOutput(); err != nil {
		t.Skipf("cannot snapshot btrfs subvolume here: %v %s", err, out)
	}
	t.Cleanup(func() {
		if out, err := exec.Command(btrfs, "subvolume", "delete", snapshot).CombinedOutput(); err != nil {
			t.Errorf("delete test Btrfs snapshot: %v %s", err, out)
		}
	})
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
	// A recycled device number could also make the legacy dev:ino pair
	// look exact. Require recorded volume evidence independently of that pair.
	if matches(state, root, snapID, storedVolume) == nil {
		t.Fatal("snapshot accepted despite mismatched recorded volume")
	}
	if matches(state, root, storedID, "") == nil {
		t.Fatal("snapshot accepted using the original legacy device/inode marker after renumbering")
	}
}

func TestSetupApplyPersistsBtrfsSubvolumeIdentityForPreflight(t *testing.T) {
	o, request := linuxNoneFixture(t)
	root, err := openRoot(o.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	parentVolume, err := volumeID(root)
	if err != nil {
		t.Fatal(err)
	}
	if parentVolume == "" {
		t.Skip("requires a Btrfs child subvolume with a persistent UUID")
	}

	ctx := contextFor(t)
	applied, err := Apply(ctx, o, request)
	if err != nil || !applied.Enabled || applied.Namespace == "" {
		t.Fatalf("production setup.Apply: result=%+v err=%v", applied, err)
	}
	policy, err := installruntime.ReadPolicySnapshot(ctx, o.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := policy.Fields["setupState"]
	if !ok {
		t.Fatal("setup.Apply did not persist setupState ownership")
	}
	var owner ownership
	if err := json.Unmarshal(raw, &owner); err != nil {
		t.Fatal(err)
	}
	state, err := openChild(root, "state")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.Close() }()
	currentVolume, err := volumeID(state)
	if err != nil {
		t.Fatal(err)
	}
	if currentVolume == "" || owner.Volume != currentVolume || currentVolume != parentVolume {
		t.Fatalf("persisted Btrfs subvolume evidence differs: marker=%q current=%q parent=%q", owner.Volume, currentVolume, parentVolume)
	}
	if err := matches(state, root, owner.DirectoryID, owner.Volume); err != nil {
		t.Fatalf("exact preflight identity refused after Apply: %v", err)
	}

	// A reboot may change st_dev while preserving the same directory inode and
	// subvolume UUID. Exercise that exact persisted-marker path without needing
	// privileged device remounting inside the hosted runner.
	_, inode, ok := strings.Cut(owner.DirectoryID, ":")
	if !ok || inode == "" {
		t.Fatalf("invalid stored directory identity %q", owner.DirectoryID)
	}
	renumbered := "999999:" + inode
	if err := inspectPersistedStateIdentity(o.ControlRoot, "state", renumbered, owner.Volume); err != nil {
		t.Fatalf("production read-only preflight refused the same Btrfs subvolume after device renumber: %v", err)
	}
	if err := inspectPersistedStateIdentity(o.ControlRoot, "state", renumbered, owner.Volume+":other"); err == nil {
		t.Fatal("preflight accepted a different recorded Btrfs subvolume")
	}

	// Re-run the ordinary preflight using the newly committed generation and
	// the exact canonical configuration bytes; this is the same validation
	// that /init performs before the activation stage.
	request.ExpectedGeneration = applied.Generation
	prepared, err := os.ReadFile(o.GlobalConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := Inspect(ctx, o, request, prepared); err != nil {
		t.Fatalf("production read-only setup preflight after Apply: %v", err)
	}
}

package installruntime

import (
	"bytes"
	"strings"
	"testing"
)

func TestPreviousNativeGenerationCannotInheritActiveDecoderFloor(t *testing.T) {
	record := NativeRecord{
		Path: "/controlled/active.app", SHA256: "active-digest", DecoderFloor: 1,
		PreviousPath: "/controlled/old.app", PreviousSHA256: "old-digest", PreviousDirectoryID: "old-dir",
	}
	active, previous := nativeGenerationByDigest(record, "active-digest"), nativeGenerationByDigest(record, "old-digest")
	if active == nil || active.DecoderFloor != 1 || previous == nil || previous.DecoderFloor != 0 {
		t.Fatalf("unattested prior generation inherited floor: active=%+v previous=%+v", active, previous)
	}
}

func TestReuseNativeGenerationKeepsFreshAttestation(t *testing.T) {
	before := NativeRecord{Published: []NativeGeneration{
		{Path: "/controlled/old.app", DirectoryID: "old-dir", SHA256: "same-digest"},
	}}
	evidence := []byte("TEST-attestation")
	fresh := NativeRecord{Path: "/controlled/new.app", DirectoryID: "new-dir", SHA256: "same-digest", DecoderFloor: 1,
		Attestation: evidence, InstalledTreeSHA256: installedNativeHash("same-digest", evidence)}
	got := publishNativeRecord(before, fresh)
	if got.Path != "/controlled/old.app" || !bytes.Equal(got.Attestation, evidence) ||
		got.InstalledTreeSHA256 != installedNativeHash(got.SHA256, evidence) {
		t.Fatalf("fresh verified attestation lost on reuse: %+v", got)
	}
}

func TestPurgeRefusesMismatchedObservedAndRemovedPaths(t *testing.T) {
	change := &NativeChange{Purge: true,
		Before: NativeRecord{Path: "/controlled/live.app", SHA256: "digest"},
		After: NativeRecord{Path: "/controlled/other.app", SHA256: "digest"}}
	if err := validateNative(change); err == nil || !strings.Contains(err.Error(), "purge paths must match") {
		t.Fatalf("purge admitted different observed and removed paths: %v", err)
	}
}

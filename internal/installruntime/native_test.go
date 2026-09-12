package installruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func skipUnsupportedNative(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("native bundle promotion requires a supported native platform")
	}
}

func nativeBundle(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "ClaudeNotifier.app")
	path := filepath.Join(root, "Contents", "MacOS", "terminal-notifier-modern")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	// This marker would be created if the legacy helper were blindly probed.
	if err := os.WriteFile(path, []byte("#!/bin/sh\ntouch '"+filepath.Join(root, "PROBED")+"'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	return root
}

func nativeFixture(t *testing.T) string {
	t.Helper()
	skipUnsupportedNative(t)
	return nativeBundle(t)
}
func TestUnknownNativeNeverProbed(t *testing.T) {
	ctx, r := request(t)
	source := nativeFixture(t)
	change, err := StageNative(ctx, r.ControlRoot, source)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(change.Staged)
	if !strings.HasPrefix(name, ".candidate-") || !strings.HasSuffix(name, ".app") || filepath.Dir(change.Staged) != filepath.Dir(change.After.Path) {
		t.Fatalf("candidate must be a private app sibling: %s", change.Staged)
	}
	if random, err := hex.DecodeString(strings.TrimSuffix(strings.TrimPrefix(name, ".candidate-"), ".app")); err != nil || len(random) != 16 {
		t.Fatalf("candidate random identity: %s", name)
	}
	if change.After.DecoderFloor != 0 {
		t.Fatal("invented old native floor")
	}
	if _, err := os.Stat(filepath.Join(source, "PROBED")); !os.IsNotExist(err) {
		t.Fatal("legacy helper executed")
	}
	if _, err := os.Stat(filepath.Join(change.Staged, "PROBED")); !os.IsNotExist(err) {
		t.Fatal("staged legacy helper executed")
	}
}

func TestNativeAliasRetargetsStableHookName(t *testing.T) {
	ctx, r := request(t)
	if err := os.MkdirAll(r.RuntimeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	first, err := StageNative(ctx, r.ControlRoot, nativeFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	aliases, err := NativeAlias(first, r.RuntimeRoot)
	if err != nil || len(aliases) != 1 {
		t.Fatalf("alias: %v %+v", err, aliases)
	}
	if filepath.Base(aliases[0].Path) != "ClaudeNotifier.app" || aliases[0].Link != first.After.Path {
		t.Fatalf("stable alias: %+v", aliases[0])
	}
	r.Native = first
	r.Files = aliases
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	second, err := StageNative(ctx, r.ControlRoot, nativeFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if second.After.Path == first.After.Path {
		t.Fatal("update reused callback identity")
	}
	aliases, err = NativeAlias(second, r.RuntimeRoot)
	if err != nil || len(aliases) != 1 {
		t.Fatalf("retarget: %v %+v", err, aliases)
	}
	if aliases[0].Link != second.After.Path || filepath.Base(aliases[0].Path) != "ClaudeNotifier.app" {
		t.Fatalf("retargeted alias: %+v", aliases[0])
	}
	r.Native = second
	r.Files = aliases
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(filepath.Join(r.RuntimeRoot, "ClaudeNotifier.app"))
	if err != nil || got != second.After.Path {
		t.Fatalf("hook alias %s %v", got, err)
	}
	if _, err := os.Stat(first.After.Path); err != nil {
		t.Fatal("generation A disappeared when alias retargeted")
	}
}
func TestNativeRetentionAndExplicitPurge(t *testing.T) {
	ctx, r := request(t)
	change, err := StageNative(ctx, r.ControlRoot, nativeFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	r.Native = change
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	r.Native = nil
	r.RemoveConsumer = true
	retained, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if retained.Native == nil {
		t.Fatal("ordinary uninstall dropped callback record")
	}
	if _, err := os.Stat(change.After.Path); err != nil {
		t.Fatal("ordinary uninstall dropped callback")
	}
	r.PurgeNative = true
	purged, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if purged.Native != nil {
		t.Fatal("purge retained active callback record")
	}
	if _, err := os.Stat(change.After.Path); !os.IsNotExist(err) {
		t.Fatal("purge retained callback entrypoint")
	}
}
func TestDecoderDowngradeRefused(t *testing.T) {
	ctx, r := request(t)
	change, err := StageNative(ctx, r.ControlRoot, nativeFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	// Fixture models a previously qualified managed decoder floor.
	change.After.DecoderFloor = 1
	r.Native = change
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	lower, err := StageNative(ctx, r.ControlRoot, nativeFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	r.Native = lower
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("decoder downgrade accepted")
	}
}
func TestUnknownManifestRefusedBeforeProbe(t *testing.T) {
	source := nativeBundle(t)
	path := filepath.Join(source, "Contents", "Resources", "managed-runtime.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"SchemaVersion":999}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyNative(context.Background(), source, source+".managed-runtime.json"); err == nil {
		t.Fatal("unknown manifest accepted")
	}
	if _, err := os.Stat(filepath.Join(source, "PROBED")); !os.IsNotExist(err) {
		t.Fatal("unknown helper executed")
	}
}
func TestUnsupportedNativeSwapPreservesBoth(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("actual swap tested by Darwin filesystem gate")
	}
	old, new := nativeBundle(t), nativeBundle(t)
	before, err := treeFingerprint(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := swapBundles(new, old); err == nil {
		t.Fatal("unsupported native exchange accepted")
	}
	after, err := treeFingerprint(old)
	if err != nil || after != before {
		t.Fatal("old callback changed")
	}
	if _, err := os.Stat(new); err != nil {
		t.Fatal("candidate lost")
	}
}

func TestManagedNativeCapabilityFloor(t *testing.T) {
	const valid = `{"schemaVersion":1,"protocolVersions":[1],"actionKinds":["none"],"receiptSupport":true,"backend":"macos.usernotifications","explicitFeatureEnabledByDefault":false}`
	for _, tc := range []struct {
		name, data string
		ok         bool
	}{
		{"PR1", valid, true},
		{"future action", strings.Replace(valid, `["none"]`, `["none","desktop_thread_v1"]`, 1), true},
		{"future protocol", strings.Replace(valid, `[1]`, `[1,2]`, 1), true},
		{"missing backend", strings.Replace(valid, `"backend":"macos.usernotifications",`, "", 1), false},
		{"wrong backend", strings.Replace(valid, "macos.usernotifications", "other", 1), false},
		{"missing disabled default", strings.Replace(valid, `,"explicitFeatureEnabledByDefault":false`, "", 1), false},
		{"duplicate field", strings.Replace(valid, `"schemaVersion":1`, `"schemaVersion":1,"schemaVersion":1`, 1), false},
		{"duplicate action", strings.Replace(valid, `["none"]`, `["none","none"]`, 1), false},
		{"no none", strings.Replace(valid, `"none"`, `"desktop_thread_v1"`, 1), false},
		{"no receipts", strings.Replace(valid, `"receiptSupport":true`, `"receiptSupport":false`, 1), false},
		{"malformed", `{`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateNativeCapabilities([]byte(tc.data)); (err == nil) != tc.ok {
				t.Fatalf("accepted=%v: %v", err == nil, err)
			}
		})
	}
}

func TestSelfAssertedManifestWithoutFingerprintNeverProbed(t *testing.T) {
	source := nativeBundle(t)
	path := filepath.Join(source, "Contents", "Resources", "managed-runtime.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"SchemaVersion":1,"ProtocolVersion":1,"DecoderFloor":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if floor, err := verifyNative(context.Background(), source, source+".managed-runtime.json"); err != nil || floor != 0 {
		t.Fatalf("expected unsupported offline floor: %d %v", floor, err)
	}
	if err := os.WriteFile(source+".managed-runtime.json", []byte(`{"SchemaVersion":1,"ProtocolVersion":1,"DecoderFloor":1,"ExecutableSHA256":"wrong"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyNative(context.Background(), source, source+".managed-runtime.json"); err == nil || !strings.Contains(err.Error(), "fingerprint mismatch") {
		t.Fatalf("expected offline fingerprint refusal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(source, "PROBED")); !os.IsNotExist(err) {
		t.Fatal("unverified helper executed")
	}
}

func TestCapabilityProbeDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX direct process fixture")
	}
	script := filepath.Join(t.TempDir(), "probe")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 10\n"), 0755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := boundedCommand(context.Background(), script); err == nil {
		t.Fatal("unbounded helper accepted")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("probe deadline exceeded: %s", elapsed)
	}
}

func TestNativeRecoveryAtPromotionBoundary(t *testing.T) {
	ctx, r := request(t)
	change, err := StageNative(ctx, r.ControlRoot, nativeFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	r.Native = change
	r.Fault = func(phase string) error {
		if phase == "native" {
			return fmt.Errorf("crash")
		}
		return nil
	}
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("native fault not reached")
	}
	if got, err := treeFingerprint(change.After.Path); err != nil || got != change.After.SHA256 {
		t.Fatalf("callback missing in crash state: %v", err)
	}
	r.Native = nil
	r.Fault = nil
	repaired, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.Native == nil || repaired.Native.SHA256 != change.After.SHA256 {
		t.Fatal("lost recovered callback owner")
	}
	again, err := Commit(ctx, r)
	if err != nil || again.ID != repaired.ID {
		t.Fatalf("repeat repair: %v", err)
	}
}

func TestRetainedSelfAssertedManifestNeverProbed(t *testing.T) {
	ctx, r := request(t)
	source := nativeFixture(t)
	path := filepath.Join(source, "Contents", "Resources", "managed-runtime.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	// Even an otherwise recognized manifest in an existing bin is not provenance.
	if err := os.WriteFile(path, []byte(`{"SchemaVersion":1,"ProtocolVersion":1,"DecoderFloor":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	change, err := StageRetainedNative(ctx, r.ControlRoot, source)
	if err != nil {
		t.Fatal(err)
	}
	if change.After.DecoderFloor != 0 {
		t.Fatal("unmanaged source gained trust")
	}
	for _, p := range []string{source, change.Staged} {
		if _, err := os.Stat(filepath.Join(p, "PROBED")); !os.IsNotExist(err) {
			t.Fatal("retained helper executed")
		}
	}
}

func TestCandidateCleanupPreservesRecoveryInput(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(fmt.Sprint(pending), func(t *testing.T) {
			ctx, r := request(t)
			change, err := StageNative(ctx, r.ControlRoot, nativeFixture(t))
			if err != nil {
				t.Fatal(err)
			}
			if pending {
				r.Native = change
				r.Fault = func(string) error { return fmt.Errorf("crash") }
				if _, err := Commit(ctx, r); err == nil {
					t.Fatal("fault not reached")
				}
			}
			if err := DiscardNative(ctx, r.ControlRoot, change); err != nil {
				t.Fatal(err)
			}
			_, err = os.Stat(change.Staged)
			if pending && err != nil {
				t.Fatal("pending recovery input deleted")
			}
			if !pending && !os.IsNotExist(err) {
				t.Fatal("unused candidate leaked")
			}
		})
	}
}

func TestAttestedNativeProbeBindsFinalExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("inert POSIX helper fixture")
	}
	source := nativeFixture(t)
	executable := filepath.Join(source, "Contents", "MacOS", "terminal-notifier-modern")
	capabilities := `{"schemaVersion":1,"protocolVersions":[1],"actionKinds":["none","desktop_thread_v1"],"receiptSupport":true,"backend":"macos.usernotifications","explicitFeatureEnabledByDefault":false}`
	payload := []byte("#!/bin/sh\n[ \"$1\" = --capabilities-json ] || exit 9\nprintf '%s' '" + capabilities + "'\n")
	if err := os.WriteFile(executable, payload, 0755); err != nil {
		t.Fatal(err)
	}
	sealed := filepath.Join(source, "Contents", "Resources", "managed-runtime.json")
	if err := os.MkdirAll(filepath.Dir(sealed), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sealed, []byte(`{"SchemaVersion":1,"ProtocolVersion":1,"DecoderFloor":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	attestation := source + ".managed-runtime.json"
	manifest := nativeManifest{SchemaVersion: 1, ProtocolVersion: 1, DecoderFloor: 1, ExecutableSHA256: fmt.Sprintf("%x", sha256.Sum256(payload))}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(attestation, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	original := nativePlatformCheck
	t.Cleanup(func() { nativePlatformCheck = original })
	checked := false
	nativePlatformCheck = func(context.Context, string) error { checked = true; return nil }
	floor, err := verifyNative(context.Background(), source, attestation)
	if err != nil || floor != 1 || !checked {
		t.Fatalf("attested direct probe: %d %v", floor, err)
	}
	ctx, r := request(t)
	change, err := StageNative(ctx, r.ControlRoot, source)
	if err != nil {
		t.Fatal(err)
	}
	r.Native = change
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(attestation); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadInstalledSnapshot(r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Ledger.Native.Attestation) == 0 {
		t.Fatal("installed contract lost attestation")
	}
	snapshot.Ledger.Native.Attestation = []byte("modified")
	if err := validateNativeRecord(snapshot.Ledger.Native); err == nil {
		t.Fatal("attestation not bound to installed tree")
	}
	if err := os.WriteFile(attestation, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	// A post-attestation executable change must refuse before even OS verification.
	checked = false
	if err := os.WriteFile(executable, append(payload, []byte("# modified after attestation\n")...), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyNative(context.Background(), source, attestation); err == nil || checked {
		t.Fatalf("changed executable reached verifier: %v", err)
	}
}

func TestNativeCandidateCapacity(t *testing.T) {
	ctx, r := request(t)
	parent := filepath.Join(r.ControlRoot, "native")
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 16; i++ {
		if err := os.Mkdir(filepath.Join(parent, fmt.Sprintf(".candidate-%d", i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := StageNative(ctx, r.ControlRoot, nativeFixture(t)); err == nil {
		t.Fatal("unbounded orphan staging")
	}
}

func TestPurgeCleanupReplaysAndPreservesForeignTombstone(t *testing.T) {
	ctx, r := request(t)
	change, err := StageNative(ctx, r.ControlRoot, nativeFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	r.Native = change
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	r.Native = nil
	r.RemoveConsumer = true
	r.PurgeNative = true
	r.Fault = func(phase string) error {
		if phase == "ledger" {
			return fmt.Errorf("crash")
		}
		return nil
	}
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("fault not reached")
	}
	data, err := os.ReadFile(filepath.Join(r.ControlRoot, "transaction.json"))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := decodeTransaction(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tx.Native.Staged); err != nil {
		t.Fatal("missing recoverable tombstone")
	}
	foreign := filepath.Join(tx.Native.Staged, "foreign")
	if err := os.WriteFile(foreign, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	r.Fault = nil
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("changed tombstone deleted")
	}
	if err := os.Remove(foreign); err != nil {
		t.Fatal(err)
	}
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tx.Native.Staged); !os.IsNotExist(err) {
		t.Fatal("tombstone leaked")
	}
}

func TestNativeStableIdentityAcrossSourceBasenames(t *testing.T) {
	ctx, r := request(t)
	source := nativeFixture(t)
	legacy := filepath.Join(filepath.Dir(source), "terminal-notifier.app")
	if err := os.Rename(source, legacy); err != nil {
		t.Fatal(err)
	}
	change, err := StageNative(ctx, r.ControlRoot, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(change.After.Path) == "terminal-notifier.app" {
		t.Fatal("source basename became installed identity")
	}
	r.Native = change
	if _, err = Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	modern, err := StageNative(ctx, r.ControlRoot, nativeFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if modern.Before.Path != change.After.Path || modern.Before.SHA256 != change.After.SHA256 {
		t.Fatal("modern candidate lost previous concrete identity")
	}
	// A migration from a pre-remediation ledger keeps its old concrete path.
	l, err := readLedger(r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(filepath.Dir(l.Native.Path), "terminal-notifier.app")
	if err = os.Rename(l.Native.Path, oldPath); err != nil {
		t.Fatal(err)
	}
	l.Native.Path = oldPath
	if err = writeJSON(filepath.Join(r.ControlRoot, "ownership.json"), l); err != nil {
		t.Fatal(err)
	}
	modern, err = StageNative(ctx, r.ControlRoot, nativeFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if modern.Before.Path != oldPath {
		t.Fatal("legacy callback identity changed")
	}
	if modern.After.Path == oldPath && modern.After.SHA256 != modern.Before.SHA256 {
		t.Fatal("new bytes reused legacy callback path")
	}
	if _, err = os.Stat(oldPath); err != nil {
		t.Fatal("legacy concrete callback removed")
	}
}

func TestNativePublishedGenerationsABCAndNoop(t *testing.T) {
	ctx, r := request(t)
	var paths []string
	var inodes []string
	for i := 0; i < 3; i++ {
		change, err := StageNative(ctx, r.ControlRoot, nativeFixture(t))
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(filepath.Base(change.After.Path), ".candidate-") {
			t.Fatal("published generation used staging name")
		}
		for _, prev := range paths {
			if change.After.Path == prev {
				t.Fatal("new bytes reused published identity")
			}
			if _, err := os.Stat(prev); err != nil {
				t.Fatal("previous generation disappeared during staging")
			}
		}
		r.Native = change
		ledger, err := Commit(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		if ledger.Schema != ledgerSchemaV2 {
			t.Fatal("generation commit kept legacy schema")
		}
		if _, err := os.Stat(change.After.Path); err != nil {
			t.Fatal("published generation missing")
		}
		id, err := nativeDirectoryID(change.After.Path)
		if err != nil {
			t.Fatal(err)
		}
		for _, prev := range inodes {
			if prev == id {
				t.Fatal("published inode reused")
			}
		}
		paths = append(paths, change.After.Path)
		inodes = append(inodes, id)
		if len(ledger.Native.Published) != i+1 {
			t.Fatalf("published inventory %d", len(ledger.Native.Published))
		}
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("generation lost after A/B/C", path)
		}
	}
	last := paths[len(paths)-1]
	digest, err := treeFingerprint(last)
	if err != nil {
		t.Fatal(err)
	}
	again, err := StageNative(ctx, r.ControlRoot, last)
	if err != nil {
		t.Fatal(err)
	}
	if again.After.Path != last || again.After.SHA256 != digest {
		t.Fatal("same-bytes reinstall assigned a new identity")
	}
	r.Native = again
	ledger, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if ledger.Native.Path != last || len(ledger.Native.Published) != 3 {
		t.Fatalf("same-bytes reinstall mutated inventory: %+v", ledger.Native)
	}
	id, err := nativeDirectoryID(last)
	if err != nil || id != inodes[len(inodes)-1] {
		t.Fatal("same-bytes reinstall moved inode")
	}
}

func TestOldWriterRejectsGenerationTransaction(t *testing.T) {
	ctx, r := request(t)
	change, err := StageNative(ctx, r.ControlRoot, nativeFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	r.Native = change
	r.Fault = func(phase string) error {
		if phase == "ledger" {
			return fmt.Errorf("crash")
		}
		return nil
	}
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("missing crash")
	}
	data, err := os.ReadFile(filepath.Join(r.ControlRoot, "transaction.json"))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := decodeTransaction(data)
	if err != nil {
		t.Fatal(err)
	}
	if tx.Schema != transactionSchemaV2 {
		t.Fatal("pending generation transaction used legacy schema")
	}
	tx.Schema = 999
	if err := writeTransaction(filepath.Join(r.ControlRoot, "transaction.json"), tx); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeTransaction(mustRead(t, filepath.Join(r.ControlRoot, "transaction.json"))); err == nil {
		t.Fatal("unknown transaction schema accepted")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func exactHeadNativeApp(t *testing.T, marker string) string {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("exact-head helper is a Darwin binary")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("missing caller")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	bin := filepath.Join(repo, "swift-notifier/.build/arm64-apple-macosx/release/terminal-notifier-modern")
	plist := filepath.Join(repo, "swift-notifier/Resources/Info.plist")
	if _, err := os.Stat(bin); err != nil {
		t.Skip("exact-head helper is not built")
	}
	root := filepath.Join(t.TempDir(), "ClaudeNotifier.app")
	macOS := filepath.Join(root, "Contents", "MacOS")
	resources := filepath.Join(root, "Contents", "Resources")
	for _, dir := range []string{macOS, resources} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	body, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(macOS, "terminal-notifier-modern"), body, 0755); err != nil {
		t.Fatal(err)
	}
	info, err := os.ReadFile(plist)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Contents", "Info.plist"), info, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resources, "generation.marker"), []byte(marker), 0644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestNativeExactHeadGenerationsPreserveCallbackIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	_, r := request(t)
	first := exactHeadNativeApp(t, "generation-A")
	change, err := StageNative(ctx, r.ControlRoot, first)
	if err != nil {
		t.Fatal(err)
	}
	r.Native = change
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	pathA := change.After.Path
	inodeA, err := nativeDirectoryID(pathA)
	if err != nil {
		t.Fatal(err)
	}
	hashA, err := treeFingerprint(pathA)
	if err != nil {
		t.Fatal(err)
	}
	second := exactHeadNativeApp(t, "generation-B")
	change, err = StageNative(ctx, r.ControlRoot, second)
	if err != nil {
		t.Fatal(err)
	}
	if change.After.Path == pathA {
		t.Fatal("new helper bytes reused the live callback path")
	}
	pathB := change.After.Path
	r.Native = change
	ledger, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	inodeAfter, err := nativeDirectoryID(pathA)
	if err != nil {
		t.Fatal(err)
	}
	hashAfter, err := treeFingerprint(pathA)
	if err != nil {
		t.Fatal(err)
	}
	if inodeAfter != inodeA || hashAfter != hashA {
		t.Fatal("queued-callback generation A moved during exact-head update")
	}
	if _, err := os.Stat(pathB); err != nil {
		t.Fatal("generation B missing")
	}
	if len(ledger.Native.Published) != 2 {
		t.Fatalf("published inventory %d", len(ledger.Native.Published))
	}
	againA := exactHeadNativeApp(t, "generation-A")
	change, err = StageNative(ctx, r.ControlRoot, againA)
	if err != nil {
		t.Fatal(err)
	}
	if change.After.Path != pathA {
		t.Fatal("rollback restage of A assigned a new callback identity")
	}
	r.Native = change
	ledger, err = Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if ledger.Native.Path != pathA {
		t.Fatal("active native path did not roll back to A")
	}
	inodeRollback, err := nativeDirectoryID(pathA)
	if err != nil || inodeRollback != inodeA {
		t.Fatal("rollback moved generation A")
	}
	if _, err := os.Stat(pathA); err != nil {
		t.Fatal("generation A missing after rollback")
	}
	if _, err := os.Stat(pathB); err != nil {
		t.Fatal("generation B deleted during rollback")
	}
	if len(ledger.Native.Published) != 2 {
		t.Fatalf("rollback mutated published inventory %d", len(ledger.Native.Published))
	}
}

package installruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/777genius/agent-notifications/internal/codexcommand"
)

// A qualified retained reader must not be re-probed merely because its stored
// decoder floor is positive. Initial installation uses the existing OS test
// seam with real attestation/capability decoding. The actual OS checker is
// restored before orphan admission; no OS qualification is claimed here.
func TestOrphanAttestedReaderPreservesEvidenceAndNeverRepeatsCapabilities(t *testing.T) {
	for _, boundary := range []string{"transaction", "ledger"} {
		t.Run(boundary, func(t *testing.T) {
			f := newOrphanFixture(t)
			source := nativeFixture(t)
			executable := filepath.Join(source, "Contents", "MacOS", "terminal-notifier-modern")
			count := filepath.Join(f.root, "capability-invocations")
			capabilities := "{\"schemaVersion\":1,\"protocolVersions\":[1],\"actionKinds\":[\"none\",\"desktop_thread_v1\"],\"receiptSupport\":true,\"backend\":\"macos.usernotifications\",\"explicitFeatureEnabledByDefault\":false}"
			payload := []byte("#!/bin/sh\n[ \"$1\" = --capabilities-json ] || exit 9\nprintf 'probe\\n' >> " + codexcommand.POSIXQuote(count) + "\nprintf '%s' '" + capabilities + "'\n")
			if err := os.WriteFile(executable, payload, 0755); err != nil {
				t.Fatal(err)
			}
			sealed := filepath.Join(source, "Contents", "Resources", "managed-runtime.json")
			orphanWrite(t, sealed, "{\"SchemaVersion\":1,\"ProtocolVersion\":1,\"DecoderFloor\":1}")
			manifest := nativeManifest{SchemaVersion: 1, ProtocolVersion: 1, DecoderFloor: 1, ExecutableSHA256: fmt.Sprintf("%x", sha256.Sum256(payload))}
			evidence, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(source+".managed-runtime.json", evidence, 0600); err != nil {
				t.Fatal(err)
			}
			// As in TestAttestedNativeProbeBindsFinalExecutable, only fixture
			// bootstrap substitutes OS signature verification. The recovery
			// request receives no replacement hash, evidence or floor.
			original := nativePlatformCheck
			t.Cleanup(func() { nativePlatformCheck = original })
			nativePlatformCheck = func(context.Context, string) error { return nil }
			change, err := StageNative(f.ctx, f.control, source)
			nativePlatformCheck = original
			if err != nil {
				t.Fatal(err)
			}
			f.before, err = Commit(f.ctx, Request{ControlRoot: f.control, RuntimeRoot: f.primary, Owner: "existing-installer", ConsumerID: "retained", Native: change})
			if err != nil {
				t.Fatal(err)
			}
			if f.before.DecoderFloor != 1 || !bytes.Equal(f.before.Native.Attestation, evidence) {
				t.Fatal("fixture lacks actual attestation-bound positive decoder floor")
			}
			qualifiedCount, err := os.ReadFile(count)
			if err != nil || len(qualifiedCount) == 0 {
				t.Fatal("installer did not qualify fixture", err)
			}
			f.abandon(t, true)
			if _, err := PreviewOrphanConsumer(f.control, f.recoveryRequest()); err != nil {
				t.Fatal(err)
			}
			f.interrupt(t, boundary)
			got, err := Recover(f.ctx, f.control)
			if err != nil {
				t.Fatal(err)
			}
			again, err := Recover(f.ctx, f.control)
			if err != nil || !reflect.DeepEqual(got, again) {
				t.Fatal("attested-reader redo did not converge", err)
			}
			assertOrphanDelta(t, f, got, false)
			if !bytes.Equal(got.Native.Attestation, evidence) || got.Native.InstalledTreeSHA256 != f.before.Native.InstalledTreeSHA256 {
				t.Fatal("attestation binding changed")
			}
			finalCount, err := os.ReadFile(count)
			if err != nil || !bytes.Equal(qualifiedCount, finalCount) {
				t.Fatal("orphan preview/commit/redo re-executed capabilities", err)
			}
		})
	}
}

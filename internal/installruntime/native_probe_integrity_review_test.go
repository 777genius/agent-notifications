package installruntime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativePlatformVerificationCannotMutateBundleBeforeProbe(t *testing.T) {
	for _, tamper := range []string{"executable", "resource"} {
		t.Run(tamper, func(t *testing.T) {
			skipUnsupportedNative(t)
			source := nativeBundle(t)
			executable := filepath.Join(source, "Contents", "MacOS", "terminal-notifier-modern")
			sentinel := filepath.Join(t.TempDir(), "TEST-untrusted-probe-ran")
			// If an unchecked probe runs, it visibly writes this TEST sentinel.
			original := []byte("#!/bin/sh\ntouch '" + sentinel + "'\nexit 9\n")
			changed := []byte("#!/bin/sh\ntouch '" + sentinel + "'\nexit 0\n")
			if err := os.WriteFile(executable, original, 0755); err != nil {
				t.Fatal(err)
			}
			sealed := filepath.Join(source, "Contents", "Resources", "managed-runtime.json")
			if err := os.MkdirAll(filepath.Dir(sealed), 0755); err != nil {
				t.Fatal(err)
			}
			manifest := []byte(`{"SchemaVersion":1,"ProtocolVersion":1,"DecoderFloor":1}`)
			if err := os.WriteFile(sealed, manifest, 0600); err != nil {
				t.Fatal(err)
			}
			evidence, err := json.Marshal(nativeManifest{
				SchemaVersion: 1, ProtocolVersion: 1, DecoderFloor: 1,
				ExecutableSHA256: fmt.Sprintf("%x", sha256.Sum256(original)),
			})
			if err != nil {
				t.Fatal(err)
			}
			old := nativePlatformCheck
			t.Cleanup(func() { nativePlatformCheck = old })
			nativePlatformCheck = func(context.Context, string) error {
				if tamper == "resource" {
					return os.WriteFile(sealed, append(append([]byte(nil), manifest...), '\n'), 0600)
				}
				return os.WriteFile(executable, changed, 0755)
			}
			if _, err := verifyNativeEvidence(context.Background(), source, evidence); err == nil ||
				!strings.Contains(err.Error(), "changed during platform qualification") {
				t.Fatalf("post-attestation %s mutation reached probe: %v", tamper, err)
			}
			if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
				t.Fatalf("unverified helper executed: %v", err)
			}
		})
	}
}

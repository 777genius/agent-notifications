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

func TestNativePlatformVerificationCannotSwapExecutableBeforeProbe(t *testing.T) {
	skipUnsupportedNative(t)
	source := nativeBundle(t)
	executable := filepath.Join(source, "Contents", "MacOS", "terminal-notifier-modern")
	sentinel := filepath.Join(t.TempDir(), "TEST-untrusted-probe-ran")
	original := []byte("#!/bin/sh\nexit 9\n")
	changed := []byte("#!/bin/sh\ntouch '" + sentinel + "'\nexit 0\n")
	if err := os.WriteFile(executable, original, 0755); err != nil {
		t.Fatal(err)
	}
	sealed := filepath.Join(source, "Contents", "Resources", "managed-runtime.json")
	if err := os.MkdirAll(filepath.Dir(sealed), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sealed, []byte(`{"SchemaVersion":1,"ProtocolVersion":1,"DecoderFloor":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	evidence, err := json.Marshal(nativeManifest{SchemaVersion: 1, ProtocolVersion: 1, DecoderFloor: 1, ExecutableSHA256: fmt.Sprintf("%x", sha256.Sum256(original))})
	if err != nil {
		t.Fatal(err)
	}
	old := nativePlatformCheck
	t.Cleanup(func() { nativePlatformCheck = old })
	nativePlatformCheck = func(context.Context, string) error {
		return os.WriteFile(executable, changed, 0755)
	}
	if _, err := verifyNativeEvidence(context.Background(), source, evidence); err == nil || !strings.Contains(err.Error(), "changed during platform qualification") {
		t.Fatalf("post-attestation native mutation reached probe: %v", err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("unverified helper executed: %v", err)
	}
}

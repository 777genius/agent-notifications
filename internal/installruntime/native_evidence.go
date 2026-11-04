package installruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"github.com/777genius/agent-notifications/internal/notifier/nativeprotocol"
)

// Native OS verification is replaced only by inert protocol fixtures in tests.
var nativePlatformCheck = verifyNativePlatform

func verifyNative(ctx context.Context, bundle, attestationPath string) (int, error) {
	evidence, err := readRegularFile(attestationPath)
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	return verifyNativeEvidence(ctx, bundle, evidence)
}

func verifyNativeEvidence(ctx context.Context, bundle string, attestation []byte) (int, error) {
	manifestPath := filepath.Join(bundle, "Contents", "Resources", "managed-runtime.json")
	data, err := readRegularFile(manifestPath)
	if os.IsNotExist(err) {
		return 0, nil
	} // Never execute a legacy/unknown notifier.
	if err != nil {
		return 0, err
	}
	var manifest nativeManifest
	if json.Unmarshal(data, &manifest) != nil || manifest.SchemaVersion != 1 || manifest.ProtocolVersion != 1 || manifest.DecoderFloor != 1 || manifest.ExecutableSHA256 != "" {
		return 0, fmt.Errorf("unsupported native offline manifest")
	}
	// The sealed marker states the protocol floor. The adjacent package
	// attestation is emitted AFTER signing: embedding a final executable hash
	// in its own signed resource seal would introduce a hash/signature cycle.
	// Neither file grants publisher trust; the adapter supplies source authority.
	if len(attestation) == 0 {
		return 0, nil
	} // Unknown fingerprint: never probe.
	var expected nativeManifest
	if json.Unmarshal(attestation, &expected) != nil || expected.SchemaVersion != manifest.SchemaVersion || expected.ProtocolVersion != manifest.ProtocolVersion || expected.DecoderFloor != manifest.DecoderFloor {
		return 0, fmt.Errorf("native package attestation disagrees with sealed floor")
	}
	executable, err := readRegularFile(filepath.Join(bundle, "Contents", "MacOS", "terminal-notifier-modern"))
	if err != nil {
		return 0, err
	}
	digest := sha256.Sum256(executable)
	if expected.ExecutableSHA256 != hex.EncodeToString(digest[:]) {
		return 0, fmt.Errorf("native offline executable fingerprint mismatch")
	}
	if err := nativePlatformCheck(ctx, bundle); err != nil {
		return 0, err
	}
	// Signature verifies bundle integrity, not publisher trust. Bind staged bytes
	// around the probe; additional supported actions do not enable those actions.
	before, err := treeFingerprint(bundle)
	if err != nil {
		return 0, err
	}
	output, err := boundedCommand(ctx, filepath.Join(bundle, "Contents", "MacOS", "terminal-notifier-modern"), "--capabilities-json")
	if err != nil {
		return 0, fmt.Errorf("native capabilities probe: %w", err)
	}
	if err := validateNativeCapabilities(output); err != nil {
		return 0, err
	}
	after, err := treeFingerprint(bundle)
	if err != nil {
		return 0, err
	}
	if before != after {
		return 0, fmt.Errorf("native changed during probe")
	}
	return manifest.DecoderFloor, nil
}

func validateNativeCapabilities(output []byte) error {
	caps, err := nativeprotocol.DecodeCapabilities(output)
	if err != nil || !caps.Supports(1, "none") {
		return fmt.Errorf("native capabilities disagree with bounded PR1 floor")
	}
	return nil
}

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/notifier/nativeprotocol"
)

// embeddedQualified keeps admission negatives on embeddedFresh while allowing
// positive installs to pass the real Darwin native guard, regardless of entry OS.
func embeddedQualified(t *testing.T) embeddedFixture {
	t.Helper()
	f := embeddedFresh(t)
	installerNativeFixture(t, f.stage)
	return f
}

const installerNativeCapabilities = `{"schemaVersion":1,"protocolVersions":[1],"actionKinds":["none"],"receiptSupport":true,"backend":"macos.usernotifications","explicitFeatureEnabledByDefault":false}`

// One process has one runtime.GOARCH. Cache unsigned bytes, never a bundle or a
// caller-owned path: codesign mutates each private copy independently.
var installerNativeCompiled installerNativeByteCache

type installerNativeByteCache struct {
	once  sync.Once
	image []byte
	err   error
}

func (c *installerNativeByteCache) bytes(compile func() ([]byte, error)) ([]byte, error) {
	c.once.Do(func() {
		image, err := compile()
		c.err = err
		if err == nil {
			c.image = bytes.Clone(image)
		}
	})
	return bytes.Clone(c.image), c.err
}

func runInstallerNativeTool(tool, input string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Stdin = strings.NewReader(input)
	cmd.WaitDelay = time.Second
	// Discard tool output; errors expose neither environment nor private paths.
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("inert native fixture %s failed (timeout=%t); standard Apple developer tools required", filepath.Base(tool), ctx.Err() != nil)
	}
	return nil
}

func compileInstallerNative() ([]byte, error) {
	root, err := os.MkdirTemp("", "TEST-installer-native-unsigned-")
	if err != nil {
		return nil, fmt.Errorf("inert native fixture compile directory unavailable")
	}
	defer func() { _ = os.RemoveAll(root) }()
	executable := filepath.Join(root, "terminal-notifier-modern")
	// Fixed C source on stdin, only libc stdio/string; reject every other request.
	source := "#include <stdio.h>\n#include <string.h>\nint main(int argc, char **argv) {\n" +
		"if (argc != 2 || strcmp(argv[1], \"--capabilities-json\") != 0) return 9;\n" +
		"return puts(" + strconv.Quote(installerNativeCapabilities) + ") < 0 ? 1 : 0;\n}\n"
	arch := "x86_64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}
	// arm64's linker otherwise adds an ad-hoc signature to the cached image.
	if err := runInstallerNativeTool("/usr/bin/clang", source, "-arch", arch, "-Wl,-no_adhoc_codesign", "-x", "c", "-", "-o", executable); err != nil {
		return nil, err
	}
	image, err := os.ReadFile(executable)
	if err != nil {
		return nil, fmt.Errorf("inert native fixture compiled image unavailable")
	}
	return image, nil
}

// installerNativeFixture creates only a private, inert capability fake. It is
// not notification qualification: no app launch, permission probe, or sender
// execution occurs. Every refresh copies cached unsigned bytes before signing.
func installerNativeFixture(t *testing.T, stage string) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		return
	}
	caps, err := nativeprotocol.DecodeCapabilities([]byte(installerNativeCapabilities))
	if err != nil || !caps.Supports(1, "none") {
		t.Fatal("invalid inert native fixture capabilities")
	}
	// LaunchServices identifies bundles from Info.plist, independently of the
	// explicit signing identifier required by the real native qualification guard.
	// A subprocess installer must never register this inert fake as the product.
	fixtureID := fmt.Sprintf("com.agentnotify.test.installer.%x", sha256.Sum256([]byte(stage)))
	bundle := filepath.Join(stage, "ClaudeNotifier.app")
	executable := filepath.Join(bundle, "Contents", "MacOS", "terminal-notifier-modern")
	embeddedPut(t, filepath.Join(bundle, "Contents", "Info.plist"), []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>`+fixtureID+`</string>
<key>CFBundleExecutable</key><string>terminal-notifier-modern</string>
<key>CFBundlePackageType</key><string>APPL</string>
</dict></plist>
`), 0600)
	embeddedPut(t, filepath.Join(bundle, "Contents", "Resources", "managed-runtime.json"), []byte(`{"SchemaVersion":1,"ProtocolVersion":1,"DecoderFloor":1}`), 0600)
	image, err := installerNativeCompiled.bytes(compileInstallerNative)
	if err != nil {
		t.Fatal(err)
	}
	embeddedPut(t, executable, image, 0700)
	// The guard requires the product signing identifier. Retain that identifier
	// while the distinct CFBundleIdentifier prevents product LS registration.
	// Seal only this private bundle; the unsigned cache remains unchanged.
	if err := runInstallerNativeTool("/usr/bin/codesign", "", "--force", "--sign", "-", "--identifier", "com.777genius.agent-notifications", bundle); err != nil {
		t.Fatal(err)
	}
	// Hash only after signing; never modify resources inside the signed bundle.
	digest := sha256.Sum256(embeddedRead(t, executable))
	embeddedPut(t, bundle+".managed-runtime.json", []byte(fmt.Sprintf(`{"SchemaVersion":1,"ProtocolVersion":1,"DecoderFloor":1,"ExecutableSHA256":"%x"}`, digest)), 0600)
}

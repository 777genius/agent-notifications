package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
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

// installerNativeFixture creates only a private, inert capability fake. It is
// not notification qualification: no app launch, permission probe, or sender
// execution occurs. Reuse the sealed bytes for every refresh of this fixture.
func installerNativeFixture(t *testing.T, stage string) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		return
	}
	const capabilities = `{"schemaVersion":1,"protocolVersions":[1],"actionKinds":["none"],"receiptSupport":true,"backend":"macos.usernotifications","explicitFeatureEnabledByDefault":false}`
	caps, err := nativeprotocol.DecodeCapabilities([]byte(capabilities))
	if err != nil || !caps.Supports(1, "none") {
		t.Fatal("invalid inert native fixture capabilities")
	}
	bundle := filepath.Join(stage, "ClaudeNotifier.app")
	executable := filepath.Join(bundle, "Contents", "MacOS", "terminal-notifier-modern")
	embeddedPut(t, filepath.Join(bundle, "Contents", "Info.plist"), []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.claude.desktop.notifier</string>
<key>CFBundleExecutable</key><string>terminal-notifier-modern</string>
<key>CFBundlePackageType</key><string>APPL</string>
</dict></plist>
`), 0600)
	embeddedPut(t, filepath.Join(bundle, "Contents", "Resources", "managed-runtime.json"), []byte(`{"SchemaVersion":1,"ProtocolVersion":1,"DecoderFloor":1}`), 0600)
	if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
		t.Fatal(err)
	}
	// Fixed C source on stdin, only libc stdio/string; reject every other request.
	source := "#include <stdio.h>\n#include <string.h>\nint main(int argc, char **argv) {\n" +
		"if (argc != 2 || strcmp(argv[1], \"--capabilities-json\") != 0) return 9;\n" +
		"return puts(" + strconv.Quote(capabilities) + ") < 0 ? 1 : 0;\n}\n"
	run := func(tool, input string, args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, tool, args...)
		cmd.Stdin = strings.NewReader(input)
		cmd.WaitDelay = time.Second
		// Discard tool output; errors expose neither environment nor private paths.
		if err := cmd.Run(); err != nil {
			t.Fatalf("inert native fixture %s failed (timeout=%t); standard Apple developer tools required", filepath.Base(tool), ctx.Err() != nil)
		}
	}
	arch := "x86_64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}
	run("/usr/bin/clang", source, "-arch", arch, "-x", "c", "-", "-o", executable)
	// Replace any linker-generated ad-hoc signature with the bundle seal.
	run("/usr/bin/codesign", "", "--force", "--sign", "-", "--identifier", "com.claude.desktop.notifier", bundle)
	// Hash only after signing; never modify resources inside the signed bundle.
	digest := sha256.Sum256(embeddedRead(t, executable))
	embeddedPut(t, bundle+".managed-runtime.json", []byte(fmt.Sprintf(`{"SchemaVersion":1,"ProtocolVersion":1,"DecoderFloor":1,"ExecutableSHA256":"%x"}`, digest)), 0600)
}

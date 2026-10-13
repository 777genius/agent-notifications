package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/macho"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/notifier/nativeprotocol"
)

// A caller mutating either returned bytes or a private file must not corrupt
// concurrently constructed copies or any future fixture; one failure is cached.
func TestInstallerNativeByteCachePrivateCopies(t *testing.T) {
	var cache installerNativeByteCache
	var calls atomic.Int32
	compiled := []byte("TEST unsigned executable bytes")
	compile := func() ([]byte, error) {
		calls.Add(1)
		return compiled, nil
	}
	root := t.TempDir()
	paths := make([]string, 8)
	t.Run("concurrent copies", func(t *testing.T) {
		for i := range paths {
			t.Run(fmt.Sprint(i), func(t *testing.T) {
				t.Parallel()
				image, err := cache.bytes(compile)
				if err != nil || !bytes.Equal(image, []byte("TEST unsigned executable bytes")) {
					t.Fatal("cached image differs from compiled bytes")
				}
				paths[i] = filepath.Join(root, fmt.Sprintf("private-image-%d", i))
				embeddedPut(t, paths[i], image, 0700)
				image[0] = '!'
				if !bytes.Equal(embeddedRead(t, paths[i]), []byte("TEST unsigned executable bytes")) {
					t.Fatal("returned bytes alias the private file")
				}
			})
		}
	})
	if t.Failed() {
		return
	}
	if calls.Load() != 1 {
		t.Fatal("concurrent requests compiled more than once")
	}
	embeddedPut(t, paths[0], []byte("TEST mutated private image"), 0700)
	for _, path := range paths[1:] {
		if !bytes.Equal(embeddedRead(t, path), []byte("TEST unsigned executable bytes")) {
			t.Fatal("mutating one file changed another private copy")
		}
	}
	compiled[0] = '?'
	image, err := cache.bytes(compile)
	if err != nil || !bytes.Equal(image, []byte("TEST unsigned executable bytes")) {
		t.Fatal("compiler-owned bytes mutated the cache")
	}
	var failed installerNativeByteCache
	failure := errors.New("TEST compile failure")
	for range 2 {
		image, err := failed.bytes(func() ([]byte, error) { return []byte("partial"), failure })
		if image != nil || err != failure {
			t.Fatal("failed compilation published partial bytes or changed its error")
		}
	}
	image, err = failed.bytes(compile)
	if image != nil || err != failure || calls.Load() != 1 {
		t.Fatal("failed cache retried compilation")
	}
}

// The real Darwin boundary catches cached signed images/shared bundles,
// incorrect stage identities, pre-signing hashes, and a fake enabling actions.
func TestInstallerNativeFixtureDarwinIsolation(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("requires Darwin compiler and bundle signer")
	}
	unsigned, err := installerNativeCompiled.bytes(compileInstallerNative)
	if err != nil {
		t.Fatal(err)
	}
	image, err := macho.NewFile(bytes.NewReader(unsigned))
	if err != nil {
		t.Fatal("cached executable is not Mach-O")
	}
	defer func() { _ = image.Close() }()
	wantCPU := macho.CpuAmd64
	if runtime.GOARCH == "arm64" {
		wantCPU = macho.CpuArm64
	}
	if image.Cpu != wantCPU {
		t.Fatal("cached executable has wrong architecture")
	}
	for _, load := range image.Loads {
		if image.ByteOrder.Uint32(load.Raw()[:4]) == 0x1d { // LC_CODE_SIGNATURE
			t.Fatal("cached executable already contains a signature")
		}
	}
	root := t.TempDir()
	bundles := make([]string, 4)
	signed := make([][]byte, len(bundles))
	identifiers := make([]string, len(bundles))
	t.Run("concurrent bundles", func(t *testing.T) {
		for i := range bundles {
			t.Run(fmt.Sprint(i), func(t *testing.T) {
				t.Parallel()
				stage := filepath.Join(root, fmt.Sprintf("stage-%d", i))
				installerNativeFixture(t, stage)
				bundle := filepath.Join(stage, "ClaudeNotifier.app")
				bundles[i] = bundle
				identifiers[i] = strings.TrimSpace(string(installerNativeOutput(t, "/usr/libexec/PlistBuddy", "-c", "Print :CFBundleIdentifier", filepath.Join(bundle, "Contents", "Info.plist"))))
				if !strings.HasPrefix(identifiers[i], "com.agentnotify.test.installer.") {
					t.Fatal("fixture uses a product bundle identifier")
				}
				verifyInstallerNativeSignature(t, bundle)
				executable := filepath.Join(bundle, "Contents", "MacOS", "terminal-notifier-modern")
				signed[i] = embeddedRead(t, executable)
				var manifest struct {
					SchemaVersion, ProtocolVersion, DecoderFloor int
					ExecutableSHA256                             string
				}
				if json.Unmarshal(embeddedRead(t, bundle+".managed-runtime.json"), &manifest) != nil ||
					manifest.SchemaVersion != 1 || manifest.ProtocolVersion != 1 || manifest.DecoderFloor != 1 ||
					manifest.ExecutableSHA256 != fmt.Sprintf("%x", sha256.Sum256(signed[i])) {
					t.Fatal("attestation does not bind the final signed executable")
				}
				caps, err := nativeprotocol.DecodeCapabilities(installerNativeOutput(t, executable, "--capabilities-json"))
				if err != nil || !caps.Supports(1, "none") || len(caps.ActionKinds) != 1 || caps.ExplicitFeatureEnabledByDefault {
					t.Fatal("fixture capabilities enable behavior beyond the inert contract")
				}
				for _, args := range [][]string{nil, {"--request-json"}, {"--capabilities-json", "extra"}} {
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					cmd := exec.CommandContext(ctx, executable, args...)
					cmd.WaitDelay = time.Second
					err := cmd.Run()
					cancel()
					var exit *exec.ExitError
					if !errors.As(err, &exit) || exit.ExitCode() != 9 {
						t.Fatal("inert fixture accepted a non-capability request")
					}
				}
			})
		}
	})
	if t.Failed() {
		return
	}
	seen := make(map[string]bool)
	for _, id := range identifiers {
		if seen[id] {
			t.Fatal("private bundles share a LaunchServices identifier")
		}
		seen[id] = true
	}
	first := filepath.Join(bundles[0], "Contents", "MacOS", "terminal-notifier-modern")
	embeddedPut(t, first, []byte("TEST corrupted private executable"), 0700)
	if runInstallerNativeTool("/usr/bin/codesign", "", "--verify", "--deep", "--strict", bundles[0]) == nil {
		t.Fatal("signature verification accepted corrupted private executable")
	}
	for i := 1; i < len(bundles); i++ {
		if !bytes.Equal(embeddedRead(t, filepath.Join(bundles[i], "Contents", "MacOS", "terminal-notifier-modern")), signed[i]) {
			t.Fatal("mutating one bundle changed another private executable")
		}
		verifyInstallerNativeSignature(t, bundles[i])
	}
	cached, err := installerNativeCompiled.bytes(compileInstallerNative)
	if err != nil || !bytes.Equal(cached, unsigned) {
		t.Fatal("signing or mutating a private bundle changed the unsigned cache")
	}
	installerNativeFixture(t, filepath.Dir(bundles[0]))
	verifyInstallerNativeSignature(t, bundles[0])
}

func verifyInstallerNativeSignature(t *testing.T, bundle string) {
	t.Helper()
	if err := runInstallerNativeTool("/usr/bin/codesign", "", "--verify", "--deep", "--strict", "-R", `=identifier "com.777genius.agent-notifications"`, bundle); err != nil {
		t.Fatal(err)
	}
}

func installerNativeOutput(t *testing.T, tool string, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("inert native fixture probe %s failed (timeout=%t)", filepath.Base(tool), ctx.Err() != nil)
	}
	return out
}

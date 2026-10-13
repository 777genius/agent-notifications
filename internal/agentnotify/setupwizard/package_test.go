package setupwizard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portableasset"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
	uapinstaller "github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/installer"
)

func TestLocalArchiveReplacementInvalidatesCache(t *testing.T) {
	for _, checked := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchecked", true: "checked"}[checked], func(t *testing.T) {
			base := t.TempDir()
			exe := filepath.Join(base, "fixture")
			if err := os.WriteFile(exe, []byte("synthetic executable"), 0700); err != nil {
				t.Fatal(err)
			}
			archive := filepath.Join(base, "package.zip")
			req := Request{ControlRoot: filepath.Join(base, "control")}
			build := func(version string) string {
				t.Helper()
				_ = os.Remove(archive)
				result, err := portableasset.Build(portableasset.BuildRequest{Version: version, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Executable: exe, OutputRoot: filepath.Join(base, version), Archive: archive})
				if err != nil {
					t.Fatal(err)
				}
				return result.ArchiveSHA256
			}
			firstDigest := build("1.43.0")
			if checked {
				req.PackageSHA256 = firstDigest
			}
			first, _, err := openLocalPackage(req, archive)
			if err != nil {
				t.Fatal(err)
			}
			same, _, err := openLocalPackage(req, archive)
			if err != nil || same != first {
				t.Fatalf("cache reuse: %s %v", same, err)
			}
			info, err := os.Stat(archive)
			if err != nil {
				t.Fatal(err)
			}
			nextDigest := build("1.44.0")
			if err := os.Chtimes(archive, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
			if checked {
				if _, _, err := openLocalPackage(req, archive); !errors.Is(err, portableasset.ErrChecksumMismatch) {
					t.Fatalf("stale checksum accepted: %v", err)
				}
				req.PackageSHA256 = nextDigest
			}
			next, _, err := openLocalPackage(req, archive)
			if err != nil {
				t.Fatal(err)
			}
			if next == first {
				t.Fatal("replaced archive reused stale extraction")
			}
			body, err := os.ReadFile(filepath.Join(next, "plugin.json"))
			if err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(filepath.Join(base, "1.44.0", "plugin.json"))
			if err != nil || string(body) != string(original) {
				t.Fatalf("wrong extracted manifest: %s, %v", body, err)
			}
			if !packageStillUsable(first) {
				t.Fatal("replacement removed a retained source")
			}
		})
	}
}

// Regression: composition cleanup deletes a shared durable source or a source
// referenced by a pending retry, instead of only its exclusive new staging.
func TestPackageCompositionCleanupPreservesSharedAndPendingSource(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	base := t.TempDir()
	exe, archive := filepath.Join(base, "TEST-executable"), filepath.Join(base, "TEST-release.zip")
	if err := os.WriteFile(exe, []byte("TEST-never-executed"), 0700); err != nil {
		t.Fatal(err)
	}
	built, err := portableasset.Build(portableasset.BuildRequest{Version: "1.43.0", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Executable: exe, OutputRoot: filepath.Join(base, "TEST-release"), Archive: archive})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	fetches := 0
	r := Request{ControlRoot: filepath.Join(base, "control"), ReleaseVersion: "1.43.0", PackageFetcher: func(_ context.Context, url string) ([]byte, error) {
		fetches++
		if strings.HasSuffix(url, "/checksums.txt") {
			return []byte(built.ArchiveSHA256 + "  " + portableasset.AssetName(runtime.GOOS, runtime.GOARCH) + "\n"), nil
		}
		return body, nil
	}}
	staged, cleanup, err := StageCurrentReleasePackage(ctx, r)
	if err != nil || !packageStillUsable(staged.PackageRoot) {
		t.Fatalf("staging: %+v %v", staged, err)
	}
	shared, err := PinCurrentReleasePackage(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if packageStillUsable(staged.PackageRoot) || !packageStillUsable(shared.PackageRoot) {
		t.Fatal("cleanup crossed acquisition ownership")
	}
	reused, release, err := StageCurrentReleasePackage(ctx, r)
	if err != nil || reused.PackageRoot != shared.PackageRoot {
		t.Fatalf("shared reuse: %+v %v", reused, err)
	}
	release()
	if !packageStillUsable(shared.PackageRoot) {
		t.Fatal("shared cache removed")
	}
	legacy := shared
	legacy.Helper = exe
	retainedShared, err := RetainCurrentReleasePackage(ctx, legacy)
	if err != nil || retainedShared.PackageRoot == shared.PackageRoot || !packageStillUsable(shared.PackageRoot) {
		t.Fatal("legacy shared source moved or lost durable address", err)
	}
	// Successful B composition has a durable address before UAP commits B.
	built, err = portableasset.Build(portableasset.BuildRequest{Version: "1.44.0", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Executable: exe, OutputRoot: filepath.Join(base, "TEST-B"), Archive: filepath.Join(base, "TEST-B.zip")})
	if err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(built.Archive)
	if err != nil {
		t.Fatal(err)
	}
	b := r
	b.ReleaseVersion, b.Helper, b.Action, b.InstallationID = "1.44.0", exe, ActionUpdate, "TEST-installed-A"
	b.BindingIDs, b.CursorConfig = map[string]string{"cursor": "TEST-binding"}, filepath.Join(base, ".cursor")
	stageB, releaseB, err := StageCurrentReleasePackage(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	competing, releaseCompeting, err := StageCurrentReleasePackage(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := RetainCurrentReleasePackage(ctx, stageB)
	if err != nil {
		t.Fatal(err)
	}
	// Bind both nonmutating representations to the pinned public snapshot API,
	// rather than letting the new preflight define its own frozen identity.
	registry, err := portablesetup.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	public, err := uapinstaller.New(uapinstaller.Config{StateRoot: filepath.Join(base, "TEST-public-digest"), Registry: registry, TrustedLocalPackages: true})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := public.LocalPackageTreeDigest(ctx, built.Root)
	if err != nil || canonical != candidate.TreeDigest {
		t.Fatalf("public143c directory framing differs: %s %s %v", canonical, candidate.TreeDigest, err)
	}
	archiveView := candidate
	archiveView.PackageRoot = built.Archive
	archiveTree, err := pendingPackageTree(ctx, archiveView)
	if err != nil || archiveTree != canonical {
		t.Fatalf("public143c archive framing differs: %s %s %v", archiveTree, canonical, err)
	}
	winner, err := RetainCurrentReleasePackage(ctx, competing)
	if err != nil || winner.PackageRoot != candidate.PackageRoot {
		t.Fatal("concurrent publication changed candidate", err)
	}
	releaseCompeting()
	releaseB()
	if !packageStillUsable(candidate.PackageRoot) {
		t.Fatal("published B still cleanup-owned")
	}
	ledger, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: r.ControlRoot, RuntimeRoot: filepath.Join(base, "runtime"), Owner: "existing-installer", ConsumerID: "TEST-owner"})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	_, reservation, err := publishWizardIntent(ctx, candidate, snap, ledger.RuntimeRoot, nil, []portable.Integration{portable.Cursor}, true)
	if err != nil || reservation == nil {
		t.Fatal("confirmed publication", err)
	}
	intent, err := portablesetup.ReadIntent(r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	intentBytes, err := os.ReadFile(portablesetup.IntentPath(r.ControlRoot))
	if err != nil {
		t.Fatal(err)
	}
	fresh := Request{ControlRoot: r.ControlRoot, Helper: exe, PackageFetcher: r.PackageFetcher}
	resumed, err := ResolvePendingPackage(ctx, fresh, intent)
	if err != nil || resumed.PackageRoot != candidate.PackageRoot {
		t.Fatalf("new request omitted B recovery: %+v %v", resumed, err)
	}
	archiveRetry := fresh
	archiveRetry.PackageRoot = built.Archive
	fromArchive, err := ResolvePendingPackage(ctx, archiveRetry, intent)
	if err != nil || fromArchive.PackageRoot != candidate.PackageRoot {
		t.Fatalf("explicit frozen B archive: %+v %v", fromArchive, err)
	}
	archiveRetry.PackageRoot = archive
	if _, err := ResolvePendingPackage(ctx, archiveRetry, intent); !errors.Is(err, portablesetup.ErrIntentConflict) {
		t.Fatalf("conflicting archive A accepted as B: %v", err)
	}
	wrong := fresh
	wrong.PackageRoot = shared.PackageRoot
	if _, err := ResolvePendingPackage(ctx, wrong, intent); !errors.Is(err, portablesetup.ErrIntentConflict) {
		t.Fatalf("A replaced pending B: %v", err)
	}
	wrong.PackageRoot = built.Root
	if err := os.WriteFile(filepath.Join(built.Root, "skills", "agent-notifications", "SKILL.md"), []byte("TEST-conflicting-B"), 0600); err != nil {
		t.Fatal(err)
	}
	assertPendingPackageRefusal(t, r.ControlRoot, []string{shared.PackageRoot, candidate.PackageRoot, built.Root}, func() {
		if _, err := ResolvePendingPackage(ctx, wrong, intent); !errors.Is(err, portablesetup.ErrIntentConflict) {
			t.Fatalf("wrong same-release B accepted: %v", err)
		}
	})
	// A different helper produces a valid archive at exactly the frozen version,
	// with no SourceDigest (the normal acquired-candidate intent).
	otherExe := filepath.Join(base, "TEST-other-executable")
	if err := os.WriteFile(otherExe, []byte("TEST-different-B"), 0700); err != nil {
		t.Fatal(err)
	}
	wrongB, err := portableasset.Build(portableasset.BuildRequest{Version: "1.44.0", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Executable: otherExe, OutputRoot: filepath.Join(base, "TEST-wrong-B"), Archive: filepath.Join(base, "TEST-wrong-B.zip")})
	if err != nil {
		t.Fatal(err)
	}
	wrong.PackageRoot = wrongB.Archive
	assertPendingPackageRefusal(t, r.ControlRoot, []string{shared.PackageRoot, candidate.PackageRoot, wrongB.Archive}, func() {
		if _, err := ResolvePendingPackage(ctx, wrong, intent); !errors.Is(err, portablesetup.ErrIntentConflict) {
			t.Fatalf("wrong same-release archive B accepted: %v", err)
		}
	})
	if raw, err := os.ReadFile(filepath.Join(built.Root, "skills", "agent-notifications", "SKILL.md")); err != nil || string(raw) != "TEST-conflicting-B" {
		t.Fatal("conflicting B cleaned", err)
	}
	wrong = fresh
	wrong.ReleaseVersion = "1.45.0"
	if _, err := ResolvePendingPackage(ctx, wrong, intent); !errors.Is(err, portablesetup.ErrIntentConflict) {
		t.Fatalf("revision thawed: %v", err)
	}
	afterBytes, err := os.ReadFile(portablesetup.IntentPath(r.ControlRoot))
	if err != nil || string(afterBytes) != string(intentBytes) || fetches != 8 || !packageStillUsable(shared.PackageRoot) || !packageStillUsable(candidate.PackageRoot) {
		t.Fatal("retry fetched, cleaned A/B, or replaced intent", fetches, err)
	}
	if _, release, err := StageCurrentReleasePackage(ctx, r); err == nil {
		t.Fatal("pending source reacquired")
	} else {
		release()
	}
	if !packageStillUsable(shared.PackageRoot) {
		t.Fatal("pending source removed")
	}
}

// Directory timestamps observe even a transient staging/snapshot creation and
// removal. Durable bytes alone would miss that rejected admission performed work.
func assertPendingPackageRefusal(t *testing.T, control string, sources []string, refuse func()) {
	t.Helper()
	uap := filepath.Join(filepath.Dir(control), "uap")
	for _, dir := range []string{filepath.Join(uap, "state", "tmp"), filepath.Join(uap, "acquired-source")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		stamp := time.Unix(1234567890, 0)
		if err := os.Chtimes(dir, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	capture := func() map[string]string {
		result := map[string]string{}
		for _, root := range append([]string{control, uap}, sources...) {
			err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				value := fmt.Sprint(info.Mode())
				if info.IsDir() {
					value += fmt.Sprint(info.ModTime().UnixNano())
				} else if info.Mode().IsRegular() {
					body, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					value += string(body)
				} else if info.Mode()&os.ModeSymlink != 0 {
					target, err := os.Readlink(path)
					if err != nil {
						return err
					}
					value += target
				}
				result[path] = value
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	before := capture()
	refuse()
	after := capture()
	if !reflect.DeepEqual(before, after) {
		for path, value := range after {
			if before[path] != value {
				t.Errorf("refusal mutated package/state or transient staging directory: %s", path)
			}
		}
		for path := range before {
			if _, ok := after[path]; !ok {
				t.Errorf("refusal cleaned source/state: %s", path)
			}
		}
	}
}

package installruntime

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type frozenReaderArtifact struct {
	SourcePath   string `json:"sourcePath"`
	ArtifactPath string `json:"artifactPath"`
	GitBlob      string `json:"gitBlob"`
	SHA256       string `json:"sha256"`
	Bytes        int64  `json:"bytes"`
}

type frozenReaderProvenance struct {
	SourceCommit     string                 `json:"sourceCommit"`
	SourceCommitFull string                 `json:"sourceCommitFull"`
	SourceTree       string                 `json:"sourceTree"`
	Files            []frozenReaderArtifact `json:"files"`
	Overlay          struct {
		TestPath      string `json:"testPath"`
		TestSHA256    string `json:"testSHA256"`
		TestBytes     int64  `json:"testBytes"`
		FixturePath   string `json:"fixturePath"`
		FixtureSHA256 string `json:"fixtureSHA256"`
		FixtureBytes  int64  `json:"fixtureBytes"`
	} `json:"overlay"`
}

const (
	frozenReaderRoot         = "testdata/frozen-readers"
	frozenReaderArchiveLimit = 64 << 20
	frozenReaderFileLimit    = 32 << 20
	frozenReaderEntryLimit   = 20000
)

func frozenReaderSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func readFrozenReaderProvenance(t *testing.T) frozenReaderProvenance {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(frozenReaderRoot, "provenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	var provenance frozenReaderProvenance
	if err := json.Unmarshal(data, &provenance); err != nil {
		t.Fatal(err)
	}
	if provenance.SourceCommit != "0f63de0" ||
		provenance.SourceCommitFull != "0f63de0dc409ddb849891d7c0526427612b86da5" ||
		provenance.SourceTree != "3bbfd65a8680cf5f2d9378dab9a7ba51451fc367" {
		t.Fatalf("frozen provenance changed: %+v", provenance)
	}
	if len(provenance.Files) != 4 {
		t.Fatalf("bounded frozen reader set changed: %d", len(provenance.Files))
	}
	return provenance
}

func verifyFrozenReaderBytes(t *testing.T, provenance frozenReaderProvenance) map[string][]byte {
	t.Helper()
	artifacts := map[string][]byte{}
	for _, artifact := range provenance.Files {
		data, err := os.ReadFile(artifact.ArtifactPath)
		if err != nil {
			t.Fatal(err)
		}
		if int64(len(data)) != artifact.Bytes || frozenReaderSHA256(data) != artifact.SHA256 {
			t.Fatalf("frozen artifact drifted: %s", artifact.ArtifactPath)
		}
		artifacts[artifact.SourcePath] = data
	}
	fixture, err := os.ReadFile(provenance.Overlay.FixturePath)
	if err != nil {
		t.Fatal(err)
	}
	if frozenReaderSHA256(fixture) != provenance.Overlay.FixtureSHA256 {
		t.Fatal("frozen missing-blob fixture drifted")
	}
	if int64(len(fixture)) != provenance.Overlay.FixtureBytes {
		t.Fatal("frozen missing-blob fixture size drifted")
	}
	testOverlay, err := os.ReadFile(provenance.Overlay.TestPath)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(testOverlay)) != provenance.Overlay.TestBytes || frozenReaderSHA256(testOverlay) != provenance.Overlay.TestSHA256 {
		t.Fatal("frozen reader overlay drifted")
	}
	return artifacts
}

func extractFrozenBaseline(t *testing.T, archivePath, destination string, artifacts map[string][]byte) {
	t.Helper()
	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader := tar.NewReader(file)
	var total int64
	entries := 0
	extracted := map[string][]byte{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		entries++
		if entries > frozenReaderEntryLimit {
			t.Fatal("frozen baseline archive exceeds entry bound")
		}
		name := filepath.Clean(header.Name)
		if name == "." || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			t.Fatalf("unsafe baseline archive member: %q", header.Name)
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			if len(header.PAXRecords) != 1 || header.PAXRecords["comment"] != "0f63de0dc409ddb849891d7c0526427612b86da5" {
				t.Fatal("unexpected frozen archive global metadata")
			}
			continue
		}
		target := filepath.Join(destination, name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				t.Fatal(err)
			}
		case tar.TypeReg:
			if header.Size < 0 || header.Size > frozenReaderFileLimit || total+header.Size > frozenReaderArchiveLimit {
				t.Fatal("frozen baseline archive exceeds byte bound")
			}
			total += header.Size
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				t.Fatal(err)
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			written, copyErr := io.Copy(out, io.LimitReader(reader, frozenReaderFileLimit+1))
			closeErr := out.Close()
			if copyErr != nil || closeErr != nil || written != header.Size {
				t.Fatalf("failed exact baseline member %s: %v %v", name, copyErr, closeErr)
			}
			if wanted, ok := artifacts[filepath.ToSlash(name)]; ok {
				actual, err := os.ReadFile(target)
				if err != nil {
					t.Fatal(err)
				}
				if string(actual) != string(wanted) {
					t.Fatalf("baseline source does not match checked-in artifact: %s", name)
				}
				extracted[name] = actual
			}
		case tar.TypeSymlink:
			link := filepath.Clean(header.Linkname)
			if filepath.IsAbs(link) || link == ".." || strings.HasPrefix(link, ".."+string(filepath.Separator)) {
				t.Fatalf("unsafe baseline archive link: %s -> %q", name, header.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				t.Fatal(err)
			}
			// The two frozen repository conveniences are bin symlinks. Keep
			// their exact target bytes inert on unprivileged Windows runners.
			if err := os.WriteFile(target, []byte(header.Linkname), 0600); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("unsupported baseline archive member type: %s %d", name, header.Typeflag)
		}
	}
	if len(extracted) != len(artifacts) {
		t.Fatalf("baseline did not contain every frozen artifact: %d/%d", len(extracted), len(artifacts))
	}
}

func copyFrozenOverlay(t *testing.T, source, destination string) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, data, 0600); err != nil {
		t.Fatal(err)
	}
}

// Breakage: a newly copied decoder is mistaken for historical behavior. This
// test verifies checked-in exact bytes, compiles the exact 0f63de0 archive with
// only the bounded overlay, and executes that production decoder against the
// malformed/missing-blob schema6 carrier. It requires offline module access.
func TestFrozenActualMainReaderProvenanceAndExecution(t *testing.T) {
	provenance := readFrozenReaderProvenance(t)
	artifacts := verifyFrozenReaderBytes(t, provenance)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal("git is required to prove the exact frozen baseline")
	}
	goName := "go"
	if runtime.GOOS == "windows" {
		goName += ".exe"
	}
	goTool := filepath.Join(runtime.GOROOT(), "bin", goName)
	if _, err := os.Stat(goTool); err != nil {
		t.Fatalf("Go is required for actual frozen reader execution: %v", err)
	}
	root, err := exec.Command(git, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatal(err)
	}
	archiveDir := t.TempDir()
	archivePath := filepath.Join(archiveDir, "main-0f63de0.tar")
	archive := exec.Command(git, "-C", strings.TrimSpace(string(root)), "archive", "--format=tar", "--output="+archivePath, provenance.SourceCommitFull)
	if output, err := archive.CombinedOutput(); err != nil {
		t.Fatalf("exact baseline archive failed: %v %s", err, output)
	}
	info, err := os.Stat(archivePath)
	if err != nil || info.Size() > frozenReaderArchiveLimit {
		t.Fatalf("exact baseline archive bound failed: %v", err)
	}
	baseline := filepath.Join(archiveDir, "baseline")
	if err := os.Mkdir(baseline, 0700); err != nil {
		t.Fatal(err)
	}
	extractFrozenBaseline(t, archivePath, baseline, artifacts)
	copyFrozenOverlay(t, provenance.Overlay.TestPath, filepath.Join(baseline, "internal", "installruntime", "frozen_actual_reader_overlay_test.go"))
	copyFrozenOverlay(t, provenance.Overlay.FixturePath, filepath.Join(baseline, "internal", "installruntime", "testdata", "windows6-missing-blobs.json"))

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, goTool, "test", "-v", "-mod=readonly", "-count=1", "-timeout=45s", "-run=^TestActualMain0F63RejectsWindows6BeforeBlobs$", "./internal/installruntime")
	command.WaitDelay = 5 * time.Second
	command.Dir = baseline
	command.Env = append(os.Environ(),
		"GOTOOLCHAIN=local",
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOWORK=off",
	)
	var captured frozenReaderOutput
	captured.cancel = cancel
	command.Stdout, command.Stderr = &captured, &captured
	err = command.Run()
	output := captured.Bytes()
	if err != nil {
		t.Fatalf("actual 0f63de0 reader execution failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "--- PASS: TestActualMain0F63RejectsWindows6BeforeBlobs") {
		t.Fatalf("actual frozen reader test did not prove the named boundary: %s", output)
	}
	archiveBytes, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("actual frozen reader baseline=%s archiveSHA256=%s bytes=%d", provenance.SourceCommitFull, frozenReaderSHA256(archiveBytes), info.Size())
}

// Both child streams share one bounded writer; excess output cancels the actor.
type frozenReaderOutput struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (w *frozenReaderOutput) Write(p []byte) (int, error) {
	if len(p) > 64*1024-w.Len() {
		w.cancel()
		return 0, io.ErrShortBuffer
	}
	return w.Buffer.Write(p)
}

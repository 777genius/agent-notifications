package portableasset

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/skills"
)

func TestAssetName(t *testing.T) {
	if AssetName("linux", "amd64") != "agent-notify-portable-linux-amd64.zip" {
		t.Fatal(AssetName("linux", "amd64"))
	}
}

func TestCanonicalSkillMatchesPortableLayoutCopy(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "portable-package", "skills", "agent-notify", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, skills.AgentNotify()) {
		t.Fatal("portable-package skill drifted from canonical embed")
	}
}

func TestBuildZipExtractRoundTrip(t *testing.T) {
	probe := buildProbe(t)
	base := t.TempDir()
	root := filepath.Join(base, "pkg")
	archive := filepath.Join(base, AssetName(runtime.GOOS, runtime.GOARCH))
	got, err := Build(BuildRequest{
		Version: "1.43.0", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Executable: probe, OutputRoot: root, Archive: archive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ArchiveSHA256 == "" || got.BinaryName == "" {
		t.Fatalf("%+v", got)
	}
	if err := VerifyLayout(root); err != nil {
		t.Fatal(err)
	}
	destParent := filepath.Join(base, "acquired")
	if err := os.Mkdir(destParent, 0700); err != nil {
		t.Fatal(err)
	}
	extracted, err := OpenArchive(archive, destParent, got.ArchiveSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if extracted != filepath.Join(destParent, "root") {
		t.Fatal(extracted)
	}
	if err := VerifyLayout(extracted); err != nil {
		t.Fatal(err)
	}
	wrong := strings.Repeat("0", 64)
	if _, err := OpenArchive(archive, filepath.Join(base, "bad"), wrong); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
}

func TestExtractRejectsTraversalAndExistingOutput(t *testing.T) {
	base := t.TempDir()
	zipPath := filepath.Join(base, "bad.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("../escape.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("no")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(base, "out")
	if err := Extract(zipPath, dest); err == nil || (!errors.Is(err, ErrUnsafeArchive) && !strings.Contains(err.Error(), "unsafe")) {
		t.Fatalf("want zip-slip rejection, got %v", err)
	}
	if _, err := os.Lstat(filepath.Join(base, "escape.txt")); !os.IsNotExist(err) {
		t.Fatal("zip slip wrote outside dest")
	}
	existing := filepath.Join(base, "exists")
	if err := os.Mkdir(existing, 0700); err != nil {
		t.Fatal(err)
	}
	if err := Extract(zipPath, existing); err == nil {
		t.Fatal("existing output accepted")
	}
}

func TestExtractRejectsUnexpectedEntry(t *testing.T) {
	base := t.TempDir()
	zipPath := filepath.Join(base, "extra.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("README.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("no")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Extract(zipPath, filepath.Join(base, "out")); err == nil || !errors.Is(err, ErrUnexpectedArchive) && !strings.Contains(err.Error(), "unexpected") {
		t.Fatalf("want unexpected-entry rejection, got %v", err)
	}
}

func TestBuildPackageInstallsThroughPublicInstaller(t *testing.T) {
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "pkg")
	got, err := Build(BuildRequest{
		Version: "1.43.0", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Executable: probe, OutputRoot: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(base, "uap")
	config := filepath.Join(base, "client")
	if err := os.Mkdir(config, 0700); err != nil {
		t.Fatal(err)
	}
	eng, err := uapinstaller.New(uapinstaller.Config{StateRoot: state, HelperExecutable: probe})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := eng.Prepare(t.Context(), uapinstaller.Request{
		Operation: uapinstaller.OpInstall, PackageRoot: got.Root, ClientID: "codex",
		ClientConfigRoot: config, ClientExecutable: probe,
		InstallationID: "00000000-0000-4000-8000-000000000099", OperationID: "portable-asset",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.Close() }()
	result, err := eng.Apply(t.Context(), prepared, uapinstaller.Decision{Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != uapinstaller.OutcomeCompleted {
		t.Fatalf("%+v", result)
	}
}

func TestBuildRefusesExistingRoot(t *testing.T) {
	probe := buildProbe(t)
	root := t.TempDir()
	if _, err := Build(BuildRequest{
		Version: "1.43.0", GOOS: "linux", GOARCH: "amd64",
		Executable: probe, OutputRoot: root,
	}); err == nil {
		t.Fatal("existing root accepted")
	}
}

func TestWindowsPackageUsesExeCommand(t *testing.T) {
	probe := buildProbe(t)
	root := filepath.Join(t.TempDir(), "win")
	got, err := Build(BuildRequest{
		Version: "1.43.0", GOOS: "windows", GOARCH: "amd64",
		Executable: probe, OutputRoot: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.BinaryName != "claude-notifications.exe" {
		t.Fatal(got.BinaryName)
	}
	body, err := os.ReadFile(filepath.Join(root, "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var mcp struct {
		Servers map[string]struct {
			Command string `json:"command"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(body, &mcp); err != nil {
		t.Fatal(err)
	}
	if mcp.Servers["agent-notify"].Command != "./bin/claude-notifications.exe" {
		t.Fatalf("%s", body)
	}
}

func buildProbe(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "probe.go")
	if err := os.WriteFile(src, []byte(`package main
import ("encoding/json"; "os")
func main() { json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true}) }
`), 0600); err != nil {
		t.Fatal(err)
	}
	name := "probe"
	if runtime.GOOS == "windows" {
		name = "probe.exe"
	}
	out := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", out, src)
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if body, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build probe: %s %v", body, err)
	}
	return out
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("missing caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
}

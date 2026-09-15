package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuildPortablePackageCommand(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "probe.go")
	if err := os.WriteFile(src, []byte(`package main
func main() {}
`), 0600); err != nil {
		t.Fatal(err)
	}
	name := "probe"
	if runtime.GOOS == "windows" {
		name = "probe.exe"
	}
	probe := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", probe, src)
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if body, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("probe: %s %v", body, err)
	}
	out := filepath.Join(dir, "agent-notify-portable-linux-amd64.zip")
	cmd = exec.Command("go", "run", ".", "-os", runtime.GOOS, "-arch", runtime.GOARCH, "-executable", probe, "-output", out, "-workdir", filepath.Join(dir, "pkg"), "-version", "1.43.0")
	cmd.Dir = commandDir(t)
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	body, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v", body, err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal(string(body), err)
	}
	if !strings.Contains(string(body), out) {
		t.Fatal(string(body))
	}
}

func commandDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("missing caller")
	}
	return filepath.Dir(file)
}

package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallerProgressCapabilityFailsWhenStdoutCannotBeWritten(t *testing.T) {
	for _, short := range []bool{false, true} {
		if code := installerProgressMain([]string{"--capabilities"}, capabilityFailedOutput{short: short}, nil); code == 0 {
			t.Fatalf("capability reported available after failed output (short=%v)", short)
		}
	}
}

type capabilityFailedOutput struct{ short bool }

func (w capabilityFailedOutput) Write(p []byte) (int, error) {
	if w.short {
		return len(p) - 1, nil
	}
	return 0, io.ErrClosedPipe
}

func closeFixture(t *testing.T, file *os.File) {
	t.Helper()
	if err := file.Close(); err != nil {
		t.Errorf("close fixture: %v", err)
	}
}

func TestInstallerProgressCapabilityHasNoTerminalEffects(t *testing.T) {
	errout, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = errout.Close() }()
	var stdout bytes.Buffer
	if code := installerProgressMain([]string{"--capabilities"}, &stdout, errout); code != 0 {
		t.Fatalf("capability exit %d", code)
	}
	if stdout.String() != "installer-progress-v1\n" {
		t.Fatalf("capability stdout %q", stdout.String())
	}
	info, err := errout.Stat()
	if err != nil || info.Size() != 0 {
		t.Fatal("capability touched terminal")
	}
}

func TestInstallerProgressNonTerminalFallbackKeepsStdoutClean(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events")
	if err := os.WriteFile(path, []byte("codex\tconfig\trunning\ncodex\tcomplete\tcompleted\nstop\t\t\n"), 0600); err != nil {
		t.Fatal(err)
	}
	errpath := filepath.Join(t.TempDir(), "stderr")
	errout, err := os.Create(errpath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = errout.Close() }()
	var stdout bytes.Buffer
	if code := installerProgressMain([]string{"--events", path, "--ui", "rich"}, &stdout, errout); code != 0 {
		t.Fatalf("observer exit %d", code)
	}
	out, err := os.ReadFile(errpath)
	if err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 || strings.Contains(string(out), "\x1b") || string(out) != "Codex CLI: configuration - running\nCodex CLI: complete - done\n" {
		t.Fatalf("fallback stdout=%q stderr=%q", stdout.String(), out)
	}
}

func TestInstallerSessionCapabilityAndPromotionAreSeparateCleanContracts(t *testing.T) {
	for _, tc := range []struct{ flag, expected string }{
		{"--session-capabilities", "installer-progress-session-v1\n"},
		{"--parallel-capabilities", "installer-parallel-v1\n"},
		{"--promotion", "Boost your productivity"},
	} {
		errout, err := os.CreateTemp(t.TempDir(), "stderr")
		if err != nil {
			t.Fatal(err)
		}
		defer closeFixture(t, errout)
		var out bytes.Buffer
		code := installerProgressMain([]string{tc.flag}, &out, errout)
		unsupported := tc.flag == "--parallel-capabilities" && runtime.GOOS != "darwin" && runtime.GOOS != "linux"
		if unsupported {
			if code != 1 || out.Len() != 0 {
				t.Fatalf("unqualified native cancellation advertised: exit=%d stdout=%q", code, out.String())
			}
			continue
		}
		if code != 0 {
			t.Fatalf("%s: exit %d", tc.flag, code)
		}
		if !strings.Contains(out.String(), tc.expected) || strings.Contains(out.String(), "\x1b") {
			t.Fatalf("%s: %q", tc.flag, out.String())
		}
		if strings.HasSuffix(tc.flag, "-capabilities") && out.String() != tc.expected {
			t.Fatal("nonexact capability")
		}
		info, err := errout.Stat()
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() != 0 {
			t.Fatal("machine/promotion contract touched terminal")
		}
		for _, short := range []bool{false, true} {
			if code := installerProgressMain([]string{tc.flag}, capabilityFailedOutput{short: short}, errout); code == 0 {
				t.Fatal("output failure accepted")
			}
		}
	}
}

func TestInstallerSessionRejectsInvalidPresentationBeforeObserving(t *testing.T) {
	for _, args := range [][]string{
		{"--session", "--events", "missing", "--activity", "logs"},
		{"--session", "--events", "missing", "--activity-limit", "0"},
		{"--session", "--events", "missing", "--activity-limit", "17"},
	} {
		errout, err := os.CreateTemp(t.TempDir(), "stderr")
		if err != nil {
			t.Fatal(err)
		}
		defer closeFixture(t, errout)
		var out bytes.Buffer
		if code := installerProgressMain(args, &out, errout); code != 2 || out.Len() != 0 {
			t.Fatalf("invalid presentation: %d %q", code, out.String())
		}
	}
}

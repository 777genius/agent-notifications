package config

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

const disabledDesktop = ` {"unknown":{"n":9007199254740993}, "notifications":{"desktop":{"enabled":false,"sound":false,"clickToFocus":false}}} `

func prepPaths(t *testing.T) (string, string, string) {
	t.Helper()
	d := t.TempDir()
	return filepath.Join(d, "new", "config.json"), filepath.Join(d, "legacy.json"), filepath.Join(d, "defaults.json")
}
func putPrep(t *testing.T, p, s string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(s), 0640); e != nil {
		t.Fatal(e)
	}
}
func TestPreparePrecedence(t *testing.T) {
	for _, source := range []string{"canonical", "legacy", "defaults"} {
		t.Run(source, func(t *testing.T) {
			c, l, d := prepPaths(t)
			putPrep(t, d, `{}`)
			if source != "defaults" {
				putPrep(t, l, disabledDesktop)
			}
			if source == "canonical" {
				putPrep(t, c, disabledDesktop)
				putPrep(t, l, `broken`)
			}
			r, e := PrepareGlobalConfig(context.Background(), c, l, d)
			if e != nil || !r.Ready || r.Source != source || r.Changed != (source != "canonical") {
				t.Fatalf("%+v %v", r, e)
			}
			b, e := os.ReadFile(c)
			if e != nil {
				t.Fatal(e)
			}
			if source != "defaults" && string(b) != disabledDesktop {
				t.Fatalf("bytes changed: %s", b)
			}
			if _, _, _, e = ValidateGlobalDesktop(b); e != nil {
				t.Fatal(e)
			}
			if source != "canonical" {
				i, _ := os.Stat(c)
				if i.Mode().Perm() != 0600 {
					t.Fatal(i.Mode())
				}
			}
		})
	}
}
func TestPreparePartial(t *testing.T) {
	c, l, d := prepPaths(t)
	putPrep(t, c, `{"n":9007199254740993,"notifications":{"other":[null,1],"desktop":{"enabled":false,"x":"kept"}}}`)
	r, e := PrepareGlobalConfig(context.Background(), c, l, d)
	if e != nil || !r.Changed {
		t.Fatal(r, e)
	}
	b, _ := os.ReadFile(c)
	a, s, f, e := ValidateGlobalDesktop(b)
	if e != nil || a || !s || !f || !strings.Contains(string(b), `9007199254740993`) || !strings.Contains(string(b), `"other":[null,1]`) || !strings.Contains(string(b), `"x":"kept"`) {
		t.Fatal(string(b), e)
	}
	i, _ := os.Stat(c)
	if i.Mode().Perm() != 0640 {
		t.Fatal(i.Mode())
	}
}
func TestPrepareInvalidUnchanged(t *testing.T) {
	for _, bad := range []string{`broken`, `null`, `{"notifications":null}`, `{"notifications":{"desktop":null}}`, `{"notifications":{"desktop":{"sound":null}}}`, `{"a":1,"a":2}`, `{"notifications":{"desktop":{"enabled":1}}}`, `{"x":"` + strings.Repeat("x", 65536) + `"}`, `{"x":` + strings.Repeat("[", 17) + `0` + strings.Repeat("]", 17) + `}`, `{"x":[` + strings.Repeat("0,", 1024) + `0]}`} {
		for _, where := range []string{"canonical", "legacy"} {
			t.Run(where, func(t *testing.T) {
				c, l, d := prepPaths(t)
				p := c
				if where == "legacy" {
					p = l
				}
				putPrep(t, p, bad)
				putPrep(t, d, disabledDesktop)
				if _, e := PrepareGlobalConfig(context.Background(), c, l, d); e == nil {
					t.Fatal("accepted")
				}
				b, _ := os.ReadFile(p)
				if string(b) != bad {
					t.Fatal("clobbered")
				}
				if where == "legacy" {
					if _, e := os.Stat(c); !os.IsNotExist(e) {
						t.Fatal("published")
					}
				}
			})
		}
	}
}
func TestPrepareMissingAndSymlinks(t *testing.T) {
	c, l, d := prepPaths(t)
	if _, e := PrepareGlobalConfig(context.Background(), c, l, d); e == nil {
		t.Fatal("missing accepted")
	}
	putPrep(t, d, disabledDesktop)
	if e := os.Symlink(d, l); e != nil {
		t.Fatal(e)
	}
	if _, e := PrepareGlobalConfig(context.Background(), c, l, d); e == nil {
		t.Fatal("legacy symlink accepted")
	}
	os.Remove(l)
	if e := os.Symlink(d, c); e != nil {
		t.Fatal(e)
	}
	if _, e := PrepareGlobalConfig(context.Background(), c, l, d); e == nil {
		t.Fatal("canonical symlink accepted")
	}
	os.Remove(c)
	os.Rename(filepath.Dir(c), filepath.Dir(c)+"-old")
	if e := os.Symlink(filepath.Dir(d), filepath.Dir(c)); e != nil {
		t.Fatal(e)
	}
	if _, e := PrepareGlobalConfig(context.Background(), c, l, d); e == nil {
		t.Fatal("parent symlink accepted")
	}
}
func TestPrepareParentSubstitution(t *testing.T) {
	c, _, _ := prepPaths(t)
	r, e := physicalParent(c, true)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	os.Rename(filepath.Dir(c), filepath.Dir(c)+"-old")
	os.Mkdir(filepath.Dir(c), 0700)
	if samePreparedParent(r, c) == nil {
		t.Fatal("substitution accepted")
	}
}
func TestPrepareManagedDisableWins(t *testing.T) {
	c, l, d := prepPaths(t)
	putPrep(t, c, `{}`)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	unlock, e := installruntime.Lock(ctx, c+".lock")
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := PrepareGlobalConfig(ctx, c, l, d); done <- e }()
	putPrep(t, c, disabledDesktop)
	unlock()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(c)
	if string(b) != disabledDesktop {
		t.Fatal("disable lost")
	}
}
func TestPrepareProcess(t *testing.T) {
	if os.Getenv("PREP_CHILD") == "" {
		return
	}
	_, e := PrepareGlobalConfig(context.Background(), os.Getenv("PREP_C"), os.Getenv("PREP_L"), os.Getenv("PREP_D"))
	if e != nil {
		t.Fatal(e)
	}
}
func TestPrepareTwoProcess(t *testing.T) {
	c, l, d := prepPaths(t)
	putPrep(t, d, disabledDesktop)
	var cmds []*exec.Cmd
	for i := 0; i < 2; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestPrepareProcess$")
		cmd.Env = append(os.Environ(), "PREP_CHILD=1", "PREP_C="+c, "PREP_L="+l, "PREP_D="+d)
		if e := cmd.Start(); e != nil {
			t.Fatal(e)
		}
		cmds = append(cmds, cmd)
	}
	for _, cmd := range cmds {
		if e := cmd.Wait(); e != nil {
			t.Fatal(e)
		}
	}
	b, _ := os.ReadFile(c)
	if string(b) != disabledDesktop {
		t.Fatal(string(b))
	}
}

func TestPrepareOmittedContainersAndStrictValidator(t *testing.T) {
	for _, raw := range []string{`{}`, `{"notifications":{}}`, `{"notifications":{"desktop":{}}}`} {
		c, l, d := prepPaths(t)
		putPrep(t, d, raw)
		if _, _, _, e := ValidateGlobalDesktop([]byte(raw)); e == nil {
			t.Fatal("incomplete runtime config accepted")
		}
		if _, e := PrepareGlobalConfig(context.Background(), c, l, d); e != nil {
			t.Fatal(e)
		}
		b, _ := os.ReadFile(c)
		a, s, f, e := ValidateGlobalDesktop(b)
		if e != nil || !a || !s || !f {
			t.Fatal(string(b), e)
		}
	}
}

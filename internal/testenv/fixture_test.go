package testenv

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	code := m.Run()
	if err := CleanupFixtures(); err != nil {
		fmt.Fprintln(os.Stderr, "remove TEST fixture compiler cache:", err)
		code = 1
	}
	os.Exit(code)
}

func TestGoFixturePrivateCopiesAndSourceIdentity(t *testing.T) {
	program := `package main; import("fmt";"os");func main(){fmt.Print(os.Getenv("TEST_FIXTURE_VALUE"))}`
	first := GoFixture(t, program)
	if err := os.WriteFile(first, []byte("corrupted private fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	second := GoFixture(t, program)
	if first == second || filepath.Dir(first) == filepath.Dir(second) {
		t.Fatal("fixture executable paths are shared")
	}
	command := exec.Command(second)
	command.Env = append(Env(t, t.TempDir()), "TEST_FIXTURE_VALUE=second private profile")
	if body, err := command.CombinedOutput(); err != nil || string(body) != "second private profile" {
		t.Fatalf("mutating first fixture affected independent executable/profile: %s %v", body, err)
	}
	third := GoFixture(t, `package main; import "fmt"; func main(){fmt.Print("different source")}`)
	command = exec.Command(third)
	command.Env = Env(t, t.TempDir())
	if body, err := command.CombinedOutput(); err != nil || string(body) != "different source" {
		t.Fatalf("distinct fixture source reused another image: %s %v", body, err)
	}
}

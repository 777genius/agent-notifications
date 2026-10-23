package testenv

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// Fixture images are immutable process-local bytes, never shared executable
// paths or hardlinks. Each caller owns an independently writable private copy.
var goFixtures = struct {
	sync.Mutex
	images map[[32]byte][]byte
}{images: make(map[[32]byte][]byte)}

// Capture the process temp root before tests install private TMPDIR values.
var fixtureTempRoot = os.TempDir()

var fixtureBuildCache struct {
	once sync.Once
	root string
	err  error
}

func fixtureCache(t *testing.T) string {
	t.Helper()
	fixtureBuildCache.once.Do(func() {
		fixtureBuildCache.root, fixtureBuildCache.err = os.MkdirTemp(fixtureTempRoot, "TEST-fixture-go-cache-")
	})
	if fixtureBuildCache.err != nil {
		t.Fatal(fixtureBuildCache.err)
	}
	return fixtureBuildCache.root
}

// FixtureBuild shares only an owned compiler cache across fixture builds. All
// runtime paths remain private. Packages using it must call CleanupFixtures
// after m.Run, so no test's cleanup can remove a later fixture's cache.
func FixtureBuild(t *testing.T, root string) []string {
	t.Helper()
	environment := Build(t, root)
	if _, exists := os.LookupEnv("GOCACHE"); !exists {
		environment = append(environment, "GOCACHE="+fixtureCache(t))
	}
	return environment
}

// CleanupFixtures removes only the process-owned compiler cache after all tests.
func CleanupFixtures() error {
	if fixtureBuildCache.root != "" {
		return os.RemoveAll(fixtureBuildCache.root)
	}
	return nil
}

// GoFixture compiles finite, standard-library-only TEST programs once for the
// exact source and explicit compiler settings. Runtime HOME/profiles stay private
// and build children receive the same allowlist as other testenv builds.
func GoFixture(t *testing.T, program string) string {
	t.Helper()
	toolchain := make(map[string]string)
	for _, key := range []string{"GOROOT", "GOPATH", "GOMODCACHE", "GOCACHE", "GOTMPDIR", "GOPROXY", "GOSUMDB", "GOMAXPROCS"} {
		if value, ok := os.LookupEnv(key); ok {
			toolchain[key] = value
		}
	}
	compiler := filepath.Join(runtime.GOROOT(), "bin", "go")
	settings, err := json.Marshal(struct {
		Source, Compiler, Version, OS, Arch string
		Environment                         map[string]string
	}{program, compiler, runtime.Version(), runtime.GOOS, runtime.GOARCH, toolchain})
	if err != nil {
		t.Fatal(err)
	}
	key := sha256.Sum256(settings)
	var image []byte
	func() {
		goFixtures.Lock()
		defer goFixtures.Unlock()
		var found bool
		image, found = goFixtures.images[key]
		if !found {
			image, err = compileGoFixture(t, compiler, program, toolchain)
			if err == nil {
				goFixtures.images[key] = image
			}
		}
	}()
	if err != nil {
		t.Fatal(err)
	}
	name := "fixture"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, image, 0700); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(path)
	if err != nil || sha256.Sum256(actual) != sha256.Sum256(image) {
		t.Fatalf("materialized TEST executable differs from immutable fixture: %v", err)
	}
	return path
}

func compileGoFixture(t *testing.T, compiler, program string, toolchain map[string]string) ([]byte, error) {
	t.Helper()
	dir := t.TempDir()
	source, output := filepath.Join(dir, "fixture.go"), filepath.Join(dir, "fixture")
	if err := os.WriteFile(source, []byte(program), 0600); err != nil {
		return nil, err
	}
	command := exec.Command(compiler, "build", "-p", "2", "-buildvcs=false", "-o", output, source)
	command.Env = append(Env(t, filepath.Join(dir, "build-home")), "GOWORK=off", "GOENV=off", "GOTOOLCHAIN=local", "CGO_ENABLED=0")
	if _, explicit := toolchain["GOCACHE"]; !explicit {
		command.Env = append(command.Env, "GOCACHE="+fixtureCache(t))
	}
	for key, value := range toolchain {
		command.Env = append(command.Env, key+"="+value)
	}
	// Named source compilation still walks the current module's go.mod. These
	// fixtures only import stdlib, and must not depend on caller module state.
	command.Dir = dir
	if body, err := command.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("compile finite TEST executable: %w: %s", err, strings.TrimSpace(string(body)))
	}
	return os.ReadFile(output)
}

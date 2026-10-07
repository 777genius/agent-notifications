package opencodeplugin

import (
	"bytes"
	"path/filepath"
	"strconv"
	"testing"
)

func TestRenderBindsOnlyAbsoluteExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), `owned "binary"`)
	root := filepath.Join(t.TempDir(), `control "root"`)
	got, err := Render(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got, []byte(executableToken)) || bytes.Contains(got, []byte(controlRootToken)) ||
		!bytes.Contains(got, []byte(strconv.Quote(path))) || !bytes.Contains(got, []byte(strconv.Quote(root))) {
		t.Fatal("bundle did not bind the requested paths exactly")
	}
	for _, path := range []string{"relative", t.TempDir() + "/../dirty", ""} {
		if _, err := Render(path, root); err == nil {
			t.Fatalf("accepted invalid path %q", path)
		}
		if _, err := Render(root, path); err == nil {
			t.Fatalf("accepted invalid root %q", path)
		}
	}
}

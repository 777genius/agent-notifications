package opencodeplugin

import (
	"bytes"
	"path/filepath"
	"strconv"
	"testing"
)

func TestRenderBindsOnlyAbsoluteExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), `owned "binary"`)
	got, err := Render(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got, []byte(executableToken)) || !bytes.Contains(got, []byte(strconv.Quote(path))) {
		t.Fatal("bundle did not bind the requested executable exactly")
	}
	for _, path := range []string{"relative", t.TempDir() + "/../dirty", ""} {
		if _, err := Render(path); err == nil {
			t.Fatalf("accepted invalid path %q", path)
		}
	}
}

package cursorinstall

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/pathpolicy"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/nativeconfig"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
)

// Regression: Darwin uses the plist-specific owner for hooks.json, loses foreign
// bytes/mode, or accepts a stale frozen basis. This is real file mechanics only.
func TestDarwinCursorRegistrationPreservesForeignAndRejectsStaleBasis(t *testing.T) {
	fixed, _ := refusalFixture(t)
	kernel := nativeconfig.New()
	a, err := New(kernel, pathpolicy.Policy{}, &fixed)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(fixed.ProfileRoot, "hooks.json")
	foreign := []byte(`{"version":1,"foreign":{"raw":1e+02},"hooks":{"stop":[{"command":"TEST-foreign"}]}}`)
	if err := os.WriteFile(path, foreign, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	selected, err := a.plan("sha256:"+strings.Repeat("1", 64), nil)
	if err != nil {
		t.Fatal(err)
	}
	selected, err = selected.WithProjectionDigest("sha256:" + strings.Repeat("2", 64))
	if err != nil {
		t.Fatal(err)
	}
	// A foreign edit after Plan must be preserved and refuse the old packet.
	drift := append(append([]byte{}, foreign...), '\n')
	if err := os.WriteFile(path, drift, 0600); err != nil {
		t.Fatal(err)
	}
	_, effect, err := a.applyRegistration(context.Background(), kernel, selected, nil)
	if err == nil || effect != domain.NativeEffectUnchanged {
		t.Fatalf("stale basis admitted: effect=%s error=%v", effect, err)
	}
	if body, err := os.ReadFile(path); err != nil || !bytes.Equal(body, drift) {
		t.Fatalf("stale refusal modified foreign bytes: %v", err)
	}
	selected, err = a.plan("sha256:"+strings.Repeat("1", 64), nil)
	if err != nil {
		t.Fatal(err)
	}
	selected, err = selected.WithProjectionDigest("sha256:" + strings.Repeat("2", 64))
	if err != nil {
		t.Fatal(err)
	}
	objects, effect, err := a.applyRegistration(context.Background(), kernel, selected, nil)
	if err != nil || effect != domain.NativeEffectCommitted || len(objects) != 1 {
		t.Fatalf("Darwin registration: effect=%s objects=%d error=%v", effect, len(objects), err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("registration changed mode: %v", err)
	}
	if err := a.remove(context.Background(), kernel, path, pureReceipt(objects[0].CursorReceipt)); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(path); err != nil || !bytes.Equal(body, drift) {
		t.Fatalf("removal lost foreign bytes: %v", err)
	}
}

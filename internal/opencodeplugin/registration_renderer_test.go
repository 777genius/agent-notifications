package opencodeplugin

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestRegistrationRendererIndependentLiteralBoundary(t *testing.T) {
	// Independent JS asset and exact rendered golden; not the generated bundle.
	const asset = `export default { executable:"__AGENT_NOTIFICATIONS_EXECUTABLE__", root:"__AGENT_NOTIFICATIONS_CONTROL_ROOT__", origin:"__AGENT_NOTIFICATIONS_ORIGIN__" };`
	origin := strings.Repeat("12", 32)
	const want = `export default { executable:"/owned/quoted \"binary\"", root:"/control/root", origin:"1212121212121212121212121212121212121212121212121212121212121212" };`
	got, err := renderRegistration(asset, `/owned/quoted "binary"`, "/control/root", origin)
	if err != nil || string(got) != want {
		t.Fatalf("renderer boundary: %s %v", got, err)
	}
	// Digest consumed by E1 is of these final bytes, not unbound source.
	sum := sha256.Sum256(got)
	if hex.EncodeToString(sum[:]) != "5abb5664a64d9d700d43a758f221f55b91ee4ec4957a0acf0d7cf1a607abaade" {
		t.Fatal("rendered E1 bundle digest changed")
	}
	for _, invalid := range []string{asset + `"__AGENT_NOTIFICATIONS_ORIGIN__"`, strings.Replace(asset, `"__AGENT_NOTIFICATIONS_ORIGIN__"`, "null", 1), strings.Replace(asset, `"__AGENT_NOTIFICATIONS_EXECUTABLE__"`, "null", 1), strings.Replace(asset, `"__AGENT_NOTIFICATIONS_CONTROL_ROOT__"`, "null", 1)} {
		if _, err := renderRegistration(invalid, "/owned/binary", "/control/root", origin); err == nil {
			t.Fatal("incomplete/duplicate asset origin-bound")
		}
	}
	for _, bad := range []string{"", strings.Repeat("a", 63), strings.Repeat("A", 64), strings.Repeat("g", 64)} {
		if _, err := renderRegistration(asset, "/owned/binary", "/control/root", bad); err == nil {
			t.Fatal("invalid origin rendered")
		}
	}
	for _, bad := range []string{"relative", "/owned/../dirty", "/owned/\x00binary"} {
		if _, err := renderRegistration(asset, bad, "/control/root", origin); err == nil {
			t.Fatal("invalid executable rendered")
		}
		if _, err := renderRegistration(asset, "/owned/binary", bad, origin); err == nil {
			t.Fatal("invalid root rendered")
		}
	}
}
func TestRegistrationRendererCurrentTwoTokenAssetFailsClosed(t *testing.T) {
	if _, err := (RegistrationRenderer{}).RenderRegistration("/owned/binary", "/control/root", strings.Repeat("11", 32)); err == nil {
		t.Fatal("old generated asset became origin-bound")
	}
	if _, err := Render("/owned/binary", "/control/root"); err != nil {
		t.Fatal("legacy placement compatibility lost", err)
	}
}

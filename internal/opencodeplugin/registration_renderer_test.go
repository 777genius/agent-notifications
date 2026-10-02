package opencodeplugin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

func TestRegistrationRendererIndependentLiteralBoundary(t *testing.T) {
	// Independent JS asset and exact rendered golden; not the generated bundle.
	const asset = `export default { executable:"__AGENT_NOTIFICATIONS_EXECUTABLE__", root:"__AGENT_NOTIFICATIONS_CONTROL_ROOT__", origin:"__AGENT_NOTIFICATIONS_ORIGIN__" };`
	origin := strings.Repeat("12", 32)
	executable, quotedExecutable, controlRoot := registrationRendererFixturePaths()
	want := `export default { executable:"/owned/quoted \"binary\"", root:"/control/root", origin:"1212121212121212121212121212121212121212121212121212121212121212" };`
	wantDigest := "5abb5664a64d9d700d43a758f221f55b91ee4ec4957a0acf0d7cf1a607abaade"
	if runtime.GOOS == "windows" {
		want = `export default { executable:"C:\\owned\\quoted binary.exe", root:"C:\\control\\root", origin:"1212121212121212121212121212121212121212121212121212121212121212" };`
		wantDigest = "5f48e2e61be48fd1423ff3fd0e857657072ff0d69e7f5a8615ef46c9ea78334b"
	}
	got, err := renderRegistration(asset, quotedExecutable, controlRoot, origin)
	if err != nil || string(got) != want {
		t.Fatalf("renderer boundary: %s %v", got, err)
	}
	// Digest consumed by E1 is of these final bytes, not unbound source.
	sum := sha256.Sum256(got)
	if hex.EncodeToString(sum[:]) != wantDigest {
		t.Fatal("rendered E1 bundle digest changed")
	}
	for _, invalid := range []string{asset + `"__AGENT_NOTIFICATIONS_ORIGIN__"`, strings.Replace(asset, `"__AGENT_NOTIFICATIONS_ORIGIN__"`, "null", 1), strings.Replace(asset, `"__AGENT_NOTIFICATIONS_EXECUTABLE__"`, "null", 1), strings.Replace(asset, `"__AGENT_NOTIFICATIONS_CONTROL_ROOT__"`, "null", 1)} {
		if _, err := renderRegistration(invalid, executable, controlRoot, origin); err == nil {
			t.Fatal("incomplete/duplicate asset origin-bound")
		}
	}
	for _, bad := range []string{"", strings.Repeat("a", 63), strings.Repeat("A", 64), strings.Repeat("g", 64)} {
		if _, err := renderRegistration(asset, executable, controlRoot, bad); err == nil {
			t.Fatal("invalid origin rendered")
		}
	}
	badPaths := []string{"relative", "/owned/../dirty", "/owned/\x00binary"}
	if runtime.GOOS == "windows" {
		badPaths = []string{"relative", `C:\owned\..\dirty`, "C:\\owned\\\x00binary"}
	}
	for _, bad := range badPaths {
		if _, err := renderRegistration(asset, bad, controlRoot, origin); err == nil {
			t.Fatal("invalid executable rendered")
		}
		if _, err := renderRegistration(asset, executable, bad, origin); err == nil {
			t.Fatal("invalid root rendered")
		}
	}
}
func TestRegistrationRendererLegacyTwoTokenAssetFailsClosed(t *testing.T) {
	const asset = `export default { executable:"__AGENT_NOTIFICATIONS_EXECUTABLE__", root:"__AGENT_NOTIFICATIONS_CONTROL_ROOT__" };`
	executable, _, controlRoot := registrationRendererFixturePaths()
	if _, err := renderRegistration(asset, executable, controlRoot, strings.Repeat("11", 32)); err == nil {
		t.Fatal("legacy two-token asset became origin-bound")
	}
	if _, err := Render(executable, controlRoot); err != nil {
		t.Fatal("legacy placement compatibility lost", err)
	}
}

// Red if the actual embedded asset loses any owned registration binding.
func TestRegistrationRendererActualAssetRetainsOwnedBindings(t *testing.T) {
	executable, _, controlRoot := registrationRendererFixturePaths()
	origin := strings.Repeat("34", 32)
	got, err := (RegistrationRenderer{}).RenderRegistration(executable, controlRoot, origin)
	if err != nil {
		t.Fatal("actual asset failed owned registration", err)
	}
	if bytes.Contains(got, []byte("__AGENT_NOTIFICATIONS_")) {
		t.Fatal("actual asset retained an unbound registration token")
	}
	for _, value := range []string{executable, controlRoot, origin} {
		encoded, err := json.Marshal(value)
		if err != nil || bytes.Count(got, encoded) != 1 {
			t.Fatal("actual asset lost or duplicated an owned binding")
		}
	}
}

func registrationRendererFixturePaths() (executable, quotedExecutable, controlRoot string) {
	if runtime.GOOS == "windows" {
		return `C:\owned\binary`, `C:\owned\quoted binary.exe`, `C:\control\root`
	}
	return "/owned/binary", `/owned/quoted "binary"`, "/control/root"
}

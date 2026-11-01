package config

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/opencodeplugin"
)

// A Windows callback upgrade may retain a valid OpenCode consumer while the
// shared owner ledger moves from v4 to v6. Managed editor admission must still
// verify the exact origin and rendered owned plugin rather than reject the floor.
func TestManagedOpenCodeReaderAcceptsKnownWindowsLedgerPromotion(t *testing.T) {
	root := filepath.Join(t.TempDir(), "control")
	runtimeRoot := filepath.Join(t.TempDir(), "runtime")
	executable := filepath.Join(runtimeRoot, "claude-notifications-"+runtime.GOOS+"-"+runtime.GOARCH)
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	plugin := filepath.Join(root, "agent-notifications.js")
	registration := &installruntime.OpenCodeRegistration{
		Origin: strings.Repeat("a", 64), Salt: strings.Repeat("b", 64),
		Namespace: strings.Repeat("c", 64), OriginBound: true,
	}
	rendered, err := (opencodeplugin.RegistrationRenderer{}).RenderRegistration(executable, root, registration.Origin)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(rendered)
	registration.BundleSHA256 = hex.EncodeToString(sum[:])
	if !registration.Valid() {
		t.Fatal("fixture registration invalid")
	}
	consumer := installruntime.Consumer{
		RuntimeRoot: runtimeRoot, Registration: plugin,
		Commands: []string{executable, "opencode-event", "--protocol", "1"},
		OpenCode: registration,
	}
	l := installruntime.Ledger{
		ID: "TEST-managed", Owner: "existing-installer", RuntimeRoot: runtimeRoot,
		Schema: 4, WriterFloor: installruntime.OpenCodeWriterFloor, PolicyGeneration: 1,
		Consumers: map[string]installruntime.Consumer{managedOpenCodeConsumer: consumer},
		Files: map[string]installruntime.Identity{
			executable: {Exists: true},
			plugin: {Exists: true, SHA256: registration.BundleSHA256},
		},
	}
	snapshot := installruntime.PolicySnapshot{
		Preimage: installruntime.Identity{Exists: true},
		Installation: installruntime.InstalledSnapshot{Ledger: l},
	}
	if !validManagedOpenCode(root, snapshot) {
		t.Fatal("existing schema4/floor4 registration refused")
	}
	snapshot.Installation.Ledger.Schema = installruntime.WindowsGenerationLedgerSchema
	snapshot.Installation.Ledger.WriterFloor = installruntime.WindowsGenerationWriterFloor
	if !validManagedOpenCode(root, snapshot) {
		t.Fatal("compatible Windows schema6/floor6 registration refused")
	}
	for _, floor := range []int{4, 5, 7} {
		snapshot.Installation.Ledger.WriterFloor = floor
		if validManagedOpenCode(root, snapshot) {
			t.Fatalf("mismatched Windows writer floor %d admitted", floor)
		}
	}
	snapshot.Installation.Ledger.WriterFloor = installruntime.WindowsGenerationWriterFloor
	// A pending reservation cannot be committed by the ordinary managed editor;
	// refuse it before showing an editable document, even at a compatible floor.
	snapshot.Installation.Ledger.PendingMutation = &installruntime.PendingMutation{}
	if validManagedOpenCode(root, snapshot) {
		t.Fatal("managed editor admitted an unresolved owner reservation")
	}
	snapshot.Installation.Ledger.PendingMutation = nil
	registration.OriginBound = false
	if validManagedOpenCode(root, snapshot) {
		t.Fatal("Windows floor promotion bypassed OpenCode origin binding")
	}
}

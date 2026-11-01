package piauthority

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/pisource"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/nativeconfig"
)

const RuntimeFile = "pi-runtime.json"

// Runtime is derived discovery data. Only the consumer, UAP receipts and policy
// authorize an event; publishing this file does not grant an effect lease.
type Runtime struct {
	SchemaVersion  int               `json:"schema_version"`
	Locator        string            `json:"locator"`
	Executable     string            `json:"executable"`
	PrimarySHA256  string            `json:"primary_sha256"`
	LoadedRevision pisource.Revision `json:"loaded_revision"`
}

// Publish runs in the common engine's committed-binding handoff, after the
// portable consumer and locator commit and before native activation. A retry
// reconstructs it from the same authoritative receipts.
func Publish(ctx context.Context, b portable.Binding, path string) (err error) {
	snapshot, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		return err
	}
	revision, _, err := installedRevision(ctx, b, path, snapshot, false)
	if err != nil {
		return err
	}
	name, err := b.Filename()
	if err != nil {
		return err
	}
	read, err := portable.ReadPiBinding(filepath.Join(b.DataRoot, name))
	if err != nil || read != b {
		return ErrDenied
	}
	executable, err := portable.ResolvePrimaryExecutable(snapshot.Ledger, b.Primary)
	if err != nil {
		return err
	}
	wanted := Runtime{SchemaVersion: 1, Locator: name, Executable: executable, PrimarySHA256: revision.PrimarySHA256, LoadedRevision: revision}
	body, err := json.Marshal(wanted)
	if err != nil {
		return err
	}
	file, err := nativeconfig.New().BeginExactFile(filepath.Join(b.DataRoot, RuntimeFile))
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
	}()
	previous := file.Original()
	if previous.Exists {
		var old Runtime
		decoder := json.NewDecoder(bytes.NewReader(previous.Body))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&old) != nil {
			return ErrDenied
		}
		canonical, marshalErr := json.Marshal(old)
		if marshalErr != nil || !bytes.Equal(canonical, previous.Body) || old.SchemaVersion != 1 || old.Locator != name || old.Executable != executable || old.LoadedRevision.BindingID != b.BindingID || old.LoadedRevision.RuntimeRoot != b.RuntimeRoot || old.LoadedRevision.BootstrapVersion != revision.BootstrapVersion || old.PrimarySHA256 != old.LoadedRevision.PrimarySHA256 {
			return ErrDenied
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return file.Apply(body)
}

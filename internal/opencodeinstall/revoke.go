package opencodeinstall

import (
	"context"
	"encoding/json"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// RevokeChannels turns off only OpenCode's two channel consents. It is the
// first removal step and remains available when a delivery asset is missing or
// damaged; cleanup may report its own conflict afterward.
func RevokeChannels(ctx context.Context, controlRoot, runtimeRoot string) error {
	s, err := installruntime.ReadRevocationSnapshot(ctx, controlRoot)
	if err != nil {
		return err
	}
	_, err = installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: controlRoot, RuntimeRoot: runtimeRoot,
		Owner: "existing-installer", ConsumerID: consumerID,
		RefreshOnly: true, PolicyOnly: true, RevokeOpenCode: true,
		ExpectedGeneration: &s.Generation, ExpectedPolicy: &s.Preimage,
		PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"openCodeNotifications":{"desktop":false,"webhook":false}}`)},
	})
	return err
}

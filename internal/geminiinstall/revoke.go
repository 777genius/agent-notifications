// Package geminiinstall owns the explicit Gemini notification lifecycle.
package geminiinstall

import (
	"context"
	"encoding/json"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// RevokeChannels is the first removal step, before reading the receipt, native
// settings or delivery assets. Cleanup can then retain a foreign-edit conflict
// without leaving the old installed command authorized to deliver.
func RevokeChannels(ctx context.Context, controlRoot, runtimeRoot string) error {
	s, err := installruntime.ReadRevocationSnapshot(ctx, controlRoot)
	if err != nil {
		return err
	}
	_, err = installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: controlRoot, RuntimeRoot: runtimeRoot,
		Owner: "existing-installer", ConsumerID: consumerID,
		RefreshOnly: true, PolicyOnly: true, RevokeGemini: true,
		ExpectedGeneration: &s.Generation, ExpectedPolicy: &s.Preimage,
		PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"geminiNotifications":{"desktop":false,"webhook":false}}`)},
	})
	return err
}

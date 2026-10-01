package geminiinstall

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Inspection reports managed registration and recorded channel consent. It does
// not claim native hook activation, OS permission or destination readiness.
type Inspection struct {
	Status     string `json:"status"`
	Registered bool   `json:"registered"`
	Desktop    bool   `json:"desktop"`
	Webhook    bool   `json:"webhook"`
}

// Inspect never creates locks, repairs assets, recovers transactions or invokes
// a native helper. A damaged registration remains a conflict, even if revoked.
func Inspect(ctx context.Context, r Request) (Inspection, error) {
	result := Inspection{Status: "conflict"}
	if !filepath.IsAbs(r.ControlRoot) {
		return result, errors.New("absolute control root required")
	}
	root, err := installruntime.CanonicalPath(r.ControlRoot)
	if err != nil {
		return result, err
	}
	l, recovery, err := installruntime.ReadOwnership(root)
	if err != nil {
		return result, err
	}
	if recovery {
		result.Status = "recovery-required"
		return result, nil
	}
	if _, ok := l.Consumers[consumerID]; !ok {
		result.Status = "absent"
		return result, nil
	}
	s, err := installruntime.ReadPolicySnapshot(ctx, root)
	if err != nil {
		return result, err
	}
	if s.Installation.Recovery {
		result.Status = "recovery-required"
		return result, nil
	}
	c := s.Installation.Ledger.Consumers[consumerID]
	if len(c.Commands) == 0 || !RegisteredFromSnapshot(s, root, c.Commands[0], "", r.GOOS, r.GOARCH) {
		return result, errors.New("Gemini registration is missing or changed")
	}
	receipt, _, err := readReceipt(root, s.Installation.Ledger)
	if err != nil {
		return result, err
	}
	result.Desktop, result.Webhook = ChannelsFromSnapshot(s, root, c.Commands[0], receipt.Binding, r.GOOS, r.GOARCH)
	if !SnapshotCurrent(root, s) {
		return Inspection{Status: "conflict"}, errors.New("Gemini installation changed during inspection")
	}
	result.Status, result.Registered = "installed", true
	return result, ctx.Err()
}

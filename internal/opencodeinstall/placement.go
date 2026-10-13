package opencodeinstall

import (
	"errors"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/installruntime"
	uap "github.com/777genius/plugin-kit-ai/install/integrationctl/opencodeplugin"
)

// Preserve the published ownership key when a frozen canonical destination
// differs only by Darwin's qualified root-owned /tmp, /var or /etc spelling.
// Arbitrary directory or leaf symlinks must never establish this equivalence.
func placementInput(r Request, ledger installruntime.Ledger, digest string) (uap.Input, error) {
	input := uap.Input{HomeDir: r.HomeDir, XDGConfigHome: r.XDGConfigHome, Override: r.OpenCodeConfigDir, FileName: pluginName, DesiredSHA256: digest}
	base, err := uap.Plan(input)
	if err != nil {
		return input, err
	}
	previous, registered := ledger.Consumers[consumerID]
	if !registered || previous.Registration == base.Target {
		return input, nil
	}
	oldTarget, err := installruntime.PhysicalPath(previous.Registration)
	if err != nil {
		return input, err
	}
	target, err := installruntime.PhysicalPath(base.Target)
	if err != nil {
		return input, err
	}
	if oldTarget != target {
		return input, errors.New("OpenCode config root changed; use the original setup environment")
	}
	input.Override = filepath.Dir(filepath.Dir(previous.Registration))
	return input, nil
}

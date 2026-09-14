package runtime

import (
	"github.com/777genius/agent-notifications/internal/config"
	"os"
)

// GlobalConfiguration is an independent read-only observation, including while
// delivery is disabled. An absent boolean means no valid value was observed.
type GlobalConfiguration struct {
	Configuration  string
	DesktopEnabled *bool
}

// InspectGlobalConfiguration uses the canonical runtime path and the same bounded
// reader and strict parser as notify. It never opens state or invokes native code.
func (b *Backend) InspectGlobalConfiguration() GlobalConfiguration {
	out := GlobalConfiguration{Configuration: "configuration_invalid"}
	raw, err := readGlobal(b.opts.GlobalConfig)
	if os.IsNotExist(err) {
		out.Configuration = "configuration_required"
		return out
	}
	if err != nil {
		return out
	}
	enabled, _, _, err := config.ValidateGlobalDesktop(raw)
	if err != nil {
		return out
	}
	out.Configuration = "configured"
	out.DesktopEnabled = &enabled
	return out
}

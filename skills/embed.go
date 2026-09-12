// Package skills exposes the canonical skill compiled into the verified installer.
package skills

import _ "embed"

//go:embed agent-notify/SKILL.md
var agentNotify string

// AgentNotify returns an independent copy of the canonical authored skill.
func AgentNotify() []byte { return []byte(agentNotify) }

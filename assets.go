// Package agentnotifications contains the canonical notification presentation
// assets used by receipt-owned native observer runtimes.
package agentnotifications

import "embed"

// NotificationAssets is embedded from the same files shipped in the plugin.
// The existing runtime writer publishes these bytes under its normal ledger.
//
//go:embed claude_icon.png sounds/*.mp3
var NotificationAssets embed.FS

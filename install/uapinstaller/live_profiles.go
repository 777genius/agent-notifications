package uapinstaller

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// LiveProfilesFile is the host-owned PLUGIN_DATA sidecar that records the live
// client config root. It is not locator JSON and is not part of Registration().
const LiveProfilesFile = "live-profiles.json"

func liveProfile(dataRoot, clientID string) string {
	if dataRoot == "" || clientID == "" {
		return ""
	}
	body, err := os.ReadFile(filepath.Join(dataRoot, LiveProfilesFile))
	if err != nil {
		return ""
	}
	var profiles map[string]string
	if json.Unmarshal(body, &profiles) != nil {
		return ""
	}
	return profiles[clientID]
}

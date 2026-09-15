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
	profiles := readLiveProfiles(dataRoot)
	if profiles == nil {
		return ""
	}
	return profiles[clientID]
}

func storeLiveProfile(dataRoot, clientID, profile string) error {
	if dataRoot == "" || clientID == "" || profile == "" {
		return nil
	}
	profiles := readLiveProfiles(dataRoot)
	if profiles == nil {
		profiles = map[string]string{}
	}
	profiles[clientID] = filepath.Clean(profile)
	body, err := json.Marshal(profiles)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataRoot, LiveProfilesFile), body, 0600)
}

func readLiveProfiles(dataRoot string) map[string]string {
	if dataRoot == "" {
		return nil
	}
	body, err := os.ReadFile(filepath.Join(dataRoot, LiveProfilesFile))
	if err != nil {
		return nil
	}
	var profiles map[string]string
	if json.Unmarshal(body, &profiles) != nil {
		return nil
	}
	return profiles
}

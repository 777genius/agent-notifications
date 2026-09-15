package setupwizard

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
)

// liveProfileFile lives in PLUGIN_DATA, not the locator JSON. Codex TargetLocator
// is under managed/clients, so the live profile has to be recorded separately
// without changing Registration() bytes.
const liveProfileFile = uapinstaller.LiveProfilesFile

func recordLiveProfile(dataRoot, clientID, profile string) error {
	if dataRoot == "" || clientID == "" || profile == "" {
		return nil
	}
	profiles, err := readLiveProfiles(dataRoot)
	if err != nil {
		return err
	}
	profiles[clientID] = filepath.Clean(profile)
	return writeLiveProfiles(dataRoot, profiles)
}

func clearLiveProfile(dataRoot, clientID string) error {
	if dataRoot == "" || clientID == "" {
		return nil
	}
	profiles, err := readLiveProfiles(dataRoot)
	if err != nil {
		return err
	}
	if _, ok := profiles[clientID]; !ok {
		return nil
	}
	delete(profiles, clientID)
	return writeLiveProfiles(dataRoot, profiles)
}

func recordedLiveProfile(dataRoot, clientID string) (string, error) {
	if dataRoot == "" || clientID == "" {
		return "", nil
	}
	profiles, err := readLiveProfiles(dataRoot)
	if err != nil {
		return "", err
	}
	return profiles[clientID], nil
}

func readLiveProfiles(dataRoot string) (map[string]string, error) {
	body, err := os.ReadFile(filepath.Join(dataRoot, liveProfileFile))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	var profiles map[string]string
	if err := json.Unmarshal(body, &profiles); err != nil {
		return nil, err
	}
	if profiles == nil {
		profiles = map[string]string{}
	}
	return profiles, nil
}

func writeLiveProfiles(dataRoot string, profiles map[string]string) error {
	path := filepath.Join(dataRoot, liveProfileFile)
	if len(profiles) == 0 {
		err := os.Remove(path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	body, err := json.Marshal(profiles)
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0600)
}

func bindingDataRoot(mat portablesetup.Materializer, installationID string, binding domain.ClientBinding) (string, error) {
	if installationID == "" || binding.DataReceiptID == "" {
		return "", nil
	}
	state, err := mat.Store.Load()
	if err != nil {
		return "", err
	}
	for _, installation := range state.Installations {
		if installation.InstallationID != installationID {
			continue
		}
		return installation.DataReceipts[binding.DataReceiptID].Locator, nil
	}
	return "", nil
}

func liveDataRoot(mat portablesetup.Materializer, installationID, clientID string) string {
	bindings, err := liveClientBindings(mat, installationID, clientID)
	if err == nil && len(bindings) == 1 {
		if root, err := bindingDataRoot(mat, installationID, bindings[0]); err == nil && root != "" {
			return root
		}
	}
	state, err := mat.Store.Load()
	if err != nil {
		return ""
	}
	for _, installation := range state.Installations {
		if installation.InstallationID != installationID {
			continue
		}
		for _, receipt := range installation.DataReceipts {
			if receipt.Locator != "" {
				return receipt.Locator
			}
		}
	}
	return ""
}

func sameLiveProfile(profile, recorded string) bool {
	return profileMatchesLive(profile, recorded) || profileMatchesLive(recorded, profile)
}

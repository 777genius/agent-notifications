package portablesetup

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

const intentVersion = 1

// Intent is the host-owned handoff record. The kernel stores only a reservation
// pointing at this file and never parses the payload.
type Intent struct {
	Version            int            `json:"version"`
	SetupIntentID      string         `json:"setupIntentID"`
	Action             string         `json:"action"`
	Stage              string         `json:"stage"`
	ExpectedGeneration uint64         `json:"expectedGeneration"`
	SourceRevision     string         `json:"sourceRevision,omitempty"`
	SourceDigest       string         `json:"sourceDigest,omitempty"`
	Targets            []IntentTarget `json:"targets"`
}

type IntentTarget struct {
	Client         string   `json:"client"`
	BindingID      string   `json:"bindingID,omitempty"`
	InstallationID string   `json:"installationID,omitempty"`
	Profile        string   `json:"profile,omitempty"`
	Units          []string `json:"units,omitempty"`
}

func IntentPath(controlRoot string) string {
	return filepath.Join(controlRoot, "portable-handoff.json")
}

func newIntentID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func marshalIntent(intent Intent) ([]byte, error) {
	if intent.Version != intentVersion || intent.SetupIntentID == "" || intent.Action == "" {
		return nil, fmt.Errorf("%w: incomplete handoff intent", ErrPreflight)
	}
	data, err := json.Marshal(intent)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func reservationFrom(intent Intent, controlRoot string) installruntime.PendingMutation {
	return installruntime.PendingMutation{ID: intent.SetupIntentID, Owner: "existing-installer", IntentRef: IntentPath(controlRoot)}
}

func ReadIntent(controlRoot string) (Intent, error) {
	var intent Intent
	data, err := os.ReadFile(IntentPath(controlRoot))
	if err != nil {
		return intent, err
	}
	if err := json.Unmarshal(data, &intent); err != nil {
		return intent, err
	}
	if intent.Version != intentVersion || intent.SetupIntentID == "" || intent.Action == "" {
		return Intent{}, fmt.Errorf("%w: incomplete handoff intent", ErrPreflight)
	}
	return intent, nil
}

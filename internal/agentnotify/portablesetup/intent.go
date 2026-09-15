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
	Version             int            `json:"version"`
	SetupIntentID       string         `json:"setupIntentID"`
	Action              string         `json:"action"`
	Stage               string         `json:"stage"`
	ExpectedGeneration  uint64         `json:"expectedGeneration"`
	SourceRevision      string         `json:"sourceRevision,omitempty"`
	SourceDigest        string         `json:"sourceDigest,omitempty"`
	TreeDigest          string         `json:"treeDigest,omitempty"`
	HelperDigest        string         `json:"helperDigest,omitempty"`
	HelperVersion       string         `json:"helperVersion,omitempty"`
	ExternalUninstalled bool           `json:"externalUninstalled,omitempty"`
	Targets             []IntentTarget `json:"targets"`
}

type IntentTarget struct {
	Client         string   `json:"client"`
	BindingID      string   `json:"bindingID,omitempty"`
	InstallationID string   `json:"installationID,omitempty"`
	DataReceiptID  string   `json:"dataReceiptID,omitempty"`
	Profile        string   `json:"profile,omitempty"`
	Units          []string `json:"units,omitempty"`
}

// ConfirmedIntent is the host-normalized SetupIntent published before the first
// wizard mutation. Handoff still uses one target; the wizard may record many.
type ConfirmedIntent struct {
	ControlRoot, RuntimeRoot, Owner         string
	ExpectedGeneration                      uint64
	Action, Stage                           string
	SourceRevision, SourceDigest            string
	TreeDigest, HelperDigest, HelperVersion string
	ExternalUninstalled                     bool
	Targets                                 []IntentTarget
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

func intentMatches(intent Intent, pendingID, action, client, digest, treeDigest string) bool {
	if intent.SetupIntentID != pendingID || intent.Action != action {
		return false
	}
	if digest != "" && intent.SourceDigest != "" && digest != intent.SourceDigest {
		return false
	}
	if treeDigest != "" && intent.TreeDigest != "" && treeDigest != intent.TreeDigest {
		return false
	}
	if client == "" || len(intent.Targets) == 0 {
		return true
	}
	for _, target := range intent.Targets {
		if target.Client == client {
			return true
		}
	}
	return false
}

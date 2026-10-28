// Package pisource reduces the public UAP Pi carrier to notification facts.
// Native prompt/response content never crosses this boundary.
package pisource

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	sdk "github.com/777genius/plugin-kit-ai/sdk"
	"github.com/777genius/plugin-kit-ai/sdk/pi"
	"github.com/google/uuid"
)

const MaxPayloadBytes = 64 << 10

type Kind string

const (
	Completed    Kind = "completed"
	Failed       Kind = "error"
	Aborted      Kind = "aborted"
	Question     Kind = "question"
	Unclassified Kind = "unclassified"
)

// Revision is a captured loaded-code identity, not event-supplied permission.
// Host composition compares it with authoritative installed receipts under lease.
type Revision struct {
	ProjectionDigest string `json:"projection_digest"`
	CanonicalDigest  string `json:"canonical_digest"`
	BootstrapVersion int    `json:"bootstrap_version"`
	BindingID        string `json:"binding_id"`
	RuntimeRoot      string `json:"runtime_root"`
	PrimarySHA256    string `json:"primary_sha256"`
}
type Facts struct {
	Kind                                            Kind
	ObserverID, ObservationID, SessionID, CWD, Mode string
	PiPID                                           int
	Revision                                        Revision
}

func validText(s string, limit int) bool {
	if s == "" || len(s) > limit || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func digest(s string, prefix bool) bool {
	if prefix {
		if !strings.HasPrefix(s, "sha256:") {
			return false
		}
		s = strings.TrimPrefix(s, "sha256:")
	}
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
func observerID(s string) bool {
	parsed, err := uuid.Parse(s)
	return err == nil && parsed.String() == s && parsed != uuid.Nil
}
func (r Revision) Valid() bool {
	return digest(r.ProjectionDigest, true) && digest(r.CanonicalDigest, true) && r.BootstrapVersion == 1 &&
		validText(r.BindingID, 256) && validText(r.RuntimeRoot, 4096) && filepath.IsAbs(r.RuntimeRoot) && filepath.Clean(r.RuntimeRoot) == r.RuntimeRoot && digest(r.PrimarySHA256, false)
}
func (f Facts) Valid() bool {
	if !observerID(f.ObserverID) || !observerID(f.ObservationID) || !validText(f.SessionID, 1024) || !validText(f.CWD, 4096) || !filepath.IsAbs(f.CWD) || filepath.Clean(f.CWD) != f.CWD || f.PiPID <= 0 || !f.Revision.Valid() {
		return false
	}
	if f.Mode != "tui" && f.Mode != "rpc" && f.Mode != "json" && f.Mode != "print" {
		return false
	}
	return f.Kind == Completed || f.Kind == Failed || f.Kind == Aborted || f.Kind == Question || f.Kind == Unclassified
}

type capturedIO struct {
	payload        []byte
	stdout, stderr bytes.Buffer
}

func (b *capturedIO) ReadStdin(ctx context.Context) ([]byte, error) {
	return append([]byte(nil), b.payload...), ctx.Err()
}
func (b *capturedIO) WriteStdout(p []byte) error { _, err := b.stdout.Write(p); return err }
func (b *capturedIO) WriteStderr(p string) error { _, err := b.stderr.WriteString(p); return err }

type noEnv struct{}

func (noEnv) LookupEnv(string) (string, bool) { return "", false }

// Decode invokes the public typed SDK. Diagnostics and raw SDK DTOs stay here;
// the outer observer transport owns the neutral response.
func Decode(ctx context.Context, payload []byte) (facts Facts, err error) {
	defer func() {
		if recover() != nil {
			facts, err = Facts{}, errors.New("invalid_pi_observation")
		}
	}()
	if ctx == nil || ctx.Err() != nil || len(payload) == 0 || len(payload) > MaxPayloadBytes {
		return Facts{}, errors.New("invalid_pi_observation")
	}
	input := &capturedIO{payload: payload}
	app := sdk.New(sdk.Config{Name: "agent-notifications", Args: []string{"agent-notifications", "pi-event"}, IO: input, Env: noEnv{}})
	valid := false
	app.Pi().OnObservation(func(e *pi.ObservationEvent) *pi.ObservationResponse {
		facts = Facts{Kind: Unclassified, ObserverID: e.ObserverID, ObservationID: e.ObservationID, SessionID: e.SessionID, CWD: e.CWD, Mode: e.Mode, PiPID: e.PiPID,
			Revision: Revision{ProjectionDigest: e.LoadedRevision.ProjectionDigest, CanonicalDigest: e.LoadedRevision.CanonicalDigest, BootstrapVersion: e.LoadedRevision.BootstrapVersion, BindingID: e.LoadedRevision.BindingID, RuntimeRoot: e.LoadedRevision.RuntimeRoot, PrimarySHA256: e.LoadedRevision.PrimarySHA256}}
		if e.Event == "agent_settled" && e.Settled != nil {
			switch {
			case e.Settled.Aborted:
				facts.Kind = Aborted
			case e.Settled.BoundaryOutcome == "completed":
				facts.Kind = Completed
			case e.Settled.BoundaryOutcome == "error":
				facts.Kind = Failed
			}
		} else if e.Event == "ui_prompt_start" && e.Prompt != nil {
			facts.Kind = Question
		}
		valid = facts.Valid()
		return &pi.ObservationResponse{}
	})
	if app.RunContext(ctx) != 0 || ctx.Err() != nil || !valid {
		return Facts{}, errors.New("invalid_pi_observation")
	}
	return facts, nil
}

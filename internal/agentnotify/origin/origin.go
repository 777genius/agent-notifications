// Package origin contains transport-supplied identity and pure routing helpers.
// It never discovers sessions, interfaces, applications or parent chats.
package origin

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"unicode"
	"unicode/utf8"

	"github.com/777genius/agent-notifications/internal/notification"
)

type Provenance string

const (
	ClientMetadata Provenance = "client_metadata"
	CallerAsserted Provenance = "caller_asserted"
)

type Locality string

const (
	Local           Locality = "local"
	Remote          Locality = "remote"
	LocalityUnknown Locality = "unknown"
)

type Interface string

const (
	Desktop          Interface = "desktop"
	Headless         Interface = "headless"
	InterfaceUnknown Interface = "unknown"
)

// Context is built by the transport, never decoded from model Payload.
// Namespace is a stable source namespace, not an application/configuration ID.
// AnonymousCaller is an explicit transport namespace, permitted only for none.
type Context struct {
	Provider        string
	Namespace       string
	SessionID       string
	AnonymousCaller string
	CallID          string
	Provenance      Provenance
	Locality        Locality
	Interface       Interface
	Hidden          bool
}

func Text(s string, limit int, required bool) bool {
	if (required && s == "") || len(s) > limit || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.Is(unicode.Cc, r) {
			return false
		}
	}
	return true
}

func (o Context) Validate(n notification.Navigation) error {
	if !Text(o.Provider, 256, true) || !Text(o.Namespace, 256, true) ||
		!Text(o.SessionID, 256, false) || !Text(o.AnonymousCaller, 256, false) || !Text(o.CallID, 256, false) {
		return errors.New("invalid_origin")
	}
	if o.Provenance != ClientMetadata && o.Provenance != CallerAsserted {
		return errors.New("invalid_origin")
	}
	if o.Locality != Local && o.Locality != Remote && o.Locality != LocalityUnknown {
		return errors.New("invalid_origin")
	}
	if o.Interface != Desktop && o.Interface != Headless && o.Interface != InterfaceUnknown {
		return errors.New("invalid_origin")
	}
	if o.SessionID == "" && (n != notification.None || o.AnonymousCaller == "") {
		return errors.New("session_required")
	}
	return nil
}

// Scope separates anonymous caller namespaces from real session identities.
// The hash avoids expanding a 256-byte ID beyond the journal's key limit.
func (o Context) Scope() (source, session string) {
	kind, id := "session\x00", o.SessionID
	if id == "" {
		kind, id = "anonymous\x00", o.AnonymousCaller
	}
	h := sha256.Sum256([]byte(kind + id))
	// Length framing avoids provider/namespace delimiter collisions.
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(o.Provider)))
	sourceHash := sha256.Sum256(append(length[:], []byte(o.Provider+o.Namespace)...))
	return hex.EncodeToString(sourceHash[:]), hex.EncodeToString(h[:])
}

type RoutePolicy struct {
	LocalRouting        bool
	AllowUnknownCaller  bool
	AllowCallerAsserted bool
	ApplicationPath     string
	TeamID              string
}
type Target struct {
	Desktop    notification.DesktopTarget
	Navigation notification.NavigationResult
}

// ResolveCodex preserves unknown interface/provenance and routes only under
// explicit operator policy. It does not claim profile or store compatibility.
func ResolveCodex(o Context, p RoutePolicy) Target {
	no := func(reason string) Target {
		return Target{Navigation: notification.NavigationResult{Capability: "unavailable", Precision: "none", Reason: reason}}
	}
	if err := o.Validate(notification.Required); err != nil {
		return no("invalid_origin")
	}
	if o.Provider != "codex" {
		return no("provider_unsupported")
	}
	if o.Locality == Remote || o.Interface == Headless {
		return no("local_gui_unavailable")
	}
	if o.Hidden {
		return no("hidden_target")
	}
	if o.SessionID == "" {
		return no("session_required")
	}
	if !p.LocalRouting {
		return no("local_routing_disabled")
	}
	if (o.Interface == InterfaceUnknown || o.Locality == LocalityUnknown) && !p.AllowUnknownCaller {
		return no("unknown_caller")
	}
	if o.Provenance == CallerAsserted && !p.AllowCallerAsserted {
		return no("caller_asserted_disabled")
	}
	if !Text(p.ApplicationPath, 1024, true) || !Text(p.TeamID, 256, true) {
		return no("application_unavailable")
	}
	return Target{Desktop: notification.DesktopTarget{ThreadID: o.SessionID, ApplicationPath: p.ApplicationPath, TeamID: p.TeamID}, Navigation: notification.NavigationResult{Capability: "available", Precision: "chat_id", Scope: "local_current_profile", Reason: "configured_codex_desktop"}}
}

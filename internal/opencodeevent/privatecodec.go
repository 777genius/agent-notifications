package opencodeevent

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"

	"github.com/777genius/agent-notifications/internal/opencodecodec"
	"github.com/777genius/agent-notifications/internal/strictjson"
	uap "github.com/777genius/plugin-kit-ai/sdk/opencode"
)

var ErrPrivateFrame = errors.New("invalid_frame")

// ClosedObject enforces exact casing, required keys and nonnull values. Callers
// validate the entire frame with strictjson first, including nested duplicates.
func ClosedObject(raw []byte, required, optional []string) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return nil, ErrPrivateFrame
	}
	allowed := make(map[string]bool, len(required)+len(optional))
	for _, k := range required {
		allowed[k] = true
		if _, ok := obj[k]; !ok {
			return nil, ErrPrivateFrame
		}
	}
	for _, k := range optional {
		allowed[k] = true
	}
	for k, v := range obj {
		if !allowed[k] || string(v) == "null" {
			return nil, ErrPrivateFrame
		}
	}
	return obj, nil
}

type canonicalNS int64

func (n *canonicalNS) UnmarshalJSON(raw []byte) error {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ErrPrivateFrame
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 || strconv.FormatInt(v, 10) != s {
		return ErrPrivateFrame
	}
	*n = canonicalNS(v)
	return nil
}

type privateAnchor struct {
	Boot    string      `json:"boot"`
	Domain  string      `json:"domain"`
	RawKind string      `json:"rawKind"`
	Lo      canonicalNS `json:"monoLoNS"`
	Hi      canonicalNS `json:"monoHiNS"`
	Wall    canonicalNS `json:"wallNS"`
	Read    canonicalNS `json:"readUncertaintyNS"`
}
type calibration struct {
	ID       string      `json:"calibrationID"`
	Epoch    string      `json:"sourceEpoch"`
	SourceLo canonicalNS `json:"sourceLoNS"`
	SourceHi canonicalNS `json:"sourceHiNS"`
	NativeLo canonicalNS `json:"nativeLoNS"`
	NativeHi canonicalNS `json:"nativeHiNS"`
	Error    canonicalNS `json:"errorNS"`
}
type privateProvenance struct {
	Epoch       string        `json:"sourceEpoch"`
	EpochStart  canonicalNS   `json:"epochStartedTickNS"`
	PolicyID    string        `json:"policyID"`
	Fence       string        `json:"fence"`
	Anchor      privateAnchor `json:"anchor"`
	Ingress     canonicalNS   `json:"ingressTickNS"`
	Spawn       canonicalNS   `json:"spawnTickNS"`
	Deadline    canonicalNS   `json:"deadlineTickNS"`
	Calibration calibration   `json:"calibration"`
}

type PrivateEvent struct {
	Event      uap.ObservedEvent
	Origin     string
	Fact       FactIdentity
	Provenance Provenance
}

// DecodePrivate retains SDK neutral decoding; only this closed additive frame
// can reach production admission. No opaque observationID parsing or restamping.
func DecodePrivate(raw []byte, selected ClockSelection) (PrivateEvent, error) {
	var out PrivateEvent
	fail := func() (PrivateEvent, error) { return PrivateEvent{}, ErrPrivateFrame }
	if strictjson.Validate(raw, strictjson.Budget{Bytes: 4096, Depth: 8, Entries: 96}) != nil {
		return fail()
	}
	obj, err := ClosedObject(raw, []string{"protocol", "origin", "event", "provenance"}, nil)
	if err != nil || string(obj["protocol"]) != "1" || json.Unmarshal(obj["origin"], &out.Origin) != nil || !hexKey(out.Origin) {
		return fail()
	}
	e, err := ClosedObject(obj["event"], []string{"version", "kind", "sessionID", "turnID", "rootSession", "provenance"}, []string{"messageID", "requestID", "nativeType"})
	if err != nil {
		return fail()
	}
	if _, err = ClosedObject(e["provenance"], []string{"generation", "observationID", "nativeTime", "timeBasis"}, []string{"nativeEventID", "nativeMessageID"}); err != nil {
		return fail()
	}
	// Actual SDK D validates original generation/timeBasis/native IDs.
	out.Event, err = uap.Decode(obj["event"])
	if err != nil || out.Event.RootSession == nil || !*out.Event.RootSession || out.Event.Provenance == nil {
		return fail()
	}
	d := out.Event.Provenance
	if !selected.valid() || d.Generation != selected.Generation || d.NativeTime > math.MaxInt64/1_000_000 {
		return fail()
	}
	pobj, err := ClosedObject(obj["provenance"], []string{"sourceEpoch", "epochStartedTickNS", "policyID", "fence", "anchor", "ingressTickNS", "spawnTickNS", "deadlineTickNS", "calibration"}, nil)
	if err != nil {
		return fail()
	}
	if _, err = ClosedObject(pobj["anchor"], []string{"boot", "domain", "rawKind", "monoLoNS", "monoHiNS", "wallNS", "readUncertaintyNS"}, nil); err != nil {
		return fail()
	}
	if _, err = ClosedObject(pobj["calibration"], []string{"calibrationID", "sourceEpoch", "sourceLoNS", "sourceHiNS", "nativeLoNS", "nativeHiNS", "errorNS"}, nil); err != nil {
		return fail()
	}
	var p privateProvenance
	if json.Unmarshal(obj["provenance"], &p) != nil || p.PolicyID != selected.Policy.ProfileID || !boundedToken(p.Epoch) {
		return fail()
	}
	a, c := p.Anchor, p.Calibration
	clock, err := selected.mapSnapshot(ClockSnapshot{a.Boot, a.Domain, a.RawKind, int64(a.Lo), int64(a.Hi), int64(a.Wall), int64(a.Read)})
	if err != nil || p.Fence != clock.Fence || c.ID != selected.CalibrationID || c.Epoch != p.Epoch || !opencodecodec.SameCoordinateCalibration(a.RawKind, int64(c.SourceLo), int64(c.SourceHi), int64(a.Lo), int64(a.Hi), selected.TranslationBoundNS) ||
		c.NativeLo != a.Lo || c.NativeHi != a.Hi || int64(c.Error) != selected.TranslationBoundNS || p.EpochStart > a.Lo ||
		p.Ingress < a.Lo || p.Spawn < p.Ingress || p.Deadline <= p.Spawn || p.Deadline-p.Spawn > 20_000_000_000 {
		return fail()
	}
	out.Fact = FactIdentity{Kind: string(out.Event.Kind), Session: out.Event.SessionID, Execution: out.Event.TurnID, Root: true}
	var binding NativeTerminalIdentity
	switch out.Event.Kind {
	case uap.TurnIdleVerified:
		out.Fact.Terminal = out.Event.MessageID
	case uap.QuestionAsked, uap.PermissionAsked:
		out.Fact.Request = out.Event.RequestID
	case uap.TerminalError:
		binding = NativeTerminalIdentity{Kind: V1FinalMessage, ID: d.NativeMessageID}
		if d.Generation == "v2" {
			binding = NativeTerminalIdentity{Kind: V2TerminalEvent, ID: d.NativeEventID}
		}
		out.Fact.Terminal = binding.ID
	default:
		return fail()
	}
	if !validFact(out.Fact) {
		return fail()
	}
	out.Provenance = Provenance{Clock: clock, NativeCreatedNS: d.NativeTime * 1_000_000, SourceEpoch: p.Epoch,
		EpochStartedTickNS: int64(p.EpochStart), IngressTickNS: int64(p.Ingress), SpawnTickNS: int64(p.Spawn), DeadlineTickNS: int64(p.Deadline), TerminalBinding: binding}
	return out, nil
}

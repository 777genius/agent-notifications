package nativeprotocol

import (
	"bytes"
	"encoding/json"
	"math"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DesktopThreadAction matches the native lane's shared action fixture. This
// type has no arbitrary command, URL or fallback application field.
type DesktopThreadAction struct {
	Type            string `json:"type"`
	SchemaVersion   int    `json:"schemaVersion"`
	ThreadID        string `json:"threadID"`
	RouteKind       string `json:"routeKind"`
	BundleID        string `json:"bundleID"`
	TeamID          string `json:"teamID"`
	ApplicationPath string `json:"applicationPath"`
	CorrelationID   string `json:"correlationID"`
}

type Request struct {
	SchemaVersion int     `json:"schemaVersion"`
	CorrelationID string  `json:"correlationID"`
	Nonce         string  `json:"nonce"`
	BootID        string  `json:"bootID"`
	NotAfter      float64 `json:"notAfter"`
	Title         string  `json:"title"`
	Body          string  `json:"body"`
	Subtitle      string  `json:"subtitle,omitempty"`
	Category      string  `json:"category"`
	// Nil encodes the native string "none"; a nonnil pointer encodes only the
	// fixed typed object. No raw JSON escapes the trusted producer.
	Action *DesktopThreadAction `json:"-"`
	Silent bool                 `json:"silent"`
}

func ValidText(s string, limit int, multiline bool) bool {
	if len(s) > limit || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) && (!multiline || (r != '\n' && r != '\t')) {
			return false
		}
		if !multiline && (r == '\u2028' || r == '\u2029') {
			return false
		}
	}
	return true
}

// ValidDesktopThreadTarget checks target syntax only, without probing an app or
// chat. Policy eligibility and action/envelope identity are separate checks.
func ValidDesktopThreadTarget(threadID, applicationPath, teamID string) bool {
	if threadID == "" || threadID == "." || threadID == ".." || !ValidText(threadID, 256, false) || len(teamID) != 10 ||
		!ValidText(applicationPath, 4096, false) || !strings.HasPrefix(applicationPath, "/") || path.Clean(applicationPath) != applicationPath || !strings.HasSuffix(applicationPath, ".app") {
		return false
	}
	for _, r := range teamID {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func (a DesktopThreadAction) valid(correlation string) bool {
	return a.Type == "desktop_thread_v1" && a.SchemaVersion == 1 && a.RouteKind == "codex_thread" && a.BundleID == "com.openai.codex" && a.CorrelationID == correlation && validUUID(correlation) &&
		ValidDesktopThreadTarget(a.ThreadID, a.ApplicationPath, a.TeamID)
}

// EncodeRequest validates decoded byte limits before JSON serialization. UTF-8
// is preserved exactly; json.Marshal's safe escaping is representation only.
func EncodeRequest(r Request) ([]byte, error) {
	if r.SchemaVersion != 1 || !validUUID(r.CorrelationID) || !validUUID(r.Nonce) || r.BootID == "" || !ValidText(r.BootID, 256, false) ||
		r.NotAfter <= 0 || math.IsNaN(r.NotAfter) || math.IsInf(r.NotAfter, 0) || !ValidText(r.Title, 256, false) || !ValidText(r.Body, 4096, true) || !ValidText(r.Subtitle, 256, false) ||
		(r.Category != "info" && r.Category != "attention" && r.Category != "progress") {
		return nil, ErrInvalidEnvelope
	}
	var action any = "none"
	if r.Action != nil {
		if !r.Action.valid(r.CorrelationID) {
			return nil, ErrInvalidEnvelope
		}
		action = *r.Action
	}
	type fields Request
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(struct {
		fields
		Action any `json:"action"`
	}{fields(r), action})
	data := bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'})
	if err != nil || len(data) > MaxEnvelopeBytes {
		return nil, ErrInvalidEnvelope
	}
	return data, nil
}

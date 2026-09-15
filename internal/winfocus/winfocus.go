// ABOUTME: Windows click-to-focus support — encodes the terminal window to raise
// ABOUTME: into a toast protocol-activation URI, decoded when the notification is clicked.
package winfocus

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// ProtocolScheme is the custom URI scheme registered under HKCU. Clicking a
// Windows toast with protocol activation relaunches the binary's focus-windows
// subcommand with a "claude-notify-focus:<payload>" argument.
const ProtocolScheme = "claude-notify-focus"

// FocusContext captures everything needed to re-find and raise the terminal
// window that triggered a notification. It is encoded into the toast's
// activation argument at notify time and decoded when the user clicks the toast.
// JSON keys are kept short because the whole struct rides inside a URI.
type FocusContext struct {
	HWND    int64  `json:"h,omitempty"` // top-level window handle captured at notify time
	PID     uint32 `json:"p,omitempty"` // owning process id (validates HWND against handle reuse)
	Title   string `json:"t,omitempty"` // window title at notify time (fallback match)
	Folder  string `json:"f,omitempty"` // project folder name (fallback match)
	WarpURL string `json:"u,omitempty"` // Warp session deep link ($WARP_FOCUS_URL)
}

// HasTarget reports whether the context carries any usable focus hint.
func (c FocusContext) HasTarget() bool {
	return c.HWND != 0 || c.PID != 0 || c.Title != "" || c.Folder != "" || c.WarpURL != ""
}

// EncodeURI renders the context as "claude-notify-focus:<base64url-json>".
// RawURLEncoding keeps the payload free of '/', '+' and '=' so it survives both
// the URI and the registry "%1" command-line substitution unescaped.
func (c FocusContext) EncodeURI() string {
	data, err := json.Marshal(c)
	if err != nil {
		return ProtocolScheme + ":"
	}
	return ProtocolScheme + ":" + base64.RawURLEncoding.EncodeToString(data)
}

// DecodeURI parses a URI produced by EncodeURI. It tolerates the scheme prefix
// in any case, an optional "//" authority separator, and trailing slashes or
// whitespace that the shell may append when launching a protocol handler.
func DecodeURI(uri string) (FocusContext, error) {
	payload := strings.TrimSpace(uri)
	if i := strings.IndexByte(payload, ':'); i >= 0 && strings.EqualFold(payload[:i], ProtocolScheme) {
		payload = payload[i+1:]
	}
	payload = strings.TrimPrefix(payload, "//")
	payload = strings.Trim(payload, "/ \t\r\n")

	var c FocusContext
	if payload == "" {
		return c, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, err
	}
	return c, nil
}

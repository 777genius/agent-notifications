// Package geminisource keeps SDK inputs and private native text inside the
// typed source boundary. Only native observation facts leave this package.
package geminisource

const (
	AfterAgent     = "AfterAgent"
	Notification   = "Notification"
	ToolPermission = "ToolPermission"
)

// Facts contains no prompt, response, path, message, details or raw SDK data.
// Timestamp is an observation marker, never a native turn or request identity.
type Facts struct {
	SessionID      string
	Timestamp      string
	Event          string
	Subtype        string
	StopHookActive bool
}

package installruntime

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

// SnapshotDiagnostic is bounded operator guidance, never configuration or
// attestation content. A missing file alone does not establish an orphan.
type SnapshotDiagnostic struct {
	Code   string `json:"code"`
	Path   string `json:"path,omitempty"`
	Action string `json:"action"`
}

// SnapshotError identifies the failed read-only check and preserves its cause.
// Error retains the underlying check's text for existing callers.
type SnapshotError struct {
	Code string
	Path string
	Err  error
}

func (e *SnapshotError) Error() string { return e.Err.Error() }
func (e *SnapshotError) Unwrap() error { return e.Err }

func snapshotFailure(code, path string, err error) error {
	return &SnapshotError{Code: code, Path: path, Err: err}
}

const snapshotDiagnosticAction = "Read the installer's read-only recovery preview, or reinstall from a trusted source; then reread status."

// SnapshotDiagnosticFor recognizes typed failures through wrapped errors. Unknown
// errors receive fixed safe guidance; their text and embedded paths are omitted.
// Nil means no diagnostic. Paths are valid UTF-8 and at most 1024 bytes; encoders
// escape JSON and human consumers should quote them to avoid terminal controls.
func SnapshotDiagnosticFor(err error) SnapshotDiagnostic {
	if err == nil {
		return SnapshotDiagnostic{}
	}
	d := SnapshotDiagnostic{Code: "snapshot_invalid", Action: snapshotDiagnosticAction}
	var e *SnapshotError
	if !errors.As(err, &e) || e == nil {
		return d
	}
	switch e.Code {
	case "managed_file_missing", "managed_file_changed", "managed_file_unreadable",
		"native_attestation_invalid", "native_identity_invalid", "native_bytes_changed", "native_unreadable",
		"policy_invalid", "ledger_invalid", "control_invalid":
		d.Code = e.Code
	default:
		return d
	}
	d.Path = strings.ToValidUTF8(e.Path, "\uFFFD")
	if len(d.Path) > 1024 {
		d.Path = d.Path[:1021]
		for !utf8.ValidString(d.Path) {
			d.Path = d.Path[:len(d.Path)-1]
		}
		d.Path += "..."
	}
	return d
}

func checkSnapshotNative(record *NativeRecord) error {
	if err := validateNativeRecord(record); err != nil {
		return snapshotFailure("native_attestation_invalid", record.Path, err)
	}
	if err := checkNativeDirectoryID(record.Path, record.DirectoryID); err != nil {
		return snapshotFailure("native_identity_invalid", record.Path, err)
	}
	got, err := treeFingerprint(record.Path)
	if err != nil {
		return snapshotFailure("native_unreadable", record.Path, err)
	}
	if got != record.SHA256 {
		return snapshotFailure("native_bytes_changed", record.Path, fmt.Errorf("installed native fingerprint mismatch"))
	}
	return nil
}

// Absence is represented by Fingerprint without an OS error.
type snapshotMissingFileError struct{ message string }

func (e *snapshotMissingFileError) Error() string { return e.message }
func (e *snapshotMissingFileError) Unwrap() error { return os.ErrNotExist }

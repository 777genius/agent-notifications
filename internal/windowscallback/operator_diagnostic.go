package windowscallback

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// UnknownCause preserves bounded evidence for a failed helper handoff while
// keeping the public classification exactly ErrUnknown. It is diagnostic only:
// it never grants replay, clears a durable fence, or changes qualification.
type UnknownCause struct {
	Cause      string
	Mode       string
	PID        int
	ExitCode   int
	Wait       error
	Stdout     []byte
	Stderr     []byte
	Diagnostic []string
}

func (e *UnknownCause) Error() string {
	if e == nil {
		return ErrUnknown.Error()
	}
	return fmt.Sprintf("%s: cause=%s mode=%s pid=%d exit=%d wait=%v stdout=%q stderr=%q diagnostic=%q",
		ErrUnknown.Error(), e.Cause, e.Mode, e.PID, e.ExitCode, e.Wait, e.Stdout, e.Stderr, e.Diagnostic)
}

func (e *UnknownCause) Unwrap() error { return ErrUnknown }

func boundedUnknown(mode, cause string, wait error, pid, exit int, output, stderr *cappedOutput, diagnostic []string) error {
	var stdout, stderrBytes []byte
	if output != nil {
		stdout = bytes.Clone(output.Bytes())
	}
	if stderr != nil {
		stderrBytes = bytes.Clone(stderr.Bytes())
	}
	return &UnknownCause{
		Cause:      cause,
		Mode:       mode,
		PID:        pid,
		ExitCode:   exit,
		Wait:       wait,
		Stdout:     stdout,
		Stderr:     stderrBytes,
		Diagnostic: append([]string(nil), diagnostic...),
	}
}

type cappedOutput struct {
	bytes.Buffer
	overflow, eof bool
}

func (b *cappedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len()+n > 256 {
		b.overflow = true
		return n, nil
	}
	return b.Buffer.Write(p)
}

// os/exec can hide pipe collection errors behind ExitError. Observe actual
// copy EOF separately; hide promoted Buffer.ReadFrom so the cap is never bypassed.
func (b *cappedOutput) ReadFrom(r io.Reader) (int64, error) {
	n, e := io.Copy(struct{ io.Writer }{b}, r)
	b.eof = e == nil
	return n, e
}
func decodeOperatorDiagnostic(raw []byte) ([]string, error) {
	if len(raw) == 0 || len(raw) > 256 || raw[len(raw)-1] != '\n' {
		return nil, ErrUnavailable
	}
	fields := strings.Split(string(raw[:len(raw)-1]), " ")
	if len(fields) != 4 || fields[0] != "WCD1" {
		return nil, ErrUnavailable
	}
	switch fields[1] {
	case "entry_apartment", "entry_arguments", "entry_deadline", "operator_deadline", "generation_custody",
		"operator_permit", "desktop_admission", "package_identity", "operator_dispatch", "operator_terminal", "registry_link_guard",
		"shared_software_open", "shared_classes_open", "shared_resolved_open", "shared_container_open",
		"shared_container_create", "shared_container_flush", "owned_leaf_create", "owned_leaf_descriptor", "owned_leaf_stamp", "class_leaf_create",
		"class_postimage", "class_children", "local_leaf_create", "local_postimage", "local_children",
		"local_default_value", "local_flush", "class_flush", "registry_close",
		"registry_software_query", "registry_software_link_value", "registry_software_other_value",
		"registry_classes_query", "registry_classes_link_value", "registry_classes_other_value",
		"registry_shared_query", "registry_shared_link_value", "registry_shared_other_value",
		"registry_resolved_query", "registry_resolved_link_value", "registry_resolved_other_value",
		"registry_owned_query", "registry_owned_link_value", "registry_owned_other_value",
		"shortcut_path", "shortcut_parent_custody", "shortcut_existing_readback", "shortcut_absence_contract",
		"shortcut_serialization", "shortcut_serialized_readback", "shortcut_stage_lookup", "shortcut_stage_create",
		"shortcut_stage_open", "shortcut_stage_bytes", "shortcut_stage_readback", "shortcut_stage_rename",
		"shortcut_stage_close", "shortcut_final_readback":
	default:
		return nil, ErrUnavailable
	}
	// Value-presence phases follow an actual successful metadata query, not an
	// API failure. Keep that semantic refusal distinct from numeric errors.
	switch fields[1] {
	case "registry_software_link_value", "registry_software_other_value",
		"registry_classes_link_value", "registry_classes_other_value",
		"registry_shared_link_value", "registry_shared_other_value",
		"registry_resolved_link_value", "registry_resolved_other_value",
		"registry_owned_link_value", "registry_owned_other_value":
		if fields[2] != "other" {
			return nil, ErrUnavailable
		}
	}
	switch fields[2] {
	case "other":
		if fields[3] != "-" {
			return nil, ErrUnavailable
		}
	case "win32", "hresult":
		value, e := strconv.ParseUint(fields[3], 10, 32)
		if e != nil || value == 0 || strconv.FormatUint(value, 10) != fields[3] {
			return nil, ErrUnavailable
		}
	default:
		return nil, ErrUnavailable
	}
	return fields[1:], nil
}
func collectedOperatorDiagnostic(waitErr error, pid, exit int, mutating bool, output, stderr *cappedOutput) []string {
	if _, known := waitErr.(*exec.ExitError); !known || !mutating || pid <= 0 || exit <= 0 ||
		!output.eof || !stderr.eof || output.overflow || stderr.overflow {
		return nil
	}
	fields, _ := decodeOperatorDiagnostic(stderr.Bytes())
	return fields
}
func operatorFailureReceipt(ticket []byte, pid, exit int, diagnostic []string) ([]byte, error) {
	if len(ticket) > 4096 {
		return nil, ErrUnavailable
	}
	fields, e := decode(ticket, 4, 4)
	if e != nil || !hexValue(fields[0], 32) || !hexValue(fields[3], 64) || pid <= 0 || exit <= 0 || uint64(pid) > 4294967295 || uint64(exit) > 4294967295 {
		return nil, ErrUnavailable
	}
	switch fields[1] {
	case "apply-clsid", "apply-aumid", "apply-shortcut", "restore-clsid", "restore-aumid", "restore-shortcut", "show":
	default:
		return nil, ErrUnavailable
	}
	end, e := strconv.ParseUint(fields[2], 10, 64)
	if e != nil || end == 0 || strconv.FormatUint(end, 10) != fields[2] {
		return nil, ErrUnavailable
	}
	if _, e = decodeOperatorDiagnostic([]byte("WCD1 " + strings.Join(diagnostic, " ") + "\n")); e != nil {
		return nil, e
	}
	fields = append(fields, strconv.Itoa(pid), strconv.Itoa(exit))
	return encode(5, append(fields, diagnostic...))
}

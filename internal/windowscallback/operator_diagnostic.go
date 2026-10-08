package windowscallback

import (
	"bytes"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

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
		"local_default_value", "local_flush", "class_flush", "registry_close":
	default:
		return nil, ErrUnavailable
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

package windowscallback

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

// Breakage: extra/untrusted diagnostics are accepted or success/unknown pipe
// collection becomes a failure receipt. These are independent wire/copy vectors.
func TestOperatorDiagnosticClosedFrame(t *testing.T) {
	for _, raw := range []string{"WCD1 shared_container_open win32 5\n", "WCD1 package_identity hresult 2147942487\n", "WCD1 class_postimage other -\n"} {
		if _, e := decodeOperatorDiagnostic([]byte(raw)); e != nil {
			t.Fatal(raw, e)
		}
	}
	for _, raw := range []string{"", "WCD1 registry_link_guard win32 0\n", "WCD1 shared_container_open win32 05\n",
		"WCD1 package_identity hresult 4294967296\n", "WCD1 package_identity hresult -1\n", "WCD1 class_postimage other 5\n",
		"WCD1 arbitrary win32 5\n", "WCD1 class_postimage unknown -\n", "WCD1 class_postimage other -",
		"WCD1 class_postimage other -\r\n", "WCD1 class_postimage other -\n\n", "WCD1 class_postimage other -\x00\n",
		"WCD1  class_postimage other -\n", "WCD1 class_postimage other - extra\n", strings.Repeat("x", 257)} {
		if _, e := decodeOperatorDiagnostic([]byte(raw)); e == nil {
			t.Fatal("unclosed diagnostic accepted", raw)
		}
	}
}

type failedRead struct{}

func (failedRead) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func TestOperatorDiagnosticRequiresActualCopyEOF(t *testing.T) {
	var output, stderr cappedOutput
	// WriterTo-hidden readers drive the same ReaderFrom used by os.File pipes.
	if _, e := io.Copy(&output, struct{ io.Reader }{strings.NewReader("WCB1 unavailable not_checked 0\n")}); e != nil {
		t.Fatal(e)
	}
	if _, e := io.Copy(&stderr, struct{ io.Reader }{strings.NewReader("WCD1 shared_container_open win32 5\n")}); e != nil {
		t.Fatal(e)
	}
	if !output.eof || !stderr.eof {
		t.Fatal("actual EOF lost")
	}
	if got := collectedOperatorDiagnostic(&exec.ExitError{}, 6672, 2, true, &output, &stderr); len(got) != 3 {
		t.Fatal(got)
	}
	for _, err := range []error{nil, exec.ErrWaitDelay, errors.New("collection unknown")} {
		if collectedOperatorDiagnostic(err, 6672, 2, true, &output, &stderr) != nil {
			t.Fatal("unknown collection admitted")
		}
	}
	for _, facts := range []struct {
		pid, exit int
		mutating  bool
	}{{0, 2, true}, {6672, 0, true}, {6672, 2, false}} {
		if collectedOperatorDiagnostic(&exec.ExitError{}, facts.pid, facts.exit, facts.mutating, &output, &stderr) != nil {
			t.Fatal(facts)
		}
	}
	var broken, overflow cappedOutput
	if _, e := io.Copy(&broken, struct{ io.Reader }{io.MultiReader(strings.NewReader("WCD1 shared_container_open win32 5\n"), failedRead{})}); e == nil || broken.eof {
		t.Fatal("broken pipe certified EOF")
	}
	if collectedOperatorDiagnostic(&exec.ExitError{}, 6672, 2, true, &output, &broken) != nil {
		t.Fatal("nonzero exit hid pipe failure")
	}
	if _, e := io.Copy(&overflow, struct{ io.Reader }{strings.NewReader(strings.Repeat("x", 257))}); e != nil || !overflow.overflow || overflow.Len() > 256 {
		t.Fatal("ReaderFrom bypassed cap")
	}
	if collectedOperatorDiagnostic(&exec.ExitError{}, 6672, 2, true, &output, &overflow) != nil {
		t.Fatal("overflow admitted")
	}
}
func TestOperatorFailureReceiptImmutableJoins(t *testing.T) {
	// Independent kind4 bytes, not encoder-produced pending expectations.
	ticket := []byte("WNCB0001\x01\x00\x04\x00\x20\x00\x00\x00" + strings.Repeat("a", 32) +
		"\x0b\x00\x00\x00apply-clsid\x01\x00\x00\x001\x40\x00\x00\x00" + strings.Repeat("b", 64))
	diag := []string{"shared_container_open", "win32", "5"}
	raw, e := operatorFailureReceipt(ticket, 6672, 2, diag)
	if e != nil {
		t.Fatal(e)
	}
	fields, e := decode(raw, 5, 9)
	want := []string{strings.Repeat("a", 32), "apply-clsid", "1", strings.Repeat("b", 64), "6672", "2", "shared_container_open", "win32", "5"}
	if e != nil || !reflect.DeepEqual(fields, want) || len(raw) > 4096 {
		t.Fatal(fields, e)
	}
	for _, bad := range [][]byte{append(bytes.Clone(ticket), 0), ticket[:len(ticket)-1], bytes.Replace(ticket, []byte("apply-clsid"), []byte("observe----"), 1), bytes.Replace(ticket, []byte(strings.Repeat("b", 64)), []byte(strings.Repeat("z", 64)), 1)} {
		if _, e := operatorFailureReceipt(bad, 6672, 2, diag); e == nil {
			t.Fatal("bad immutable join admitted")
		}
	}
	if _, e := operatorFailureReceipt(ticket, 0, 2, diag); e == nil {
		t.Fatal("no entry admitted")
	}
	if _, e := operatorFailureReceipt(ticket, 6672, 2, []string{"class_postimage", "other", "5"}); e == nil {
		t.Fatal("invalid diagnostic admitted")
	}
}

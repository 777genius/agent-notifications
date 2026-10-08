//go:build windows

package windowscallback

import (
	"bytes"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
)

// One fixed refusal slot bounds orphan/unknown operator state. A pending or
// collected obligation is never reclaimed from PID absence, expiry or names.
func (g *Custody) operatorObligation() error {
	for _, name := range []string{"operator.pending", "operator.collected"} {
		if _, e := readAt(g.rootHandle, name, g.SID, 4096); e == nil || !isMissing(e) {
			return ErrUnknown
		}
	}
	return nil
}
func isMissing(e error) bool {
	return e == windows.ERROR_FILE_NOT_FOUND || e == windows.ERROR_PATH_NOT_FOUND
}

type operatorFence struct {
	g        *Custody
	id, mode string
	end      uint64
	held     windows.Handle
	identity windows.ByHandleFileInformation
	ticket   []byte
}

func (g *Custody) fenceOperator(id, mode string, end uint64) (*operatorFence, error) {
	ticket, e := encode(4, []string{id, mode, strconv.FormatUint(end, 10), g.Binding.SHA256})
	if e != nil {
		return nil, e
	}
	if e = writeAt(g.rootHandle, "operator.pending", g.SID, ticket); e != nil {
		return nil, ErrUnknown
	}
	h, e := at(g.rootHandle, "operator.pending", windows.FILE_GENERIC_READ|windows.READ_CONTROL, windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE, nil)
	if e != nil {
		return nil, ErrUnknown
	}
	f := &operatorFence{g: g, id: id, mode: mode, end: end, held: h, ticket: ticket}
	if e = windows.GetFileInformationByHandle(h, &f.identity); e != nil {
		windows.CloseHandle(h)
		return nil, ErrUnknown
	}
	return f, nil
}
func (f *operatorFence) close() error {
	if f == nil || f.held == 0 {
		return nil
	}
	h := f.held
	f.held = 0
	return windows.CloseHandle(h)
}
func (f *operatorFence) collectionFact(pid, exit int) []byte {
	kind := "WinOperatorCollected1"
	if pid == 0 {
		kind = "WinOperatorNoEntry1"
	}
	return []byte(strings.Join([]string{kind, f.id, f.mode, strconv.FormatUint(f.end, 10), f.g.Binding.SHA256, strconv.Itoa(pid), strconv.Itoa(exit)}, " ") + "\n")
}
func (f *operatorFence) collected(pid, exit int) error {
	// Nonzero PID facts follow Cmd.Wait on the owned child with bounded pipes;
	// PID zero means Start never succeeded. Neither grants replay permission or
	// proves SDK/global quiescence. WaitDelay/error paths retain pending refusal.
	return writeAt(f.g.rootHandle, "operator.collected", f.g.SID, f.collectionFact(pid, exit))
}
func sameFile(a, b windows.ByHandleFileInformation) bool {
	return a.VolumeSerialNumber == b.VolumeSerialNumber && a.FileIndexHigh == b.FileIndexHigh && a.FileIndexLow == b.FileIndexLow
}
func (g *Custody) deleteHeld(name string, want []byte, identity *windows.ByHandleFileInformation) error {
	h, e := at(g.rootHandle, name, windows.FILE_GENERIC_READ|windows.READ_CONTROL|windows.DELETE, windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE, nil)
	if e != nil {
		return e
	}
	defer func() {
		if h != 0 {
			_ = windows.CloseHandle(h)
		}
	}()
	if ownedHandle(h, g.SID) != nil {
		return ErrUnknown
	}
	var actual windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &actual) != nil || identity != nil && !sameFile(*identity, actual) {
		return ErrUnknown
	}
	got, e := heldBytes(h, 4096)
	if e != nil || !bytes.Equal(got, want) {
		return ErrUnknown
	}
	// Delete the admitted handle incarnation, never a freshly resolved pathname.
	disposition := byte(1)
	if e = windows.SetFileInformationByHandle(h, windows.FileDispositionInfo, &disposition, 1); e != nil {
		return e
	}
	closed := windows.CloseHandle(h)
	h = 0
	return closed
}
func (f *operatorFence) clear(pid, exit int) error {
	if e := f.collected(pid, exit); e != nil {
		return ErrUnknown
	}
	if e := f.close(); e != nil {
		return ErrUnknown
	}
	if e := f.g.deleteHeld("operator.pending", f.ticket, &f.identity); e != nil {
		return ErrUnknown
	}
	return f.g.deleteHeld("operator.collected", f.collectionFact(pid, exit), nil)
}

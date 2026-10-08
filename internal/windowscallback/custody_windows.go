//go:build windows

package windowscallback

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// FILE_ALL_ACCESS from the Windows SDK; x/sys does not export this mask.
const fileAllAccess uint32 = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff

type Custody struct {
	mutex      sync.Mutex
	active     int // admitted native operators and record filesystem work
	closing    bool
	rootHandle windows.Handle
	handles    []windows.Handle
	Root, SID  string
	Snapshot   Snapshot
	Binding    Binding
}

func (g *Custody) closeHandles() error {
	var failure error
	for i := len(g.handles) - 1; i >= 0; i-- {
		failure = errors.Join(failure, windows.CloseHandle(g.handles[i]))
	}
	g.handles = nil
	return failure
}
func (g *Custody) Close() error {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	g.closing = true
	if g.active != 0 {
		return ErrUnknown
	}
	return g.closeHandles()
}
func (g *Custody) admitOperator() error {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	if g.closing {
		return ErrUnavailable
	}
	g.active++
	return nil
}
func (g *Custody) collectOperator() {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	g.active--
	if g.closing && g.active == 0 {
		_ = g.closeHandles()
	}
}
func heldBytes(h windows.Handle, limit uint32) ([]byte, error) {
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &info) != nil || info.FileSizeHigh != 0 || info.FileSizeLow == 0 || info.FileSizeLow > limit {
		return nil, ErrUnavailable
	}
	b := make([]byte, info.FileSizeLow)
	var n uint32
	e := windows.ReadFile(h, b, &n, nil)
	if e != nil || n != uint32(len(b)) {
		return nil, ErrUnavailable
	}
	return b, nil
}

func owner() (string, error) {
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return "", e
	}
	return u.User.Sid.String(), nil
}
func security(sid string) (*windows.SECURITY_DESCRIPTOR, error) {
	return windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;;FA;;;" + sid + ")(A;;FA;;;SY)")
}
func ownedHandle(h windows.Handle, sid string) error {
	sd, e := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return e
	}
	who, _, e := sd.Owner()
	if e != nil || who == nil || who.String() != sid {
		return ErrUnavailable
	}
	control, _, e := sd.Control()
	if e != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return ErrUnavailable
	}
	acl, _, e := sd.DACL()
	if e != nil || acl == nil || acl.AceCount != 2 {
		return ErrUnavailable
	}
	seen := map[string]bool{}
	for i := uint32(0); i < 2; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, i, &ace) != nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags != 0 || uint32(ace.Mask) != fileAllAccess {
			return ErrUnavailable
		}
		value := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		if (value != sid && value != "S-1-5-18") || seen[value] {
			return ErrUnavailable
		}
		seen[value] = true
	}
	return nil
}
func physical(h windows.Handle) (string, error) {
	buf := make([]uint16, 32768)
	n, e := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
	if e != nil || n == 0 || n >= uint32(len(buf)) {
		return "", ErrUnavailable
	}
	return windows.UTF16ToString(buf[:n]), nil
}
func at(parent windows.Handle, name string, access, disposition, options uint32, sd *windows.SECURITY_DESCRIPTOR) (windows.Handle, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `\/:`) {
		return 0, ErrUnavailable
	}
	n, e := windows.NewNTUnicodeString(name)
	if e != nil {
		return 0, e
	}
	attrs := windows.OBJECT_ATTRIBUTES{RootDirectory: parent, ObjectName: n, Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE, SecurityDescriptor: sd}
	attrs.Length = uint32(unsafe.Sizeof(attrs))
	var h windows.Handle
	var status windows.IO_STATUS_BLOCK
	share := uint32(windows.FILE_SHARE_READ)
	if options&windows.FILE_DIRECTORY_FILE != 0 {
		share |= windows.FILE_SHARE_WRITE
	}
	e = windows.NtCreateFile(&h, access|windows.SYNCHRONIZE, &attrs, &status, nil, windows.FILE_ATTRIBUTE_NORMAL, share, disposition, options|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	if nt, ok := e.(windows.NTStatus); ok {
		e = nt.Errno()
	}
	if e != nil {
		return 0, e
	}
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &info) != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (options&windows.FILE_DIRECTORY_FILE == 0 && (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 || info.NumberOfLinks != 1)) {
		_ = windows.CloseHandle(h)
		return 0, ErrUnavailable
	}
	return h, nil
}
func hold(path string) (*Custody, error) {
	if strings.HasPrefix(path, `\\?\`) {
		path = path[4:]
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(filepath.VolumeName(path)) != 2 {
		return nil, ErrUnavailable
	}
	sid, e := owner()
	if e != nil {
		return nil, e
	}
	g := &Custody{SID: sid}
	p, _ := windows.UTF16PtrFromString(filepath.VolumeName(path) + `\`)
	h, e := windows.CreateFile(p, windows.FILE_LIST_DIRECTORY|windows.FILE_TRAVERSE|windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		return nil, e
	}
	g.handles = append(g.handles, h)
	parts := strings.Split(path[len(filepath.VolumeName(path))+1:], `\`)
	for i, part := range parts {
		access := uint32(windows.FILE_GENERIC_READ)
		if i == len(parts)-1 {
			access |= windows.FILE_GENERIC_WRITE
		}
		h, e = at(h, part, access, windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE, nil)
		if e != nil {
			g.Close()
			return nil, e
		}
		g.handles = append(g.handles, h)
	}
	g.Root, e = physical(h)
	if e != nil {
		g.Close()
		return nil, e
	}
	return g, nil
}
func readAt(root windows.Handle, name, sid string, limit int) ([]byte, error) {
	h, e := at(root, name, windows.FILE_GENERIC_READ|windows.READ_CONTROL, windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE, nil)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(h), name)
	defer f.Close()
	if e = ownedHandle(h, sid); e != nil {
		return nil, e
	}
	info, e := f.Stat()
	if e != nil || info.Size() > int64(limit) {
		return nil, ErrUnavailable
	}
	b, e := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if e != nil || len(b) > limit {
		return nil, ErrUnavailable
	}
	return b, nil
}
func writeAt(root windows.Handle, name, sid string, data []byte) error {
	sd, e := security(sid)
	if e != nil {
		return e
	}
	h, e := at(root, name, windows.FILE_GENERIC_WRITE|windows.READ_CONTROL, windows.FILE_CREATE, windows.FILE_NON_DIRECTORY_FILE, sd)
	if e != nil {
		return e
	}
	f := os.NewFile(uintptr(h), name)
	n, e := f.Write(data)
	if e == nil && n != len(data) {
		e = io.ErrShortWrite
	}
	if e == nil {
		e = f.Sync()
	}
	closed := f.Close()
	if e == nil {
		e = closed
	}
	return e
}
func createDir(root windows.Handle, name, sid string) (windows.Handle, error) {
	sd, e := security(sid)
	if e != nil {
		return 0, e
	}
	h, e := at(root, name, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.READ_CONTROL, windows.FILE_CREATE, windows.FILE_DIRECTORY_FILE, sd)
	if e == nil {
		e = ownedHandle(h, sid)
	}
	if e != nil && h != 0 {
		windows.CloseHandle(h)
	}
	return h, e
}
func NewID() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return hex.EncodeToString(b[:]), nil
}
func Plan(ctx context.Context, controlRoot string, end uint64) (Snapshot, Binding, error) {
	full, e := DiscoverSelected(ctx, end)
	if e != nil {
		return Snapshot{}, Binding{}, e
	}
	asset, digest, e := TrustedAsset()
	if e != nil || len(asset) > 16*1024*1024 {
		return Snapshot{}, Binding{}, ErrUnavailable
	}
	g, e := hold(controlRoot)
	if e != nil {
		return Snapshot{}, Binding{}, e
	}
	defer g.Close()
	id, e := NewID()
	if e != nil {
		return Snapshot{}, Binding{}, e
	}
	cls, e := NewID()
	if e != nil {
		return Snapshot{}, Binding{}, e
	}
	cls = "{" + cls[:8] + "-" + cls[8:12] + "-" + cls[12:16] + "-" + cls[16:20] + "-" + cls[20:] + "}"
	s := Snapshot{Generation: id, CanonicalRoot: filepath.Join(g.Root, "windows-callback", id), OwnerSID: g.SID, HelperSHA256: digest, AUMID: "AgentNotifications." + id, CLSID: cls, VendorName: VendorName, Publisher: VendorPublisher, Family: VendorFamily, FullName: full}
	b, e := EncodeSnapshot(s)
	if e != nil {
		return s, Binding{}, e
	}
	return s, Binding{SnapshotPath: filepath.Join(s.CanonicalRoot, "generation.wne"), SHA256: Digest(b)}, budget(ctx, end)
}

// Bootstrap is used only after the owner transaction persists its absent
// preimages. Creation ACLs never include Administrators or a repair phase.
func Bootstrap(ctx context.Context, s Snapshot, end uint64) error {
	if s.Validate() != nil || budget(ctx, end) != nil {
		return ErrUnavailable
	}
	parent, e := hold(filepath.Dir(filepath.Dir(s.CanonicalRoot)))
	if e != nil {
		return e
	}
	defer parent.Close()
	if parent.SID != s.OwnerSID {
		return ErrUnavailable
	}
	root := parent.handles[len(parent.handles)-1]
	if budget(ctx, end) != nil {
		return ErrUnavailable
	}
	dir, e := at(root, "windows-callback", windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE, windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE, nil)
	if os.IsNotExist(e) {
		if budget(ctx, end) != nil {
			return ErrUnavailable
		}
		dir, e = createDir(root, "windows-callback", s.OwnerSID)
	}
	if e != nil {
		return e
	}
	defer windows.CloseHandle(dir)
	if ownedHandle(dir, s.OwnerSID) != nil {
		return ErrUnavailable
	}
	if budget(ctx, end) != nil {
		return ErrUnavailable
	}
	gen, e := createDir(dir, s.Generation, s.OwnerSID)
	if os.IsExist(e) {
		gen, e = at(dir, s.Generation, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE, windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE, nil)
	}
	if e != nil {
		return e
	}
	defer windows.CloseHandle(gen)
	if ownedHandle(gen, s.OwnerSID) != nil {
		return ErrUnavailable
	}
	for _, name := range []string{"records", "attempts"} {
		if budget(ctx, end) != nil {
			return ErrUnavailable
		}
		h, e := createDir(gen, name, s.OwnerSID)
		if os.IsExist(e) {
			h, e = at(gen, name, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE, windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE, nil)
		}
		if e != nil {
			return e
		}
		if ownedHandle(h, s.OwnerSID) != nil {
			windows.CloseHandle(h)
			return ErrUnavailable
		}
		if e = windows.CloseHandle(h); e != nil {
			return e
		}
	}
	if budget(ctx, end) != nil {
		return ErrUnavailable
	}
	helper, digest, e := TrustedAsset()
	if e != nil || digest != s.HelperSHA256 {
		return ErrUnavailable
	}
	data, e := EncodeSnapshot(s)
	if e != nil {
		return e
	}
	for name, b := range map[string][]byte{"helper.exe": helper, "generation.wne": data, "capacity.state": (Capacity{}).bytes(), "capacity.lock": []byte("WCAPLOCK1")} {
		if budget(ctx, end) != nil {
			return ErrUnavailable
		}
		existing, e := readAt(gen, name, s.OwnerSID, len(b))
		if e == nil {
			if !bytes.Equal(existing, b) {
				return ErrUnavailable
			}
			continue
		}
		if !os.IsNotExist(e) {
			return e
		}
		if e = writeAt(gen, name, s.OwnerSID, b); e != nil {
			return e
		}
	}
	return budget(ctx, end)
}
func Open(ctx context.Context, b Binding, end uint64) (*Custody, error) {
	if !hexValue(b.SHA256, 64) || filepath.Base(b.SnapshotPath) != "generation.wne" || budget(ctx, end) != nil {
		return nil, ErrUnavailable
	}
	g, e := hold(filepath.Dir(b.SnapshotPath))
	if e != nil {
		return nil, e
	}
	fail := func(e error) (*Custody, error) { g.Close(); return nil, e }
	root := g.handles[len(g.handles)-1]
	g.rootHandle = root
	if e = ownedHandle(root, g.SID); e != nil {
		return fail(e)
	}
	snapshot, e := at(root, "generation.wne", windows.FILE_GENERIC_READ|windows.READ_CONTROL, windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE, nil)
	if e != nil {
		return fail(e)
	}
	g.handles = append(g.handles, snapshot)
	if ownedHandle(snapshot, g.SID) != nil {
		return fail(ErrUnavailable)
	}
	if named, e := physical(snapshot); e != nil || named != b.SnapshotPath {
		return fail(ErrUnavailable)
	}
	data, e := heldBytes(snapshot, MaxEnvelope)
	if e != nil || Digest(data) != b.SHA256 {
		return fail(ErrUnavailable)
	}
	g.Snapshot, e = DecodeSnapshot(data)
	if e != nil || g.Snapshot.CanonicalRoot != g.Root || g.Snapshot.OwnerSID != g.SID {
		return fail(ErrUnavailable)
	}
	h, e := at(root, "helper.exe", windows.FILE_GENERIC_READ|windows.READ_CONTROL, windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE, nil)
	if e != nil {
		return fail(e)
	}
	g.handles = append(g.handles, h)
	if named, e := physical(h); e != nil || named != filepath.Join(g.Root, "helper.exe") {
		return fail(ErrUnavailable)
	}
	if e = ownedHandle(h, g.SID); e != nil {
		return fail(e)
	}
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &info) != nil || info.FileSizeHigh != 0 || info.FileSizeLow > 16*1024*1024 {
		return fail(ErrUnavailable)
	}
	asset := make([]byte, info.FileSizeLow)
	var read uint32
	e = windows.ReadFile(h, asset, &read, nil)
	if e != nil || read != uint32(len(asset)) || Digest(asset) != g.Snapshot.HelperSHA256 {
		return fail(ErrUnavailable)
	}
	expected, digest, e := TrustedAsset()
	if e != nil || digest != g.Snapshot.HelperSHA256 || !bytes.Equal(asset, expected) {
		return fail(ErrUnavailable)
	}
	for _, name := range []string{"records", "attempts"} {
		dir, e := at(root, name, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE, windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE, nil)
		if e != nil {
			return fail(e)
		}
		g.handles = append(g.handles, dir)
		if ownedHandle(dir, g.SID) != nil {
			return fail(ErrUnavailable)
		}
	}
	g.Binding = b
	if e = g.operatorObligation(); e != nil {
		return fail(e)
	}
	if e = budget(ctx, end); e != nil {
		return fail(e)
	}
	return g, nil
}

// reserve charges a fixed maximum before publication. A pending or torn counter
// is unavailable, never inferred reclaimable from process/file absence.
func (g *Custody) reserve(ctx context.Context, record bool, id string, end uint64) error {
	root := g.rootHandle
	p, _ := windows.UTF16PtrFromString(filepath.Join(g.Root, "capacity.lock"))
	var lock windows.Handle
	var e error
	for {
		if budget(ctx, end) != nil {
			return ErrCapacity
		}
		lock, e = windows.CreateFile(p, windows.GENERIC_READ|windows.READ_CONTROL, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if e == nil {
			break
		}
		if e != windows.ERROR_SHARING_VIOLATION {
			return ErrCapacity
		}
		time.Sleep(time.Millisecond)
	}
	defer windows.CloseHandle(lock)
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(lock, &info) != nil || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 || info.NumberOfLinks != 1 || ownedHandle(lock, g.SID) != nil {
		return ErrCapacity
	}
	pending, e := readAt(root, "capacity.pending", g.SID, 128)
	if e == nil || !os.IsNotExist(e) || len(pending) > 0 {
		return ErrCapacity
	}
	state, e := readAt(root, "capacity.state", g.SID, 128)
	if e != nil {
		return ErrCapacity
	}
	c, e := decodeCapacity(state)
	if e != nil {
		return e
	}
	next, e := c.reserve(record)
	if e != nil {
		return e
	}
	if e = budget(ctx, end); e != nil {
		return e
	}
	if e = writeAt(root, "capacity.pending", g.SID, []byte(id)); e != nil {
		return e
	}
	if e = budget(ctx, end); e != nil {
		return e
	}
	if e = writeAt(root, "capacity.next", g.SID, next.bytes()); e != nil {
		return e
	}
	a, _ := windows.UTF16PtrFromString(filepath.Join(g.Root, "capacity.next"))
	b, _ := windows.UTF16PtrFromString(filepath.Join(g.Root, "capacity.state"))
	if e = budget(ctx, end); e != nil {
		return e
	}
	if e = windows.MoveFileEx(a, b, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); e != nil {
		return e
	}
	p, _ = windows.UTF16PtrFromString(filepath.Join(g.Root, "capacity.pending"))
	if e = budget(ctx, end); e != nil {
		return e
	}
	if e = windows.DeleteFile(p); e != nil {
		return e
	}
	return budget(ctx, end)
}
func (g *Custody) PublishRecord(ctx context.Context, id string, end uint64) (Record, error) {
	if e := g.admitOperator(); e != nil {
		return Record{}, e
	}
	defer g.collectOperator()
	ref, e := NewID()
	if e != nil {
		return Record{}, e
	}
	r := Record{g.Snapshot.Generation, ref, "codex", id, g.Binding.SHA256}
	b, e := EncodeRecord(r)
	if e != nil {
		return r, e
	}
	if e = g.reserve(ctx, true, ref, end); e != nil {
		return r, e
	}
	root := g.rootHandle
	dir, e := at(root, "records", windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE, windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE, nil)
	if e != nil {
		return r, e
	}
	defer windows.CloseHandle(dir)
	if e = ownedHandle(dir, g.SID); e != nil {
		return r, e
	}
	if e = budget(ctx, end); e != nil {
		return r, e
	}
	if e = writeAt(dir, ref+".wne", g.SID, b); e != nil {
		return r, e
	}
	return r, budget(ctx, end)
}

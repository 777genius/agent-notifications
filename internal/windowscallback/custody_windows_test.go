//go:build windows

package windowscallback

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func inertGeneration(t *testing.T) (*Custody, Binding) {
	t.Helper()
	_, digest, e := TrustedAsset()
	if e != nil {
		t.Skip("trusted compiled fixture absent in untagged build")
	}
	root := t.TempDir()
	parent, e := hold(root)
	if e != nil {
		t.Fatal(e)
	}
	canonical, sid := parent.Root, parent.SID
	if e = parent.Close(); e != nil {
		t.Fatal(e)
	}
	s := Snapshot{Generation: strings.Repeat("a", 32), CanonicalRoot: filepath.Join(canonical, "windows-callback", strings.Repeat("a", 32)), OwnerSID: sid, HelperSHA256: digest, AUMID: "AgentNotifications.TEST", CLSID: "{11111111-1111-1111-1111-111111111111}", VendorName: VendorName, Publisher: VendorPublisher, Family: VendorFamily, FullName: "OpenAI.Codex_26.930.7945.0_x64__2p2nqsd0c76g0"}
	b, e := EncodeSnapshot(s)
	if e != nil {
		t.Fatal(e)
	}
	binding := Binding{filepath.Join(s.CanonicalRoot, "generation.wne"), Digest(b)}
	end := BootMilliseconds() + 10000
	if e = Bootstrap(context.Background(), s, end); e != nil {
		t.Fatal(e)
	}
	g, e := Open(context.Background(), binding, end)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = g.Close() })
	return g, binding
}

// Breakage: an admitted snapshot/helper can change or move while the native
// operator would reread its path. This is actual finite filesystem custody,
// without helper execution, registry, package queries, Show or COM registration.
func TestAdmittedSnapshotAndHelperCannotChange(t *testing.T) {
	g, b := inertGeneration(t)
	for _, path := range []string{b.SnapshotPath, filepath.Join(g.Root, "helper.exe")} {
		p, e := windows.UTF16PtrFromString(path)
		if e != nil {
			t.Fatal(e)
		}
		h, e := windows.CreateFile(p, windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if e == nil {
			windows.CloseHandle(h)
			t.Fatal("admitted bytes writable", path)
		}
		if e != windows.ERROR_SHARING_VIOLATION {
			t.Fatal("unexpected refusal", e)
		}
		if e = os.Rename(path, path+".moved"); e == nil {
			t.Fatal("admitted identity moved", path)
		}
	}
	if e := g.Close(); e != nil {
		t.Fatal(e)
	}
	if e := os.Rename(b.SnapshotPath, b.SnapshotPath+".moved"); e != nil {
		t.Fatal("custody did not release", e)
	}
}

// Breakage: two live producers spend one counter reservation, or quota-lock
// hardlink aliasing is treated as a valid exclusive canonical lock.
func TestActualConcurrentChargedRecordReservations(t *testing.T) {
	g, _ := inertGeneration(t)
	ctx := context.Background()
	end := BootMilliseconds() + 10000
	var workers sync.WaitGroup
	failed := make(chan error, 16)
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func() { defer workers.Done(); _, e := g.PublishRecord(ctx, "TEST/thread%opaque", end); failed <- e }()
	}
	workers.Wait()
	close(failed)
	for e := range failed {
		if e != nil {
			t.Fatal(e)
		}
	}
	b, e := readAt(g.rootHandle, "capacity.state", g.SID, 128)
	if e != nil {
		t.Fatal(e)
	}
	c, e := decodeCapacity(b)
	if e != nil || c.Records != 16 || c.RecordBytes != 1048576 || c.Attempts != 0 {
		t.Fatal(c, e)
	}
	if e = os.Link(filepath.Join(g.Root, "capacity.lock"), filepath.Join(g.Root, "aliased-lock")); e != nil {
		t.Fatal(e)
	}
	if _, e = g.PublishRecord(ctx, "TEST/blocked", end); e != ErrCapacity {
		t.Fatal("multiply linked quota admitted", e)
	}
}

// Breakage: a fresh Open or supervisor admission bypasses the durable unknown
// actor obligation after its original Go custody object is closed. No child,
// SDK, registry or Show executes; this exercises persisted refusal ownership.
func TestUnknownOperatorFenceSurvivesFreshCustody(t *testing.T) {
	g, b := inertGeneration(t)
	end := BootMilliseconds() + 10000
	f, e := g.fenceOperator(strings.Repeat("c", 32), "apply-clsid", end)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.close(); e != nil {
		t.Fatal(e)
	}
	if e = g.Close(); e != nil {
		t.Fatal(e)
	}
	if reopened, e := Open(context.Background(), b, end); e != ErrUnknown {
		if reopened != nil {
			_ = reopened.Close()
		}
		t.Fatal("unknown actor admitted", e)
	}
	// A process-collection fact alone is also not automatic replay permission.
	value := []byte("WinOperatorCollected1 cccccccccccccccccccccccccccccccc apply-clsid 1 " + b.SHA256 + " 1 0\n")
	held, e := hold(filepath.Dir(b.SnapshotPath))
	if e != nil {
		t.Fatal(e)
	}
	defer held.Close()
	if e = writeAt(held.handles[len(held.handles)-1], "operator.collected", held.SID, value); e != nil {
		t.Fatal(e)
	}
	if reopened, e := Open(context.Background(), b, end); e != ErrUnknown {
		if reopened != nil {
			_ = reopened.Close()
		}
		t.Fatal("collection marker permitted replay", e)
	}
}

// Breakage: a closed custody object reads a recycled root handle or publishes
// through it instead of refusing before any filesystem/helper admission.
func TestClosedCustodyRejectsWorkBeforeHandleAccess(t *testing.T) {
	g, _ := inertGeneration(t)
	if e := g.Close(); e != nil {
		t.Fatal(e)
	}
	end := BootMilliseconds() + 10000
	if _, e := g.Operator(context.Background(), "apply-clsid", nil, end); e != ErrUnavailable {
		t.Fatal("closed operator admitted", e)
	}
	if _, e := g.PublishRecord(context.Background(), "TEST/closed", end); e != ErrUnavailable {
		t.Fatal("closed publication admitted", e)
	}
	if _, e := os.Lstat(filepath.Join(g.Root, "operator.pending")); !os.IsNotExist(e) {
		t.Fatal("closed operator wrote intent", e)
	}
}

// Breakage: Close releases a live producer's physical generation while its
// actual publication waits on the exclusive quota lock. The wait uses the real
// filesystem; sharing refusals and later release are observed on both files.
func TestBlockedPublicationRetainsCustodyUntilReturn(t *testing.T) {
	g, b := inertGeneration(t)
	p, e := windows.UTF16PtrFromString(filepath.Join(g.Root, "capacity.lock"))
	if e != nil {
		t.Fatal(e)
	}
	lock, e := windows.CreateFile(p, windows.GENERIC_READ|windows.READ_CONTROL, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := make(chan error, 1)
	go func() { _, e := g.PublishRecord(ctx, "TEST/blocked", BootMilliseconds()+5000); done <- e }()
	joined := false
	defer func() {
		// Every Fatal path releases only our quota lock and cancels this
		// admitted publication, then joins its actual return before cleanup.
		if lock != 0 {
			if e := windows.CloseHandle(lock); e != nil {
				t.Error("owned lock close", e)
			}
			lock = 0
		}
		cancel()
		if !joined {
			select {
			case <-done:
				joined = true
			case <-time.After(6 * time.Second):
				t.Error("publication cleanup collection unknown")
			}
		}
	}()
	// Synchronize on actual admitted work, never manufacture active state. This
	// prevents Close winning before the producer reaches its blocking syscall.
	until := time.Now().Add(time.Second)
	for {
		g.mutex.Lock()
		active := g.active
		g.mutex.Unlock()
		if active != 0 {
			break
		}
		if time.Now().After(until) {
			t.Fatal("publication never admitted")
		}
		time.Sleep(time.Millisecond)
	}
	if e = g.Close(); e != ErrUnknown {
		t.Fatal("live custody released", e)
	}
	checkWrite := func(path string, blocked bool) {
		t.Helper()
		name, e := windows.UTF16PtrFromString(path)
		if e != nil {
			t.Fatal(e)
		}
		h, e := windows.CreateFile(name, windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if e == nil {
			_ = windows.CloseHandle(h)
		}
		if blocked && e != windows.ERROR_SHARING_VIOLATION {
			t.Fatal("live file writable", path, e)
		}
		if !blocked && e != nil {
			t.Fatal("collected file still held", path, e)
		}
	}
	for _, path := range []string{b.SnapshotPath, filepath.Join(g.Root, "helper.exe")} {
		checkWrite(path, true)
	}
	if e = windows.CloseHandle(lock); e != nil {
		t.Fatal(e)
	}
	lock = 0
	select {
	case e = <-done:
		joined = true
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("publication did not collect")
	}
	for _, path := range []string{b.SnapshotPath, filepath.Join(g.Root, "helper.exe")} {
		checkWrite(path, false)
	}
}

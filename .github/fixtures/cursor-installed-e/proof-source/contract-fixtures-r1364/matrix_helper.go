// TEST helper, never product code. Go 1.25.8 / godbus v5.2.2 are build inputs.
package main

import (
    "fmt"
    "encoding/json"
    "os"
    "runtime"
    "strconv"
    "strings"
    "syscall"
    "time"

    "github.com/godbus/dbus/v5"
)

func check(err error) { if err != nil { panic(err) } }

// Evidence describes actual actor results; it never supplies admission facts.
// The observer's real records, fresh identities and consumed monitor replies
// remain the only authority used by the original pair.
func evidence(label string, values map[string]interface{}) {
    values["label"] = label
    values["tid"] = syscall.Gettid()
    values["pid"] = os.Getpid()
    data, err := json.Marshal(values); check(err)
    path := os.Getenv("TEST_MATRIX_EVIDENCE") + "/helper-" + label + ".json"
    f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600); check(err)
    _, err = f.Write(append(data, '\n')); check(err)
    check(f.Sync()); check(f.Close())
}

func inOtherThread(operation func() error) {
    result := make(chan error)
    go func() {
        runtime.LockOSThread()
        defer runtime.UnlockOSThread()
        evidence("destructive-thread", map[string]interface{}{})
        result <- operation()
    }()
    check(<-result)
}

func threadExit(fd int, unshare bool) {
    tidReady := make(chan int)
    go func() {
        runtime.LockOSThread()
        if unshare {
            // Copy the table without closing the tracked socket. This actual
            // table now has a last exiting owner different from the peer owner.
            _, _, e := syscall.RawSyscall(436, 0xfffffffe, 0xfffffffe, 2)
            if e != 0 { panic(e) }
        }
        evidence("exiting-thread", map[string]interface{}{"fd": fd, "unshare": unshare})
        tidReady <- syscall.Gettid()
        syscall.RawSyscall(syscall.SYS_EXIT, 0, 0, 0)
        panic("TEST SYS_EXIT returned")
    }()
    tid := <-tidReady
    deadline := time.Now().Add(1500 * time.Millisecond)
    for {
        _, err := os.Stat(fmt.Sprintf("/proc/self/task/%d", tid))
        if os.IsNotExist(err) { break }
        check(err)
        if time.Now().After(deadline) { panic("TEST thread exit deadline") }
        time.Sleep(time.Millisecond)
    }
}

func main() {
    if len(os.Args) != 5 || strings.Join(os.Args[1:4], " ") != "cursor-event stop --binding" { os.Exit(71) }
    runtime.LockOSThread()
    mode := os.Getenv("TEST_MATRIX_MODE")
    alias, err := syscall.Dup(0); check(err)
    buf := make([]byte, 128)
    payload := []byte{}
    for {
        n, e := syscall.Read(alias, buf); check(e)
        payload = append(payload, buf[:n]...)
        if n == 0 { break }
    }
    check(syscall.Close(alias))
    if string(payload) != "TEST_ORIGINAL_STDIN\n" { os.Exit(72) }
    if mode == "stdin" { return }
    conn, err := dbus.Dial(os.Getenv("DBUS_SESSION_BUS_ADDRESS"))
    if mode == "connect-refused" {
        if err == nil { panic("TEST removed socket unexpectedly connected") }
        evidence("connect-refused", map[string]interface{}{"error": err.Error()})
        return
    }
    check(err)
    if mode == "auth-absent" { check(conn.Close()); return }
    if mode == "auth-refused" {
        err = conn.Auth([]dbus.Auth{dbus.AuthExternal("999999")})
        if err == nil { panic("TEST wrong EXTERNAL unexpectedly admitted") }
        evidence("auth-refused", map[string]interface{}{"error": err.Error()})
        check(conn.Close()); return
    }
    check(conn.Auth(nil))
    if mode == "hello-absent" { check(conn.Close()); return }
    if mode == "hello-refused" {
        // Genuine first Hello, with an invalid argument, receives a real
        // standard daemon ERROR. There is no successful Hello or name fallback.
        call := conn.BusObject().Call("org.freedesktop.DBus.Hello", 0, "TEST invalid Hello argument")
        err = call.Err
        if err == nil { panic("TEST invalid Hello unexpectedly admitted") }
        evidence("hello-refused", map[string]interface{}{"error": err.Error()})
        check(conn.Close())
        return
    }
    check(conn.Hello())
    fd := -1
    entries, err := os.ReadDir("/proc/self/fd"); check(err)
    for _, entry := range entries {
        value, e := strconv.Atoi(entry.Name()); if e != nil { continue }
        address, e := syscall.Getpeername(value)
        if e == nil {
            if unix, ok := address.(*syscall.SockaddrUnix); ok && unix.Name == strings.TrimPrefix(os.Getenv("DBUS_SESSION_BUS_ADDRESS"), "unix:path=") {
                if fd != -1 { panic("TEST ambiguous connected bus socket") }; fd = value
            }
        }
    }
    if fd < 0 { panic("TEST actual bus fd not found") }
    if mode == "final-incarnation-drift" {
        // Export the actual endpoint through the already inherited TEST stage
        // socket. The independent PID1 actor shuts it down only at the root's
        // gate after ORIGINAL credential roundtrips are consumed. These bytes
        // and SCM_RIGHTS never provide observer admission or ACK authority.
        stage, e := strconv.Atoi(os.Getenv("TEST_MATRIX_STAGE_FD")); check(e)
        check(syscall.Sendmsg(stage, []byte("S"), syscall.UnixRights(fd), nil, 0))
        evidence("exported-socket", map[string]interface{}{"fd": fd, "stage": stage})
        check(conn.Close())
        return
    }
    if strings.HasPrefix(mode, "shared-") {
        operation := strings.TrimPrefix(mode, "shared-")
        inOtherThread(func() error {
            switch operation {
            case "alias-close":
                duplicate, e := syscall.Dup(fd); if e != nil { return e }
                return syscall.Close(duplicate)
            case "dup2", "dup3":
                replacement, e := syscall.Open("/dev/null", syscall.O_RDONLY, 0); if e != nil { return e }
                defer syscall.Close(replacement)
                if operation == "dup2" { return syscall.Dup2(replacement, fd) }
                return syscall.Dup3(replacement, fd, syscall.O_CLOEXEC)
            case "shutdown": return syscall.Shutdown(fd, syscall.SHUT_RDWR)
            case "close-range-unshare":
                _, _, e := syscall.RawSyscall(436, uintptr(fd), uintptr(fd), 2)
                if e != 0 { return e }; return nil
            case "close": return conn.Close()
            default: panic("TEST unknown shared operation")
            }
        })
        // The UNSHARE worker closed only its copied table. Main still owns
        // the original descriptor and closes it through the real godbus path.
        check(conn.Close())
        return
    }
    switch mode {
    case "wrong-name":
        names := conn.Names()
        if len(names) == 0 { panic("TEST actual unique name absent") }
        check(conn.Emit("/org/freedesktop/DBus", "org.freedesktop.DBus.NameOwnerChanged", names[0], names[0], ""))
        evidence("wrong-name", map[string]interface{}{"name": names[0]})
        check(conn.Close())
    case "bus-close": check(conn.Close())
    case "alias-close":
        duplicate, e := syscall.Dup(fd); check(e); check(syscall.Close(duplicate)); check(conn.Close())
    case "dup2", "dup3":
        replacement, e := syscall.Open("/dev/null", syscall.O_RDONLY, 0); check(e)
        if mode == "dup2" { check(syscall.Dup2(replacement, fd)) } else { check(syscall.Dup3(replacement, fd, syscall.O_CLOEXEC)) }
        // Closing the replaced numeric fd is not a second credential proof.
        check(conn.Close()); check(syscall.Close(replacement))
    case "shutdown": check(syscall.Shutdown(fd, syscall.SHUT_RDWR)); check(conn.Close())
    case "close-range-unshare":
        _, _, e := syscall.RawSyscall(436, uintptr(fd), uintptr(fd), 2)
        if e != 0 { panic(e) }
        err = conn.Close()
        if err != nil && !os.IsNotExist(err) && !strings.Contains(err.Error(), "bad file descriptor") { panic(err) }
        evidence("close-range-result", map[string]interface{}{"fd": fd, "closeError": fmt.Sprint(err)})
    case "exit-group": syscall.RawSyscall(syscall.SYS_EXIT_GROUP, 0, 0, 0)
    case "thread-exit": threadExit(fd, false); check(conn.Close())
    case "last-table-exit": threadExit(fd, true); panic("TEST last table unexpectedly exited without refusal")
    case "owner-exit":
        go func() {
            runtime.LockOSThread()
            deadline := time.Now().Add(1500 * time.Millisecond)
            for {
                raw, e := os.ReadFile(fmt.Sprintf("/proc/self/task/%d/stat", os.Getpid())); check(e)
                state := strings.Fields(string(raw[strings.LastIndex(string(raw), ")")+2:]))[0]
                if state == "Z" { break }
                if time.Now().After(deadline) { panic("TEST owner exit deadline") }
                time.Sleep(time.Millisecond)
            }
            // A subsequent real entry must not gain authority from a dead
            // peer owner even though this independently owned thread survives.
            check(syscall.Shutdown(fd, syscall.SHUT_RDWR))
            syscall.RawSyscall(syscall.SYS_EXIT_GROUP, 0, 0, 0)
        }()
        syscall.RawSyscall(syscall.SYS_EXIT, 0, 0, 0)
        panic("TEST owner SYS_EXIT returned")
    case "double-name":
        second, e := dbus.Connect(os.Getenv("DBUS_SESSION_BUS_ADDRESS")); check(e)
        check(second.Close()); check(conn.Close())
    default: panic(fmt.Sprintf("TEST unknown matrix mode %q", mode))
    }
}

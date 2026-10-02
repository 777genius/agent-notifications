//go:build darwin && cgo

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
	"syscall"
	"testing"
)

func portFixtureRoot(t *testing.T, dir string) string {
	t.Helper()
	return dir
}

func portProcessSettled(pid int) bool { return syscall.Kill(pid, 0) == syscall.ESRCH }
func portAssertProcessSettled(t *testing.T, pid int) {
	t.Helper()
	if !portProcessSettled(pid) {
		t.Fatal("receipt preceded actual probe Wait")
	}
}

// Native CI only: oversized saved args/environment must deny before the probe
// or callback. The disposable non-serve parent owns and Waits the actual helper.
func TestRuntimeDarwinLargeEnvironmentDeniesBeforeCallback(t *testing.T) {
	dir := t.TempDir()
	image := filepath.Join(dir, "large-env-fixture")
	source := filepath.Join(dir, "fixture.go")
	program := `package main
import("os";"os/exec";"encoding/json";"strings";"strconv";"time";"fmt")
` + portFixtureChildSource + `
func main(){image,_:=os.Executable()
 if len(os.Args)!=2||os.Args[1]!="not-serve" {os.WriteFile(image+".probe",[]byte("probe"),0600);fmt.Print("2.0.21");return}
 prefix:=image+"."+strconv.Itoa(os.Getpid());os.WriteFile(prefix+".host",[]byte("ready"),0600)
 var raw []byte;for {var e error;raw,e=os.ReadFile(prefix+".launch");if e==nil {break};time.Sleep(5*time.Millisecond)}
 var launch struct{Helper,Input string;Environment []string};if json.Unmarshal(raw,&launch)!=nil {os.Exit(3)}
 portRunChild(prefix,launch.Helper,launch.Input,launch.Environment);for {time.Sleep(time.Second)}
}`
	if e := os.WriteFile(source, []byte(program), 0600); e != nil {
		t.Fatal(e)
	}
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", image, source)
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatalf("TEST environment fixture: %v %s", e, out)
	}
	parent := exec.Command(image, "not-serve")
	parent.Env = []string{"PATH=", "PAD=" + strings.Repeat("x", 8192), "/TEST/environment-tail", "fixture", "serve", "TAIL=" + strings.Repeat("x", 3900)}
	if e := parent.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = parent.Process.Kill(); _ = parent.Wait() })
	in := runtimeProfileInput{Protocol: 1, HostExecutable: image, Origin: strings.Repeat("11", 32), ControlRoot: dir, NativePID: parent.Process.Pid, Entry: "serve", PublicExecPath: image}
	portAwait(t, image+"."+strconv.Itoa(parent.Process.Pid)+".host")
	if runtimeDarwinServe(parent.Process.Pid) {
		t.Fatal("large saved environment supplied false serve evidence")
	}
	portLaunch(t, in, in, "", "accept")
	if out := portOutput(t, in); string(out) != portDeniedReceipt {
		t.Fatalf("large saved environment granted: %q", out)
	}
	portNoProbe(t, in)
}
func portPrepareStartFailure(t *testing.T, name string) {
	t.Helper()
	if e := os.Chmod(name, 0600); e != nil {
		t.Fatal(e)
	}
}
func TestRuntimeDarwinSDKRejectsAbsentProcess(t *testing.T) {
	if _, e := runtimeDarwinProcessInfo(-1); e == nil {
		t.Fatal("invalid PID accepted")
	}
	if _, e := runtimeDarwinProcessInfo(os.Getpid()); e != nil {
		t.Fatal("native SDK process/path ABI unavailable", e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if runtimeDarwinMapped(ctx, os.Getpid(), 0, 0) {
		t.Fatal("cancelled region scan succeeded")
	}
	if _, e := verifyRuntimeLiveImage(context.Background(), runtimeProfileInput{NativePID: os.Getpid(), Entry: "serve"}); e == nil {
		t.Fatal("self/invalid target accepted")
	}
}

// Red failure: start seconds/usecs and vnode remain identical on self-exec.
// The retained native event epoch must revoke that otherwise equal snapshot.
func TestRuntimeDarwinSameImageExecRevokesEpoch(t *testing.T) {
	dir := t.TempDir()
	image := filepath.Join(dir, "exec-fixture")
	source := filepath.Join(dir, "fixture.go")
	program := `package main
import("os";"syscall";"time")
func main(){image,_:=os.Executable()
 if os.Getenv("AN_TEST_EXEC_STAGE")=="" {os.WriteFile(image+".ready",[]byte("ready"),0600);for {if _,e:=os.Stat(image+".exec");e==nil {break};time.Sleep(5*time.Millisecond)};if syscall.Exec(image,os.Args,append(os.Environ(),"AN_TEST_EXEC_STAGE=1"))!=nil {os.Exit(4)}}
 os.WriteFile(image+".executed",[]byte("executed"),0600);for {time.Sleep(time.Second)}
}`
	if e := os.WriteFile(source, []byte(program), 0600); e != nil {
		t.Fatal(e)
	}
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", image, source)
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatalf("TEST exec fixture: %v %s", e, out)
	}
	child := exec.Command(image, "serve")
	child.Env = []string{"PATH="}
	if e := child.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	portAwait(t, image+".ready")
	pid := child.Process.Pid
	before, e := runtimeDarwinProcessInfo(pid)
	if e != nil {
		t.Fatal(e)
	}
	fd := runtimeDarwinWatch(pid)
	if fd < 0 {
		t.Fatal("native SDK process epoch unavailable")
	}
	defer unix.Close(fd)
	if !runtimeDarwinUnchanged(fd) {
		t.Fatal("new epoch already revoked")
	}
	if e = os.WriteFile(image+".exec", []byte("exec"), 0600); e != nil {
		t.Fatal(e)
	}
	portAwait(t, image+".executed")
	after, e := runtimeDarwinProcessInfo(pid)
	if e != nil || before != after {
		t.Fatal("fixture did not preserve equal process/image snapshot", e)
	}
	if runtimeDarwinUnchanged(fd) {
		t.Fatal("same-image exec escaped held epoch revocation")
	}
}

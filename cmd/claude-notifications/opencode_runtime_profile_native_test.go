//go:build (darwin && cgo) || windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/opencodehost"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const portDeniedReceipt = "{\"protocol\":1,\"semantic\":\"unverified\",\"generation\":\"none\",\"resourceClosure\":\"reaped_or_not_started\"}\n"
const portEligibleReceipt = "{\"protocol\":1,\"semantic\":\"eligible\",\"generation\":\"v2\",\"resourceClosure\":\"reaped_or_not_started\"}\n"

// Both TEST parent programs retain actual Start/Wait and independent streams.
// Returning the consumed input length keeps draining stderr after the file cap.
const portFixtureChildSource = `
type portDiagnosticWriter struct {file *os.File; remaining int}
func(w *portDiagnosticWriter) Write(p []byte)(int,error){
 n:=len(p);if n>w.remaining {n=w.remaining}
 if _,e:=w.file.Write(p[:n]);e!=nil {return 0,e};w.remaining-=n;return len(p),nil
}
func portRunChild(prefix,helper,input string,environment []string){
 os.WriteFile(prefix+".parent-stage",[]byte("child_streams"),0600)
 output,e:=os.OpenFile(prefix+".receipt",os.O_CREATE|os.O_WRONLY,0600);if e!=nil {os.Exit(4)}
 diagnostics,e:=os.OpenFile(prefix+".stderr",os.O_CREATE|os.O_WRONLY,0600);if e!=nil {os.Exit(5)}
 child:=exec.Command(helper,"-test.run=^TestRuntimePortHelper$");child.Env=environment;child.Stdin=strings.NewReader(input);child.Stdout=output;child.Stderr=&portDiagnosticWriter{diagnostics,4096}
 os.WriteFile(prefix+".parent-stage",[]byte("child_start_wait"),0600)
 status:="ok";if e:=child.Start();e!=nil {status=fmt.Sprintf("start: %v",e)} else {os.WriteFile(prefix+".helper",[]byte(strconv.Itoa(child.Process.Pid)),0600);if e:=child.Wait();e!=nil {status=fmt.Sprintf("wait: %v",e)}}
 output.Close();diagnostics.Close()
 // Existence commits the complete Wait result, never an empty in-progress file.
 done,e:=os.CreateTemp(prefix[:strings.LastIndexByte(prefix,os.PathSeparator)+1],"TEST-port-done-");if e!=nil {os.Exit(6)}
 if _,e=done.Write([]byte(status));e!=nil {done.Close();os.Remove(done.Name());os.Exit(7)}
 if e=done.Close();e!=nil {os.Remove(done.Name());os.Exit(8)}
 if e=os.Rename(done.Name(),prefix+".done");e!=nil {os.Remove(done.Name());os.Exit(9)}
}
`

// SERVER TEST bytes only. The actual serve process owns Start/Wait of its
// helper; the driver is a sibling. These fixtures never qualify official bytes.
func portFixture(t *testing.T) string {
	t.Helper()
	dir := portFixtureRoot(t, t.TempDir())
	program := `package main
import("os";"os/exec";"fmt";"time";"path/filepath";"encoding/json";"strings";"strconv")
` + portFixtureChildSource + `
func main(){
 // Fixture routing uses the launched spelling: Darwin may resolve a hard link
 // to the original executable path. This supplies no production image proof.
 image:=os.Args[0];dir:=filepath.Dir(image);base:=strings.TrimSuffix(filepath.Base(image),".exe")
 if len(os.Args)==2&&os.Args[1]=="serve" {
  prefix:=image+"."+strconv.Itoa(os.Getpid());os.WriteFile(prefix+".parent-stage",[]byte("await_launch"),0600);os.WriteFile(prefix+".host",[]byte("ready"),0600)
  var raw []byte
  for {var e error;raw,e=os.ReadFile(prefix+".launch");if e==nil {break};time.Sleep(5*time.Millisecond)}
  os.WriteFile(prefix+".parent-stage",[]byte("launch_read"),0600)
  var launch struct{Helper,Input string;Environment []string};if json.Unmarshal(raw,&launch)!=nil {os.WriteFile(prefix+".parent-stage",[]byte("launch_invalid"),0600);os.Exit(3)}
  portRunChild(prefix,launch.Helper,launch.Input,launch.Environment)
  for {time.Sleep(time.Second)}
 }
 cwd,_:=os.Getwd();proof,_:=json.Marshal(struct{PID int;Argv,Env []string;Cwd string}{os.Getpid(),os.Args[1:],os.Environ(),cwd});os.WriteFile(filepath.Join(dir,base+".probe"),proof,0600)
 switch base {case "overflow":fmt.Print(strings.Repeat("PRIVATE_RAW",500));case "malformed":fmt.Print("PRIVATE_RAW");case "slow":time.Sleep(30*time.Second);fmt.Print("2.0.21");case "replace":for {if _,e:=os.Stat(image+".continue");e==nil {break};time.Sleep(5*time.Millisecond)};fmt.Print("2.0.21");default:fmt.Print("2.0.21")}
}`
	source := filepath.Join(dir, "fixture.go")
	if e := os.WriteFile(source, []byte(program), 0600); e != nil {
		t.Fatal(e)
	}
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", filepath.Join(dir, "fixture"+portExecutableSuffix()), source)
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatalf("TEST fixture: %v %s", e, out)
	}
	return dir
}
func portExecutableSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}
func portAwait(t *testing.T, path string, prefix ...string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, e := os.Stat(path); e == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(prefix) != 0 {
		t.Fatalf("TEST barrier missing %s; %s", filepath.Base(path), portFailureDiagnostics(prefix[0]))
	}
	t.Fatal("TEST barrier missing", filepath.Base(path))
}
func portPrefix(in runtimeProfileInput) string {
	return in.HostExecutable + "." + strconv.Itoa(in.NativePID)
}
func portProbeName(in runtimeProfileInput) string {
	return strings.TrimSuffix(in.HostExecutable, ".exe") + ".probe"
}
func portStart(t *testing.T, dir, mode string) (runtimeProfileInput, *exec.Cmd) {
	t.Helper()
	image := filepath.Join(dir, mode+portExecutableSuffix())
	if _, e := os.Stat(image); os.IsNotExist(e) {
		data, e := os.ReadFile(filepath.Join(dir, "fixture"+portExecutableSuffix()))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(image, data, 0700); e != nil {
			t.Fatal(e)
		}
	}
	cmd := exec.Command(image, "serve")
	cmd.Env = []string{"PATH="}
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	in := runtimeProfileInput{Protocol: 1, HostExecutable: image, Origin: strings.Repeat("11", 32), ControlRoot: dir, NativePID: cmd.Process.Pid, Entry: "serve", PublicExecPath: image}
	portAwait(t, portPrefix(in)+".host", portPrefix(in))
	return in, cmd
}
func portLaunch(t *testing.T, host, in runtimeProfileInput, raw, mode string) {
	t.Helper()
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	if raw == "" {
		data, e := json.Marshal(in)
		if e != nil {
			t.Fatal(e)
		}
		raw = string(data)
	}
	coverage, e := os.MkdirTemp(host.ControlRoot, "helper-cover-")
	if e != nil {
		t.Fatal("owned helper coverage directory", e)
	}
	env := []string{"AGENT_NOTIFICATIONS_HOST_EXECUTABLE=" + in.HostExecutable, "AGENT_NOTIFICATIONS_ORIGIN=" + in.Origin, "AGENT_NOTIFICATIONS_CONTROL_ROOT=" + in.ControlRoot, "AGENT_NOTIFICATIONS_NATIVE_PID=" + strconv.Itoa(in.NativePID), "AGENT_NOTIFICATIONS_HOST_ENTRY=" + in.Entry, "AGENT_NOTIFICATIONS_PUBLIC_EXEC_PATH=" + in.PublicExecPath, "GOCOVERDIR=" + coverage, "AN_TEST_PORT_HELPER=1", "AN_TEST_PORT_MODE=" + mode, "AN_TEST_PORT_PREFIX=" + portPrefix(host), "TMPDIR=" + os.Getenv("TMPDIR"), "TMP=" + os.Getenv("TMP"), "TEMP=" + os.Getenv("TEMP")}
	data, e := json.Marshal(struct {
		Helper, Input string
		Environment   []string
	}{exe, raw, env})
	if e != nil {
		t.Fatal(e)
	}
	// Publish the complete descriptor before the owned parent can observe it.
	launch, e := os.CreateTemp(host.ControlRoot, "TEST-port-launch-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.Remove(launch.Name())
	if _, e = launch.Write(data); e != nil {
		_ = launch.Close()
		t.Fatal(e)
	}
	if e = launch.Close(); e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(launch.Name(), portPrefix(host)+".launch"); e != nil {
		t.Fatal(e)
	}
}
func portOutput(t *testing.T, in runtimeProfileInput) []byte {
	t.Helper()
	status, e := portReadDone(portPrefix(in)+".done", nil)
	if e != nil || string(status) != "ok" {
		t.Fatalf("helper not waited successfully: %v; first status=%q; %s", e, status, portFailureDiagnostics(portPrefix(in)))
	}
	portAssertDiagnostics(t, portPrefix(in))
	data, e := os.ReadFile(portPrefix(in) + ".receipt")
	if e != nil {
		t.Fatal(e)
	}
	return data
}

// Wait for readable publication; afterRead observes actual IO without replacing it.
func portReadDone(path string, afterRead func(error)) (status []byte, e error) {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		status, e = os.ReadFile(path)
		if afterRead != nil {
			afterRead(e)
		}
		if e == nil || (!errors.Is(e, os.ErrNotExist) && !(runtime.GOOS == "windows" && errors.Is(e, syscall.Errno(32)))) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	return status, e
}

func portBoundedDiagnostic(name string) ([]byte, error) {
	f, e := os.Open(name)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, 4097))
}
func portFailureDiagnostics(prefix string) string {
	stage, stageErr := portBoundedDiagnostic(prefix + ".parent-stage")
	status, statusErr := portBoundedDiagnostic(prefix + ".done")
	diagnostic, diagnosticErr := portBoundedDiagnostic(prefix + ".stderr")
	stdout, stdoutErr := portBoundedDiagnostic(prefix + ".receipt")
	return fmt.Sprintf("parent stage=%q (%v); wait status=%q (%v); stderr=%q (%v); stdout=%q (%v)", stage, stageErr, status, statusErr, diagnostic, diagnosticErr, stdout, stdoutErr)
}
func portAssertDiagnostics(t *testing.T, prefix string) {
	t.Helper()
	diagnostic, e := portBoundedDiagnostic(prefix + ".stderr")
	if e != nil || len(diagnostic) != 0 {
		t.Fatalf("helper stderr must be empty and bounded: %s", portFailureDiagnostics(prefix))
	}
}
func portNoCallback(t *testing.T, in runtimeProfileInput) {
	t.Helper()
	if _, e := os.Stat(in.HostExecutable + ".qualified"); !os.IsNotExist(e) {
		t.Fatal("denied proof invoked trusted callback", e)
	}
}
func portNoProbe(t *testing.T, in runtimeProfileInput) {
	t.Helper()
	if _, e := os.Stat(portProbeName(in)); !os.IsNotExist(e) {
		t.Fatal("unbound proof invoked probe", e)
	}
	portNoCallback(t, in)
}
func portProbeSettled(t *testing.T, in runtimeProfileInput) {
	t.Helper()
	data, e := os.ReadFile(portProbeName(in))
	if e != nil {
		t.Fatal("missing actual probe", e)
	}
	var p struct {
		PID       int
		Argv, Env []string
		Cwd       string
	}
	if json.Unmarshal(data, &p) != nil {
		t.Fatal("invalid fixture proof")
	}
	if strings.Join(p.Argv, ",") != "--version" || !strings.Contains(filepath.Base(p.Cwd), "agentplugins-version-probe-") || p.Cwd == in.ControlRoot {
		t.Fatal("probe argv/cwd changed")
	}
	pathSeen := false
	for _, v := range p.Env {
		if v == "PATH=" {
			pathSeen = true
			continue
		}
		if runtime.GOOS == "windows" && strings.HasPrefix(strings.ToUpper(v), "SYSTEMROOT=") {
			continue
		}
		t.Fatal("ambient probe environment")
	}
	if !pathSeen {
		t.Fatal("probe PATH changed")
	}
	portAssertProcessSettled(t, p.PID)
	if _, e = os.Stat(p.Cwd); !os.IsNotExist(e) {
		t.Fatal("private cwd survived Wait")
	}
}
func TestRuntimePortHelper(t *testing.T) {
	if os.Getenv("AN_TEST_PORT_HELPER") != "1" {
		return
	}
	in := ownedRuntimeDescriptor()
	prefix := os.Getenv("AN_TEST_PORT_PREFIX")
	mode := os.Getenv("AN_TEST_PORT_MODE")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stop := make(chan struct{})
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				if _, e := os.Stat(prefix + ".cancel"); e == nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { close(stop); <-joined }()
	if mode == "lease" || mode == "leaseCancel" || mode == "leaseOriginCancel" {
		held, e := holdRuntimeLiveImage(ctx, in)
		if e != nil {
			t.Fatal("actual parent lease unavailable", e)
		}
		defer held.Close()
		if e = os.WriteFile(prefix+".held", []byte("held"), 0600); e != nil {
			t.Fatal(e)
		}
		for {
			if _, e = os.Stat(prefix + ".recheck"); e == nil {
				break
			}
			if ctx.Err() != nil {
				t.Fatal("lease barrier expired")
			}
			time.Sleep(5 * time.Millisecond)
		}
		check := ctx
		if mode == "leaseCancel" {
			c, stop := context.WithCancel(ctx)
			stop()
			check = c
		}
		if mode == "leaseOriginCancel" {
			cancel()
			check = context.Background()
		}
		if _, e = held.Revalidate(check); e == nil {
			t.Fatal("revoked lease remained live")
		}
		if _, e = held.Revalidate(context.Background()); e == nil {
			t.Fatal("revoked lease renewed by fresh context")
		}
		held.Close()
		if _, e = held.Revalidate(context.Background()); e == nil {
			t.Fatal("closed lease revalidated")
		}
		if e = os.WriteFile(prefix+".settled", []byte("denied"), 0600); e != nil {
			t.Fatal(e)
		}
		return
	}
	var qualify observerQualification
	if mode != "production" {
		qualify = func(p opencodehost.Profile, image runtimeLiveImage) runtimeObserverGeneration {
			if p.Version != "2.0.21" || p.Family != opencodehost.V2 || image.NativePID != in.NativePID || image.GOOS != runtime.GOOS || image.GOARCH != runtime.GOARCH || len(image.SHA256) != 64 || image.fingerprint == [32]byte{} {
				t.Fatal("invalid trusted callback evidence")
			}
			if e := os.WriteFile(in.HostExecutable+".qualified", []byte("TEST"), 0600); e != nil {
				t.Fatal(e)
			}
			return observerV2
		}
	}
	if mode == "startfailure" {
		held, e := holdRuntimeLiveImage(ctx, in)
		if e != nil {
			t.Fatal("start failure obscured valid OS proof", e)
		}
		held.Close()
	}
	var out bytes.Buffer
	if runtimeProfileOperation(ctx, []string{"--protocol", "1"}, os.Stdin, &out, in, qualify) != 0 {
		t.Fatal("transport failed")
	}
	if _, e := os.Stdout.Write(out.Bytes()); e != nil {
		t.Fatal(e)
	}
	os.Exit(0)
}
func TestRuntimePortActualParentAndProbeWait(t *testing.T) {
	dir := portFixture(t)
	for _, mode := range []string{"accept", "production", "malformed", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			in, _ := portStart(t, dir, mode)
			portLaunch(t, in, in, "", mode)
			want := portDeniedReceipt
			if mode == "accept" {
				want = portEligibleReceipt
			}
			if out := portOutput(t, in); string(out) != want {
				t.Fatalf("native ABI/parent receipt: %q", out)
			}
			portProbeSettled(t, in)
			if mode != "accept" {
				portNoCallback(t, in)
			}
		})
	}
}
func TestRuntimePortSiblingInvalidTargetAndOversize(t *testing.T) {
	dir := portFixture(t)
	for _, mode := range []string{"sibling", "target", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			host, _ := portStart(t, dir, mode)
			bad := host
			raw := ""
			switch mode {
			case "sibling":
				bad, _ = portStart(t, dir, mode)
			case "target":
				bad.HostExecutable = filepath.Join(dir, "fixture"+portExecutableSuffix())
				bad.PublicExecPath = bad.HostExecutable
			case "oversize":
				raw = strings.Repeat(" ", 4097)
			}
			portLaunch(t, host, bad, raw, "accept")
			if out := portOutput(t, host); string(out) != portDeniedReceipt {
				t.Fatalf("invalid target granted: %q", out)
			}
			portNoProbe(t, bad)
		})
	}
}
func TestRuntimePortCancelledProbeWaitsBeforeReceipt(t *testing.T) {
	dir := portFixture(t)
	in, _ := portStart(t, dir, "slow")
	portLaunch(t, in, in, "", "accept")
	portAwait(t, portProbeName(in), portPrefix(in))
	if e := os.WriteFile(portPrefix(in)+".cancel", []byte("cancel"), 0600); e != nil {
		t.Fatal(e)
	}
	if out := portOutput(t, in); string(out) != portDeniedReceipt {
		t.Fatalf("cancelled probe granted: %q", out)
	}
	portProbeSettled(t, in)
	portNoCallback(t, in)
}
func TestRuntimePortStartFailureStillSettles(t *testing.T) {
	dir := portFixture(t)
	in, _ := portStart(t, dir, "startfailure")
	portPrepareStartFailure(t, in.HostExecutable)
	portLaunch(t, in, in, "", "startfailure")
	if out := portOutput(t, in); string(out) != portDeniedReceipt {
		t.Fatalf("start failure granted: %q", out)
	}
	portNoProbe(t, in)
}

// Red failure: equal bytes on a new file ID must not preserve the held proof.
func TestRuntimePortCandidateReplacementDeniesAfterActualWait(t *testing.T) {
	dir := portFixture(t)
	parentName := "replacement-parent"
	if runtime.GOOS == "darwin" {
		// Bind the parent to the actual candidate before launch; the subsequent
		// replacement still changes the held candidate's inode after the probe.
		parentName = "replace"
	}
	host, _ := portStart(t, dir, parentName)
	in := host
	in.HostExecutable = filepath.Join(dir, "replace"+portExecutableSuffix())
	in.PublicExecPath = in.HostExecutable
	if runtime.GOOS != "darwin" {
		if e := os.Link(host.HostExecutable, in.HostExecutable); e != nil {
			t.Fatal(e)
		}
	}
	original, e := os.ReadFile(in.HostExecutable)
	if e != nil {
		t.Fatal(e)
	}
	portLaunch(t, host, in, "", "accept")
	portAwait(t, portProbeName(in), portPrefix(host))
	if e = os.Rename(in.HostExecutable, in.HostExecutable+".old"); e != nil {
		t.Fatal("native replacement fixture unavailable", e)
	}
	if e = os.WriteFile(in.HostExecutable, original, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(in.HostExecutable+".continue", []byte("continue"), 0600); e != nil {
		t.Fatal(e)
	}
	if out := portOutput(t, host); string(out) != portDeniedReceipt {
		t.Fatalf("same-byte target replacement granted: %q", out)
	}
	portProbeSettled(t, in)
	portNoCallback(t, in)
}
func TestRuntimePortHeldParentDeathRevokes(t *testing.T) {
	dir := portFixture(t)
	in, parent := portStart(t, dir, "death")
	portLaunch(t, in, in, "", "lease")
	portAwait(t, portPrefix(in)+".held", portPrefix(in))
	// The deliberate orphan uses its own settlement marker and native termination
	// check, since the dead host cannot emit its ordinary child-Wait marker.
	if e := parent.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	_ = parent.Wait()
	if e := os.WriteFile(portPrefix(in)+".recheck", []byte("recheck"), 0600); e != nil {
		t.Fatal(e)
	}
	portAwait(t, portPrefix(in)+".settled", portPrefix(in))
	portNoProbe(t, in)
	portAwait(t, portPrefix(in)+".helper", portPrefix(in))
	raw, e := os.ReadFile(portPrefix(in) + ".helper")
	if e != nil {
		t.Fatal(e)
	}
	pid, e := strconv.Atoi(string(raw))
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if portProcessSettled(pid) {
			portAssertDiagnostics(t, portPrefix(in))
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("orphaned helper did not settle")
}
func TestRuntimePortLeaseCancellationCannotRenew(t *testing.T) {
	dir := portFixture(t)
	in, _ := portStart(t, dir, "leasecancel")
	portLaunch(t, in, in, "", "leaseCancel")
	portAwait(t, portPrefix(in)+".held", portPrefix(in))
	if e := os.WriteFile(portPrefix(in)+".recheck", []byte("recheck"), 0600); e != nil {
		t.Fatal(e)
	}
	portAwait(t, portPrefix(in)+".settled", portPrefix(in))
	_ = portOutput(t, in)
	portNoProbe(t, in)
}

// Red failure: the actual acquired parent lease cannot outlive its invocation,
// even when its first post-cancellation recheck receives a fresh live context.
func TestRuntimePortOriginCancellationFirstFreshRecheckDenies(t *testing.T) {
	dir := portFixture(t)
	in, _ := portStart(t, dir, "origin-cancel")
	portLaunch(t, in, in, "", "leaseOriginCancel")
	portAwait(t, portPrefix(in)+".held", portPrefix(in))
	if e := os.WriteFile(portPrefix(in)+".recheck", []byte("recheck"), 0600); e != nil {
		t.Fatal(e)
	}
	portAwait(t, portPrefix(in)+".settled", portPrefix(in))
	_ = portOutput(t, in)
	portNoProbe(t, in)
}

// Red failure: hashing only bytes/size/mtime misses changed target permissions.
func TestRuntimePortHeldTargetModeMutationRevokes(t *testing.T) {
	dir := portFixture(t)
	in, _ := portStart(t, dir, "modechange")
	portLaunch(t, in, in, "", "lease")
	portAwait(t, portPrefix(in)+".held", portPrefix(in))
	if e := os.Chmod(in.HostExecutable, 0444); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.Chmod(in.HostExecutable, 0700) })
	if e := os.WriteFile(portPrefix(in)+".recheck", []byte("recheck"), 0600); e != nil {
		t.Fatal(e)
	}
	portAwait(t, portPrefix(in)+".settled", portPrefix(in))
	_ = portOutput(t, in)
	portNoProbe(t, in)
}

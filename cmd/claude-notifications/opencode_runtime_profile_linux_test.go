//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/opencodehost"
)

const runtimeReceiptGolden = "{\"protocol\":1,\"semantic\":\"unverified\",\"generation\":\"none\",\"resourceClosure\":\"reaped_or_not_started\"}\n"
const qualifiedReceiptGolden = "{\"protocol\":1,\"semantic\":\"eligible\",\"generation\":\"v2\",\"resourceClosure\":\"reaped_or_not_started\"}\n"

// Ordinary independent TEST native bytes. The serve process itself starts and
// waits for the helper: the purported host and helper must never be siblings.
func nativeProfileFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	program := `package main
import("os";"os/exec";"fmt";"time";"path/filepath";"encoding/json";"strings";"strconv")
type profileDiagnosticWriter struct {file *os.File; remaining int}
func(w *profileDiagnosticWriter) Write(p []byte)(int,error){
 n:=len(p);if n>w.remaining {n=w.remaining}
 if _,e:=w.file.Write(p[:n]);e!=nil {return 0,e};w.remaining-=n;return len(p),nil
}
func publish(name string, body []byte) error {
 f,e:=os.CreateTemp(filepath.Dir(name),"TEST-profile-publication-");if e!=nil{return e};defer os.Remove(f.Name())
 if _,e=f.Write(body);e!=nil{f.Close();return e};if e=f.Close();e!=nil{return e};return os.Rename(f.Name(),name)
}
func main(){
 image,_:=os.Executable();base:=filepath.Base(image);dir:=filepath.Dir(image)
 if len(os.Args)==2&&os.Args[1]=="serve" {
  prefix:=image+"."+strconv.Itoa(os.Getpid())
  stage:=func(value string){_ = publish(prefix+".parent-stage",[]byte(value))}
  stage("await_launch")
  if publish(prefix+".host",[]byte("ready"))!=nil{os.Exit(2)}
  var raw []byte
  for {var err error;raw,err=os.ReadFile(prefix+".launch");if err==nil {break};time.Sleep(5*time.Millisecond)}
  var launch struct {Helper,Input string;Environment []string}
  if json.Unmarshal(raw,&launch)!=nil {stage("launch_decode_error");os.Exit(3)}
  stage("launch_received")
  output,err:=os.OpenFile(prefix+".receipt",os.O_CREATE|os.O_WRONLY,0600);if err!=nil {os.Exit(4)}
  diagnostics,err:=os.OpenFile(prefix+".stderr",os.O_CREATE|os.O_WRONLY,0600);if err!=nil {os.Exit(5)}
  child:=exec.Command(launch.Helper,"-test.run=^TestRuntimeProfileHelperProcess$")
  child.Env=launch.Environment;child.Stdin=strings.NewReader(launch.Input);child.Stdout=output;child.Stderr=&profileDiagnosticWriter{diagnostics,4096}
  status:="ok"
  if child.Start()!=nil {stage("helper_start_error");status="failed"} else {
   if publish(prefix+".helper",[]byte(strconv.Itoa(child.Process.Pid)))!=nil{status="failed"}
   stage("helper_wait")
   if child.Wait()!=nil {stage("helper_wait_error");status="failed"}
  }
  output.Close();diagnostics.Close()
  if publish(prefix+".done",[]byte(status))!=nil{stage("done_publish_error");os.Exit(6)}
  for {time.Sleep(time.Second)}
 }
 cwd,_:=os.Getwd();proof,_:=json.Marshal(map[string]any{"pid":os.Getpid(),"argv":os.Args[1:],"env":os.Environ(),"cwd":cwd})
 os.WriteFile(filepath.Join(dir,base+".probe"),proof,0600)
 switch base {
 case "malformed":fmt.Print("RAW_NATIVE_SENTINEL")
 case "overflow":fmt.Print(strings.Repeat("RAW_NATIVE_SENTINEL",300))
 case "slow":time.Sleep(30*time.Second);fmt.Print("2.0.21")
 case "replace":
  data,_:=os.ReadFile(image);os.Rename(image,image+".old");os.WriteFile(image,data,0700);fmt.Print("2.0.21")
 default:fmt.Print("2.0.21")
 }
}
`
	source := filepath.Join(dir, "fixture.go")
	if err := os.WriteFile(source, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	compiler, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	build := exec.Command(compiler, "build", "-o", filepath.Join(dir, "fixture"), source)
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("TEST native fixture: %v %s", err, out)
	}
	return dir
}
func awaitProfileFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("TEST child barrier missing %s; parent stage=%s", filepath.Base(path), profileParentStage(path))
}
func profileParentStage(barrier string) string {
	prefix := strings.TrimSuffix(barrier, filepath.Ext(barrier))
	f, err := os.Open(prefix + ".parent-stage")
	if err != nil {
		return "unavailable"
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 65))
	if err != nil || len(data) > 64 {
		return "invalid"
	}
	return string(data)
}
func profilePrefix(in runtimeProfileInput) string {
	return in.HostExecutable + "." + strconv.Itoa(in.NativePID)
}
func startLiveFixture(t *testing.T, dir, mode string) runtimeProfileInput {
	t.Helper()
	image := filepath.Join(dir, mode)
	if _, err := os.Stat(image); os.IsNotExist(err) {
		data, readErr := os.ReadFile(filepath.Join(dir, "fixture"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if err = os.WriteFile(image, data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(image, "serve")
	cmd.Env = []string{"PATH="}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	in := runtimeProfileInput{Protocol: 1, HostExecutable: image, Origin: strings.Repeat("11", 32), ControlRoot: dir, NativePID: cmd.Process.Pid, Entry: "serve", PublicExecPath: image}
	awaitProfileFile(t, profilePrefix(in)+".host")
	return in
}
func descriptorEnvironment(in runtimeProfileInput) []string {
	return []string{
		"AGENT_NOTIFICATIONS_HOST_EXECUTABLE=" + in.HostExecutable, "AGENT_NOTIFICATIONS_ORIGIN=" + in.Origin,
		"AGENT_NOTIFICATIONS_CONTROL_ROOT=" + in.ControlRoot, "AGENT_NOTIFICATIONS_NATIVE_PID=" + strconv.Itoa(in.NativePID),
		"AGENT_NOTIFICATIONS_HOST_ENTRY=" + in.Entry, "AGENT_NOTIFICATIONS_PUBLIC_EXEC_PATH=" + in.PublicExecPath,
	}
}
func launchProfileHelper(t *testing.T, host, in runtimeProfileInput, raw, qualification string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if raw == "" {
		body, e := json.Marshal(in)
		if e != nil {
			t.Fatal(e)
		}
		raw = string(body)
	}
	coverage, err := os.MkdirTemp(host.ControlRoot, "helper-cover-")
	if err != nil {
		t.Fatal("owned helper coverage directory", err)
	}
	environment := append(descriptorEnvironment(in), "GOCOVERDIR="+coverage, "AN_TEST_PROFILE_HELPER=1", "TMPDIR="+os.Getenv("TMPDIR"))
	if qualification != "" {
		environment = append(environment, "AN_TEST_PROFILE_QUALIFICATION="+qualification)
	}
	launch := struct {
		Helper, Input string
		Environment   []string
	}{exe, raw, environment}
	data, err := json.Marshal(launch)
	if err != nil {
		t.Fatal(err)
	}
	// The polling parent must observe the complete descriptor, never a newly
	// created but still empty/partial launch file.
	launchFile, err := os.CreateTemp(host.ControlRoot, "TEST-profile-launch-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(launchFile.Name())
	if _, err = launchFile.Write(data); err != nil {
		_ = launchFile.Close()
		t.Fatal(err)
	}
	if err = launchFile.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(launchFile.Name(), profilePrefix(host)+".launch"); err != nil {
		t.Fatal(err)
	}
}
func profileOutput(t *testing.T, host runtimeProfileInput) []byte {
	t.Helper()
	prefix := profilePrefix(host)
	awaitProfileFile(t, prefix+".done")
	status, err := os.ReadFile(prefix + ".done")
	if err != nil || string(status) != "ok" {
		t.Fatal("failure semantics broke valid closure transport", err, string(status))
	}
	diagnostics, err := os.Open(prefix + ".stderr")
	if err != nil {
		t.Fatal(err)
	}
	diagnostic, err := io.ReadAll(io.LimitReader(diagnostics, 4097))
	_ = diagnostics.Close()
	if err != nil || len(diagnostic) != 0 {
		t.Fatalf("helper stderr must be empty and bounded: %q (%v)", diagnostic, err)
	}
	out, err := os.ReadFile(prefix + ".receipt")
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func requiredObservers() []opencodehost.Capability {
	return []opencodehost.Capability{opencodehost.LocalPluginDual, opencodehost.ObserverCompletion, opencodehost.ObserverQuestion, opencodehost.ObserverPermission, opencodehost.ObserverTerminalError}
}
func TestRuntimeProfileHelperProcess(t *testing.T) {
	if os.Getenv("AN_TEST_PROFILE_HELPER") != "1" {
		return
	}
	mode := os.Getenv("AN_TEST_PROFILE_QUALIFICATION")
	if mode == "" {
		os.Args = []string{os.Args[0], "opencode-runtime-profile", "--protocol", "1"}
		main()
		return
	}
	// Synthetic trusted authority at the real composition boundary, confined to
	// this test binary. Production has no environment switch or default grant.
	qualify := func(p opencodehost.Profile, image runtimeLiveImage) runtimeObserverGeneration {
		observed, _ := json.Marshal(struct {
			Profile opencodehost.Profile
			Image   runtimeLiveImage
		}{p, image})
		if err := os.WriteFile(ownedRuntimeDescriptor().HostExecutable+".qualified", observed, 0600); err != nil {
			return observerUnverified
		}
		switch mode {
		case "accept":
			return observerV2
		case "wrongGeneration":
			return observerV1
		case "generic":
			_, err := opencodehost.Select(p, []opencodehost.ArtifactRequirement{{ID: "test-observer", Adapter: opencodehost.ObserverV2, Required: requiredObservers()}})
			if err == nil {
				return observerV2
			}
		}
		return observerUnverified
	}
	started := time.Now()
	signals, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithDeadline(signals, started.Add(10*time.Second))
	defer cancel()
	code := runtimeProfileOperation(ctx, []string{"--protocol", "1"}, os.Stdin, os.Stdout, ownedRuntimeDescriptor(), qualify)
	os.Exit(code)
}
func assertProfileReceipt(t *testing.T, out []byte) {
	t.Helper()
	if string(out) != runtimeReceiptGolden || len(out) > 1024 || bytes.Contains(out, []byte("RAW_NATIVE_SENTINEL")) {
		t.Fatalf("invalid/private receipt: %q", out)
	}
}
func assertNoQualification(t *testing.T, in runtimeProfileInput) {
	t.Helper()
	if _, err := os.Stat(in.HostExecutable + ".qualified"); !os.IsNotExist(err) {
		t.Fatal("unverified parent/image/probe invoked trusted binder", err)
	}
}
func assertNoProbe(t *testing.T, in runtimeProfileInput) {
	t.Helper()
	if _, err := os.Stat(in.HostExecutable + ".probe"); !os.IsNotExist(err) {
		t.Fatal("unverified descriptor/parent invoked probe", err)
	}
	assertNoQualification(t, in)
}
func assertProbeGoneAndPrivateLaunch(t *testing.T, in runtimeProfileInput) {
	t.Helper()
	data, err := os.ReadFile(in.HostExecutable + ".probe")
	if err != nil {
		t.Fatal("synchronous probe was not observed", err)
	}
	var proof struct {
		PID       int `json:"pid"`
		Argv, Env []string
		Cwd       string
	}
	if json.Unmarshal(data, &proof) != nil {
		t.Fatal("invalid TEST evidence")
	}
	if strings.Join(proof.Argv, ",") != "--version" || strings.Join(proof.Env, ",") != "PATH=" || proof.Cwd == filepath.Dir(in.HostExecutable) || !strings.Contains(filepath.Base(proof.Cwd), "agentplugins-version-probe-") {
		t.Fatal("probe inherited ambient environment/cwd/argv", proof)
	}
	if _, err = os.Stat("/proc/" + strconv.Itoa(proof.PID)); !os.IsNotExist(err) {
		t.Fatal("typed closure emitted before actual probe Wait", err)
	}
	if _, err = os.Stat(proof.Cwd); !os.IsNotExist(err) {
		t.Fatal("private probe cwd not reclaimed")
	}
}
func TestRuntimeProfileActualProbeResultsWaitBeforeTypedReceipt(t *testing.T) {
	dir := nativeProfileFixture(t)
	for _, mode := range []string{"success", "malformed", "overflow", "startfailure", "replace"} {
		t.Run(mode, func(t *testing.T) {
			in := startLiveFixture(t, dir, mode)
			if mode == "startfailure" {
				if err := os.Chmod(in.HostExecutable, 0600); err != nil {
					t.Fatal(err)
				}
			}
			qualify := "accept"
			if mode == "success" {
				qualify = ""
			}
			launchProfileHelper(t, in, in, "", qualify)
			assertProfileReceipt(t, profileOutput(t, in))
			assertNoQualification(t, in)
			if mode == "startfailure" {
				assertNoProbe(t, in)
			} else {
				assertProbeGoneAndPrivateLaunch(t, in)
			}
		})
	}
}
func TestRuntimeProfileTERMJoinsSlowInnerProbe(t *testing.T) {
	dir := nativeProfileFixture(t)
	in := startLiveFixture(t, dir, "slow")
	launchProfileHelper(t, in, in, "", "accept")
	awaitProfileFile(t, in.HostExecutable+".probe")
	awaitProfileFile(t, profilePrefix(in)+".helper")
	raw, err := os.ReadFile(profilePrefix(in) + ".helper")
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err = syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	assertProfileReceipt(t, profileOutput(t, in))
	assertProbeGoneAndPrivateLaunch(t, in)
	assertNoQualification(t, in)
}
func TestRuntimeProfileSameImageSiblingDeniedBeforeProbe(t *testing.T) {
	dir := nativeProfileFixture(t)
	host := startLiveFixture(t, dir, "same-image")
	sibling := startLiveFixture(t, dir, "same-image")
	// Every descriptor field matches a genuine serve process of the SAME inode,
	// version and image. Only the actual OS parent differs.
	launchProfileHelper(t, host, sibling, "", "accept")
	assertProfileReceipt(t, profileOutput(t, host))
	assertNoProbe(t, sibling)
}
func TestRuntimeProfileTrustedQualificationReachableOnlyAfterParentProbe(t *testing.T) {
	dir := nativeProfileFixture(t)
	for _, mode := range []string{"accept", "generic", "wrongGeneration"} {
		t.Run(mode, func(t *testing.T) {
			in := startLiveFixture(t, dir, mode)
			launchProfileHelper(t, in, in, "", mode)
			out := profileOutput(t, in)
			assertProbeGoneAndPrivateLaunch(t, in)
			raw, err := os.ReadFile(in.HostExecutable + ".qualified")
			if err != nil {
				t.Fatal("valid actual parent/probe could not reach qualification", err)
			}
			var observed struct {
				Profile opencodehost.Profile
				Image   runtimeLiveImage
			}
			if json.Unmarshal(raw, &observed) != nil {
				t.Fatal("invalid qualification evidence")
			}
			if observed.Profile.Version != "2.0.21" || observed.Profile.Family != opencodehost.V2 || observed.Image.NativePID != in.NativePID || observed.Image.Entry != "serve" || observed.Image.GOOS != runtime.GOOS || observed.Image.GOARCH != runtime.GOARCH || len(observed.Image.SHA256) != 64 || observed.Image.ProcessStartTick == 0 {
				t.Fatal("binder did not receive verified same-host evidence", observed)
			}
			for _, c := range requiredObservers()[1:] {
				if observed.Profile.Capabilities[c] != opencodehost.Unverified {
					t.Fatal("Resolve granted private observer authority")
				}
			}
			if mode == "accept" {
				if string(out) != qualifiedReceiptGolden {
					t.Fatalf("trusted synthetic binder remained unreachable: %q", out)
				}
			} else {
				assertProfileReceipt(t, out)
			}
		})
	}
}
func TestRuntimeProfileClosedDescriptorAndLiveBindingDenyBeforeProbe(t *testing.T) {
	dir := nativeProfileFixture(t)
	template := runtimeProfileInput{Protocol: 1, HostExecutable: filepath.Join(dir, "unused"), Origin: strings.Repeat("11", 32), ControlRoot: dir, NativePID: 1, Entry: "serve", PublicExecPath: filepath.Join(dir, "unused")}
	valid, _ := json.Marshal(template)
	frames := map[string]func(string) string{
		"duplicate": func(raw string) string { return strings.Replace(raw, `"protocol":1`, `"protocol":1,"protocol":1`, 1) },
		"forgedPublicCapabilities": func(raw string) string {
			return strings.Replace(raw, `"protocol":1`, `"protocol":1,"capabilities":{"observer_completion":"supported","observer_question":"supported","observer_permission":"supported","observer_terminal_error":"supported"}`, 1)
		},
		"alias":    func(raw string) string { return strings.Replace(raw, `"nativePID"`, `"NativePID"`, 1) },
		"missing":  func(raw string) string { return strings.Replace(raw, `"entry":"serve",`, "", 1) },
		"null":     func(raw string) string { return strings.Replace(raw, `"entry":"serve"`, `"entry":null`, 1) },
		"trailing": func(raw string) string { return raw + "{}" },
		"overflow": func(raw string) string { return raw + strings.Repeat(" ", 4097) },
	}
	for name, mutate := range frames {
		t.Run(name, func(t *testing.T) {
			in := startLiveFixture(t, dir, name)
			body, err := json.Marshal(in)
			if err != nil {
				t.Fatal(err)
			}
			launchProfileHelper(t, in, in, mutate(string(body)), "accept")
			assertProfileReceipt(t, profileOutput(t, in))
			assertNoProbe(t, in)
		})
	}
	for _, mode := range []string{"pid", "wrongImage", "publicPath", "entry"} {
		t.Run(mode, func(t *testing.T) {
			in := startLiveFixture(t, dir, mode)
			bad := in
			switch mode {
			case "pid":
				bad.NativePID = os.Getpid()
			case "wrongImage":
				bad.HostExecutable = filepath.Join(dir, "fixture")
				bad.PublicExecPath = bad.HostExecutable
			case "publicPath":
				bad.PublicExecPath = filepath.Join(dir, "different")
			case "entry":
				bad.Entry = "tui"
			}
			launchProfileHelper(t, in, bad, "", "accept")
			assertProfileReceipt(t, profileOutput(t, in))
			assertNoProbe(t, in)
		})
	}
	var out bytes.Buffer
	if code := runtimeProfileOperation(context.Background(), []string{"--protocol", "2"}, io.NopCloser(bytes.NewReader(valid)), &out, template, nil); code == 0 || out.Len() != 0 {
		t.Fatal("invalid argv granted transport")
	}
}
func TestRuntimeProfileStalledInputCancellationProvesNotStarted(t *testing.T) {
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	defer func() { _ = writer.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var output bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- runtimeProfileOperation(ctx, []string{"--protocol", "1"}, input, &output, runtimeProfileInput{}, nil)
	}()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatal("not-started transport failed")
		}
	case <-time.After(time.Second):
		t.Fatal("stalled reader survived helper cancellation")
	}
	assertProfileReceipt(t, output.Bytes())
	if _, err = writer.Write([]byte("x")); err == nil {
		t.Fatal("owned helper input not closed")
	}
}

type profileShortWriter struct{}

func (profileShortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }
func TestRuntimeProfilePartialReceiptCannotGrantValidTransport(t *testing.T) {
	if code := runtimeProfileOperation(context.Background(), []string{"--protocol", "1"}, io.NopCloser(strings.NewReader("{}")), profileShortWriter{}, runtimeProfileInput{}, nil); code == 0 {
		t.Fatal("partial inner proof received valid transport exit")
	}
}

package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/777genius/agent-notifications/internal/opencodeevent"
	"github.com/777genius/agent-notifications/internal/strictjson"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/clientdetect"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/opencodehost"
)

type runtimeProfileInput struct {
	Protocol       int    `json:"protocol"`
	HostExecutable string `json:"hostExecutable"`
	Origin         string `json:"origin"`
	ControlRoot    string `json:"controlRoot"`
	NativePID      int    `json:"nativePID"`
	Entry          string `json:"entry"`
	PublicExecPath string `json:"publicExecPath"`
}
type runtimeProfileReceipt struct {
	Protocol        int    `json:"protocol"`
	Semantic        string `json:"semantic"`
	Generation      string `json:"generation"`
	ResourceClosure string `json:"resourceClosure"`
}

// Live image proof is deliberately separate from official host qualification.
// Parent can bind the shared trusted tuple API here when that module lands.
type runtimeLiveImage struct {
	GOOS, GOARCH, Entry, SHA256     string
	Device, Inode, ProcessStartTick uint64
	NativePID                       int
	// Complete OS fingerprint; lifetime handles deliberately stay outside equality.
	fingerprint [32]byte
}
type runtimeObserverGeneration uint8

const (
	observerUnverified runtimeObserverGeneration = iota
	observerV1
	observerV2
)

// This callback is trusted composition authority, never descriptor transport.
// Its implementation must bind the shared private native evidence and Select
// all required observer capabilities. A public Profile/capability map is not a
// binding result. The pinned module lacks that binder; production supplies nil.
type observerQualification func(opencodehost.Profile, runtimeLiveImage) runtimeObserverGeneration

func canonicalPrivatePath(s string) bool {
	return filepath.IsAbs(s) && filepath.Clean(s) == s && !strings.ContainsRune(s, 0)
}
func validPrivateOrigin(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func ownedRuntimeDescriptor() runtimeProfileInput {
	pidText := os.Getenv("AGENT_NOTIFICATIONS_NATIVE_PID")
	pid, _ := strconv.Atoi(pidText)
	if strconv.Itoa(pid) != pidText {
		pid = 0
	}
	return runtimeProfileInput{Protocol: 1, HostExecutable: os.Getenv("AGENT_NOTIFICATIONS_HOST_EXECUTABLE"), Origin: os.Getenv("AGENT_NOTIFICATIONS_ORIGIN"),
		ControlRoot: os.Getenv("AGENT_NOTIFICATIONS_CONTROL_ROOT"), NativePID: pid, Entry: os.Getenv("AGENT_NOTIFICATIONS_HOST_ENTRY"), PublicExecPath: os.Getenv("AGENT_NOTIFICATIONS_PUBLIC_EXEC_PATH")}
}
func runOpenCodeRuntimeProfile(args []string, input io.ReadCloser, output io.Writer) int {
	started := time.Now()
	signals, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithDeadline(signals, started.Add(10*time.Second))
	defer cancel()
	return runtimeProfileOperation(ctx, args, input, output, ownedRuntimeDescriptor(), nil)
}

func runtimeProfileOperation(ctx context.Context, args []string, input io.ReadCloser, output io.Writer, owned runtimeProfileInput, qualify observerQualification) int {
	if len(args) != 2 || args[0] != "--protocol" || args[1] != "1" {
		return 2
	}
	result := runtimeProfileReceipt{1, "unverified", "none", "reaped_or_not_started"}
	// This scope has no asynchronous probe. Until the one synchronous call below,
	// not-started is positive proof; afterwards its Cmd.Run/Wait return proves reap.
	raw, err := opencodeevent.ReadOwnedBounded(ctx, input)
	var in runtimeProfileInput
	valid := err == nil && strictjson.Validate(raw, strictjson.Budget{Bytes: 4096, Depth: 8, Entries: 96}) == nil
	if valid {
		_, err = opencodeevent.ClosedObject(raw, []string{"protocol", "hostExecutable", "origin", "controlRoot", "nativePID", "entry", "publicExecPath"}, nil)
		valid = err == nil && json.Unmarshal(raw, &in) == nil && in == owned && in.Protocol == 1 && in.NativePID > 0 && in.Entry == "serve" &&
			validPrivateOrigin(in.Origin) && canonicalPrivatePath(in.ControlRoot) && canonicalPrivatePath(in.HostExecutable) && in.PublicExecPath == in.HostExecutable
	}
	if valid && ctx.Err() == nil {
		held, liveErr := holdRuntimeLiveImage(ctx, in)
		if liveErr == nil {
			defer held.Close()
			before := held.image
			// Source-backed settlement contract: this exact pinned API runs/waits
			// synchronously, even on startup, output, identity or cancellation error.
			evidence, probeErr := clientdetect.ProbeOpenCodeTarget(ctx, clientdetect.ProbeTarget{Executable: in.HostExecutable, Environment: []string{"PATH="}, Timeout: 10 * time.Second})
			profile := opencodehost.Resolve(evidence.VersionEvidence)
			after, afterErr := held.Revalidate(ctx)
			if probeErr == nil && afterErr == nil && before == after && ctx.Err() == nil && qualify != nil {
				generation := qualify(profile.Clone(), before)
				if generation == observerV1 && profile.Family == opencodehost.ModernV1 || generation == observerV2 && profile.Family == opencodehost.V2 {
					result.Semantic = "eligible"
					if generation == observerV1 {
						result.Generation = "v1"
					} else {
						result.Generation = "v2"
					}
				}
			}
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded)+1 > 1024 {
		return 2
	}
	if n, writeErr := output.Write(append(encoded, '\n')); writeErr != nil || n != len(encoded)+1 {
		return 2
	}
	return 0
}

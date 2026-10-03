package main

import (
	"encoding/hex"
	"strconv"

	"github.com/777genius/agent-notifications/internal/opencodecodec"
	"github.com/777genius/agent-notifications/internal/opencodeevent"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/opencodehost"
)

func runtimeImageKey(live runtimeLiveImage) opencodecodec.ImageKey {
	return opencodecodec.ImageKey{GOOS: live.GOOS, GOARCH: live.GOARCH, Entry: live.Entry, SHA256: live.SHA256}
}

// Pure comparison only. These strings do not authenticate any OS process. The
// only production caller follows the successful synchronous probe, equal held
// before/after images, and an independent packaged-reader qualification lookup.
func selectBoundNativeObserver(e opencodehost.VersionEvidence, actual opencodehost.NativeObserverTuple) runtimeObserverGeneration {
	descriptor, ok := opencodehost.NativeObserverEvidenceForImage(e.Version, actual.GOOS, actual.GOARCH, actual.ImageSHA256)
	if !ok {
		return observerUnverified
	}
	profile := opencodehost.BindNativeObserver(e, actual)
	selected, err := opencodehost.Select(profile, []opencodehost.ArtifactRequirement{{
		ID: "agent-notifications-native-dual", Adapter: descriptor.Tuple.Adapter,
		Required: []opencodehost.Capability{opencodehost.LocalPluginDual, opencodehost.ObserverCompletion,
			opencodehost.ObserverQuestion, opencodehost.ObserverPermission, opencodehost.ObserverTerminalError},
	}})
	if err != nil || selected.ArtifactID == "" || selected.Adapter == "" {
		return observerUnverified
	}
	switch selected.Adapter {
	case opencodehost.ObserverV1:
		return observerV1
	case opencodehost.ObserverV2:
		return observerV2
	default:
		return observerUnverified
	}
}

func qualifyRuntimeObserver(profile opencodehost.Profile, live runtimeLiveImage) runtimeObserverGeneration {
	candidate, ok := opencodecodec.LookupCandidate(runtimeImageKey(live))
	if !ok || profile.Version != candidate.Version || live.NativePID <= 0 {
		return observerUnverified
	}
	// Each held native lease supplies its own private lifetime identity.
	// Darwin/Windows fingerprints include their birth and image metadata;
	// Linux retains the actual proc start tick instead of a synthetic value.
	switch live.GOOS {
	case "linux":
		if live.ProcessStartTick == 0 {
			return observerUnverified
		}
	case "darwin", "windows":
		if live.fingerprint == ([32]byte{}) {
			return observerUnverified
		}
	default:
		return observerUnverified
	}
	reader, bound := runtimeReaderTuple(live, candidate.Version)
	if bound {
		identity := live.SHA256 + ":" + strconv.Itoa(live.NativePID) + ":" + strconv.FormatUint(live.ProcessStartTick, 10) + ":" +
			strconv.FormatUint(live.Device, 10) + ":" + strconv.FormatUint(live.Inode, 10) + ":" + hex.EncodeToString(live.fingerprint[:])
		generation := selectBoundNativeObserver(opencodehost.VersionEvidence{Version: profile.Version, Source: "host_runtime", ProbeStatus: "ok", ExecutableIdentity: identity}, reader)
		if _, clockErr := opencodeevent.SelectTrustedImageClock(runtimeImageKey(live)); clockErr != nil {
			return observerUnverified
		}
		if generation == observerV1 && candidate.Generation == "v1" || generation == observerV2 && candidate.Generation == "v2" {
			return generation
		}
	}
	return observerUnverified
}

// The reader tuple comes from independent qualification data, never by cloning
// NativeObserverEvidence. Image and platform still come from this held parent.
func runtimeReaderTuple(live runtimeLiveImage, version string) (opencodehost.NativeObserverTuple, bool) {
	if _, ok := opencodecodec.LookupCandidate(runtimeImageKey(live)); !ok {
		return opencodehost.NativeObserverTuple{}, false
	}
	descriptor, described := opencodehost.NativeObserverEvidenceForImage(version, live.GOOS, live.GOARCH, live.SHA256)
	if !described {
		return opencodehost.NativeObserverTuple{}, false
	}
	for _, reader := range qualifiedRuntimeReaders {
		if reader == descriptor.Tuple && reader.Version == version && reader.ImageSHA256 == live.SHA256 && reader.GOOS == live.GOOS && reader.GOARCH == live.GOARCH && reader.Entry == "official_native_serve_default_dual_autoload" {
			return reader, true
		}
	}
	return opencodehost.NativeObserverTuple{}, false
}

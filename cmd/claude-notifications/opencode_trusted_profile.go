package main

import (
	"encoding/hex"
	"strconv"

	"github.com/777genius/agent-notifications/internal/opencodecodec"
	"github.com/777genius/agent-notifications/internal/opencodeevent"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/opencodehost"
)

// runtimeImageKey is the identity every candidate, reader and clock lookup uses.
// A publisher-verified family member resolves to the reviewed reference image.
func runtimeImageKey(live runtimeLiveImage) opencodecodec.ImageKey {
	return qualificationKey(live)
}

// The entry is derived from the held native argv, never from event assertions.
// These names select literal reader evidence and do not qualify a new image.
func runtimeReaderEntry(entry string) (string, bool) {
	switch entry {
	case "serve":
		return "official_native_serve_default_dual_autoload", true
	case "tui":
		return "official_native_tui_default_dual_autoload", true
	case "run":
		return "official_native_run_local_dual_autoload", true
	default:
		return "", false
	}
}

// Pure comparison only. These strings do not authenticate any OS process. The
// only production caller follows the successful synchronous probe, equal held
// before/after images, and an independent packaged-reader qualification lookup.
func selectBoundNativeObserver(e opencodehost.VersionEvidence, actual opencodehost.NativeObserverTuple) runtimeObserverGeneration {
	descriptor, ok := opencodehost.NativeObserverEvidenceForEntry(e.Version, actual.GOOS, actual.GOARCH, actual.ImageSHA256, actual.Entry)
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
	// An exact image must match its qualified version. A family member carries the
	// independently probed release version, which must stay inside the family range.
	versionOK := profile.Version == candidate.Version
	if familyEligible(live) {
		versionOK = familyVersionOK(profile.Version)
	}
	if !ok || !versionOK || live.NativePID <= 0 {
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
		generation := selectBoundNativeObserver(opencodehost.VersionEvidence{Version: candidate.Version, Source: "host_runtime", ProbeStatus: "ok", ExecutableIdentity: identity}, reader)
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
	key := runtimeImageKey(live)
	if _, ok := opencodecodec.LookupCandidate(key); !ok {
		return opencodehost.NativeObserverTuple{}, false
	}
	entry, recognized := runtimeReaderEntry(live.Entry)
	if !recognized {
		return opencodehost.NativeObserverTuple{}, false
	}
	descriptor, described := opencodehost.NativeObserverEvidenceForEntry(version, key.GOOS, key.GOARCH, key.SHA256, entry)
	if !described {
		return opencodehost.NativeObserverTuple{}, false
	}
	for _, reader := range qualifiedRuntimeReaders {
		if reader == descriptor.Tuple && reader.Version == version && reader.ImageSHA256 == key.SHA256 && reader.GOOS == key.GOOS && reader.GOARCH == key.GOARCH && reader.Entry == entry {
			return reader, true
		}
	}
	return opencodehost.NativeObserverTuple{}, false
}

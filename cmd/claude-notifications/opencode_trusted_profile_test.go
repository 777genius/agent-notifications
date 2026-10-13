package main

import (
	"encoding/json"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/opencodehost"
	"testing"
)

// Pure binder contract fixtures. Complete strings pass a pure comparison;
// nothing in these tests claims a verified OS parent or native/runtime success.
func TestPureNativeBinderRequiresCompletePrivateTuple(t *testing.T) {
	for _, reader := range qualifiedRuntimeReaders {
		version := reader.Version
		d, ok := opencodehost.NativeObserverEvidenceForImage(version, reader.GOOS, reader.GOARCH, reader.ImageSHA256)
		if !ok {
			t.Fatal("missing source descriptor")
		}
		e := opencodehost.VersionEvidence{Version: version, Source: "host_runtime", ProbeStatus: "ok", ExecutableIdentity: "TEST-pure-comparison-only"}
		want := observerV1
		if version == "2.0.21" {
			want = observerV2
		}
		if got := selectBoundNativeObserver(e, d.Tuple); got != want {
			t.Fatal("complete pure tuple did not select all five facts")
		}
		changes := map[string]func(*opencodehost.NativeObserverTuple){
			"version":    func(v *opencodehost.NativeObserverTuple) { v.Version = "2.0.22" },
			"image":      func(v *opencodehost.NativeObserverTuple) { v.ImageSHA256 = "unknown" },
			"platform":   func(v *opencodehost.NativeObserverTuple) { v.GOOS = "unknown" },
			"arch":       func(v *opencodehost.NativeObserverTuple) { v.GOARCH = "unknown" },
			"entry":      func(v *opencodehost.NativeObserverTuple) { v.Entry = "serve" },
			"reader":     func(v *opencodehost.NativeObserverTuple) { v.ReaderContract = "" },
			"provenance": func(v *opencodehost.NativeObserverTuple) { v.ProvenanceBasis = "" },
			"adapter": func(v *opencodehost.NativeObserverTuple) {
				if v.Adapter == opencodehost.ObserverV1 {
					v.Adapter = opencodehost.ObserverV2
				} else {
					v.Adapter = opencodehost.ObserverV1
				}
			},
			"upstream": func(v *opencodehost.NativeObserverTuple) { v.UpstreamCommit = "" },
			"source":   func(v *opencodehost.NativeObserverTuple) { v.NativeAdapterSHA256 = "" },
			"evidence": func(v *opencodehost.NativeObserverTuple) { v.EvidenceID = "" },
		}
		for i := range d.Tuple.EvidenceHashes {
			index := i
			changes["hash"+string(rune('0'+i))] = func(v *opencodehost.NativeObserverTuple) { v.EvidenceHashes[index] = "" }
		}
		for name, change := range changes {
			t.Run(version+"/"+name, func(t *testing.T) {
				actual := d.Tuple
				change(&actual)
				if selectBoundNativeObserver(e, actual) != observerUnverified {
					t.Fatal("partial/mutated tuple granted binding")
				}
			})
		}
		for _, change := range []func(*opencodehost.VersionEvidence){
			func(v *opencodehost.VersionEvidence) { v.Source = "executable_version" },
			func(v *opencodehost.VersionEvidence) { v.ProbeStatus = "failed" },
			func(v *opencodehost.VersionEvidence) { v.ExecutableIdentity = "" },
		} {
			bad := e
			change(&bad)
			if selectBoundNativeObserver(bad, d.Tuple) != observerUnverified {
				t.Fatal("non-runtime evidence bound")
			}
		}
		p := opencodehost.Resolve(e)
		for _, c := range d.Capabilities {
			p.Capabilities[c] = opencodehost.Supported
		}
		for _, serialized := range []bool{false, true} {
			if serialized {
				raw, _ := json.Marshal(p)
				var detached opencodehost.Profile
				if json.Unmarshal(raw, &detached) != nil {
					t.Fatal("json fixture")
				}
				p = detached
			}
			selected, err := opencodehost.Select(p, []opencodehost.ArtifactRequirement{{ID: "TEST-public", Adapter: d.Tuple.Adapter, Required: []opencodehost.Capability{opencodehost.LocalPluginDual, opencodehost.ObserverCompletion, opencodehost.ObserverQuestion, opencodehost.ObserverPermission, opencodehost.ObserverTerminalError}}})
			if err == nil || selected.ArtifactID != "" || selected.Adapter != "" {
				t.Fatal("public flags/serialization granted private observer authority")
			}
		}
	}
}

// Regression: accepted exact platform evidence cannot inherit copied Linux
// proofs, even when the claimed runtime identity and probe look complete.
func TestPlatformImagesCannotBindCopiedReader(t *testing.T) {
	for _, image := range []struct{ goos, goarch, version, generation, sha string }{
		{"linux", "arm64", "1.18.33", "v1", "986fef2069a03b5181a9ec920786836f98fe3e4950c630941908687854e42757"},
		{"linux", "arm64", "2.0.21", "v2", "d2f4c9ee106d9930d20ca5cf5f2c2216aab6fed836992cf24979d9481242c01c"},
		{"darwin", "amd64", "1.18.33", "v1", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"},
		{"darwin", "amd64", "2.0.21", "v2", "4642b7da61279c8aa5d389d9f29454936e449fea6bc510689e9cc976fff6579f"},
		{"darwin", "arm64", "1.18.33", "v1", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"},
		{"darwin", "arm64", "2.0.21", "v2", "0b2b68c1efaf20a29aaf636c2ffccc1abb56243a82f48cce45e257d232e03442"},
		{"windows", "amd64", "1.18.33", "v1", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"},
		{"windows", "amd64", "2.0.21", "v2", "ec7a3909bad41ef88e4650f737ab6f0b0c402a7f49a588812d0a79820c2dfc1f"},
	} {
		t.Run(image.goos+"/"+image.goarch+"/"+image.version, func(t *testing.T) {
			descriptor, ok := opencodehost.NativeObserverEvidence(image.version)
			if !ok {
				t.Fatal("missing Linux source fixture")
			}
			copied := descriptor.Tuple
			copied.GOOS, copied.GOARCH, copied.ImageSHA256 = image.goos, image.goarch, image.sha
			e := opencodehost.VersionEvidence{Version: image.version, Source: "host_runtime", ProbeStatus: "ok", ExecutableIdentity: "TEST-copied-proof-only"}
			live := runtimeLiveImage{GOOS: image.goos, GOARCH: image.goarch, Entry: "serve", SHA256: image.sha, NativePID: 1}
			// Match the actual native port shape; this is a pure binding fixture,
			// not native process or platform qualification evidence.
			if image.goos == "linux" {
				live.ProcessStartTick = 1
			} else {
				live.fingerprint = [32]byte{1}
			}
			if selectBoundNativeObserver(e, copied) != observerUnverified {
				t.Fatal("copied Linux proofs bound another image")
			}
			reader, ok := runtimeReaderTuple(live, image.version)
			if !ok {
				t.Fatal("accepted image missing independent literal reader")
			}
			want := observerV1
			if image.generation == "v2" {
				want = observerV2
			}
			if selectBoundNativeObserver(e, reader) != want || qualifyRuntimeObserver(opencodehost.Resolve(e), live) != want {
				t.Fatal("exact independent reader and clock data did not select")
			}
			missingIdentity := live
			if image.goos == "linux" {
				missingIdentity.ProcessStartTick = 0
				missingIdentity.fingerprint = [32]byte{1}
			} else {
				missingIdentity.fingerprint = [32]byte{}
				missingIdentity.ProcessStartTick = 1
			}
			if qualifyRuntimeObserver(opencodehost.Resolve(e), missingIdentity) != observerUnverified {
				t.Fatal("another platform's identity substituted for missing native lifetime proof")
			}
			unknownPlatform := live
			unknownPlatform.GOOS = "unknown"
			if qualifyRuntimeObserver(opencodehost.Resolve(e), unknownPlatform) != observerUnverified {
				t.Fatal("unknown platform inherited native observer authority")
			}
			live.SHA256 = "unknown"
			if _, ok := runtimeReaderTuple(live, image.version); ok || qualifyRuntimeObserver(opencodehost.Resolve(e), live) != observerUnverified {
				t.Fatal("unknown image inherited reader or clock eligibility")
			}
		})
	}
}

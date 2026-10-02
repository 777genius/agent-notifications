package main

import (
	"encoding/json"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/opencodehost"
	"testing"
)

// Pure binder contract fixtures. Complete strings pass a pure comparison;
// nothing in these tests claims a verified OS parent or native/runtime success.
func TestPureNativeBinderRequiresCompletePrivateTuple(t *testing.T) {
	for _, version := range []string{"1.18.33", "1.18.34", "2.0.21"} {
		d, ok := opencodehost.NativeObserverEvidence(version)
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
			"platform":   func(v *opencodehost.NativeObserverTuple) { v.GOOS = "windows" },
			"arch":       func(v *opencodehost.NativeObserverTuple) { v.GOARCH = "arm64" },
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

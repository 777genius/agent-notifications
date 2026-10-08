package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// These checks fail if omitted flags opt in to Webhook, reset explicit false,
// couple the two products, or make the frozen dispatch differ from the summary.
func TestBootstrapObserverChannelDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, products, policy string
		flags                  []string
		want                   [][2]bool
	}{
		{name: "fresh", products: "opencode,gemini", want: [][2]bool{{true, false}, {true, false}}},
		{name: "preserve-independent", products: "opencode,gemini", policy: `{"openCodeNotifications":{"desktop":false,"webhook":false},"geminiNotifications":{"desktop":false,"webhook":true}}`, want: [][2]bool{{false, false}, {false, true}}},
		{name: "shared-destination-grants-nothing", products: "gemini", policy: `{"openCodeNotifications":{"desktop":false,"webhook":true},"foreign":{"destination":"TEST"}}`, want: [][2]bool{{true, false}}},
		{name: "explicit-selected-only", products: "opencode", policy: `{"openCodeNotifications":{"desktop":false,"webhook":true},"geminiNotifications":{"desktop":false,"webhook":true}}`, flags: []string{"--desktop"}, want: [][2]bool{{true, false}}},
		{name: "explicit-disabled", products: "gemini", flags: []string{"--desktop=false", "--webhook=false"}, want: [][2]bool{{false, false}}},
		{name: "legacy-webhook-only", products: "opencode,gemini", flags: []string{"--webhook"}, want: [][2]bool{{false, true}, {false, true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, err := parseSetupProducts(append([]string{"confirm", "--products", tc.products}, tc.flags...))
			if err != nil {
				t.Fatal(err)
			}
			i := confirmedBootstrapIntent{Request: a, Initial: bootstrapInitialObservation{ChannelPolicy: json.RawMessage(tc.policy)}}
			for n, id := range a.Products {
				d, w, err := resolveBootstrapChannels(a, id, i.Initial.ChannelPolicy)
				if err != nil || [2]bool{d, w} != tc.want[n] {
					t.Fatalf("%s: %v %v %v", id, d, w, err)
				}
				i.Units = append(i.Units, bootstrapProductUnits{Product: id, Native: true, Desktop: d, Webhook: w})
			}
			var dispatch bytes.Buffer
			if err := writeIntentScalars(&dispatch, i); err != nil {
				t.Fatal(err)
			}
			rows, err := bootstrapIntentSummary(i, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, u := range i.Units {
				expected := u.Product + "-desktop\x00" + fmt.Sprint(u.Desktop) + "\x00" + u.Product + "-webhook\x00" + fmt.Sprint(u.Webhook) + "\x00"
				if !bytes.Contains(dispatch.Bytes(), []byte(expected)) || !strings.Contains(strings.Join(rows, "\n"), "Channels: Desktop "+bootstrapOnOff(u.Desktop)+", Webhook "+bootstrapOnOff(u.Webhook)) {
					t.Fatalf("inconsistent frozen decision: %q %v", dispatch.Bytes(), rows)
				}
			}
		})
	}
	for _, raw := range []string{`null`, `{"geminiNotifications":null}`, `{"geminiNotifications":{"desktop":false}}`, `{"geminiNotifications":{"desktop":"false","webhook":true}}`} {
		if _, _, err := resolveBootstrapChannels(setupProductsArgs{}, "gemini", json.RawMessage(raw)); err == nil {
			t.Fatalf("malformed saved choice accepted: %s", raw)
		}
	}
}

// Shared policies legitimately allow more entries than the intent codec. Only
// selected channel decisions must enter that bounded immutable handoff.
func TestBootstrapChannelIntentOmitsUnrelatedPolicy(t *testing.T) {
	i, path := bootstrapCodecFixture(t)
	route := map[string]any{"geminiNotifications": map[string]bool{"desktop": false, "webhook": true}, "openCodeNotifications": map[string]bool{"desktop": true, "webhook": true}}
	for n := 0; n < 300; n++ {
		route[fmt.Sprintf("TEST-foreign-%d", n)] = map[string]any{"keep": n}
	}
	data, err := json.Marshal(route)
	if err != nil {
		t.Fatal(err)
	}
	i.Initial.ChannelPolicy, err = selectedBootstrapChannelPolicy(i.Request.Products, data)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(i.Initial.ChannelPolicy, []byte("TEST-foreign")) || bytes.Contains(i.Initial.ChannelPolicy, []byte("openCodeNotifications")) {
		t.Fatal("unselected preferences entered handoff")
	}
	if err := writeBootstrapIntent(path, i); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadBootstrapIntent(path, i.Provenance)
	if err != nil {
		t.Fatal(err)
	}
	var dispatch bytes.Buffer
	if err := writeIntentScalars(&dispatch, loaded); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(dispatch.Bytes(), []byte("gemini-desktop\x00false\x00gemini-webhook\x00true\x00")) {
		t.Fatalf("saved channel decision lost: %q", dispatch.Bytes())
	}
}

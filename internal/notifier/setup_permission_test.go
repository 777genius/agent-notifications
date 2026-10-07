package notifier

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/777genius/agent-notifications/internal/notifier/nativeprotocol"
	"testing"
)

type setupProcessFake struct {
	status                                  string
	baseInvalid, setupInvalid, requestError bool
	requests, setups, probes                int
}

func (f *setupProcessFake) Probe(context.Context, string) ([]byte, error) {
	f.probes++
	if f.baseInvalid {
		return []byte(`{}`), nil
	}
	return []byte(`{"schemaVersion":1,"protocolVersions":[1],"actionKinds":["none"],"receiptSupport":true,"backend":"macos.usernotifications","explicitFeatureEnabledByDefault":false}`), nil
}
func (f *setupProcessFake) ProbeSetup(context.Context, string) ([]byte, error) {
	f.setups++
	if f.setupInvalid {
		return []byte(`{}`), nil
	}
	return []byte(`{"schemaVersion":1,"permissionRequestVersions":[1],"backend":"macos.usernotifications"}`), nil
}
func (f *setupProcessFake) ProbePermission(_ context.Context, _ string, id, nonce string) ([]byte, error) {
	return json.Marshal(nativeprotocol.Permission{SchemaVersion: 1, CorrelationID: id, Nonce: nonce, Backend: "macos.usernotifications", Permission: f.status})
}
func (f *setupProcessFake) RequestPermission(c context.Context, e, id, nonce string) ([]byte, error) {
	f.requests++
	if f.requestError {
		return nil, errors.New("lost reply")
	}
	f.status = "allowed"
	return f.ProbePermission(c, e, id, nonce)
}
func TestSetupPermissionSequence(t *testing.T) {
	for _, status := range []string{"allowed", "denied", "undetermined", "unavailable"} {
		for _, request := range []bool{false, true} {
			f := &setupProcessFake{status: status}
			got, err := setupPermissionSequence(context.Background(), f, "/fixture", request)
			want := status
			n := 0
			if request && status == "undetermined" {
				want = "allowed"
				n = 1
			}
			if err != nil || got != want || f.requests != n {
				t.Fatalf("%s/%v: %s %v requests%d", status, request, got, err, f.requests)
			}
			if !request && f.setups != 0 {
				t.Fatal("readonly status probed authorization support")
			}
		}
	}
	for _, f := range []*setupProcessFake{{status: "undetermined", baseInvalid: true}, {status: "undetermined", setupInvalid: true}, {status: "undetermined", requestError: true}} {
		got, err := setupPermissionSequence(context.Background(), f, "/fixture", true)
		if err == nil || got != "unavailable" || f.requests > 1 {
			t.Fatal("unsafe retry/success")
		}
		if !f.requestError && f.requests != 0 {
			t.Fatal("unsupported helper invoked")
		}
	}
}

func TestSetupPermissionCanceledBeforeProbe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, ctx := range []context.Context{nil, ctx} {
		f := &setupProcessFake{status: "undetermined"}
		got, err := setupPermissionSequence(ctx, f, "/fixture", true)
		if err == nil || got != "unavailable" || f.probes != 0 || f.setups != 0 || f.requests != 0 {
			t.Fatal("canceled setup started process")
		}
	}
}

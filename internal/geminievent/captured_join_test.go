package geminievent

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/geminisource"
)

type capturedJoinInput struct {
	InstallationID string `json:"installation_id"`
	Generation     uint64 `json:"generation"`
	Event          string `json:"event"`
	Payload        []byte `json:"payload"`
	Entries        []struct {
		Key  string `json:"key"`
		Bits uint8  `json:"bits"`
	} `json:"entries"`
}

type capturedJoinResult struct {
	Class            string `json:"class"`
	Matched          bool `json:"matched"`
	WebhookAttempted bool `json:"webhook_attempted"`
}

// Pure TEST reader: the real SDK decoder and private production marker are used,
// but no consumer, policy lease, claim, clock/cache write or delivery is created.
func capturedJoin(input io.Reader) (out capturedJoinResult) {
	out.Class = "unavailable"
	defer func() {
		if recover() != nil {
			out = capturedJoinResult{Class: "unavailable"}
		}
	}()
	raw, err := io.ReadAll(io.LimitReader(input, 1048577))
	if err != nil || len(raw) > 1048576 {
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var in capturedJoinInput
	if decoder.Decode(&in) != nil || len(in.Payload) == 0 || len(in.Payload) > geminisource.MaxPayloadBytes || len(in.Entries) > 256 || len(in.InstallationID) == 0 || len(in.InstallationID) > 128 || in.Generation == 0 {
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return
	}
	if in.Event != geminisource.AfterAgent && in.Event != geminisource.Notification {
		return
	}
	seen := make(map[string]uint8, len(in.Entries))
	for _, entry := range in.Entries {
		decoded, err := hex.DecodeString(entry.Key)
		if err != nil || len(decoded) != 32 || strings.ToLower(entry.Key) != entry.Key || entry.Bits < 1 || entry.Bits > 3 || seen[entry.Key] != 0 {
			return
		}
		seen[entry.Key] = entry.Bits
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	facts, err := geminisource.Decode(ctx, in.Event, in.Payload)
	if err != nil || ctx.Err() != nil || facts.Timestamp == "" || facts.SessionID == "" {
		return
	}
	if facts.Event != geminisource.AfterAgent && facts.Event != geminisource.Notification || facts.Event == geminisource.AfterAgent && facts.StopHookActive || facts.Event == geminisource.Notification && facts.Subtype != geminisource.ToolPermission {
		return
	}
	bits := seen[marker(Binding{InstallationID: in.InstallationID, Generation: in.Generation}, facts)]
	return capturedJoinResult{Class: "joined", Matched: bits != 0, WebhookAttempted: bits&2 != 0}
}

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--AN-TEST-captured-cache-join" {
		_ = json.NewEncoder(os.Stdout).Encode(capturedJoin(os.Stdin))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// Independent wire/key fixture: wrong installation/generation/native identity
// must not borrow a real stored claim, and a missing key is not a delivery proof.
func TestCapturedCacheJoinFixedKeyAndPrivacy(t *testing.T) {
	request := `{"installation_id":"TEST-installation","generation":7,"event":"Notification","payload":"eyJob29rX2V2ZW50X25hbWUiOiJOb3RpZmljYXRpb24iLCJzZXNzaW9uX2lkIjoiVEVTVC1zZXNzaW9uIiwidGltZXN0YW1wIjoiMjAyNi0xMC0wMVQwNTowMDowMFoiLCJub3RpZmljYXRpb25fdHlwZSI6IlRvb2xQZXJtaXNzaW9uIn0=","entries":[{"key":"5662cf0d792993ce40599374010621b7de560f0cbb18745ea24b4cd5494a1fac","bits":2}]}`
	got := capturedJoin(strings.NewReader(request))
	if got.Class != "joined" || !got.Matched || !got.WebhookAttempted {
		t.Fatalf("independent key did not join: %+v", got)
	}
	for _, changed := range []string{strings.Replace(request, `"generation":7`, `"generation":8`, 1), strings.Replace(request, "TEST-installation", "other-installation", 1)} {
		got := capturedJoin(strings.NewReader(changed))
		if got.Class != "joined" || got.Matched || got.WebhookAttempted {
			t.Fatal("foreign binding borrowed claim")
		}
	}
	if got := capturedJoin(strings.NewReader(strings.Replace(request, `"event":"Notification"`, `"event":"AfterAgent"`, 1))); got.Class != "unavailable" {
		t.Fatal("selector mismatch accepted")
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "5662") || strings.Contains(string(encoded), "TEST-session") {
		t.Fatal("private key/facts escaped")
	}
}

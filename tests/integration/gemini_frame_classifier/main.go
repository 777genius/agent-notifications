// The TEST orchestrator supplies already captured synthetic frames. This tool
// has no consumer, cache, policy lease or delivery port and never emits Facts.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/777genius/agent-notifications/internal/geminisource"
)

type result struct {
	Version          int    `json:"version"`
	Classification   string `json:"classification"`
	Eligible         bool   `json:"eligible"`
	TimestampPresent bool   `json:"timestamp_present"`
	SessionSHA256    string `json:"session_sha256,omitempty"`
	TimestampSHA256  string `json:"timestamp_sha256,omitempty"`
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func classify(ctx context.Context, selector string, input io.Reader) result {
	r := result{Version: 1, Classification: "invalid"}
	payload, err := io.ReadAll(io.LimitReader(input, geminisource.MaxPayloadBytes+1))
	if err != nil || len(payload) == 0 || len(payload) > geminisource.MaxPayloadBytes {
		return r
	}
	facts, err := geminisource.Decode(ctx, selector, payload)
	if err != nil {
		return r
	}
	r.Classification = "decoded"
	r.Eligible = facts.Event == geminisource.AfterAgent && !facts.StopHookActive || facts.Event == geminisource.Notification && facts.Subtype == geminisource.ToolPermission
	r.TimestampPresent = facts.Timestamp != ""
	r.SessionSHA256, r.TimestampSHA256 = digest(facts.SessionID), digest(facts.Timestamp)
	return r
}

func main() {
	r := result{Version: 1, Classification: "invalid"}
	if len(os.Args) == 2 {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		r = classify(ctx, os.Args[1], os.Stdin)
		cancel()
	}
	// The trusted orchestrator bounds and collects this process, including a
	// blocked stdin pipe. Decoder time is bounded independently of old delivery.
	_ = json.NewEncoder(os.Stdout).Encode(r)
}

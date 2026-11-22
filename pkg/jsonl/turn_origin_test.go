package jsonl

import (
	"strings"
	"testing"
)

// Records shaped like the ones Claude Code 2.1.32x writes.
const (
	toHuman        = `{"type":"user","origin":{"kind":"human"},"message":{"role":"user","content":"do the thing"}}`
	toHumanArray   = `{"type":"user","origin":{"kind":"human"},"message":{"role":"user","content":[{"type":"text","text":"again?"}]}}`
	toTaskNotif    = `{"type":"user","origin":{"kind":"task-notification","producer":"session-task"},"message":{"role":"user","content":"<task-notification>done</task-notification>"}}`
	toPeer         = `{"type":"user","isMeta":true,"origin":{"kind":"peer","from":"reviewer"},"message":{"role":"user","content":"Another Claude session sent a message:\n..."}}`
	toNoOrigin     = `{"type":"user","message":{"role":"user","content":"Another Claude session sent a message:\n<teammate-message>"}}`
	toIdleNotice   = `{"type":"user","isMeta":true,"message":{"role":"user","content":"[Cross-session idle notice] \"worker\" is idle"}}`
	toSkillText    = `{"type":"user","isMeta":true,"message":{"role":"user","content":[{"type":"text","text":"Base directory for this skill: /x"}]}}`
	toCommand      = `{"type":"user","message":{"role":"user","content":"<command-name>/review</command-name>"}}`
	toInterrupt    = `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}`
	toCompact      = `{"type":"user","isCompactSummary":true,"message":{"role":"user","content":"This session is being continued from a previous conversation"}}`
	toBoundary     = `{"type":"system","subtype":"compact_boundary"}`
	toAttachment   = `{"type":"attachment"}`
	toToolUse      = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash"}]}}`
	toToolResult   = `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`
	toAssistantTxt = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`
	toRepoOrigin   = `{"type":"user","origin":{"host":"github.com","remote":"o/r"},"message":{"role":"user","content":"hi"}}`
)

func TestTurnOrigin(t *testing.T) {
	for _, tc := range []struct {
		name      string
		records   []string
		wantKind  string
		wantKnown bool
	}{
		{name: "empty transcript", wantKnown: false},
		{name: "legacy transcript without origins",
			records:   []string{toNoOrigin, toToolUse, toToolResult, toAssistantTxt},
			wantKnown: false},
		{name: "origin without kind is legacy",
			records:   []string{toRepoOrigin, toAssistantTxt},
			wantKnown: false},
		{name: "typed input",
			records:  []string{toHuman, toToolUse, toToolResult, toAssistantTxt},
			wantKind: OriginKindHuman, wantKnown: true},
		{name: "typed input with array content",
			records:  []string{toHumanArray, toAssistantTxt},
			wantKind: OriginKindHuman, wantKnown: true},
		{name: "subagent result after a finished human turn",
			records:  []string{toHuman, toAssistantTxt, toTaskNotif, toToolUse, toToolResult, toAssistantTxt},
			wantKind: "task-notification", wantKnown: true},
		{name: "peer message",
			records:  []string{toHuman, toAssistantTxt, toPeer, toAssistantTxt},
			wantKind: "peer", wantKnown: true},
		{name: "teammate message without origin",
			records:  []string{toHuman, toAssistantTxt, toNoOrigin, toAssistantTxt},
			wantKind: "", wantKnown: true},
		{name: "idle notice without origin",
			records:  []string{toHuman, toAssistantTxt, toIdleNotice, toAssistantTxt},
			wantKind: "", wantKnown: true},
		{name: "slash command batch counts as human",
			records:  []string{toHuman, toAssistantTxt, toCommand, toHuman, toSkillText, toAttachment, toAssistantTxt},
			wantKind: OriginKindHuman, wantKnown: true},
		{name: "skill loaded mid-turn continues the human turn",
			records:  []string{toHuman, toToolUse, toToolResult, toSkillText, toToolUse, toToolResult, toAssistantTxt},
			wantKind: OriginKindHuman, wantKnown: true},
		{name: "subagent result queued mid-turn continues the human turn",
			records:  []string{toHuman, toToolUse, toToolResult, toTaskNotif, toAssistantTxt},
			wantKind: OriginKindHuman, wantKnown: true},
		{name: "skill loaded mid-turn continues the peer turn",
			records:  []string{toHuman, toAssistantTxt, toPeer, toToolUse, toToolResult, toSkillText, toAssistantTxt},
			wantKind: "peer", wantKnown: true},
		{name: "human interjection mid-turn",
			records:  []string{toHuman, toAssistantTxt, toPeer, toToolUse, toToolResult, toHuman, toAssistantTxt},
			wantKind: OriginKindHuman, wantKnown: true},
		{name: "interrupt then typed input",
			records:  []string{toHuman, toToolUse, toToolResult, toInterrupt, toHuman, toAssistantTxt},
			wantKind: OriginKindHuman, wantKnown: true},
		{name: "compaction mid-turn continues the human turn",
			records:  []string{toHuman, toToolUse, toToolResult, toAssistantTxt, toBoundary, toCompact, toAssistantTxt},
			wantKind: OriginKindHuman, wantKnown: true},
		{name: "compaction mid-turn continues the peer turn",
			records:  []string{toHuman, toAssistantTxt, toPeer, toToolUse, toToolResult, toBoundary, toCompact, toAssistantTxt},
			wantKind: "peer", wantKnown: true},
		{name: "peer message right after compaction",
			records:  []string{toHuman, toAssistantTxt, toBoundary, toCompact, toPeer, toAssistantTxt},
			wantKind: "peer", wantKnown: true},
		{name: "untyped input after compaction does not inherit earlier human turn",
			records: []string{toHuman, toAssistantTxt, toCompact, toNoOrigin, toAssistantTxt},
			wantKind: "", wantKnown: true},
		{name: "summary-only batch keeps earlier human turn",
			records: []string{toHuman, toAssistantTxt, toCompact, toAssistantTxt},
			wantKind: OriginKindHuman, wantKnown: true},
		{name: "typed input right after compaction",
			records:  []string{toPeer, toAssistantTxt, toBoundary, toCompact, toHuman, toAssistantTxt},
			wantKind: OriginKindHuman, wantKnown: true},
		{name: "compaction mid-turn continues the subagent turn",
			records:  []string{toCompact, toAssistantTxt, toTaskNotif, toToolUse, toToolResult, toBoundary, toCompact, toAssistantTxt},
			wantKind: "task-notification", wantKnown: true},
		{name: "turn input not yet answered",
			records:  []string{toHuman, toAssistantTxt, toTaskNotif},
			wantKind: "task-notification", wantKnown: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			messages, err := Parse(strings.NewReader(strings.Join(tc.records, "\n")))
			if err != nil {
				t.Fatal(err)
			}
			kind, known := TurnOrigin(messages)
			if kind != tc.wantKind || known != tc.wantKnown {
				t.Fatalf("TurnOrigin() = (%q, %v), want (%q, %v)", kind, known, tc.wantKind, tc.wantKnown)
			}
		})
	}
}

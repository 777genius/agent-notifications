package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/summary"
)

// TurnInsight is the policy-relevant view of a Codex event derived by an
// enricher: the notification status plus an optional pre-rendered body.
// An empty Body means "let the message generator use its defaults".
type TurnInsight struct {
	Status analyzer.Status
	Body   string
}

// CodexTurnEnricher derives notification policy inputs for Codex events.
//
// This is the seam for richer turn analysis: the default implementation is a
// pure heuristic over the hook payload, and a future adapter may consult the
// Codex app-server thread/turn API instead. Implementations must be
// side-effect-free and fast — they run inside the hook's time budget and
// must never block delivery on external state.
type CodexTurnEnricher interface {
	// EnrichStop classifies a completed (sub)agent turn.
	EnrichStop(ctx context.Context, ev Event, p StopPayload) TurnInsight
	// EnrichPreToolUse classifies an interactive-tool call. A returned
	// StatusUnknown means "no notification for this tool".
	EnrichPreToolUse(ctx context.Context, ev Event, p PreToolUsePayload) TurnInsight
}

// codexQuestionTool is the Codex tool that asks the user questions; it is
// the only PreToolUse tool the default policy reacts to (hooks-codex.json
// matches only this tool, this is a second line of defense).
const codexQuestionTool = "request_user_input"

// heuristicEnricher is the default CodexTurnEnricher: payload-only, no IO.
type heuristicEnricher struct{}

func (heuristicEnricher) EnrichStop(_ context.Context, _ Event, p StopPayload) TurnInsight {
	return TurnInsight{Status: analyzer.ClassifyLastMessage(p.AssistantMessage)}
}

func (heuristicEnricher) EnrichPreToolUse(_ context.Context, _ Event, p PreToolUsePayload) TurnInsight {
	if p.ToolName != codexQuestionTool {
		return TurnInsight{Status: analyzer.StatusUnknown}
	}
	return TurnInsight{
		Status: analyzer.StatusQuestion,
		Body:   questionBodyFromToolInput(p.ToolInput),
	}
}

// requestUserInputArgs mirrors the allowlisted subset of the Codex
// request_user_input tool schema. Only header/question text is ever
// projected into a notification body; option lists, ids, and secret flags
// stay out.
type requestUserInputArgs struct {
	Questions []struct {
		Header   string `json:"header"`
		Question string `json:"question"`
	} `json:"questions"`
}

func questionBodyFromToolInput(toolInput json.RawMessage) string {
	var args requestUserInputArgs
	if err := json.Unmarshal(toolInput, &args); err != nil || len(args.Questions) == 0 {
		return ""
	}

	first := strings.TrimSpace(args.Questions[0].Question)
	if first == "" {
		first = strings.TrimSpace(args.Questions[0].Header)
	}
	if first == "" {
		return ""
	}

	body := truncateRunes(summary.CleanMarkdown(first), 150)
	if extra := len(args.Questions) - 1; extra > 0 {
		body = fmt.Sprintf("%s (+%d more)", body, extra)
	}
	return body
}

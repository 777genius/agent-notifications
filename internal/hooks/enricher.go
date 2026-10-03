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
	Status   analyzer.Status
	Body     string
	Question string // current tool question, independent of transcript summaries
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
	if p.ToolName != codexQuestionTool && p.ToolName != "request_user_input_async" {
		return TurnInsight{Status: analyzer.StatusUnknown}
	}
	return questionInsight(p.ToolInput, p.ToolName == "request_user_input_async")
}

// requestUserInputArgs mirrors the allowlisted subset of the Codex
// request_user_input tool schema. Only header/question text is ever
// projected into a notification body; option lists, ids, and secret flags
// stay out.
type requestUserInputArgs struct {
	Questions []struct {
		Header   string `json:"header"`
		Question string `json:"question"`
		Title    string `json:"title"` // asynchronous user-input schema
	} `json:"questions"`
}

func questionBodyFromToolInput(toolInput json.RawMessage) string {
	return questionInsight(toolInput, false).Body
}

func questionInsight(toolInput json.RawMessage, async bool) TurnInsight {
	insight := TurnInsight{Status: analyzer.StatusQuestion}
	var args requestUserInputArgs
	if err := json.Unmarshal(toolInput, &args); err != nil || len(args.Questions) == 0 {
		return insight
	}

	first := strings.TrimSpace(args.Questions[0].Question)
	if async {
		first = strings.TrimSpace(args.Questions[0].Title)
	}
	insight.Question = truncateRunes(summary.CleanMarkdown(first), 150)
	if first == "" {
		first = strings.TrimSpace(args.Questions[0].Header)
	}
	if first == "" {
		return insight
	}

	body := truncateRunes(summary.CleanMarkdown(first), 150)
	if extra := len(args.Questions) - 1; extra > 0 {
		body = fmt.Sprintf("%s (+%d more)", body, extra)
	}
	insight.Body = body
	return insight
}

package webhook

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/logging"
	"github.com/777genius/agent-notifications/internal/platform"
	"github.com/777genius/agent-notifications/internal/sessionname"
)

var templatePattern = regexp.MustCompile(`\$\{\{\s*([^{}]+?)\s*\}\}`)

// SendContext carries per-notification metadata used by webhook templates.
//
// Message remains the pre-joined "[session|branch folder] body actions" string
// so existing ${{message}} templates and downstream consumers keep working. The
// structured fields below let formatters that render rich layouts (e.g. Discord
// embeds) avoid re-parsing the joined output.
type SendContext struct {
	Status    analyzer.Status
	Message   string
	SessionID string
	CWD       string

	SessionName   string // friendly session label (e.g. "phoenix 439d1884")
	GitBranch     string // empty when CWD is not a git working tree
	Folder        string // filepath.Base(CWD); empty when CWD is empty
	RawBody       string // summary body without prefix/actions
	ActionSummary string // action segment only (e.g. "📝 1 new  ▶ 2 cmds  ⏱ 41s")
	AgentSource   string // originating agent, e.g. "claude" or "codex" (hooks.Product); "" for legacy callers
}

type runtimeContext struct {
	sendCtx    SendContext
	statusInfo config.StatusInfo
	now        time.Time

	gitLoaded bool
	gitMeta   platform.GitMetadata
}

func newRuntimeContext(sendCtx SendContext, statusInfo config.StatusInfo) *runtimeContext {
	return &runtimeContext{
		sendCtx:    sendCtx,
		statusInfo: statusInfo,
		now:        time.Now(),
	}
}

func (c *runtimeContext) resolveHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}

	resolved := make(map[string]string, len(headers))
	for key, value := range headers {
		rendered, ok, err := c.resolveString(value)
		if err != nil {
			logging.Warn("Skipping webhook header: invalid template")
			continue
		}
		if !ok {
			logging.Warn("Skipping webhook header: template value unavailable")
			continue
		}
		resolved[key] = stringifyTemplateValue(rendered)
	}

	return resolved
}

func (c *runtimeContext) resolvePayloadFields(fields map[string]interface{}) (map[string]interface{}, error) {
	if len(fields) == 0 {
		return nil, nil
	}

	resolved, ok, err := c.resolveValue("", fields)
	if err != nil {
		return nil, err
	}
	if !ok {
		return map[string]interface{}{}, nil
	}

	resolvedMap, ok := resolved.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("payloadFields must resolve to a JSON object")
	}

	return resolvedMap, nil
}

func (c *runtimeContext) resolveValue(path string, value interface{}) (interface{}, bool, error) {
	switch typed := value.(type) {
	case string:
		rendered, ok, err := c.resolveString(typed)
		if err != nil {
			return nil, false, err
		}
		if !ok && path != "" {
			logging.Warn("Skipping webhook payload field: template value unavailable")
		}
		return rendered, ok, nil
	case map[string]interface{}:
		resolved := make(map[string]interface{}, len(typed))
		for key, item := range typed {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			rendered, ok, err := c.resolveValue(childPath, item)
			if err != nil {
				return nil, false, err
			}
			if ok {
				resolved[key] = rendered
			}
		}
		return resolved, true, nil
	case []interface{}:
		resolved := make([]interface{}, 0, len(typed))
		for i, item := range typed {
			childPath := fmt.Sprintf("%s[%d]", path, i)
			if path == "" {
				childPath = fmt.Sprintf("[%d]", i)
			}
			rendered, ok, err := c.resolveValue(childPath, item)
			if err != nil {
				return nil, false, err
			}
			if ok {
				resolved = append(resolved, rendered)
			}
		}
		return resolved, true, nil
	default:
		return value, true, nil
	}
}

func (c *runtimeContext) resolveString(input string) (interface{}, bool, error) {
	matches := templatePattern.FindAllStringSubmatchIndex(input, -1)
	if len(matches) == 0 {
		return input, true, nil
	}

	if len(matches) == 1 && matches[0][0] == 0 && matches[0][1] == len(input) {
		token := strings.TrimSpace(input[matches[0][2]:matches[0][3]])
		return c.lookupTemplateValue(token)
	}

	var builder strings.Builder
	last := 0
	for _, match := range matches {
		builder.WriteString(input[last:match[0]])
		token := strings.TrimSpace(input[match[2]:match[3]])
		value, ok, err := c.lookupTemplateValue(token)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		builder.WriteString(stringifyTemplateValue(value))
		last = match[1]
	}
	builder.WriteString(input[last:])

	return builder.String(), true, nil
}

func (c *runtimeContext) lookupTemplateValue(token string) (interface{}, bool, error) {
	switch token {
	case "status":
		return string(c.sendCtx.Status), true, nil
	case "title":
		return c.statusInfo.Title, true, nil
	case "message":
		return c.sendCtx.Message, true, nil
	case "session_id":
		return c.sendCtx.SessionID, true, nil
	case "session_name":
		if c.sendCtx.SessionName != "" {
			return c.sendCtx.SessionName, true, nil
		}
		return sessionname.GenerateSessionLabel(c.sendCtx.SessionID), true, nil
	case "source":
		return "claude-notifications", true, nil
	case "agent_source":
		return normalizeAgentSource(c.sendCtx.AgentSource), true, nil
	case "cwd":
		return c.sendCtx.CWD, true, nil
	case "folder":
		if c.sendCtx.Folder != "" {
			return c.sendCtx.Folder, true, nil
		}
		if c.sendCtx.CWD == "" {
			return "", false, nil
		}
		return filepath.Base(c.sendCtx.CWD), true, nil
	case "raw_body":
		return c.sendCtx.RawBody, c.sendCtx.RawBody != "", nil
	case "action_summary":
		return c.sendCtx.ActionSummary, c.sendCtx.ActionSummary != "", nil
	case "time.rfc3339":
		return c.now.Format(time.RFC3339), true, nil
	case "time.unix":
		return c.now.Unix(), true, nil
	case "time.unix_ms":
		return c.now.UnixMilli(), true, nil
	}

	if strings.HasPrefix(token, "env.") {
		name := strings.TrimPrefix(token, "env.")
		if value, ok := os.LookupEnv(name); ok {
			return value, true, nil
		}
		return nil, false, nil
	}

	if strings.HasPrefix(token, "git.") {
		return c.lookupGitTemplateValue(token)
	}

	return nil, false, fmt.Errorf("unknown webhook template %q", token)
}

func (c *runtimeContext) lookupGitTemplateValue(token string) (interface{}, bool, error) {
	meta := c.gitMetadata()

	switch token {
	case "git.branch":
		if c.sendCtx.GitBranch != "" {
			return c.sendCtx.GitBranch, true, nil
		}
		return meta.Branch, meta.Branch != "", nil
	case "git.user.name":
		return meta.UserName, meta.UserName != "", nil
	case "git.user.email":
		return meta.UserEmail, meta.UserEmail != "", nil
	case "git.commit.hash":
		return meta.CommitHash, meta.CommitHash != "", nil
	case "git.commit.short_hash":
		return meta.CommitShortHash, meta.CommitShortHash != "", nil
	case "git.commit.author.name":
		return meta.CommitAuthorName, meta.CommitAuthorName != "", nil
	case "git.commit.author.email":
		return meta.CommitAuthorEmail, meta.CommitAuthorEmail != "", nil
	default:
		return nil, false, fmt.Errorf("unknown webhook template %q", token)
	}
}

func (c *runtimeContext) gitMetadata() platform.GitMetadata {
	if !c.gitLoaded {
		c.gitMeta = platform.GetGitMetadata(c.sendCtx.CWD)
		c.gitLoaded = true
	}
	return c.gitMeta
}

func stringifyTemplateValue(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

func mergePayloadMaps(base, overrides map[string]interface{}) {
	for key, value := range overrides {
		if existing, ok := base[key]; ok {
			existingMap, existingIsMap := existing.(map[string]interface{})
			overrideMap, overrideIsMap := value.(map[string]interface{})
			if existingIsMap && overrideIsMap {
				mergePayloadMaps(existingMap, overrideMap)
				continue
			}
			logging.Debug("Webhook payload field overridden")
		}
		base[key] = value
	}
}

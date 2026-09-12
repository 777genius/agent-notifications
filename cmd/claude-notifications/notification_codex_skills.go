package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/777genius/agent-notifications/internal/strictjson"
)

// This inventory-only app-server exchange initializes no thread, model, or MCP
// connection. The sole discovery cwd is the selected client home, never a project.
// Unknown response shapes or discovery errors cannot authorize a projection.
func probeNotificationCodexSkills(ctx context.Context, home string) ([]notificationCodexSkill, error) {
	child, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(child, "codex", "app-server")
	command.Dir = home
	command.Env = append(os.Environ(), "CODEX_HOME="+home)
	command.WaitDelay = time.Second
	input, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	command.Stderr = &notificationInventoryBuffer{}
	if err = command.Start(); err != nil {
		return nil, err
	}
	defer func() { input.Close(); cancel(); command.Wait() }()
	return notificationCodexSkillsExchangeContext(child, input, output, home)
}

// Closing both pipes on cancellation also fences a descendant retaining stdout:
// WaitDelay alone cannot help while the exchange has not yet returned to Wait.
func notificationCodexSkillsExchangeContext(ctx context.Context, input io.WriteCloser, output io.ReadCloser, home string) ([]notificationCodexSkill, error) {
	stop := context.AfterFunc(ctx, func() { input.Close(); output.Close() })
	defer stop()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	skills, err := notificationCodexSkillsExchange(input, output, home)
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}
	return skills, err
}

func notificationCodexSkillsExchange(input io.Writer, output io.Reader, home string) ([]notificationCodexSkill, error) {
	encoder := json.NewEncoder(input)
	scanner := bufio.NewScanner(io.LimitReader(output, 2<<20))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	receive := func(id int) (json.RawMessage, error) {
		for messages := 0; messages < 64 && scanner.Scan(); messages++ {
			var response struct {
				ID     *int            `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if strictjson.Validate(scanner.Bytes(), strictjson.Budget{Bytes: 1 << 20, Depth: 24, Entries: 10000}) != nil || json.Unmarshal(scanner.Bytes(), &response) != nil {
				return nil, errors.New("inventory_protocol")
			}
			if response.ID == nil {
				continue
			}
			if *response.ID != id || len(response.Error) > 0 || len(response.Result) == 0 {
				return nil, errors.New("inventory_protocol")
			}
			return response.Result, nil
		}
		return nil, errors.New("inventory_unavailable")
	}
	if err := encoder.Encode(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]string{"name": "agent-notifications-inventory", "version": "1"}}}); err != nil {
		return nil, err
	}
	if _, err := receive(1); err != nil {
		return nil, err
	}
	if err := encoder.Encode(map[string]any{"method": "initialized"}); err != nil {
		return nil, err
	}
	if err := encoder.Encode(map[string]any{"id": 2, "method": "skills/list", "params": map[string]any{"cwds": []string{home}, "forceReload": false}}); err != nil {
		return nil, err
	}
	raw, err := receive(2)
	if err != nil {
		return nil, err
	}
	var result struct {
		Data []struct {
			Cwd    string            `json:"cwd"`
			Errors []json.RawMessage `json:"errors"`
			Skills []struct {
				Name     string  `json:"name"`
				PluginID *string `json:"pluginId"`
				Path     string  `json:"path"`
				Enabled  *bool   `json:"enabled"`
			} `json:"skills"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &result) != nil || len(result.Data) != 1 || result.Data[0].Cwd != home || len(result.Data[0].Errors) > 0 || result.Data[0].Skills == nil {
		return nil, errors.New("inventory_unknown")
	}
	skills := []notificationCodexSkill{}
	for _, s := range result.Data[0].Skills {
		if s.PluginID == nil || !ownNotificationPlugin(*s.PluginID) {
			continue
		}
		if s.Enabled == nil || s.Name == "" || !configurePhysical(s.Path) {
			return nil, errors.New("inventory_unknown")
		}
		skills = append(skills, notificationCodexSkill{Name: s.Name, PluginID: *s.PluginID, Path: s.Path, Enabled: *s.Enabled})
	}
	return skills, nil
}

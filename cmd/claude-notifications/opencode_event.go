package main

import (
	"context"
	"encoding/json"
	"io"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/notifier"
	"github.com/777genius/agent-notifications/internal/opencodeevent"
	"github.com/777genius/agent-notifications/internal/opencodeinstall"
	"github.com/777genius/agent-notifications/internal/webhook"
)

var openCodeRegistrationGate opencodeevent.Gate = opencodeinstall.CurrentGate{}

func runOpenCodeEvent(args []string, input io.Reader, output io.Writer) int {
	result := opencodeevent.Receipt{Status: "rejected", Reason: "invalid_command"}
	if len(args) == 2 && args[0] == "--protocol" && args[1] == "1" {
		consumer := opencodeevent.Consumer{Gate: openCodeRegistrationGate}
		if openCodeRegistrationGate != nil {
			cfg, err := config.LoadForAgentQuiet(getPluginRoot(), config.AgentOpenCode)
			if err != nil {
				result = opencodeevent.Receipt{Status: "rejected", Reason: "invalid_config"}
			} else {
				consumer.Config = cfg
				consumer.Clock = notifier.SystemBootClock{}
				consumer.Desktop = newOpenCodeDesktopPort()
				consumer.SendWebhook = func(cfg *config.Config, ctx webhook.SendContext) error {
					return webhook.New(cfg).SendWithContext(ctx)
				}
				result = consumer.Consume(context.Background(), input)
			}
		} else {
			result = consumer.Consume(context.Background(), input)
		}
	}
	_ = json.NewEncoder(output).Encode(result)
	return 0 // observation must never interrupt OpenCode
}

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/777genius/plugin-kit-ai/cli/installerui"
)

// The bootstrap supplies its controlling terminal as input. Prompts go to that
// terminal through stderr; stdout contains only the selected product IDs. This
// operation performs no discovery, config reads, registration or mutation.
func runSetupProducts(args []string, input io.Reader, output, prompts io.Writer) int {
	if len(args) == 1 && args[0] == "capabilities" {
		_, err := fmt.Fprintln(output, "setup-products-v1 claude codex opencode gemini")
		if err != nil {
			return 1
		}
		return 0
	}
	if len(args) != 1 || args[0] != "select" {
		_, _ = fmt.Fprintln(prompts, "usage: setup-products select")
		return 2
	}
	ui, err := installerui.New(installerui.Config{Input: input, Output: prompts})
	if err != nil {
		_, _ = fmt.Fprintf(prompts, "setup-products: %v\n", err)
		return 1
	}
	selection, err := ui.SelectMany(context.Background(), installerui.SelectRequest{
		Title: "Install notifications for", Options: []installerui.Option{
			{ID: "claude", Label: "Claude Code"},
			{ID: "codex", Label: "Codex"},
			{ID: "opencode", Label: "OpenCode"},
			{ID: "gemini", Label: "Gemini CLI"},
		},
	})
	if err != nil {
		_, _ = fmt.Fprintf(prompts, "setup-products: %v\n", err)
		return 1
	}
	if selection.Cancelled || len(selection.IDs) == 0 {
		return 0
	}
	if _, err := fmt.Fprintln(output, strings.Join(selection.IDs, ",")); err != nil {
		return 1
	}
	return 0
}

func setupProductsMain(args []string) int {
	return runSetupProducts(args, os.Stdin, os.Stdout, os.Stderr)
}

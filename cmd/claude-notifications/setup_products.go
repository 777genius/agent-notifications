package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/777genius/plugin-kit-ai/cli/installerui"
)

// Existing ownership may include several release binaries. Confirmation and
// preflight fingerprint that state repeatedly; CLI discovery has a smaller budget.
const bootstrapInspectionTimeout = 30 * time.Second

// setup-products continues PR283's stdout-clean command. Line injection is the
// legacy test/embedding seam. Actual file handles always require the shared
// terminal constructor; a pipe is never treated as an interactive answer.
func runSetupProducts(args []string, input io.Reader, output, prompts io.Writer) int {
	return runSetupProductsContext(context.Background(), args, input, output, prompts)
}

func runSetupProductsContext(ctx context.Context, args []string, input io.Reader, output, prompts io.Writer) int {
	a, err := parseSetupProducts(args)
	fail := func(code int, err error) int {
		diagnostic, displayErr := setupProductsNotice(err.Error())
		if displayErr != nil {
			diagnostic = "invalid or oversized setup-products diagnostic\n"
		}
		_, _ = io.WriteString(prompts, "setup-products: "+diagnostic)
		return code
	}
	if err != nil {
		return fail(2, err)
	}
	if a.Operation == "capabilities" {
		if err := writeSelectorResult(ctx, output, "setup-products-v1 claude codex opencode gemini\n"); err != nil {
			return fail(1, err)
		}
		return 0
	}
	if a.Operation == "features" {
		if err := writeSelectorResult(ctx, output, "terminal-selector-v1\n"); err != nil {
			return fail(1, err)
		}
		return 0
	}
	if a.Operation == "intent-args" || a.Operation == "preflight" {
		provenance, err := currentSelectorProvenance()
		if err != nil {
			return fail(1, err)
		}
		intent, err := loadBootstrapIntent(a.IntentFile, provenance)
		if err != nil {
			return fail(1, err)
		}
		if a.Operation == "intent-args" {
			err = writeIntentScalars(output, intent)
		} else {
			checkpoint, cancel := context.WithTimeout(ctx, bootstrapInspectionTimeout)
			defer cancel()
			err = preflightBootstrapIntent(checkpoint, intent, a)
		}
		if err != nil {
			return fail(1, err)
		}
		return 0
	}
	options := []installerui.Option{}
	defaults := []string{}
	if a.Operation == "select" {
		if _, production := input.(*os.File); production {
			e, err := captureProductEnvironment()
			if err != nil {
				return fail(2, err)
			}
			observation, cancel := context.WithTimeout(ctx, 2*time.Second)
			facts, _, err := discoverProducts(observation, a, e)
			cancel()
			if err != nil {
				return fail(setupProductErrorCode(err), err)
			}
			for _, f := range facts {
				if !f.Selectable {
					rows, e := setupProductsNotice(f.ID + ": " + f.Reason)
					if e != nil {
						return fail(1, e)
					}
					if err := writeSelectorResult(ctx, prompts, rows); err != nil {
						return fail(1, err)
					}
					continue
				}
				label := f.Label
				if f.Present {
					label += " (CLI present)"
					if f.ID != "cursor" && f.ID != "copilot-vscode" {
						defaults = append(defaults, f.ID)
					}
				} else {
					label += " (CLI not found in selected PATH)"
				}
				options = append(options, installerui.Option{ID: f.ID, Label: label})
			}
		} else {
			for _, id := range productOrder {
				options = append(options, installerui.Option{ID: id, Label: productLabels[id]})
			}
		}
		if len(options) == 0 {
			return fail(1, errors.New("no selectable products"))
		}
	}
	var terminal *installerui.Terminal
	var line *installerui.UI
	// Confirm's read-only facts and bounded summary are composed before a question
	// is rendered; the record is published only after fresh Yes and cleanup.
	if a.Operation == "confirm" || a.Operation == "prepare" {
		prepare := a.Operation == "prepare"
		a.Operation = "confirm"
		provenance, err := currentSelectorProvenance()
		if err != nil {
			return fail(1, err)
		}
		if a.IntentFile != "" {
			root, leaf, err := openIntentStage(a.IntentFile, provenance)
			if err != nil {
				return fail(2, err)
			}
			_, existsErr := root.Lstat(leaf)
			_ = root.Close()
			if !os.IsNotExist(existsErr) {
				return fail(2, errors.New("intent leaf must be absent"))
			}
		}
		env, err := captureProductEnvironment()
		if err != nil {
			return fail(2, err)
		}
		observe, cancel := context.WithTimeout(ctx, bootstrapInspectionTimeout)
		intent, rows, err := buildConfirmedBootstrapIntent(observe, a, env, provenance)
		cancel()
		if err != nil {
			return fail(setupProductErrorCode(err), err)
		}
		if prepare {
			if a.IntentFile == "" {
				return fail(2, errors.New("prepare requires intent-file"))
			}
			if err := writeBootstrapIntent(a.IntentFile, intent); err != nil {
				return fail(1, err)
			}
			if err := writeSelectorResult(ctx, output, "prepared\n"); err != nil {
				return fail(1, errors.Join(err, removeBootstrapIntent(a.IntentFile, provenance)))
			}
			return 0
		}
		terminal, line, err = newSetupProductsPrompt(input, prompts, a.Mode)
		if err != nil {
			return fail(1, err)
		}
		req := installerui.ConfirmRequest{Title: setupProductsConfirmationTitle, Summary: rows, Default: true}
		req.Summary, err = setupProductsConfirmationRows(rows, terminal != nil && terminal.Mode() == installerui.ModeRich)
		if err != nil {
			return fail(1, err)
		}
		var answer installerui.Confirmation
		if terminal != nil {
			answer, err = terminal.Confirm(ctx, req)
		} else {
			answer, err = line.Confirm(ctx, req)
		}
		if err != nil {
			return fail(1, err)
		}
		if answer.Cancelled || !answer.Accepted {
			if err := writeSelectorResult(ctx, prompts, "Installation cancelled. No changes were applied.\n"); err != nil {
				return fail(1, err)
			}
			return 0
		}
		if err := ctx.Err(); err != nil {
			return fail(1, err)
		}
		if a.IntentFile != "" {
			if err := writeBootstrapIntent(a.IntentFile, intent); err != nil {
				return fail(1, err)
			}
		}
		if err := writeSelectorResult(ctx, output, "approved\n"); err != nil {
			if a.IntentFile != "" {
				err = errors.Join(err, removeBootstrapIntent(a.IntentFile, provenance))
			}
			return fail(1, err)
		}
		return 0
	}
	terminal, line, err = newSetupProductsPrompt(input, prompts, a.Mode)
	if err != nil {
		return fail(1, err)
	}
	title := "Install notifications for"
	if a.Operation == "channels" {
		products := []string{}
		for _, id := range a.Products {
			if id == "opencode" || id == "gemini" || id == "cursor" || id == "copilot-vscode" {
				products = append(products, productLabels[id])
			}
		}
		title = "Notification channels for " + strings.Join(products, ", ")
		options = []installerui.Option{{ID: "desktop", Label: "Desktop notifications (recommended)"}, {ID: "webhook", Label: "Webhook notifications (optional; requires a configured destination)"}}
		defaults = []string{"desktop"}
	}
	request := installerui.SelectRequest{Title: title, Options: options, Defaults: defaults}
	var selection installerui.Selection
	if terminal != nil {
		selection, err = terminal.SelectMany(ctx, installerui.MultiSelectRequest{SelectRequest: request, MinSelected: 0})
	} else {
		selection, err = line.SelectMany(ctx, request)
	}
	if err != nil {
		return fail(1, err)
	}
	if selection.Cancelled || !selection.Accepted || len(selection.IDs) == 0 {
		if err := writeSelectorResult(ctx, prompts, "Installation cancelled. No changes were applied.\n"); err != nil {
			return fail(1, err)
		}
		return 0
	}
	if err := writeSelectorResult(ctx, output, strings.Join(selection.IDs, ",")+"\n"); err != nil {
		return fail(1, err)
	}
	return 0
}

func newSetupProductsPrompt(in io.Reader, out io.Writer, mode string) (*installerui.Terminal, *installerui.UI, error) {
	input, files := in.(*os.File)
	if !files {
		ui, err := installerui.New(installerui.Config{Input: in, Output: out})
		return nil, ui, err
	}
	visible, ok := out.(*os.File)
	if !ok {
		return nil, nil, installerui.ErrUnavailable
	}
	noColor := os.Getenv("NO_COLOR") != "" || mode == "plain"
	terminal, err := installerui.NewTerminal(installerui.TerminalConfig{Input: input, Output: visible, Mode: installerui.TerminalMode(mode), NoColor: noColor})
	if err != nil {
		return nil, nil, err
	}
	if terminal.Mode() == installerui.ModePlain && !noColor {
		terminal, err = installerui.NewTerminal(installerui.TerminalConfig{Input: input, Output: visible, Mode: installerui.ModePlain, NoColor: true})
	}
	return terminal, nil, err
}

func setupProductsNotice(raw string) (string, error) {
	rows, err := escapeProductNotice(raw)
	if err != nil {
		return "", err
	}
	return strings.Join(rows, "\n") + "\n", nil
}
func writeSelectorResult(ctx context.Context, out io.Writer, s string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	n, err := io.WriteString(out, s)
	if err == nil && n != len(s) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = ctx.Err()
	}
	return err
}

type selectorSignal struct{ signal os.Signal }

func (s selectorSignal) Error() string { return "selector interrupted by " + s.signal.String() }
func setupProductsMain(args []string) int {
	ctx, cancel := context.WithCancelCause(context.Background())
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case sig := <-signals:
			cancel(selectorSignal{sig})
		case <-ctx.Done():
		}
	}()
	defer func() { signal.Stop(signals); cancel(nil); <-done }()
	code := runSetupProductsContext(ctx, args, os.Stdin, os.Stdout, os.Stderr)
	var interrupted selectorSignal
	if errors.As(context.Cause(ctx), &interrupted) {
		switch interrupted.signal {
		case syscall.SIGTERM:
			return 143
		case syscall.SIGHUP:
			return 129
		default:
			return 130
		}
	}
	return code
}

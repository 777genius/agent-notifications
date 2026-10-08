//go:build linux || darwin

package main

import (
	"strings"
	"testing"
)

func TestBootstrapPublicConfirmationDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, mode, answer string
		install            bool
	}{{"rich Enter installs", "rich", "\r", true}, {"rich Right then Enter cancels", "rich", "\x1b[C\r", false},
		{"plain Enter installs", "plain", "\n", true}, {"plain No cancels", "plain", "n\n", false}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newBootstrapFixture(t)
			f.publicJourney()
			before := f.snapshot()
			// Explicitly choose Webhook only so this keyboard test never asks for
			// desktop permissions or launches a notification helper.
			channels := promptStep{"Notification channels", " \x1b[B \r", nil}
			if tc.mode == "plain" {
				channels = promptStep{"comma-separated", "webhook\n", nil}
			}
			r := f.terminal([]string{"--product", "opencode", "--ui=" + tc.mode},
				channels,
				promptStep{setupProductsConfirmationTitle, tc.answer, nil})
			requireBootstrapSuccess(t, r)
			if tc.install {
				f.assertConsumer("opencode-notifications", true)
			} else {
				f.unchanged(before)
				if !strings.Contains(r.screen, "Installation cancelled. No changes were applied.") {
					t.Fatalf("Enter cancelled silently: %s", r.screen)
				}
			}
		})
	}
}

func TestBootstrapPublicDesktopDefaults(t *testing.T) {
	for _, tc := range []struct{ mode, acceptChannels, cancel string }{
		{"rich", "\r", "\x1b[C\r"}, {"plain", "\n", "n\n"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			f := newBootstrapFixture(t)
			f.publicJourney()
			before := f.snapshot()
			control := "Notification channels"
			if tc.mode == "plain" {
				control = "comma-separated"
			}
			r := f.terminal([]string{"--product", "opencode", "--ui=" + tc.mode},
				promptStep{control, tc.acceptChannels, nil},
				promptStep{setupProductsConfirmationTitle, tc.cancel, nil})
			requireBootstrapSuccess(t, r)
			if !strings.Contains(r.screen, "Channels: Desktop on, Webhook off") {
				t.Fatalf("default Desktop did not reach final consent: %s", r.screen)
			}
			if !strings.Contains(r.screen, "Installation cancelled. No changes were applied.") {
				t.Fatalf("public cancellation missing: %s", r.screen)
			}
			f.unchanged(before)
		})
	}
}

package notification

import "testing"

func TestWithTerminalClickRequiresTrustedOptIn(t *testing.T) {
	base := InformationalDesktopRequest(Content{Title: "Cursor CLI", Body: "Stopping", Category: "info"}, "request", Deadline{}, false)
	target := &TerminalTarget{Agent: "cursor", WindowID: "123", OwnerPID: 42}
	for _, tc := range []struct {
		name       string
		target     *TerminalTarget
		authorized bool
		want       bool
	}{
		{"no_target", nil, true, false},
		{"no_consent", target, false, false},
		{"opted_in", target, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := WithTerminalClick(base, tc.target, tc.authorized)
			if (r.Terminal != nil) != tc.want {
				t.Fatalf("terminal present=%v, want %v", r.Terminal != nil, tc.want)
			}
			if tc.want {
				if r.Navigation != BestEffort || !r.Policy.ClickToFocus {
					t.Fatal("trusted target did not activate the typed route")
				}
				r.Terminal.WindowID = "456"
				if target.WindowID != "123" {
					t.Fatal("target was aliased, not snapshotted")
				}
			} else if r.Navigation != None || r.Policy.ClickToFocus {
				t.Fatal("unauthorized click route changed informational policy")
			}
		})
	}
}

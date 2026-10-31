package opencodeevent

import (
	"testing"

	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/google/uuid"
)

func TestDesktopRequestDoesNotGrantOpenCodeNavigation(t *testing.T) {
	content := notification.Content{Title: "OpenCode", Body: "Completed", Category: "info"}
	deadline := notification.Deadline{BootID: "boot", NotAfter: 101.5}
	for _, tc := range []struct {
		name       string
		nativeSound bool
	}{
		{"default_silent", false},
		{"explicit_os_sound", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := desktopRequest(content, deadline, tc.nativeSound)
			if got.Content != content || got.Deadline != deadline ||
				got.Navigation != notification.None || got.Target != (notification.DesktopTarget{}) {
				t.Fatalf("untrusted OpenCode event acquired a navigation target: %+v", got)
			}
			if !got.Policy.Valid || !got.Policy.ExplicitEnabled || !got.Policy.DesktopEnabled ||
				got.Policy.ClickToFocus || got.Policy.SoundEnabled != tc.nativeSound || got.Silent == tc.nativeSound {
				t.Fatalf("sound consent or silent policy mismatch: %+v", got)
			}
			if _, err := uuid.Parse(got.CorrelationID); err != nil {
				t.Fatalf("missing valid delivery identity: %v", err)
			}
		})
	}
}

package notification

import "testing"

func TestInformationalDesktopRequestKeepsNavigationDisabled(t *testing.T) {
	content := Content{Title: "TEST native agent", Body: "Prompt requires attention", Category: "attention"}
	deadline := Deadline{BootID: "TEST-boot", NotAfter: 104}
	for _, sound := range []bool{false, true} {
		req := InformationalDesktopRequest(content, "TEST-correlated-by-source", deadline, sound)
		if req.Content != content || req.Deadline != deadline || req.CorrelationID != "TEST-correlated-by-source" {
			t.Fatalf("native fact or admission identity changed: %+v", req)
		}
		if req.Navigation != None || req.Target != (DesktopTarget{}) || req.Policy.ClickToFocus {
			t.Fatalf("informational observer acquired navigation authority: %+v", req)
		}
		if !req.Policy.Valid || !req.Policy.ExplicitEnabled || !req.Policy.DesktopEnabled ||
			req.Policy.SoundEnabled != sound || req.Silent == sound {
			t.Fatalf("native sound policy changed: sound=%t request=%+v", sound, req)
		}
	}
}

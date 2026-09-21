package notifier

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestWindowsToastContentIsXMLData(t *testing.T) {
	for _, text := range []string{`close CDATA ]]> then <toast launch="$()">`, `$([System.IO.File]::WriteAllText('owned','x'))`} {
		t.Run(text, func(t *testing.T) {
			encoded, err := encodeWindowsToast(windowsToastPayload{Title: text, Body: text, Silent: true})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "<toast launch=\"$()\">") || strings.Contains(string(encoded), "<![CDATA[") {
				t.Fatalf("content became markup: %s", encoded)
			}
			var decoded windowsToastDocument
			if err = xml.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if len(decoded.Visual.Binding.Text) != 2 || decoded.Visual.Binding.Text[0] != text || decoded.Visual.Binding.Text[1] != text {
				t.Fatalf("content changed: %#v", decoded.Visual.Binding.Text)
			}
			if decoded.Audio == nil || decoded.Audio.Silent != "true" {
				t.Fatal("silent policy missing")
			}
		})
	}
}

func TestWindowsToastAudiblePolicyOmitsSilentAudio(t *testing.T) {
	encoded, err := encodeWindowsToast(windowsToastPayload{Title: "title", Body: "body"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "<audio") {
		t.Fatalf("audible toast forced silent: %s", encoded)
	}
}

func TestWindowsToastIconIsOptionalEscapedData(t *testing.T) {
	icon := `C:\icons\agent&notify"<logo>.png`
	encoded, err := encodeWindowsToast(windowsToastPayload{Title: "title", Body: "body", Icon: icon})
	if err != nil {
		t.Fatal(err)
	}
	var decoded windowsToastDocument
	if err = xml.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Visual.Binding.Image == nil || decoded.Visual.Binding.Image.Placement != "appLogoOverride" || decoded.Visual.Binding.Image.Source != icon {
		t.Fatalf("icon changed or missing: %s", encoded)
	}
	without, err := encodeWindowsToast(windowsToastPayload{Title: "title", Body: "body"})
	if err != nil || strings.Contains(string(without), "<image") {
		t.Fatalf("empty icon emitted: %s (%v)", without, err)
	}
}

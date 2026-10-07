package notifier

import (
	"encoding/xml"
)

type windowsToastPayload struct {
	AppID, Title, Body, Icon, ActivationType, ActivationArguments string
	Silent                                                        bool
}

type windowsToastDocument struct {
	XMLName        xml.Name           `xml:"toast"`
	ActivationType string             `xml:"activationType,attr,omitempty"`
	Launch         string             `xml:"launch,attr,omitempty"`
	Visual         windowsToastVisual `xml:"visual"`
	Audio          *windowsToastAudio `xml:"audio,omitempty"`
}
type windowsToastVisual struct {
	Binding windowsToastBinding `xml:"binding"`
}
type windowsToastBinding struct {
	Template string             `xml:"template,attr"`
	Text     []string           `xml:"text"`
	Image    *windowsToastImage `xml:"image,omitempty"`
}
type windowsToastImage struct {
	Placement string `xml:"placement,attr"`
	Source    string `xml:"src,attr"`
}
type windowsToastAudio struct {
	Silent string `xml:"silent,attr"`
}

func encodeWindowsToast(p windowsToastPayload) ([]byte, error) {
	doc := windowsToastDocument{
		ActivationType: p.ActivationType,
		Launch:         p.ActivationArguments,
		Visual: windowsToastVisual{Binding: windowsToastBinding{
			Template: "ToastGeneric",
			Text:     []string{p.Title, p.Body},
		}},
	}
	if p.Icon != "" {
		doc.Visual.Binding.Image = &windowsToastImage{Placement: "appLogoOverride", Source: p.Icon}
	}
	if p.Silent {
		doc.Audio = &windowsToastAudio{Silent: "true"}
	}
	return xml.Marshal(doc)
}

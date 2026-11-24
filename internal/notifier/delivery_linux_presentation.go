//go:build linux

package notifier

import (
	"bytes"
	"image/color"
	"image/png"
	"sync"

	agentnotifications "github.com/777genius/agent-notifications"
)

// freedesktopImageData is the standard D-Bus (iiibiiay) image-data hint.
// The icon is canonical embedded release content, not an on-disk plugin-cache
// path that a Claude marketplace reinstall could replace or remove.
type freedesktopImageData struct {
	Width, Height, Rowstride int32
	HasAlpha                 bool
	BitsPerSample, Channels  int32
	Data                     []byte
}

var agentNotifyIcon = struct {
	sync.Once
	image freedesktopImageData
	ok    bool
}{}

func agentNotifyImageData() (freedesktopImageData, bool) {
	agentNotifyIcon.Do(func() {
		asset, err := agentnotifications.NotificationAssets.ReadFile("claude_icon.png")
		if err != nil {
			return
		}
		decoded, err := png.Decode(bytes.NewReader(asset))
		if err != nil {
			return
		}
		bounds := decoded.Bounds()
		if bounds.Dx() < 1 || bounds.Dy() < 1 {
			return
		}
		// 64x64 is small enough to send on every notification without reading
		// a mutable filesystem path or leaking user content into D-Bus hints.
		const side = 64
		pixels := make([]byte, side*side*4)
		for y := 0; y < side; y++ {
			for x := 0; x < side; x++ {
				sourceX := bounds.Min.X + x*bounds.Dx()/side
				sourceY := bounds.Min.Y + y*bounds.Dy()/side
				rgba := color.NRGBAModel.Convert(decoded.At(sourceX, sourceY)).(color.NRGBA)
				i := (y*side + x) * 4
				pixels[i], pixels[i+1], pixels[i+2], pixels[i+3] = rgba.R, rgba.G, rgba.B, rgba.A
			}
		}
		agentNotifyIcon.image = freedesktopImageData{
			Width: side, Height: side, Rowstride: side * 4,
			HasAlpha: true, BitsPerSample: 8, Channels: 4, Data: pixels,
		}
		agentNotifyIcon.ok = true
	})
	return agentNotifyIcon.image, agentNotifyIcon.ok
}

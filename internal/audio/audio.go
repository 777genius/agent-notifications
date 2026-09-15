// ABOUTME: Audio playback module with device selection support.
// ABOUTME: Uses malgo (miniaudio bindings) for cross-platform audio output.

package audio

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gen2brain/malgo"
	"github.com/go-audio/aiff"
	"github.com/go-audio/audio"
	"github.com/gopxl/beep/flac"
	"github.com/gopxl/beep/mp3"
	"github.com/gopxl/beep/vorbis"
	"github.com/gopxl/beep/wav"

	"github.com/777genius/agent-notifications/internal/logging"
)

// DeviceInfo represents an audio output device
type DeviceInfo struct {
	Name      string
	IsDefault bool
}

// Player plays audio on a specific device
type Player struct {
	ctx        *malgo.AllocatedContext
	deviceID   *malgo.DeviceID // Stored copy of device ID (nil = default device)
	deviceName string
	volume     float64
	mu         sync.Mutex
}

// ListDevices returns all available audio output devices
func ListDevices() ([]DeviceInfo, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to init audio context: %w", err)
	}
	defer func() {
		_ = ctx.Uninit()
		ctx.Free()
	}()

	devices, err := ctx.Devices(malgo.Playback)
	if err != nil {
		return nil, fmt.Errorf("failed to enumerate devices: %w", err)
	}

	result := make([]DeviceInfo, 0, len(devices))
	for _, dev := range devices {
		result = append(result, DeviceInfo{
			Name:      dev.Name(),
			IsDefault: dev.IsDefault != 0,
		})
	}

	return result, nil
}

// NewPlayer creates a new audio player for the specified device
// If deviceName is empty, the system default device is used
func NewPlayer(deviceName string, volume float64) (*Player, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to init audio context: %w", err)
	}

	player := &Player{
		ctx:        ctx,
		deviceName: deviceName,
		volume:     volume,
	}

	// Find the device by name if specified
	if deviceName != "" {
		devices, err := ctx.Devices(malgo.Playback)
		if err != nil {
			_ = ctx.Uninit()
			ctx.Free()
			return nil, fmt.Errorf("failed to enumerate devices: %w", err)
		}

		var found bool
		for _, dev := range devices {
			if dev.Name() == deviceName {
				// Copy the DeviceID to avoid dangling pointer after devices slice is freed
				id := dev.ID
				player.deviceID = &id
				found = true
				logging.Debug("Audio device found: %s", deviceName)
				break
			}
		}

		if !found {
			_ = ctx.Uninit()
			ctx.Free()
			return nil, fmt.Errorf("audio device not found: %s", deviceName)
		}
	}

	return player, nil
}

// Play plays an audio file
func (p *Player) Play(soundPath string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Check if player is closed
	if p.ctx == nil {
		return fmt.Errorf("player is closed")
	}

	// Check if file exists
	if _, err := os.Stat(soundPath); os.IsNotExist(err) {
		return fmt.Errorf("sound file not found: %s", soundPath)
	}

	// Decode audio file
	samples, sampleRate, channels, err := p.decodeAudio(soundPath)
	if err != nil {
		return fmt.Errorf("failed to decode audio: %w", err)
	}

	// Apply volume
	if p.volume < 1.0 {
		for i := range samples {
			samples[i] = int16(float64(samples[i]) * p.volume)
		}
	}

	// Convert to bytes
	audioData := samplesToBytes(samples)

	// Create device config with larger buffer to prevent crackling
	deviceConfig := malgo.DefaultDeviceConfig(malgo.Playback)
	deviceConfig.Playback.Format = malgo.FormatS16
	deviceConfig.Playback.Channels = uint32(channels)
	deviceConfig.SampleRate = sampleRate
	deviceConfig.PeriodSizeInFrames = 4096
	deviceConfig.Periods = 4
	deviceConfig.Alsa.NoMMap = 1

	// Set specific device if configured
	if p.deviceID != nil {
		deviceConfig.Playback.DeviceID = p.deviceID.Pointer()
	}

	// Playback state
	var pos int
	var done = make(chan struct{})
	var doneOnce sync.Once

	// Data callback
	dataCallback := func(outputSamples, inputSamples []byte, frameCount uint32) {
		bytesToWrite := int(frameCount) * channels * 2 // 2 bytes per sample (16-bit)
		if pos+bytesToWrite > len(audioData) {
			bytesToWrite = len(audioData) - pos
		}

		if bytesToWrite > 0 {
			copy(outputSamples, audioData[pos:pos+bytesToWrite])
			pos += bytesToWrite
		}

		// Fill remaining with silence
		for i := bytesToWrite; i < len(outputSamples); i++ {
			outputSamples[i] = 0
		}

		// Signal done when finished
		if pos >= len(audioData) {
			doneOnce.Do(func() {
				close(done)
			})
		}
	}

	// Initialize device
	device, err := malgo.InitDevice(p.ctx.Context, deviceConfig, malgo.DeviceCallbacks{
		Data: dataCallback,
	})
	if err != nil {
		return fmt.Errorf("failed to init audio device: %w", err)
	}
	defer device.Uninit()

	// Start playback
	if err := device.Start(); err != nil {
		return fmt.Errorf("failed to start audio device: %w", err)
	}

	// Wait for playback to complete or timeout
	select {
	case <-done:
		// Delay to let buffer drain completely
		time.Sleep(200 * time.Millisecond)
		logging.Debug("Audio playback completed: %s", soundPath)
	case <-time.After(30 * time.Second):
		logging.Warn("Audio playback timeout: %s", soundPath)
	}

	_ = device.Stop()
	return nil
}

// Close releases resources
func (p *Player) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.ctx != nil {
		_ = p.ctx.Uninit()
		p.ctx.Free()
		p.ctx = nil
	}
	return nil
}

// decodeAudio decodes an audio file and returns samples, sample rate, and channel count
func (p *Player) decodeAudio(soundPath string) ([]int16, uint32, int, error) {
	f, err := os.Open(soundPath)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("failed to open audio file: %w", err)
	}
	defer func() { _ = f.Close() }()

	ext := strings.ToLower(filepath.Ext(soundPath))

	switch ext {
	case ".mp3":
		return p.decodeMP3(f)
	case ".wav":
		return p.decodeWAV(f)
	case ".flac":
		return p.decodeFLAC(f)
	case ".ogg":
		return p.decodeOGG(f)
	case ".aiff", ".aif":
		return p.decodeAIFF(f)
	default:
		return nil, 0, 0, fmt.Errorf("unsupported audio format: %s", ext)
	}
}

func (p *Player) decodeMP3(f *os.File) ([]int16, uint32, int, error) {
	streamer, format, err := mp3.Decode(f)
	if err != nil {
		return nil, 0, 0, err
	}
	defer func() { _ = streamer.Close() }()

	return streamToSamples(streamer, int(format.SampleRate), format.NumChannels)
}

func (p *Player) decodeWAV(f *os.File) ([]int16, uint32, int, error) {
	streamer, format, err := wav.Decode(f)
	if err != nil {
		return nil, 0, 0, err
	}
	defer func() { _ = streamer.Close() }()

	return streamToSamples(streamer, int(format.SampleRate), format.NumChannels)
}

func (p *Player) decodeFLAC(f *os.File) ([]int16, uint32, int, error) {
	streamer, format, err := flac.Decode(f)
	if err != nil {
		return nil, 0, 0, err
	}
	defer func() { _ = streamer.Close() }()

	return streamToSamples(streamer, int(format.SampleRate), format.NumChannels)
}

func (p *Player) decodeOGG(f *os.File) ([]int16, uint32, int, error) {
	streamer, format, err := vorbis.Decode(f)
	if err != nil {
		return nil, 0, 0, err
	}
	defer func() { _ = streamer.Close() }()

	return streamToSamples(streamer, int(format.SampleRate), format.NumChannels)
}

func (p *Player) decodeAIFF(f *os.File) ([]int16, uint32, int, error) {
	decoder := aiff.NewDecoder(f)
	if !decoder.IsValidFile() {
		return nil, 0, 0, fmt.Errorf("invalid AIFF file")
	}

	decoder.ReadInfo()

	buf, err := decoder.FullPCMBuffer()
	if err != nil {
		return nil, 0, 0, fmt.Errorf("failed to read AIFF data: %w", err)
	}

	// Convert samples based on bit depth
	bitDepth := int(decoder.BitDepth)
	samples := intBufferToSamples(buf, bitDepth)
	return samples, uint32(decoder.SampleRate), int(decoder.NumChans), nil
}

// streamToSamples converts a beep streamer to int16 samples
func streamToSamples(streamer interface {
	Stream([][2]float64) (int, bool)
}, sampleRate int, numChannels int) ([]int16, uint32, int, error) {
	var allSamples []int16
	buffer := make([][2]float64, 512)

	for {
		n, ok := streamer.Stream(buffer)
		if n == 0 {
			break
		}

		for i := 0; i < n; i++ {
			// Left channel
			sample := int16(buffer[i][0] * 32767)
			allSamples = append(allSamples, sample)

			// Right channel (if stereo)
			if numChannels >= 2 {
				sample = int16(buffer[i][1] * 32767)
				allSamples = append(allSamples, sample)
			}
		}

		if !ok {
			break
		}
	}

	return allSamples, uint32(sampleRate), numChannels, nil
}

// intBufferToSamples converts go-audio IntBuffer to int16 samples
// bitDepth specifies the source bit depth (8, 16, 24, 32) for proper scaling
func intBufferToSamples(buf *audio.IntBuffer, bitDepth int) []int16 {
	samples := make([]int16, len(buf.Data))

	// Calculate shift amount based on bit depth
	// We need to convert from source bit depth to 16-bit
	switch bitDepth {
	case 8:
		// 8-bit: shift left by 8
		for i, v := range buf.Data {
			samples[i] = int16(v << 8)
		}
	case 16:
		// 16-bit: no conversion needed
		for i, v := range buf.Data {
			samples[i] = int16(v)
		}
	case 24:
		// 24-bit: shift right by 8 to get upper 16 bits
		for i, v := range buf.Data {
			samples[i] = int16(v >> 8)
		}
	case 32:
		// 32-bit: shift right by 16 to get upper 16 bits
		for i, v := range buf.Data {
			samples[i] = int16(v >> 16)
		}
	default:
		// Fallback: assume 16-bit
		for i, v := range buf.Data {
			samples[i] = int16(v)
		}
	}

	return samples
}

// samplesToBytes converts int16 samples to bytes (little-endian)
func samplesToBytes(samples []int16) []byte {
	bytes := make([]byte, len(samples)*2)
	for i, s := range samples {
		bytes[i*2] = byte(s)
		bytes[i*2+1] = byte(s >> 8)
	}
	return bytes
}

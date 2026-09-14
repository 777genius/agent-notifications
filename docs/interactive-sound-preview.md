# Interactive Sound Preview

Use [shared OS path selection](CONFIGURATION.md#manual-configuration) and the [revision-checked settings recipe](../commands/settings.md). Examples show fields, not replacement documents. Keep unchanged by default, submit only requested leaf edits through private stdin, and ask for an explicit decision after CAS conflicts. Safe inspect omits free-form values; omission is not an instruction to reset them.

## Overview

The `/claude-notifications-go:settings` command now includes **interactive sound preview** functionality, allowing users to listen to notification sounds before choosing them.

This feature was inspired by the bash version of the plugin and provides a superior user experience during setup.

## How It Works

### For Users

When running `/claude-notifications-go:settings`, users can preview any sound by typing:

```
play task-complete
play Glass
preview Hero
прослушать Ping
проиграть Sosumi
```

**Supported languages:**
- English: `play`, `preview`
- Russian: `прослушать`, `проиграть`

### For Claude

The setup wizard command (`commands/setup-notifications.md`) contains detailed instructions for Claude to:

1. **Detect preview requests** - Parse user messages for keywords (play, preview, прослушать, проиграть)
2. **Extract sound name** - Get the sound name from the user's message
3. **Determine sound path** - Resolve to either built-in sound or system sound
4. **Play the sound** - Execute `bin/sound-preview <path>`
5. **Ask for next action** - Let user preview more sounds or proceed with selection

### Preview Flow

```
Step 1-2: Detect system and list available sounds
    ↓
Step 3: INTERACTIVE PREVIEW PHASE
    User: "play Glass"
    Claude: [plays Glass.aiff]
    Claude: "Would you like to hear another sound?"
    User: "preview Hero"
    Claude: [plays Hero.aiff]
    User: "ready"
    ↓
Step 4: Ask only for requested changes; Keep unchanged is the default
    (Users can still preview during questions)
    ↓
Step 5: Optional explicit webhook leaf changes
    ↓
Step 6: Submit only requested edits with config edit --stdin --expect-revision TOKEN
    ↓
Step 7: Summary & test
```

## Technical Details

### Sound Preview Utility

The `bin/sound-preview` binary is a Go application that:

- **Supports multiple formats:** MP3, WAV, FLAC, OGG/Vorbis, AIFF
- **Native playback:** Uses `malgo` (miniaudio) for cross-platform output, with in-process format decoding
- **No shell-out:** Does not depend on `afplay`, `paplay`, `aplay`, or PowerShell audio APIs
- **Cross-platform:** Works on macOS, Linux, and Windows
- **Fast:** Loads and plays sounds in <1 second
- **Volume control:** Adjustable volume from 0.0 (silent) to 1.0 (full volume)

**Source:** `cmd/sound-preview/main.go`

**Usage:**
```bash
# Full volume (default)
bin/sound-preview sounds/task-complete.mp3

# 30% volume (recommended for testing)
bin/sound-preview --volume 0.3 sounds/task-complete.mp3

# 50% volume
bin/sound-preview --volume 0.5 /System/Library/Sounds/Glass.aiff

# Show help
bin/sound-preview --help
```

### Available Sounds

**Built-in sounds** (included in `sounds/` directory):
- `task-complete.mp3` - Triumphant completion chime
- `review-complete.mp3` - Gentle notification tone
- `question.mp3` - Attention-grabbing sound
- `plan-ready.mp3` - Professional planning tone

**System sounds** (macOS only):
- Location: `/System/Library/Sounds/*.aiff`
- Examples: Glass, Hero, Funk, Sosumi, Ping, Purr, Basso, Blow, Frog, Submarine, Bottle, Morse, Tink
- Formats: AIFF (automatically handled by sound-preview)

**System sounds** (Linux):
- Location: `/usr/share/sounds/**/*.ogg` (varies by distribution)
- Formats: OGG Vorbis, WAV

### Error Handling

If a sound is not found:

```
User: play unknown-sound
Claude: "❌ Sound 'unknown-sound' not found. Available sounds are:
  Built-in: task-complete, review-complete, question, plan-ready
  System (macOS): Glass, Hero, Funk, Sosumi, Ping, Purr, Basso, etc.
Try: 'play Glass' or 'preview task-complete'"
```

## Example Usage

### Complete Setup Flow

```
User: /setup-notifications

Claude: [detects system, lists available sounds]

Claude: "🎵 Want to hear sounds before choosing?
I can play any sound for you! Just say:
- 'play task-complete' - Built-in task-complete sound
- 'play Glass' - macOS Glass system sound
- 'preview Hero' - Preview any available sound

Try as many as you like! When you're ready, I'll ask you to select sounds."

User: play Glass

Claude: [executes: bin/sound-preview --volume 0.3 /System/Library/Sounds/Glass.aiff]
Claude: "🔊 Playing: Glass... (volume: 30%) ✓ Playback complete!
Would you like to hear another sound, or ready to choose?"

User: play Hero

Claude: [executes: bin/sound-preview --volume 0.3 /System/Library/Sounds/Hero.aiff]
Claude: "🔊 Playing: Hero... (volume: 30%) ✓ Playback complete!"

User: ready

Claude: "Great! Let's configure your notification sounds..."
Claude: [proceeds with AskUserQuestion for 4 notification types]
```

### Preview During Questions

Users can still preview sounds even after the preview phase:

```
Claude: "🎵 Reminder: You can still preview sounds! Just say 'play [sound_name]' before making your choice."
Claude: [shows AskUserQuestion: "Which sound for Task Complete notifications?"]

User: wait, let me hear Funk first

Claude: [executes: bin/sound-preview --volume 0.3 /System/Library/Sounds/Funk.aiff]
Claude: "🔊 Playing: Funk... (volume: 30%) ✓ Playback complete!"
Claude: [re-shows the question]

User: [selects Funk from options]
```

## Benefits

✅ **Better UX** - Users can hear sounds before choosing
✅ **Reduces mistakes** - No need to reconfigure after selecting wrong sounds
✅ **Supports multiple languages** - English and Russian commands
✅ **Patient interaction** - Users can preview as many sounds as they want
✅ **No external tools** - Uses built-in `bin/sound-preview` binary

## Comparison with Bash Version

| Feature | Bash Version | Go Version |
|---------|--------------|------------|
| Interactive preview | ✅ Yes | ✅ Yes |
| Sound preview during questions | ✅ Yes | ✅ Yes |
| Russian commands | ✅ Yes | ✅ Yes |
| Cross-platform | ✅ macOS/Linux/Windows | ✅ macOS/Linux/Windows |
| Sound playback | `afplay` (macOS only) | `malgo`/miniaudio (all platforms) |
| Supported formats | MP3, AIFF, OGG | MP3, WAV, FLAC, OGG, AIFF |

## Testing

To test the sound preview utility manually:

```bash
# Test built-in sound (full volume)
./bin/sound-preview sounds/task-complete.mp3

# Test with reduced volume (30% - recommended for testing)
./bin/sound-preview --volume 0.3 sounds/task-complete.mp3

# Test macOS system sound at 30% volume
./bin/sound-preview --volume 0.3 /System/Library/Sounds/Glass.aiff

# Test Linux system sound at 50% volume
./bin/sound-preview --volume 0.5 /usr/share/sounds/freedesktop/stereo/complete.oga

# Test very quiet (10% volume)
./bin/sound-preview --volume 0.1 sounds/question.mp3

# Show help and options
./bin/sound-preview --help

# Should show error for non-existent file
./bin/sound-preview non-existent.mp3

# Should show error for invalid volume
./bin/sound-preview --volume 1.5 sounds/task-complete.mp3  # Volume must be 0.0-1.0
```

**Volume Recommendations:**
- **Testing/development:** Use `--volume 0.3` (30%) to avoid disturbing others
- **Setup wizard preview:** Always use `--volume 0.3` (30%)
- **User volume preference test:** Use `--volume 1.0` (full volume, default)
- **Very quiet environment:** Use `--volume 0.1` (10%)

## Future Enhancements

Potential improvements:

- [ ] Show sound duration before playing
- [ ] Add volume control option
- [ ] Preview multiple sounds in sequence (e.g., "play all")
- [ ] Visual waveform display (low priority)
- [ ] Save favorite sounds list for quick access

## Related Files

- **Setup command:** `commands/setup-notifications.md`
- **Sound preview tool:** `cmd/sound-preview/main.go`
- **Built-in sounds:** `sounds/`
- **Config file:** the shared file selected by `config path` (create-only initialization; subsequent edits use revision checks)

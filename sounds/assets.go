// Package soundassets embeds the repository's existing, reviewed built-in
// notification cues. The Qoder native package copies these exact bytes without
// reading ambient user paths or changing the shared runtime's ownership.
package soundassets

import "embed"

// Builtins contains the original five tracked MP3 files.
//
//go:embed *.mp3
var Builtins embed.FS

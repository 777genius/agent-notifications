// Package portablepackage embeds the canonical Pi producer into portable release
// assets; there is one source shared by the repository package and release ZIP.
package portablepackage

import _ "embed"

//go:embed com.universal-agent-plugins.pi/index.ts
var PiExtension []byte

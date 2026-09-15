package warpfocus

// Open launches a validated Warp session focus URL with the platform handler
// (open / xdg-open / rundll32). Callers must pass Normalize/FromEnv output.
func Open(focusURL string) error {
	url := Normalize(focusURL)
	if url == "" {
		return errInvalidFocusURL
	}
	return openURL(url)
}

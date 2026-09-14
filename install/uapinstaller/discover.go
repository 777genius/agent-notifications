package uapinstaller

// Discover returns metadata of this beta's supported providers. It does not
// create state, run a helper, or execute a found client executable.
func (e *Engine) Discover() []ClientMetadata {
	return []ClientMetadata{
		{ClientID: "claude", Scopes: []string{"user"}},
		{ClientID: "codex", Scopes: []string{"user"}},
	}
}

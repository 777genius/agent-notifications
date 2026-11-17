//go:build !linux

package notifier

import "errors"

// Unqualified platforms never attach approximate click actions to a new CLI.
func (n *Notifier) sendVerifiedTerminal(_, _, _, _ string, _ sendOptions) error {
	return errors.New("strict_terminal_unavailable")
}

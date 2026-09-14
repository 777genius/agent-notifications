//go:build !linux && !darwin

package journal

import "context"

func (s *Store) bootstrap(context.Context) error                              { return ErrUnavailable }
func (s *Store) transaction(context.Context, func(*disk) (bool, error)) error { return ErrUnavailable }

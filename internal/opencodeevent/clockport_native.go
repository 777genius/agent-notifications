package opencodeevent

// NewSystemSnapshotPort has no config, lease, store, provider or notification
// dependencies. Both helper and admission must sample this same OS coordinate.
func NewSystemSnapshotPort() SnapshotPort {
	return ClockPort{Counter: systemCounter{}, Wall: systemWall{}}
}

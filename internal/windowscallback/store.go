package windowscallback

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
)

const MaxGenerations = 4
const MaxRecords = 1024
const MaxAttempts = 2048
const StorageBytes = 64 * 1024 * 1024
const AttemptReservation = 32 * 1024

var ErrUnknown = errors.New("native Windows handoff uncertain")
var ErrDisabled = errors.New("Windows notifications disabled")
var ErrCapacity = errors.New("retained callback capacity unavailable")

func Digest(b []byte) string { d := sha256.Sum256(b); return hex.EncodeToString(d[:]) }

// Capacity is a charged reservation ledger. Unknown/crash never releases units.
// Fixed pending/next records make recovery fail closed without directory scans.
type Capacity struct{ Records, RecordBytes, Attempts, AttemptBytes uint64 }

func (c Capacity) bytes() []byte {
	b := make([]byte, 40)
	copy(b, "WCAP0001")
	for i, v := range []uint64{c.Records, c.RecordBytes, c.Attempts, c.AttemptBytes} {
		binary.LittleEndian.PutUint64(b[8+i*8:], v)
	}
	d := sha256.Sum256(b)
	return append(b, d[:]...)
}
func decodeCapacity(b []byte) (Capacity, error) {
	var c Capacity
	if len(b) != 72 || string(b[:8]) != "WCAP0001" || Digest(b[:40]) != hex.EncodeToString(b[40:]) {
		return c, ErrCapacity
	}
	c = Capacity{binary.LittleEndian.Uint64(b[8:]), binary.LittleEndian.Uint64(b[16:]), binary.LittleEndian.Uint64(b[24:]), binary.LittleEndian.Uint64(b[32:])}
	if c.Records > MaxRecords || c.Attempts > MaxAttempts || c.RecordBytes > StorageBytes || c.AttemptBytes > StorageBytes || c.RecordBytes != c.Records*MaxEnvelope || c.AttemptBytes != c.Attempts*AttemptReservation {
		return Capacity{}, ErrCapacity
	}
	return c, nil
}
func (c Capacity) reserve(record bool) (Capacity, error) {
	if record {
		if c.Records >= MaxRecords || c.RecordBytes > StorageBytes-MaxEnvelope {
			return c, ErrCapacity
		}
		c.Records++
		c.RecordBytes += MaxEnvelope
	} else {
		if c.Attempts >= MaxAttempts || c.AttemptBytes > StorageBytes-AttemptReservation {
			return c, ErrCapacity
		}
		c.Attempts++
		c.AttemptBytes += AttemptReservation
	}
	return c, nil
}

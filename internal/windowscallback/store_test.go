package windowscallback

import (
	"bytes"
	"os"
	"testing"
)

// Breakage: the producer and native reader disagree on an independently written
// charged ledger, or count/byte bounds admit another record/attempt after full.
func TestIndependentCapacityContract(t *testing.T) {
	b, e := os.ReadFile("testdata/capacity.wcap")
	if e != nil {
		t.Fatal(e)
	}
	c, e := decodeCapacity(b)
	if e != nil || c != (Capacity{1023, 67043328, 2047, 67076096}) {
		t.Fatal(c, e)
	}
	if !bytes.Equal(c.bytes(), b) {
		t.Fatal("golden capacity bytes changed")
	}
	c, e = c.reserve(true)
	if e != nil || c.Records != 1024 || c.RecordBytes != 67108864 {
		t.Fatal(c, e)
	}
	if _, e = c.reserve(true); e != ErrCapacity {
		t.Fatal("record capacity over-admission", e)
	}
	c, e = c.reserve(false)
	if e != nil || c.Attempts != 2048 || c.AttemptBytes != 67108864 {
		t.Fatal(c, e)
	}
	if _, e = c.reserve(false); e != ErrCapacity {
		t.Fatal("attempt capacity over-admission", e)
	}
	overflow, e := os.ReadFile("testdata/capacity-overflow.wcap")
	if e != nil {
		t.Fatal(e)
	}
	for _, bad := range [][]byte{append(b, 0), b[:71], overflow} {
		if _, e = decodeCapacity(bad); e != ErrCapacity {
			t.Fatal("invalid charged ledger admitted", e)
		}
	}
}

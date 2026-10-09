//go:build windows

package windowscallback

import (
	"context"
	"fmt"
	"runtime"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var familyPackages = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetPackagesByPackageFamily")
var packageID = windows.NewLazySystemDLL("kernel32.dll").NewProc("PackageIdFromFullName")
var tickCount = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetTickCount64")

func BootMilliseconds() uint64 { n, _, _ := tickCount.Call(); return uint64(n) }
func budget(ctx context.Context, end uint64) error {
	if ctx == nil || ctx.Err() != nil || BootMilliseconds() >= end {
		return ErrUnavailable
	}
	return nil
}

type packageIdentity struct {
	Reserved, Architecture                 uint32
	Version                                uint64
	Name, Publisher, Resource, PublisherID uintptr
}

// boundedUTF16 never dereferences an OS pointer until its entire terminated
// string is proved to lie in the returned caller-owned, aligned buffer.
func boundedUTF16(buffer []byte, p uintptr) (string, error) {
	if len(buffer) < 2 {
		return "", ErrUnavailable
	}
	start := uintptr(unsafe.Pointer(&buffer[0]))
	end := start + uintptr(len(buffer))
	if p < start || p >= end || p%2 != 0 {
		return "", ErrUnavailable
	}
	units := unsafe.Slice((*uint16)(unsafe.Pointer(&buffer[int(p-start)])), int((end-p)/2))
	n := 0
	for n < len(units) && units[n] != 0 {
		n++
	}
	if n == len(units) {
		return "", ErrUnavailable
	}
	for i := 0; i < n; i++ {
		if units[i] >= 0xd800 && units[i] <= 0xdbff {
			if i+1 >= n || units[i+1] < 0xdc00 || units[i+1] > 0xdfff {
				return "", ErrUnavailable
			}
			i++
		} else if units[i] >= 0xdc00 && units[i] <= 0xdfff {
			return "", ErrUnavailable
		}
	}
	return string(utf16.Decode(units[:n])), nil
}

// DiscoverSelected is read-only, current-user, exact-one official x64 package.
// Negotiation uses the same original deadline; size changes never trigger retry.
func DiscoverSelected(ctx context.Context, end uint64) (string, error) {
	if runtime.GOARCH != "amd64" || budget(ctx, end) != nil {
		return "", ErrUnavailable
	}
	family, _ := windows.UTF16PtrFromString(VendorFamily)
	var count, characters uint32
	status, _, _ := familyPackages.Call(uintptr(unsafe.Pointer(family)), uintptr(unsafe.Pointer(&count)), 0, uintptr(unsafe.Pointer(&characters)), 0)
	if budget(ctx, end) != nil || status != uintptr(windows.ERROR_INSUFFICIENT_BUFFER) || count == 0 || count > 8 || characters == 0 || characters > 32768 {
		return "", ErrUnavailable
	}
	expectedCount, expectedCharacters := count, characters
	names := make([]uintptr, count)
	data := make([]uint16, characters)
	if budget(ctx, end) != nil {
		return "", ErrUnavailable
	}
	status, _, _ = familyPackages.Call(uintptr(unsafe.Pointer(family)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&names[0])), uintptr(unsafe.Pointer(&characters)), uintptr(unsafe.Pointer(&data[0])))
	if budget(ctx, end) != nil || status != 0 || count != expectedCount || characters != expectedCharacters || count != 1 {
		return "", ErrUnavailable
	}
	owned := unsafe.Slice((*byte)(unsafe.Pointer(&data[0])), len(data)*2)
	full, e := boundedUTF16(owned, names[0])
	if e != nil || !text(full, 256) {
		return "", ErrUnavailable
	}
	name, _ := windows.UTF16PtrFromString(full)
	var bytes uint32
	// PACKAGE_INFORMATION_FULL=0x100. BASIC omits the publisher pin.
	if budget(ctx, end) != nil {
		return "", ErrUnavailable
	}
	status, _, _ = packageID.Call(uintptr(unsafe.Pointer(name)), 0x100, uintptr(unsafe.Pointer(&bytes)), 0)
	if budget(ctx, end) != nil || status != uintptr(windows.ERROR_INSUFFICIENT_BUFFER) || bytes < uint32(unsafe.Sizeof(packageIdentity{})) || bytes > 65536 {
		return "", ErrUnavailable
	}
	expectedBytes := bytes
	buffer := make([]byte, bytes)
	if budget(ctx, end) != nil {
		return "", ErrUnavailable
	}
	status, _, _ = packageID.Call(uintptr(unsafe.Pointer(name)), 0x100, uintptr(unsafe.Pointer(&bytes)), uintptr(unsafe.Pointer(&buffer[0])))
	if budget(ctx, end) != nil || status != 0 || bytes != expectedBytes {
		return "", ErrUnavailable
	}
	selected, e := decodePackageID(buffer, full)
	if e != nil {
		return "", e
	}
	runtime.KeepAlive(data)
	runtime.KeepAlive(buffer)
	runtime.KeepAlive(names)
	return selected, nil
}

// decodePackageID validates caller-owned FULL ABI bytes. The officially pinned
// main package has no resource ID; NULL is the documented absent representation.
func decodePackageID(buffer []byte, full string) (string, error) {
	if len(buffer) < int(unsafe.Sizeof(packageIdentity{})) || len(buffer) > 65536 {
		return "", ErrUnavailable
	}
	id := (*packageIdentity)(unsafe.Pointer(&buffer[0]))
	vendor, e := boundedUTF16(buffer, id.Name)
	publisher, pe := boundedUTF16(buffer, id.Publisher)
	publisherID, ie := boundedUTF16(buffer, id.PublisherID)
	resource := ""
	var re error
	if id.Resource != 0 {
		resource, re = boundedUTF16(buffer, id.Resource)
	}
	if e != nil || pe != nil || ie != nil || re != nil || resource != "" || id.Architecture != 9 || vendor != VendorName || publisher != VendorPublisher || publisherID != "2p2nqsd0c76g0" {
		return "", ErrUnavailable
	}
	expected := fmt.Sprintf("%s_%d.%d.%d.%d_x64_%s_%s", vendor, id.Version>>48, (id.Version>>32)&65535, (id.Version>>16)&65535, id.Version&65535, resource, publisherID)
	if full != expected {
		return "", ErrUnavailable
	}
	return full, nil
}

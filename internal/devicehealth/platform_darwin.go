package devicehealth

import (
	"fmt"
	"os"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/unix"
	"shioji/internal/domain"
)

// The stable HOST_VM_INFO64_REV1 prefix, from mach/vm_statistics.h.
// Later kernels accept its count without requiring newer trailing fields.
type vmStats struct {
	Free, Active, Inactive, Wired                                            uint32
	Zero, Reactivated, Pageins, Pageouts, Faults, Cow, Lookups, Hits, Purges uint64
	Purgeable, Speculative                                                   uint32
	Decompressions, Compressions, Swapins, Swapouts                          uint64
	Compressor, Throttled, External, Internal                                uint32
	Uncompressed                                                             uint64
}
type nativeAPI struct {
	err              error
	host             uint32
	hostSelf         func() uint32
	hostStatistics   func(uint32, int32, *uint32, *uint32) int32
	hostVM           func(uint32, int32, *vmStats, *uint32) int32
	stringCreate     func(uintptr, string, uint32) uintptr
	release          func(uintptr)
	assertionCreate  func(uintptr, uint32, uintptr, *uint32) uint32
	assertionRelease func(uint32) uint32
}

var systemAPI = sync.OnceValue(func() *nativeAPI {
	a := &nativeAPI{}
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		a.err = err
		return a
	}
	io, err := purego.Dlopen("/System/Library/Frameworks/IOKit.framework/IOKit", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		a.err = err
		return a
	}
	sys, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		a.err = err
		return a
	}
	purego.RegisterLibFunc(&a.hostSelf, sys, "mach_host_self")
	purego.RegisterLibFunc(&a.hostStatistics, sys, "host_statistics")
	purego.RegisterLibFunc(&a.hostVM, sys, "host_statistics64")
	purego.RegisterLibFunc(&a.assertionCreate, io, "IOPMAssertionCreateWithName")
	purego.RegisterLibFunc(&a.assertionRelease, io, "IOPMAssertionRelease")
	purego.RegisterLibFunc(&a.stringCreate, cf, "CFStringCreateWithCString")
	purego.RegisterLibFunc(&a.release, cf, "CFRelease")
	a.host = a.hostSelf()
	return a
})

func (a *nativeAPI) str(s string) uintptr { return a.stringCreate(0, s, 0x08000100) }
func platform() (func() reading, func() (func() error, error)) {
	a := systemAPI()
	var previous [4]uint32
	var havePrevious bool
	total, _ := unix.SysctlUint64("hw.memsize")
	pageSize := uint64(os.Getpagesize())
	read := func() reading {
		var r reading
		if a.err != nil {
			return r
		}
		var ticks [4]uint32
		count := uint32(4)
		if a.hostStatistics(a.host, 3, &ticks[0], &count) == 0 && count == 4 {
			if havePrevious {
				r.CPU = cpuPercent(previous, ticks)
			}
			previous = ticks
			havePrevious = true
		} else {
			havePrevious = false
		}
		var stats vmStats
		count = uint32(unsafe.Sizeof(stats) / 4)
		if a.hostVM(a.host, 4, &stats, &count) == 0 && count == uint32(unsafe.Sizeof(stats)/4) {
			r.Memory = memoryUsage(total, pageSize, stats.Internal, stats.Purgeable, stats.Wired, stats.Compressor)
		}
		return r
	}
	acquire := func() (func() error, error) {
		if a.err != nil {
			return nil, a.err
		}
		kind, name := a.str("PreventUserIdleSystemSleep"), a.str("Shioji keep awake")
		defer a.release(kind)
		defer a.release(name)
		var id uint32
		if code := a.assertionCreate(kind, 255, name, &id); code != 0 {
			return nil, fmt.Errorf("无法保持设备唤醒（%#x）", code)
		}
		return func() error {
			if code := a.assertionRelease(id); code != 0 {
				return fmt.Errorf("无法关闭保持唤醒（%#x）", code)
			}
			return nil
		}, nil
	}
	return read, acquire
}

// Anonymous app memory minus purgeable pages, plus wired memory and the
// compressor's actual physical footprint. File-backed cache and swapped or
// uncompressed logical pages are not counted as occupied application memory.
func memoryUsage(total, pageSize uint64, internal, purgeable, wired, compressed uint32) *domain.DeviceMemory {
	if total == 0 || pageSize == 0 || internal < purgeable {
		return nil
	}
	used := (uint64(internal-purgeable) + uint64(wired) + uint64(compressed)) * pageSize
	if used > total {
		return nil
	}
	return &domain.DeviceMemory{UsedBytes: used, TotalBytes: total}
}

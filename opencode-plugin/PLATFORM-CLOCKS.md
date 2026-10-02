# Native platform clock source checkpoint R1

All ports are **UNQUALIFIED**. No clock qualification row, semantic ID, codec,
observer, provider, registry, SDK, bundle or workflow is changed by this checkpoint.
The source dispatch is `await createPlatformClock()` from `platform-clock.mjs`.
Preparation is asynchronous for Bun FFI imports; the returned `sample()` and
`dispose()` are synchronous. Existing prepared delivery continues to use its
Linux factory and closed Linux policy. Root owns native execution and integration.

| Platform | Raw Go coordinate / identity | Source reading / containing interval | Wall reading |
| --- | --- | --- | --- |
| Linux x64 / arm64 | `linux-boottime`; kernel boot UUID; `linux-time:dev:ino` | guarded `/proc/uptime`, `[first floor, second floor + 10ms]` | `Date.now()` integer milliseconds |
| Darwin x64 / arm64 | `darwin-monotonic-raw`; lowercase kernel boot-session UUID; `darwin-kernel` | `mach_continuous_time * numer / denom`, integer floor; `[first, second + 1ns]` | `Date.now()` integer milliseconds |
| Windows x64 | `windows-interrupt-precise`; SDK-encoded boot-environment GUID; `windows-kernel` | `QueryInterruptTimePrecise` unsigned 100ns ticks; `[first, second + 100ns]` | native precise FILETIME, integer Unix nanoseconds |

The native modules return frozen `{boot, domain, rawKind, loNS, hiNS, wallNS}`.
They intentionally have no offset/Q/T fields: the Linux preparation contract
must not accept them as if it already had a native platform comparison policy.
Their containing quantum addresses integer conversion only, not physical clock
accuracy or resolution. Native brackets must be at most 100ms including the
quantum. Both counters and wall are positive int64 nanoseconds. Boot, domain,
image proof, counter nonregression (including across samples), wall
nonregression and Darwin timebase are lifetime invariants. Any failed sample
releases every owned resource and permanently denies subsequent reads.
Explicit disposal is sticky, including when a release fails; cleanup attempts
every resource in reverse order and reports failure after attempting them all.

## Coordinate and source/comparison assumptions

Linux arm64 uses the existing proc contract. The staged Linux primary source
calls `ktime_get_boottime_ts64`, applies `timens_add_boottime`, and prints
two-decimal floors using unsigned long. Both owned Linux architectures are
LP64. The reader retains held procfs/nsfs magic and identities, verifies the
CURRENT calling task's time namespace before/after reads and rechecks boot.
The only change to the Linux reader is its architecture guard. Its existing
10ms containing quantum, Q2ms offset checks and T430ms refusal thresholds are
unchanged **candidate assumptions**, not a new arm64 grant. Bun bigint fs,
namespace and native Go/proc equivalence need actual arm64 root qualification.

Darwin uses `/usr/lib/libSystem.B.dylib`, `kern.bootsessionuuid` and Mach
continuous time. Apple's staged Libc makes `CLOCK_MONOTONIC_RAW` the same
continuous-clock integer multiply/divide, including suspend. We reject
unsigned multiply overflow even when mathematical division would fit; this
avoids disagreement with Libc's wrapping uint64 expression. XNU registers the
boot-session string read-only; neither `kern.boottime` nor a random UUID is a
boot identity. LP64 size_t, little-endian typed arrays, protected OS/dyld image
and stable timebase are assumptions for the two supported ABIs. Source Date
rounding, source event timestamp construction, Go wall agreement, suspend/rate
continuity and a complete comparison T are separate unproved root obligations.
There is no Darwin Q2/T430 inheritance and no measured-bound selection.

Windows uses native precise wall on both source candidates, never V1 Date
synthesis. This does **not** establish a bound to V1 event timestamps: the
staged Bun1.3.14 WebKit source synthesizes UTC from low-resolution FILETIME
and QPC, with drift resync and a backwards clamp. Its 31.25ms resync threshold
and 2-second clamp are not a complete source error bound. Bun1.4.2's staged
WebKit precise wall path is a different source assumption and depends on the
actual `USE(BUN_JSC_ADDITIONS)` build. Neither version inherits Linux Q2/T430.
No Windows event-to-native-wall comparison or TimePolicy is enabled.

## Fixed FFI contracts and resources

Darwin: `uint64_t mach_continuous_time(void)`;
`kern_return_t mach_timebase_info(mach_timebase_info_data_t*)` (two uint32s);
`int sysctlbyname(const char*, void*, size_t*, void*, size_t)`.
Require zero status, exactly 37 returned boot bytes, ASCII UUID plus terminating
NUL, nonzero timebase fields and aligned output buffers. Canonicalization to
lowercase matches Go `clockport_darwin.go`.

Windows: `GetSystemDirectoryW(ptr,u32)->u32`,
`LoadLibraryExW(ptr,u64,u32)->u64`, `GetProcAddress(u64,ptr)->ptr`,
`FreeLibrary(u64)->i32`, `GetSystemTimePreciseAsFileTime(ptr)->void`,
`NtQuerySystemInformation(u32,ptr,u32,ptr)->i32`, and
`QueryInterruptTimePrecise(ptr)->void`. Win64 unifies the calling convention;
x86/arm64 Windows are denied. All VOID outputs are zeroed first; return
registers and stale last-error are not status. Interrupt time is multiplied by
100; FILETIME subtracts `116444736000000000` ticks before multiplication.
NT class 90 must return success and EXACTLY 32 bytes: 16-byte GUID,
uint32 firmware type, padding, uint64 flags. Go/SDK GUID encoding reads the
first DWORD/WORD/WORD little endian and the last eight bytes in memory order.
`UuidCreate(UUID*)->RPC_STATUS` creates an identity, not the kernel boot GUID.
`GetTickCount64(void)->ULONGLONG` is milliseconds and `QueryInterruptTime`
is a different, coarse API. None is imported or used as a fallback here.

The Windows bootstrap accepts only protected `C:\Windows\System32` with
canonical paths and no symlink/junction aliases. Other Windows installations
remain denied. The module checks `GetSystemDirectoryW` against that literal,
holds and verifies both DLL disk identities and follows PR2956602's repaired
API-set loader contract: load the literal
`api-ms-win-core-realtime-l1-1-1.dll` TOKEN with
`LOAD_LIBRARY_SEARCH_SYSTEM32` (0x800), then resolve its precise symbol with
`GetProcAddress`. It never opens a guessed API-set file or searches PATH/cwd.
The OS directory's protection is a trusted composition assumption, not a disk
hash authenticated by these source tests. The staged fixture authenticates OS
bootstrap hashes separately; root must establish that native custody.

Both pinned Bun sources expose `dlopen`, `ptr` and `linkSymbols`. The Windows
resolved-address wrapper uses closeable `linkSymbols` instead of `CFunction`:
the staged Bun1.4.2 `CFunction.close` is a no-op. Wrapper release precedes
`FreeLibrary`; libraries and held files/image also close in reverse acquisition
order. Darwin owns one
library and one image fd. All Bun FFI imports occur inside the specific port
constructors after platform/architecture and closed image checks.

## Pins, gaps and scope

Native image pins come from staged `closed-inputs.json`: official 1.18.33 /
Bun1.3.14 and 2.0.21 / Bun1.4.2 for Darwin x64/arm64 and Windows x64. No
non-Linux 1.18.34 identity is supplied, so it is denied. A source image guard
hashes a held executable fd, checks the exact closed SHA/platform/arch/Bun
tuple, and rechecks canonical executing-path and held/path stat identity,
size, mtime and ctime during sampling. Missing bigint identity support or
changed disk evidence denies the port. This is conditional on trusted process
globals/builtins and immutable protected mappings; it is not independent
kernel live-image attestation or authentication against privileged tampering.

Primary Apple/kernel/Bun/WebKit source hashes are verified in the artifact
ledger. Microsoft SDK/header bodies and complete Mach kernel implementations
are not staged. Windows NT/API-set contracts are grounded in current Go source
and the staged repaired native CI source, not newly fetched Microsoft code.
Exact stock-image FFI availability (including TinyCC build restrictions), OS
mapping custody, API-set resolution, alignment and successful closure remain
native root CI gaps. Any missing API or proof is a refusal, never a grant.

The prior 37-leaf algorithm root `7db560b8c59fe0443a884b9e85b124ec29bb3d0abd0a5354ff59430306645006`
is invalid for the modified Linux leaf. The four other immutable Linux module
bytes remain identical. New native modules/dispatch require root to define
and bind the expanded algorithm closure, using the supplied SDK leaves and
Merkle recipe. No new canonical algorithm root is fabricated and no generated
bundle is rebuilt. The narrow new dispatch is independent of Linux prepared
delivery, whose synchronous factory/protocol/semantic ID remain unchanged.

`native-clock.test.mjs` contains closest pure/library checks using literal
independent conversion/ABI/GUID vectors and read scripts. Run it only from a
private TEST fixture containing the exact copied source closure. It exercises
no native clock, proc read, FFI, host, runtime or provider; filesystem image
lifetime checks write only inside that fixture. Passing tests establish source
contracts, never native qualification or product E2E.

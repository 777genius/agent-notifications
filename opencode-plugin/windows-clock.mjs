import { lstatSync, realpathSync } from 'node:fs';
import { holdFile, pinNativeImage } from './native-clock-image.mjs';
import { address, closeAll, createNativeClock, filetimeNS, interruptABI, interruptNS,
  uint64, unavailable, windowsBoot, windowsKernelABI, windowsNtABI } from './native-clock-contract.mjs';

const directory = 'C:\\Windows\\System32';
function verifyPaths() {
  for (const path of ['C:\\', 'C:\\Windows', directory, `${directory}\\kernel32.dll`, `${directory}\\ntdll.dll`]) {
    if (lstatSync(path).isSymbolicLink() || realpathSync(path).toLowerCase() !== path.toLowerCase()) unavailable();
  }
}
// Private loaded-module lifetime, not a universal process singleton. A rejected
// attempt remains rejected; separately loaded module copies have separate state.
let bindingAttempt;
async function initializeBinding() {
  const releases = [];
  try {
    verifyPaths();
    const files = ['kernel32.dll', 'ntdll.dll'].map(name => `${directory}\\${name}`);
    const held = [];
    for (const path of files) { const file = holdFile(path); held.push(file); releases.push(file.close); }
    const { dlopen, ptr, linkSymbols } = await import('bun:ffi');
    if (typeof dlopen !== 'function' || typeof ptr !== 'function' || typeof linkSymbols !== 'function') unavailable();
    function library(path, abi) {
      const lib = dlopen(path, abi); releases.push(() => lib.close()); return lib.symbols;
    }
    const aligned = view => address(ptr(view), view.BYTES_PER_ELEMENT);
    const kernel = library(files[0], windowsKernelABI);
    const system = new Uint16Array(32768);
    const length = kernel.GetSystemDirectoryW(aligned(system), system.length);
    if (!Number.isInteger(length) || length <= 0 || length >= system.length || system[length] !== 0 ||
        String.fromCharCode(...system.subarray(0, length)).toLowerCase() !== directory.toLowerCase()) unavailable();
    const contract = Buffer.from('api-ms-win-core-realtime-l1-1-1.dll\0', 'utf16le');
    const handle = uint64(kernel.LoadLibraryExW(address(ptr(contract), 2), 0n, 0x800));
    if (handle === 0n) unavailable();
    releases.push(() => { if (kernel.FreeLibrary(handle) === 0) unavailable(); });
    const name = Buffer.from('QueryInterruptTimePrecise\0');
    const target = address(kernel.GetProcAddress(handle, address(ptr(name))));
    const wrapper = linkSymbols({ QueryInterruptTimePrecise: { ...interruptABI, ptr: target } });
    releases.push(() => wrapper.close());
    const nt = library(files[1], windowsNtABI);
    function verify() { verifyPaths(); for (const file of held) file.verify(); }
    verify();
    // Retain the genuine DLL/FFI owners and original held files. Successful
    // bindings are never unloaded by instance disposal, failure or cancellation.
    return Object.freeze({ kernel: Object.freeze(kernel), nt: Object.freeze(nt), aligned, verify,
      interrupt: wrapper.symbols.QueryInterruptTimePrecise, retained: Object.freeze(releases) });
  } catch { try { closeAll(releases); } catch {} unavailable(); }
}

export async function createWindowsClock({ signal } = {}) {
  if (signal?.aborted || process.platform !== 'win32' || process.arch !== 'x64') unavailable();
  const releases = []; let clock;
  const abort = () => { try { if (clock) clock.dispose(); else closeAll(releases); } catch {} };
  function closeInstance() { signal?.removeEventListener('abort', abort); closeAll(releases); }
  signal?.addEventListener('abort', abort, { once: true });
  try {
    if (signal?.aborted) unavailable();
    const image = pinNativeImage(); releases.push(image.close);
    // In-flight cancellation closes this image only. The at-most-once shared
    // attempt may finish and remain bounded; cancellation never starts a retry.
    const binding = await (bindingAttempt ??= initializeBinding());
    if (signal?.aborted) unavailable();
    const { kernel, nt, aligned } = binding;
    const info = new BigUint64Array(4), returned = new Uint32Array(1);
    const counter = new BigUint64Array(1), wall = new BigUint64Array(1);
    clock = createNativeClock({
      imageSHA256: image.imageSHA256,
      readBoot() {
        info.fill(0n); returned[0] = 0;
        const status = nt.NtQuerySystemInformation(90, aligned(info), 32, aligned(returned));
        return windowsBoot(new Uint8Array(info.buffer), returned[0], status);
      },
      readCounter() {
        counter[0] = 0n; binding.interrupt(aligned(counter));
        return interruptNS(counter[0]); // VOID: never interpret return/last-error.
      },
      readWall() {
        wall[0] = 0n; kernel.GetSystemTimePreciseAsFileTime(aligned(wall));
        return filetimeNS(wall[0]);
      },
      verify() { if (signal?.aborted) unavailable(); image.verify(); binding.verify(); },
      close: closeInstance,
    }, 'windows-kernel');
    return clock;
  } catch { try { closeInstance(); } catch {} unavailable(); }
}

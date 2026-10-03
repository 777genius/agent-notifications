// Generated with the UAP observer pinned in package-lock.json.
var __defProp = Object.defineProperty;
var __getOwnPropNames = Object.getOwnPropertyNames;
var __esm = (fn, res, err) => function __init() {
  if (err) throw err[0];
  try {
    return fn && (res = (0, fn[__getOwnPropNames(fn)[0]])(fn = 0)), res;
  } catch (e) {
    throw err = [e], e;
  }
};
var __export = (target, all) => {
  for (var name in all)
    __defProp(target, name, { get: all[name], enumerable: true });
};

// protocol.mjs
import { createHash } from "node:crypto";
function ns(value) {
  if (typeof value !== "string" || !/^(0|[1-9][0-9]{0,18})$/.test(value)) invalid();
  const result2 = BigInt(value);
  if (result2 > int64Max) invalid();
  return result2;
}
function closed(value, keys, required = keys) {
  if (!value || Object.getPrototypeOf(value) !== Object.prototype || Reflect.ownKeys(value).some((key) => !keys.includes(key)) || required.some((key) => !Object.hasOwn(value, key))) invalid();
  return value;
}
function domainOK(x, kind = "linux-boottime") {
  if (kind === "darwin-monotonic-raw") return x === "darwin-kernel";
  if (kind === "windows-interrupt-precise") return x === "windows-kernel";
  if (kind !== "linux-boottime" || typeof x !== "string" || !/^linux-time:[1-9][0-9]*:[1-9][0-9]*$/.test(x)) return false;
  return x.split(":").slice(1).every((part) => part.length <= 20 && BigInt(part) <= 18446744073709551615n);
}
function parseJSON(bytes, max = 4096) {
  if (!Buffer.isBuffer(bytes) || bytes.length > max) invalid();
  const text = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  const token = /\s*("(?:[^"\\\u0000-\u001f]|\\(?:["\\/bfnrt]|u[0-9a-fA-F]{4}))*"|-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?|true|false|null|[{}\[\],:])/y;
  let at = 0, entries = 0;
  function next() {
    token.lastIndex = at;
    const m = token.exec(text);
    if (!m) invalid();
    at = token.lastIndex;
    return m[1];
  }
  function value(t, depth) {
    if (depth > 8 || ++entries > 96) invalid();
    if (t === "{") {
      const keys = /* @__PURE__ */ new Set();
      let k = next();
      if (k === "}") return;
      for (; ; ) {
        if (!k.startsWith('"')) invalid();
        const key = JSON.parse(k);
        if (keys.has(key)) invalid();
        keys.add(key);
        if (next() !== ":") invalid();
        value(next(), depth + 1);
        k = next();
        if (k === "}") return;
        if (k !== ",") invalid();
        k = next();
      }
    }
    if (t === "[") {
      let v = next();
      if (v === "]") return;
      for (; ; ) {
        value(v, depth + 1);
        v = next();
        if (v === "]") return;
        if (v !== ",") invalid();
        v = next();
      }
    }
    if (["}", "]", ",", ":"].includes(t) || !t.startsWith('"') && !["true", "false", "null"].includes(t) && (!/^-?(0|[1-9][0-9]*)$/.test(t) || !Number.isSafeInteger(Number(t)))) invalid();
  }
  value(next(), 0);
  if (text.slice(at).trim()) invalid();
  return JSON.parse(text);
}
function profileReceipt(output) {
  const receipt = closed(parseJSON(output, 1024), ["protocol", "semantic", "generation", "resourceClosure"]);
  if (receipt.protocol !== 1 || receipt.resourceClosure !== "reaped_or_not_started" || !(receipt.semantic === "unverified" && receipt.generation === "none" || receipt.semantic === "eligible" && ["v1", "v2"].includes(receipt.generation))) invalid();
  return Object.freeze(receipt);
}
function clockReceipt(output) {
  const r = closed(parseJSON(output, 1024), [
    "protocol",
    "boot",
    "clockDomain",
    "clockKind",
    "monoLoNs",
    "monoHiNs",
    "wallUnixNs",
    "uncertaintyNs"
  ]);
  const lo = ns(r.monoLoNs), hi = ns(r.monoHiNs);
  if (r.protocol !== 1 || !bootOK(r.boot) || !domainOK(r.clockDomain, r.clockKind) || hi < lo || hi - lo > 100000000n || ns(r.wallUnixNs) === 0n || ns(r.uncertaintyNs) !== hi - lo + 3000000n) invalid();
  return Object.freeze({
    boot: r.boot,
    domain: r.clockDomain,
    rawKind: r.clockKind,
    monoLoNS: r.monoLoNs,
    monoHiNS: r.monoHiNs,
    wallNS: r.wallUnixNs,
    readUncertaintyNS: r.uncertaintyNs
  });
}
function fence(anchor, policy) {
  const hash = createHash("sha256");
  for (const string of ["AN/OpenCode/clock-policy/v1", anchor.boot, anchor.domain, anchor.rawKind, policy.profileID]) {
    const data = Buffer.from(string), length = Buffer.alloc(4);
    length.writeUInt32BE(data.length);
    hash.update(length).update(data);
  }
  for (const bound of [policy.nativeReadBoundNS, policy.comparisonBoundNS]) {
    const data = Buffer.alloc(8);
    data.writeBigInt64BE(bound);
    hash.update(data);
  }
  return hash.digest("hex");
}
function validateFrame(frame, policy) {
  const p = closed(frame, ["protocol", "origin", "event", "provenance"]);
  if (p.protocol !== 1 || typeof p.origin !== "string" || !/^[a-f0-9]{64}$/.test(p.origin)) invalid();
  const e = closed(
    p.event,
    ["version", "kind", "sessionID", "turnID", "rootSession", "provenance", "messageID", "requestID", "nativeType"],
    ["version", "kind", "sessionID", "turnID", "rootSession", "provenance"]
  );
  if (e.version !== 1 || e.rootSession !== true || !identity(e.sessionID) || !identity(e.turnID) || !["turn_idle_verified", "question_asked", "permission_asked", "terminal_error"].includes(e.kind) || e.nativeType !== void 0 || e.messageID !== void 0 && !identity(e.messageID) || e.requestID !== void 0 && !identity(e.requestID) || e.kind === "turn_idle_verified" && (!identity(e.messageID) || e.requestID !== void 0) || e.kind.endsWith("_asked") && (!identity(e.requestID) || e.messageID !== void 0) || e.kind === "terminal_error" && (e.messageID !== void 0 || e.requestID !== void 0)) invalid();
  const native = closed(
    e.provenance,
    ["generation", "observationID", "nativeTime", "timeBasis", "nativeEventID", "nativeMessageID"],
    ["generation", "observationID", "nativeTime", "timeBasis"]
  );
  if (!identity(native.observationID) || !Number.isSafeInteger(native.nativeTime) || native.nativeTime <= 0 || BigInt(native.nativeTime) * 1000000n > int64Max || !["v1", "v2"].includes(native.generation) || native.generation !== policy.generation || native.nativeEventID !== void 0 && !identity(native.nativeEventID) || native.nativeMessageID !== void 0 && !identity(native.nativeMessageID) || native.generation === "v2" && (!identity(native.nativeEventID) || native.timeBasis !== "envelope_created") || native.generation === "v1" && native.timeBasis !== (e.kind === "turn_idle_verified" ? "assistant_completed" : "assistant_created_lower_bound") || native.generation === "v1" && e.kind === "terminal_error" && !identity(native.nativeMessageID)) invalid();
  const v = closed(p.provenance, [
    "sourceEpoch",
    "epochStartedTickNS",
    "policyID",
    "fence",
    "anchor",
    "ingressTickNS",
    "spawnTickNS",
    "deadlineTickNS",
    "calibration"
  ]);
  if (!identity(v.sourceEpoch, 128) || !/^[\x20-\x7e]+$/.test(v.sourceEpoch) || v.policyID !== policy.profileID || !rawKindOK(policy.rawKind) || policy.nativeReadBoundNS < 3000000n || policy.nativeReadBoundNS > 103000000n || policy.comparisonBoundNS < 2n * policy.nativeReadBoundNS || policy.comparisonBoundNS > 2000000000n || policy.translationBoundNS < 0n || 2n * policy.nativeReadBoundNS + policy.translationBoundNS > policy.comparisonBoundNS) invalid();
  const a = closed(v.anchor, ["boot", "domain", "rawKind", "monoLoNS", "monoHiNS", "wallNS", "readUncertaintyNS"]);
  const lo = ns(a.monoLoNS), hi = ns(a.monoHiNS);
  if (!bootOK(a.boot) || !domainOK(a.domain, a.rawKind) || a.rawKind !== policy.rawKind || hi < lo || hi - lo > 100000000n || ns(a.wallNS) === 0n || ns(a.readUncertaintyNS) !== hi - lo + 3000000n || ns(a.readUncertaintyNS) > policy.nativeReadBoundNS || v.fence !== fence(a, policy)) invalid();
  const start = ns(v.epochStartedTickNS), ingress = ns(v.ingressTickNS), spawn2 = ns(v.spawnTickNS), deadline = ns(v.deadlineTickNS);
  if (start > lo || lo > ingress || ingress > spawn2 || spawn2 >= deadline || spawn2 - ingress > 30000000000n || deadline - spawn2 > 20000000000n) invalid();
  const c = closed(v.calibration, ["calibrationID", "sourceEpoch", "sourceLoNS", "sourceHiNS", "nativeLoNS", "nativeHiNS", "errorNS"]);
  if (c.calibrationID !== policy.calibrationID || c.sourceEpoch !== v.sourceEpoch || ns(c.sourceLoNS) >= ns(c.sourceHiNS) || ns(c.sourceHiNS) - ns(c.sourceLoNS) > policy.translationBoundNS || ns(c.sourceLoNS) > hi || lo >= ns(c.sourceHiNS) || c.nativeLoNS !== a.monoLoNS || c.nativeHiNS !== a.monoHiNS || ns(c.errorNS) !== policy.translationBoundNS) invalid();
  return frame;
}
function encodeFrame(frame, policy) {
  validateFrame(frame, policy);
  const output = Buffer.from(JSON.stringify(frame));
  if (output.length > 4096) invalid();
  return output;
}
var invalid, int64Max, identity, bootOK, rawKindOK;
var init_protocol = __esm({
  "protocol.mjs"() {
    invalid = () => {
      throw new TypeError("invalid_private_protocol");
    };
    int64Max = 9223372036854775807n;
    identity = (x, limit = 256) => typeof x === "string" && x.length > 0 && !/[\u0000-\u001f\u007f]/u.test(x) && Buffer.byteLength(x) <= limit;
    bootOK = (x) => typeof x === "string" && /^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(x) && x !== "00000000-0000-0000-0000-000000000000";
    rawKindOK = (kind) => ["linux-boottime", "darwin-monotonic-raw", "windows-interrupt-precise"].includes(kind);
  }
});

// native-clock-contract.mjs
function uint64(value) {
  if (typeof value !== "bigint" && !Number.isSafeInteger(value)) unavailable();
  const n = BigInt(value);
  if (n < 0n || n > U64) unavailable();
  return n;
}
function positiveNS(value) {
  if (typeof value !== "bigint" || value <= 0n || value > int64Max) unavailable();
  return value;
}
function machNS(ticks, numer, denom) {
  ticks = uint64(ticks);
  if (!Number.isInteger(numer) || !Number.isInteger(denom) || numer <= 0 || denom <= 0 || numer > 4294967295 || denom > 4294967295 || ticks > U64 / BigInt(numer)) unavailable();
  return positiveNS(ticks * BigInt(numer) / BigInt(denom));
}
function interruptNS(ticks) {
  return positiveNS(uint64(ticks) * 100n);
}
function filetimeNS(ticks) {
  return positiveNS((uint64(ticks) - 116444736000000000n) * 100n);
}
function darwinBoot(bytes, length, status) {
  if (status !== 0 || length !== 37n || !(bytes instanceof Uint8Array) || bytes.length < 37 || bytes[36] !== 0 || !bytes.subarray(0, 36).every((b) => b > 0 && b < 128)) unavailable();
  const boot = Buffer.from(bytes.subarray(0, 36)).toString("ascii").toLowerCase();
  if (!bootOK(boot)) unavailable();
  return boot;
}
function windowsBoot(bytes, length, status) {
  if (status !== 0 || length !== 32 || !(bytes instanceof Uint8Array) || bytes.length !== 32) unavailable();
  const b = Buffer.from(bytes), hex = (n, w) => n.toString(16).padStart(w, "0");
  const boot = `${hex(b.readUInt32LE(0), 8)}-${hex(b.readUInt16LE(4), 4)}-${hex(b.readUInt16LE(6), 4)}-${b.subarray(8, 10).toString("hex")}-${b.subarray(10, 16).toString("hex")}`;
  if (!bootOK(boot)) unavailable();
  return boot;
}
function address(value, alignment = 1) {
  if (!Number.isSafeInteger(value) || value <= 0 || value % alignment !== 0) unavailable();
  return value;
}
function closeAll(releases) {
  let failed = false;
  for (const release of releases.splice(0).reverse()) {
    try {
      release();
    } catch {
      failed = true;
    }
  }
  if (failed) unavailable();
}
function abi(definitions) {
  return Object.freeze(Object.fromEntries(Object.entries(definitions).map(([key, [returns, ...args]]) => [key, Object.freeze({ returns, args: Object.freeze(args) })])));
}
function withWallOffset(sample, sourceWallBoundNS) {
  if (!bootOK(sample.boot) || !domainOK(sample.domain, sample.rawKind) || typeof sourceWallBoundNS !== "bigint" || sourceWallBoundNS < 0n || sourceWallBoundNS > 2000000000n || typeof sample.loNS !== "bigint" || sample.loNS < 0n || sample.loNS > int64Max || sample.rawKind !== "linux-boottime" && sample.loNS === 0n || typeof sample.hiNS !== "bigint" || sample.hiNS <= sample.loNS || sample.hiNS > int64Max || sample.hiNS - sample.loNS > (sample.rawKind === "linux-boottime" ? 110000000n : 100000000n)) unavailable();
  const wallNS = positiveNS(sample.wallNS);
  const offsetLoNS = wallNS - sample.hiNS - sourceWallBoundNS, offsetHiNS = wallNS - sample.loNS + sourceWallBoundNS;
  if (offsetLoNS < -int64Max || offsetHiNS > int64Max) unavailable();
  return Object.freeze({ ...sample, offsetLoNS, offsetHiNS });
}
function createNativeClock(port, domain) {
  let disposed = false, previous, boot, rawKind, quantum2;
  function dispose() {
    if (!disposed) {
      disposed = true;
      port.close();
    }
  }
  try {
    rawKind = domain === "darwin-kernel" ? "darwin-monotonic-raw" : domain === "windows-kernel" ? "windows-interrupt-precise" : unavailable();
    quantum2 = domain === "darwin-kernel" ? 1n : 100n;
    port.verify();
    boot = port.readBoot();
    if (!bootOK(boot)) unavailable();
  } catch {
    try {
      dispose();
    } catch {
    }
    unavailable();
  }
  function sample() {
    try {
      if (disposed) unavailable();
      port.verify();
      if (port.readBoot() !== boot) unavailable();
      const loNS = positiveNS(port.readCounter());
      const wallNS = positiveNS(port.readWall());
      const last = positiveNS(port.readCounter()), hiNS = positiveNS(last + quantum2);
      if (last < loNS || hiNS - loNS > 100000000n || previous && (loNS < previous.last || wallNS < previous.wallNS)) unavailable();
      if (port.readBoot() !== boot) unavailable();
      port.verify();
      previous = { last, wallNS };
      return Object.freeze({ boot, domain, rawKind, loNS, hiNS, wallNS });
    } catch {
      try {
        dispose();
      } catch {
      }
      unavailable();
    }
  }
  return Object.freeze({ sample, dispose, imageSHA256: port.imageSHA256 });
}
var unavailable, U64, darwinABI, windowsKernelABI, windowsNtABI, interruptABI;
var init_native_clock_contract = __esm({
  "native-clock-contract.mjs"() {
    init_protocol();
    unavailable = () => {
      throw new TypeError("clock_unavailable");
    };
    U64 = 18446744073709551615n;
    darwinABI = abi({
      mach_continuous_time: ["u64"],
      mach_timebase_info: ["i32", "ptr"],
      sysctlbyname: ["i32", "ptr", "ptr", "ptr", "ptr", "u64"]
    });
    windowsKernelABI = abi({
      GetSystemDirectoryW: ["u32", "ptr", "u32"],
      LoadLibraryExW: ["u64", "ptr", "u64", "u32"],
      GetProcAddress: ["ptr", "u64", "ptr"],
      FreeLibrary: ["i32", "u64"],
      GetSystemTimePreciseAsFileTime: ["void", "ptr"]
    });
    windowsNtABI = abi({ NtQuerySystemInformation: ["i32", "u32", "ptr", "u32", "ptr"] });
    interruptABI = Object.freeze({ returns: "void", args: Object.freeze(["ptr"]) });
  }
});

// native-clock-image.mjs
import { openSync as openSync2, closeSync as closeSync2, readSync as readSync2, fstatSync as fstatSync2, statSync as statSync3, realpathSync as realpathSync2, constants as constants2 } from "node:fs";
import { createHash as createHash3 } from "node:crypto";
function holdFile(path2) {
  const fd = openSync2(path2, constants2.O_RDONLY | (constants2.O_NOFOLLOW ?? 0));
  try {
    let verify = function() {
      if (disposed || !same2(initial, fstatSync2(fd, { bigint: true })) || !same2(initial, statSync3(path2, { bigint: true }))) unavailable();
    };
    const initial = fstatSync2(fd, { bigint: true });
    if (!initial.isFile() || !["dev", "ino", "size", "mtimeNs", "ctimeNs"].every((k) => typeof initial[k] === "bigint") || initial.ino <= 0n || initial.size <= 0n || initial.size > 536870912n) unavailable();
    const hash = createHash3("sha256"), block = Buffer.alloc(65536);
    let offset = 0;
    while (offset < Number(initial.size)) {
      const count = readSync2(fd, block, 0, Math.min(block.length, Number(initial.size) - offset), offset);
      if (count <= 0) unavailable();
      hash.update(block.subarray(0, count));
      offset += count;
    }
    const digest = hash.digest("hex");
    let disposed = false;
    verify();
    return Object.freeze({ digest, verify, close() {
      if (!disposed) {
        disposed = true;
        closeSync2(fd);
      }
    } });
  } catch {
    try {
      closeSync2(fd);
    } catch {
    }
    unavailable();
  }
}
function pinNativeImage() {
  const bun = globalThis.Bun?.version;
  if (!images2.some((row) => row[0] === process.platform && row[1] === process.arch && row[2] === bun)) unavailable();
  const path2 = realpathSync2(process.execPath), held2 = holdFile(path2);
  try {
    if (!images2.some((row) => row[0] === process.platform && row[1] === process.arch && row[2] === bun && row[3] === held2.digest)) unavailable();
    return Object.freeze({ imageSHA256: held2.digest, close: held2.close, verify() {
      if (globalThis.Bun?.version !== bun || realpathSync2(process.execPath) !== path2) unavailable();
      held2.verify();
    } });
  } catch {
    held2.close();
    unavailable();
  }
}
var images2, same2;
var init_native_clock_image = __esm({
  "native-clock-image.mjs"() {
    init_native_clock_contract();
    images2 = Object.freeze([
      ["darwin", "x64", "1.3.14", "f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9"],
      ["darwin", "arm64", "1.3.14", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524"],
      ["win32", "x64", "1.3.14", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"],
      ["darwin", "x64", "1.4.2", "4642b7da61279c8aa5d389d9f29454936e449fea6bc510689e9cc976fff6579f"],
      ["darwin", "arm64", "1.4.2", "0b2b68c1efaf20a29aaf636c2ffccc1abb56243a82f48cce45e257d232e03442"],
      ["win32", "x64", "1.4.2", "ec7a3909bad41ef88e4650f737ab6f0b0c402a7f49a588812d0a79820c2dfc1f"]
    ].map(Object.freeze));
    same2 = (a, b) => ["dev", "ino", "size", "mtimeNs", "ctimeNs"].every((k) => a[k] === b[k]);
  }
});

// darwin-clock.mjs
var darwin_clock_exports = {};
__export(darwin_clock_exports, {
  createDarwinClock: () => createDarwinClock
});
async function createDarwinClock({ signal } = {}) {
  if (signal?.aborted || process.platform !== "darwin" || !["x64", "arm64"].includes(process.arch)) unavailable();
  const releases = [];
  const abort = () => {
    try {
      closeAll(releases);
    } catch {
    }
  };
  signal?.addEventListener("abort", abort, { once: true });
  try {
    let readBoot = function() {
      buffer.fill(0);
      size[0] = BigInt(buffer.byteLength);
      const status = lib.sysctlbyname(address(ptr(name)), aligned(buffer), aligned(size), null, 0n);
      return darwinBoot(new Uint8Array(buffer.buffer), size[0], status);
    };
    const image = pinNativeImage();
    releases.push(image.close);
    const { dlopen, ptr } = await import("bun:ffi");
    if (signal?.aborted) unavailable();
    if (typeof dlopen !== "function" || typeof ptr !== "function") unavailable();
    const library = dlopen("/usr/lib/libSystem.B.dylib", darwinABI);
    releases.push(() => library.close());
    const lib = library.symbols, aligned = (view) => address(ptr(view), view.BYTES_PER_ELEMENT);
    const base = new Uint32Array(2);
    if (lib.mach_timebase_info(aligned(base)) !== 0 || base[0] === 0 || base[1] === 0) unavailable();
    const numer = base[0], denom = base[1];
    const name = Buffer.from("kern.bootsessionuuid\0");
    const buffer = new Uint32Array(16), size = new BigUint64Array(1);
    return createNativeClock({
      imageSHA256: image.imageSHA256,
      readBoot,
      readCounter: () => machNS(lib.mach_continuous_time(), numer, denom),
      readWall() {
        const ms = Date.now();
        if (!Number.isSafeInteger(ms)) unavailable();
        return positiveNS(BigInt(ms) * 1000000n);
      },
      verify() {
        image.verify();
        base.fill(0);
        if (lib.mach_timebase_info(aligned(base)) !== 0 || base[0] !== numer || base[1] !== denom) unavailable();
      },
      close: () => closeAll(releases)
    }, "darwin-kernel");
  } catch {
    try {
      closeAll(releases);
    } catch {
    }
    unavailable();
  } finally {
    signal?.removeEventListener("abort", abort);
  }
}
var init_darwin_clock = __esm({
  "darwin-clock.mjs"() {
    init_native_clock_image();
    init_native_clock_contract();
  }
});

// windows-clock.mjs
var windows_clock_exports = {};
__export(windows_clock_exports, {
  createWindowsClock: () => createWindowsClock
});
import { lstatSync, realpathSync as realpathSync3 } from "node:fs";
async function createWindowsClock({ signal } = {}) {
  if (signal?.aborted || process.platform !== "win32" || process.arch !== "x64") unavailable();
  const releases = [];
  const abort = () => {
    try {
      closeAll(releases);
    } catch {
    }
  };
  signal?.addEventListener("abort", abort, { once: true });
  try {
    let verifyPaths = function() {
      for (const path2 of ["C:\\", "C:\\Windows", directory, ...files]) {
        if (lstatSync(path2).isSymbolicLink() || realpathSync3(path2).toLowerCase() !== path2.toLowerCase()) unavailable();
      }
    }, library = function(path2, abi2) {
      const lib = dlopen(path2, abi2);
      releases.push(() => lib.close());
      return lib.symbols;
    };
    const image = pinNativeImage();
    releases.push(image.close);
    const files = ["kernel32.dll", "ntdll.dll"].map((name2) => `${directory}\\${name2}`);
    verifyPaths();
    const held2 = [];
    for (const path2 of files) {
      const file = holdFile(path2);
      held2.push(file);
      releases.push(file.close);
    }
    const { dlopen, ptr, linkSymbols } = await import("bun:ffi");
    if (signal?.aborted) unavailable();
    if (typeof dlopen !== "function" || typeof ptr !== "function" || typeof linkSymbols !== "function") unavailable();
    const aligned = (view) => address(ptr(view), view.BYTES_PER_ELEMENT);
    const kernel = library(files[0], windowsKernelABI);
    const system = new Uint16Array(32768);
    const length = kernel.GetSystemDirectoryW(aligned(system), system.length);
    if (!Number.isInteger(length) || length <= 0 || length >= system.length || system[length] !== 0 || String.fromCharCode(...system.subarray(0, length)).toLowerCase() !== directory.toLowerCase()) unavailable();
    const contract = Buffer.from("api-ms-win-core-realtime-l1-1-1.dll\0", "utf16le");
    const handle = uint64(kernel.LoadLibraryExW(address(ptr(contract), 2), 0n, 2048));
    if (handle === 0n) unavailable();
    releases.push(() => {
      if (kernel.FreeLibrary(handle) === 0) unavailable();
    });
    const name = Buffer.from("QueryInterruptTimePrecise\0");
    const target = address(kernel.GetProcAddress(handle, address(ptr(name))));
    const wrapper = linkSymbols({ QueryInterruptTimePrecise: { ...interruptABI, ptr: target } });
    releases.push(() => wrapper.close());
    const nt = library(files[1], windowsNtABI);
    const info = new BigUint64Array(4), returned = new Uint32Array(1);
    const counter = new BigUint64Array(1), wall = new BigUint64Array(1);
    return createNativeClock({
      imageSHA256: image.imageSHA256,
      readBoot() {
        info.fill(0n);
        returned[0] = 0;
        const status = nt.NtQuerySystemInformation(90, aligned(info), 32, aligned(returned));
        return windowsBoot(new Uint8Array(info.buffer), returned[0], status);
      },
      readCounter() {
        counter[0] = 0n;
        wrapper.symbols.QueryInterruptTimePrecise(aligned(counter));
        return interruptNS(counter[0]);
      },
      readWall() {
        wall[0] = 0n;
        kernel.GetSystemTimePreciseAsFileTime(aligned(wall));
        return filetimeNS(wall[0]);
      },
      verify() {
        image.verify();
        verifyPaths();
        for (const file of held2) file.verify();
      },
      close: () => closeAll(releases)
    }, "windows-kernel");
  } catch {
    try {
      closeAll(releases);
    } catch {
    }
    unavailable();
  } finally {
    signal?.removeEventListener("abort", abort);
  }
}
var directory;
var init_windows_clock = __esm({
  "windows-clock.mjs"() {
    init_native_clock_image();
    init_native_clock_contract();
    directory = "C:\\Windows\\System32";
  }
});

// node_modules/universal-agent-plugins-opencode-events/observer-core.js
var id = (x) => typeof x === "string" && x.length > 0 && !/[\u0000-\u001f]/u.test(x) && new TextEncoder().encode(x).length <= 256;
var timestamp = (x) => Number.isSafeInteger(x) && x > 0;
var canonicalID = (generation, session, turn, kind, nativeID) => JSON.stringify([generation, session, turn, kind, nativeID]);
function createCore(options) {
  const now = options.clock ? () => options.clock.now() : () => performance.now();
  const clockID = options.clock?.id ?? "local-performance";
  if (!id(clockID) || !Number.isFinite(now())) throw new TypeError("valid monotonic clock required");
  const diag = (reason) => {
    try {
      options.onDiagnostic?.(reason);
    } catch {
    }
  };
  const jobs = /* @__PURE__ */ new Set(), lookups = /* @__PURE__ */ new Set(), tails = /* @__PURE__ */ new Map(), emitWaiters = /* @__PURE__ */ new Set();
  let generation = 0, emitting = 0;
  const limit = (x, max) => Number.isInteger(x) ? Math.max(1, Math.min(max, x)) : max;
  const jobLimit = limit(options.maxJobs, 256), lookupLimit = limit(options.maxConcurrentLookups, 16);
  const timeout = limit(options.lookupTimeoutMs, 2e3);
  function invalidate() {
    generation++;
    for (const job of jobs) job.controller.abort();
    for (const controller of lookups) controller.abort();
    for (const wake of emitWaiters) wake();
  }
  function submit(sid, relevant, work, born = now()) {
    if (jobs.size >= jobLimit) {
      diag("job_capacity");
      return;
    }
    const controller = new AbortController(), connection = generation;
    const valid = () => !controller.signal.aborted && connection === generation && now() - born <= 3e4 && relevant();
    const job = { controller, valid };
    jobs.add(job);
    const timer = setTimeout(() => {
      diag("job_expired");
      controller.abort();
    }, Math.max(0, 3e4 - (now() - born)));
    const before = tails.get(sid) ?? Promise.resolve();
    const after = before.then(async () => {
      if (valid()) await work({ signal: controller.signal, isCurrent: valid, metadataDeadline: now() + timeout, ingressMonotonicMs: born, clockID });
    }).catch(() => diag("job_failed")).finally(() => {
      clearTimeout(timer);
      jobs.delete(job);
      if (tails.get(sid) === after) tails.delete(sid);
    });
    tails.set(sid, after);
    return after;
  }
  async function lookup(call, handoff) {
    if (!handoff.isCurrent()) return;
    if (lookups.size >= lookupLimit) {
      diag("lookup_capacity");
      return;
    }
    const remaining = Math.min(timeout, (handoff.metadataDeadline ?? now() + timeout) - now());
    if (remaining <= 0) {
      diag("lookup_timeout");
      return;
    }
    const deadline = now() + remaining;
    const controller = new AbortController();
    lookups.add(controller);
    const abort = () => controller.abort();
    handoff.signal.addEventListener("abort", abort, { once: true });
    let timer, cancel;
    const request = Promise.resolve().then(() => {
      if (controller.signal.aborted) return;
      if (now() > deadline) {
        diag("lookup_timeout");
        controller.abort();
        return;
      }
      return call(controller.signal);
    });
    request.then(() => lookups.delete(controller), () => lookups.delete(controller));
    try {
      const result2 = await Promise.race([request, new Promise((resolve) => {
        cancel = () => resolve(void 0);
        controller.signal.addEventListener("abort", cancel, { once: true });
        timer = setTimeout(() => {
          diag("lookup_timeout");
          controller.abort();
        }, remaining);
        if (controller.signal.aborted) cancel();
      })]);
      if (now() > deadline && !controller.signal.aborted) {
        diag("lookup_timeout");
        controller.abort();
      }
      return handoff.isCurrent() && !controller.signal.aborted ? result2 : void 0;
    } catch {
      diag("lookup_failed");
    } finally {
      clearTimeout(timer);
      controller.signal.removeEventListener("abort", cancel);
      handoff.signal.removeEventListener("abort", abort);
    }
  }
  async function emit(fact, handoff) {
    const deadline = handoff.metadataDeadline ?? now() + timeout;
    const current = () => handoff.isCurrent() && now() < deadline;
    while (emitting >= 4 && current()) {
      await new Promise((resolve) => {
        let timer;
        const wake = () => {
          clearTimeout(timer);
          emitWaiters.delete(wake);
          handoff.signal.removeEventListener("abort", wake);
          resolve();
        };
        emitWaiters.add(wake);
        handoff.signal.addEventListener("abort", wake, { once: true });
        timer = setTimeout(wake, Math.max(0, deadline - now()));
        if (handoff.signal.aborted) wake();
      });
    }
    if (!current()) return false;
    if (fact.provenance) Object.freeze(fact.provenance);
    Object.freeze(fact);
    if (new TextEncoder().encode(JSON.stringify(fact)).length > 4096) {
      diag("frame_capacity");
      return false;
    }
    emitting++;
    let preparation, preparationController;
    const abortPreparation = () => preparationController?.abort();
    handoff.signal.addEventListener("abort", abortPreparation, { once: true });
    try {
      if (options.beforeEmit) {
        const ready = await lookup((signal) => {
          preparationController = new AbortController();
          const abort = () => preparationController.abort();
          signal.addEventListener("abort", abort, { once: true });
          if (signal.aborted || handoff.signal.aborted) abort();
          const projected = Object.freeze({
            signal: preparationController.signal,
            ingressMonotonicMs: handoff.ingressMonotonicMs,
            clockID: handoff.clockID,
            metadataDeadline: deadline,
            isCurrent: () => current() && !preparationController.signal.aborted
          });
          preparation = Promise.resolve(projected.isCurrent() ? options.beforeEmit(fact, projected) : false);
          preparation.then(
            () => signal.removeEventListener("abort", abort),
            () => signal.removeEventListener("abort", abort)
          );
          return preparation;
        }, { ...handoff, metadataDeadline: deadline });
        if (ready !== true || !current()) return false;
      }
      if (handoff.revalidate && !await handoff.revalidate()) return false;
      if (!current()) return false;
      await options.emit(fact, handoff);
      return true;
    } catch {
      diag("observed event callback failed");
      return false;
    } finally {
      abortPreparation();
      try {
        await preparation;
      } catch {
      }
      handoff.signal.removeEventListener("abort", abortPreparation);
      emitting--;
      for (const wake of emitWaiters) wake();
    }
  }
  return {
    now,
    clockID,
    diag,
    submit,
    lookup,
    emit,
    invalidate,
    recheck() {
      for (const job of jobs) if (!job.valid()) job.controller.abort();
    },
    drain: () => Promise.all([...tails.values()]),
    busy: () => jobs.size > 0
  };
}

// node_modules/universal-agent-plugins-opencode-events/v1.js
var object = (x) => x !== null && typeof x === "object" && !Array.isArray(x);
var id2 = (x) => typeof x === "string" && x.length > 0 && !/[\u0000-\u001f]/u.test(x) && new TextEncoder().encode(x).length <= 256;
var typeOK = (x) => typeof x === "string" && /^[a-z][a-z0-9.-]{0,79}$/.test(x);
var nativeSessionID = (event) => ["session.updated", "session.deleted"].includes(event?.type) ? event?.properties?.info?.id ?? event?.properties?.sessionID : event?.properties?.sessionID ?? event?.properties?.info?.sessionID;
function createObserver(options) {
  if (typeof options?.emit !== "function" || typeof options.client?.session?.messages !== "function" || typeof options.client?.session?.get !== "function") {
    throw new TypeError("emit, messages and get required");
  }
  const limit = Math.max(2, Math.min(100, Number.isInteger(options.messageLimit) ? options.messageLimit : 30));
  const maxSessions = Math.max(1, Math.min(512, Number.isInteger(options.dedupLimit) ? options.dedupLimit : 512));
  const timeoutMs = Math.max(100, Math.min(2e3, Number.isInteger(options.lookupTimeoutMs) ? options.lookupTimeoutMs : 2e3));
  const sessions = /* @__PURE__ */ new Map(), unknownSeen = /* @__PURE__ */ new Set();
  let activeJobs = 0, disposed = false;
  const lifetime = new AbortController(), handoffs = /* @__PURE__ */ new Set(), core = createCore({
    ...options,
    lookupTimeoutMs: timeoutMs,
    onDiagnostic: (reason) => options.onDiagnostic?.(reason === "lookup_capacity" ? "messages lookup capacity exceeded" : ["lookup_timeout", "lookup_failed"].includes(reason) ? "lookup failed or timed out" : reason)
  }), jobsBySession = /* @__PURE__ */ new Map();
  const now = core.now;
  const strict = typeof options.runtimeEligibility === "function";
  let runtime = "supported";
  if (strict) {
    try {
      runtime = options.runtimeEligibility();
    } catch {
      runtime = "unverified";
    }
  }
  const diag = (reason) => {
    try {
      options.onDiagnostic?.(reason);
    } catch {
    }
  };
  const state = (sid) => {
    if (!sessions.has(sid)) {
      const fresh = {
        user: "",
        userCreated: void 0,
        seenUsers: /* @__PURE__ */ new Set(),
        assistant: "",
        failedAssistant: "",
        overflowPending: false,
        idleObserved: false,
        retry: false,
        retryAssistant: "",
        cancelled: false,
        questions: /* @__PURE__ */ new Set(),
        permissions: /* @__PURE__ */ new Set(),
        requestBindings: /* @__PURE__ */ new Map(),
        resolved: /* @__PURE__ */ new Set(),
        admitted: /* @__PURE__ */ new Set(),
        turnEpoch: 0,
        revision: 0,
        errorPending: 0,
        rootSession: void 0,
        activeAssistant: "",
        activeCompleted: false,
        ambiguousAssistant: false,
        sourceError: void 0,
        compaction: false,
        activeCreated: void 0,
        compactionFloor: void 0
      };
      if (strict && sessions.size >= maxSessions) {
        for (const [key, value] of sessions) {
          if (!jobsBySession.has(key) && (value.cancelled || value.admitted.has("idle") || value.admitted.has("error"))) {
            sessions.delete(key);
            break;
          }
        }
        if (sessions.size >= maxSessions) {
          diag("session_capacity");
          return { ...fresh, cancelled: true };
        }
      }
      sessions.set(sid, fresh);
      if (sessions.size > maxSessions) sessions.delete(sessions.keys().next().value);
    }
    return sessions.get(sid);
  };
  async function emit(fact, key, session, nativeTime, timeBasis, revalidate, born = now(), nativeMessageID) {
    if (disposed) return;
    if (strict && (!timestamp(nativeTime) || fact.rootSession !== true || !session?.scopeMatched)) {
      core.diag("native_provenance_unverified");
      return;
    }
    const controller = new AbortController();
    const epoch = session?.turnEpoch, revision = session?.revision;
    const requestSet = fact.kind === "question_asked" ? session?.questions : session?.permissions;
    const relevant = () => !disposed && !controller.signal.aborted && now() - born <= 3e4 && (!session || sessions.get(fact.sessionID) === session && session.turnEpoch === epoch && (fact.requestID ? requestSet.has(fact.requestID) && !session.cancelled && !session.compaction && !session.ambiguousAssistant && !session.sourceError : session.revision === revision));
    if (!relevant()) return;
    if (strict && timestamp(nativeTime)) fact.provenance = {
      generation: "v1",
      observationID: canonicalID("v1", fact.sessionID, fact.turnID, fact.kind, fact.requestID ?? fact.messageID ?? nativeMessageID),
      nativeTime,
      timeBasis,
      ...nativeMessageID ? { nativeMessageID } : {}
    };
    const handoff = { signal: controller.signal, isCurrent: relevant, revalidate, ingressMonotonicMs: born, clockID: core.clockID, metadataDeadline: born + timeoutMs };
    const timer = setTimeout(() => controller.abort(), Math.max(0, 3e4 - (now() - born)));
    const owned = { controller, relevant };
    handoffs.add(owned);
    const admitted = session ? session.admitted : unknownSeen;
    if (admitted.has(key)) {
      clearTimeout(timer);
      handoffs.delete(owned);
      return;
    }
    admitted.add(key);
    if (!session && admitted.size > maxSessions) admitted.delete(admitted.values().next().value);
    try {
      await core.emit({ version: 1, ...fact }, handoff);
    } catch {
      diag("observed event callback failed");
    } finally {
      clearTimeout(timer);
      handoffs.delete(owned);
    }
  }
  async function boundedLookup(call, deadline = now() + timeoutMs) {
    return core.lookup(call, {
      signal: lifetime.signal,
      isCurrent: () => !disposed,
      metadataDeadline: deadline
    });
  }
  async function rootStatus(sid, deadline) {
    const s = state(sid);
    if (!strict && typeof s.rootSession === "boolean") return s.rootSession;
    const result2 = await boundedLookup((signal) => options.client.session.get({ path: { id: sid }, signal }), deadline);
    const info = result2?.data ?? result2;
    if (!object(info) || info.id !== sid || info.parentID !== void 0 && !id2(info.parentID)) {
      diag("invalid session ancestry");
      return;
    }
    s.rootSession = info.parentID === void 0;
    s.scopeMatched = typeof options.location === "string" && info.directory === options.location;
    return s.rootSession;
  }
  async function latest(sid, deadline) {
    const result2 = await boundedLookup((signal) => options.client.session.messages({
      path: { id: sid },
      query: { limit },
      signal
    }), deadline);
    const rows = Array.isArray(result2) ? result2 : result2?.data;
    if (!Array.isArray(rows) || rows.length > limit) {
      diag("invalid messages lookup");
      return;
    }
    const messages = rows.map((x) => x?.info ?? x);
    if (!messages.every((m) => object(m) && id2(m.id) && m.sessionID === sid && ["user", "assistant"].includes(m.role))) {
      diag("invalid or mismatched messages");
      return;
    }
    return messages;
  }
  function currentTurn(messages, uid) {
    const lastUser = messages.findLast((m) => m.role === "user");
    if (lastUser) return lastUser.id === uid;
    return messages.at(-1)?.role === "assistant" && messages.at(-1).parentID === uid;
  }
  async function idle(sid, born, deadline) {
    const s = state(sid), revision = s.revision, uid = s.user;
    s.idleObserved = true;
    const failed = Boolean(s.failedAssistant || s.overflowPending);
    if (activeJobs > 256 || !uid || s.compaction || s.ambiguousAssistant || !s.assistant && !failed || s.admitted.has("idle") || s.admitted.has("error") || s.retry && !failed || s.cancelled || s.errorPending || s.questions.size || s.permissions.size) return;
    const messages = await latest(sid, deadline);
    if (!messages || sessions.get(sid) !== s || revision !== s.revision || s.user !== uid || s.retry && !failed || s.cancelled || s.errorPending || s.questions.size || s.permissions.size) return;
    if (!currentTurn(messages, uid)) return;
    const answer = messages.at(-1);
    if (strict && answer?.path?.cwd !== options.location) {
      diag("scope_unverified");
      return;
    }
    if (s.failedAssistant || s.overflowPending) {
      if (strict && !s.sourceError) return;
      if (!answer || answer.role !== "assistant" || answer.parentID !== uid || s.failedAssistant && answer.id !== s.failedAssistant || strict && !timestamp(answer.time?.completed) || !object(answer.error) || s.sourceError && (answer.id !== s.sourceError.messageID || answer.error.name !== s.sourceError.name) || s.compaction || /abort|cancel/i.test(String(answer.error.name ?? ""))) return;
      if (s.overflowPending && !s.failedAssistant && answer.error.name !== "ContextOverflowError") return;
      const rootSession2 = await rootStatus(sid, deadline);
      if (rootSession2 === void 0 || sessions.get(sid) !== s || revision !== s.revision || s.user !== uid || s.cancelled) return;
      s.cancelled = true;
      s.revision++;
      await emit({ kind: "terminal_error", sessionID: sid, turnID: uid, rootSession: rootSession2 }, "error", s, answer.time?.created, "assistant_created_lower_bound", void 0, born, answer.id);
      return;
    }
    if (!answer || answer.role !== "assistant" || answer.id !== s.assistant || answer.parentID !== uid || answer.finish !== "stop" || !Number.isFinite(answer.time?.completed) || answer.error != null) return;
    const rootSession = await rootStatus(sid, deadline);
    if (rootSession === void 0 || sessions.get(sid) !== s || revision !== s.revision || s.user !== uid || s.cancelled || s.errorPending) return;
    await emit({ kind: "turn_idle_verified", sessionID: sid, turnID: uid, messageID: answer.id, rootSession }, "idle", s, answer.time?.completed, "assistant_completed", void 0, born);
  }
  async function observe(event, born, deadline) {
    if (!object(event) || !typeOK(event.type) || !object(event.properties)) {
      diag("invalid native event");
      return;
    }
    const { type, properties: p } = event;
    if (type === "message.updated") {
      const m = p.info;
      if (!object(m) || !id2(m.id) || !id2(m.sessionID) || !["user", "assistant"].includes(m.role)) {
        diag("invalid message.updated");
        return;
      }
      const s = state(m.sessionID);
      if (m.role === "user" && m.id !== s.user) {
        const created = m.time?.created;
        if (s.seenUsers.has(m.id) || Number.isFinite(created) && Number.isFinite(s.userCreated) && created < s.userCreated) return;
        s.seenUsers.add(m.id);
        if (s.seenUsers.size > 512) s.seenUsers.delete(s.seenUsers.values().next().value);
        s.userCreated = Number.isFinite(created) ? created : void 0;
        s.user = m.id;
        s.assistant = "";
        s.retry = false;
        s.retryAssistant = "";
        s.cancelled = false;
        s.failedAssistant = "";
        s.overflowPending = false;
        s.idleObserved = false;
        s.questions.clear();
        s.permissions.clear();
        s.requestBindings.clear();
        s.resolved.clear();
        s.admitted.clear();
        s.activeAssistant = "";
        s.activeCreated = void 0;
        s.activeCompleted = false;
        s.ambiguousAssistant = false;
        s.sourceError = void 0;
        s.compaction = false;
        s.turnEpoch++;
        s.revision++;
      }
      if (m.role === "assistant" && m.parentID === s.user && m.summary !== true) {
        if (strict && s.activeAssistant && s.activeAssistant !== m.id) {
          for (const requestID of [...s.questions, ...s.permissions]) s.resolved.add(requestID);
          s.questions.clear();
          s.permissions.clear();
          s.revision++;
        }
        if (strict && s.activeAssistant && s.activeAssistant !== m.id && !s.activeCompleted) s.ambiguousAssistant = true;
        if (s.compaction && timestamp(m.time?.created) && timestamp(s.compactionFloor) && m.time.created > s.compactionFloor && m.id !== s.activeAssistant) {
          s.compaction = false;
          s.sourceError = void 0;
          s.ambiguousAssistant = false;
        }
        s.activeAssistant = m.id;
        s.activeCreated = m.time?.created;
        s.activeCompleted = Number.isFinite(m.time?.completed);
      }
      if (strict && m.role === "assistant" && m.summary === true && m.parentID === s.user) {
        s.compactionFloor = m.time?.created;
        s.compaction = true;
        s.questions.clear();
        s.permissions.clear();
        s.revision++;
      }
      if (m.role === "assistant" && (!strict || m.summary !== true) && m.parentID === s.user && m.finish === "stop" && Number.isFinite(m.time?.completed) && m.error == null && m.id !== s.retryAssistant) {
        s.assistant = m.id;
        s.failedAssistant = "";
        s.overflowPending = false;
        s.retry = false;
        s.revision++;
      }
      if (m.role === "assistant" && (!strict || m.summary !== true) && m.parentID === s.user && m.error != null) {
        s.assistant = "";
        s.failedAssistant = m.id;
        s.revision++;
        s.questions.clear();
        s.permissions.clear();
        if (strict && s.idleObserved && !s.errorPending) await idle(m.sessionID, born, deadline);
      }
      return;
    }
    if (type === "session.status") {
      if (!id2(p.sessionID) || !object(p.status) || !["busy", "idle", "retry"].includes(p.status.type)) {
        diag("invalid session.status");
        return;
      }
      if (p.status.type === "busy") {
        const s = state(p.sessionID);
        s.idleObserved = false;
        s.revision++;
      }
      if (p.status.type === "retry") {
        const s = state(p.sessionID);
        s.retryAssistant = s.assistant;
        s.assistant = "";
        s.failedAssistant = "";
        s.overflowPending = false;
        s.sourceError = void 0;
        s.idleObserved = false;
        s.retry = true;
        for (const requestID of [...s.questions, ...s.permissions]) s.resolved.add(requestID);
        s.questions.clear();
        s.permissions.clear();
        s.revision++;
      }
      if (p.status.type === "idle") await idle(p.sessionID, born, deadline);
      return;
    }
    if (type === "session.idle") {
      if (!id2(p.sessionID)) {
        diag("invalid session.idle");
        return;
      }
      await idle(p.sessionID, born, deadline);
      return;
    }
    if (type === "question.asked" || type === "permission.asked") {
      const requestMessageID = p.messageID ?? p.tool?.messageID;
      if (!id2(p.sessionID) || !id2(p.id) || requestMessageID !== void 0 && !id2(requestMessageID)) {
        diag("invalid request");
        return;
      }
      if (type === "question.asked" && (!Array.isArray(p.questions) || p.questions.length === 0)) {
        diag("invalid question shape");
        return;
      }
      if (type === "permission.asked" && (typeof p.permission !== "string" || !p.permission)) {
        diag("invalid permission shape");
        return;
      }
      const s = state(p.sessionID), uid = s.user, epoch = s.turnEpoch;
      if (!uid || strict && (s.cancelled || s.compaction || s.sourceError || s.admitted.has("idle") || s.admitted.has("error")) || s.resolved.has(p.id)) {
        diag("unmatched or resolved request");
        return;
      }
      const pending = type === "question.asked" ? s.questions : s.permissions;
      if (s.questions.size + s.permissions.size + s.resolved.size >= 256 || strict && s.requestBindings.size >= 256) {
        s.cancelled = true;
        s.questions.clear();
        s.permissions.clear();
        s.revision++;
        diag("request_capacity");
        return;
      }
      const assistantAtIngress = s.activeAssistant, createdAtIngress = s.activeCreated;
      if (strict) {
        const binding = JSON.stringify([type, requestMessageID, p.tool?.callID, createdAtIngress]);
        if (s.requestBindings.has(p.id)) {
          if (s.requestBindings.get(p.id) !== binding) {
            s.cancelled = true;
            s.questions.clear();
            s.permissions.clear();
            s.revision++;
            diag("request_identity_ambiguous");
          }
          return;
        }
        s.requestBindings.set(p.id, binding);
      }
      pending.add(p.id);
      s.revision++;
      const messages = await latest(p.sessionID, deadline);
      if (sessions.get(p.sessionID) !== s || s.turnEpoch !== epoch || s.user !== uid || !pending.has(p.id) || s.resolved.has(p.id)) return;
      if (!messages || !currentTurn(messages, uid)) {
        pending.delete(p.id);
        diag("unmatched request turn");
        return;
      }
      if (requestMessageID && requestMessageID !== uid && !messages.some((m) => m.role === "assistant" && m.id === requestMessageID && m.parentID === uid)) {
        pending.delete(p.id);
        diag("unmatched request message");
        return;
      }
      const rootSession = await rootStatus(p.sessionID, deadline);
      if (rootSession === void 0 || sessions.get(p.sessionID) !== s || s.turnEpoch !== epoch || s.user !== uid || !pending.has(p.id) || s.resolved.has(p.id)) return;
      const nativeAssistant = messages.find((m) => m.role === "assistant" && m.id === requestMessageID && m.parentID === uid);
      if (strict && (nativeAssistant?.path?.cwd !== options.location || nativeAssistant.summary === true || s.ambiguousAssistant || messages.findLast((m) => m.role === "assistant" && m.summary !== true)?.id !== requestMessageID)) {
        diag("scope_unverified");
        return;
      }
      const authority = options.callbackAuthority === "qualified_native_sync";
      const nativeBound = authority && requestMessageID === assistantAtIngress && assistantAtIngress === s.activeAssistant && id2(p.tool?.callID) && timestamp(createdAtIngress) && nativeAssistant?.time?.created === createdAtIngress && nativeAssistant?.summary !== true && !s.compaction && !s.sourceError && !s.ambiguousAssistant;
      if (strict && !nativeBound) {
        diag("callback_attention_authority_unverified");
        return;
      }
      s.assistant = "";
      s.revision++;
      await emit(
        {
          kind: type === "question.asked" ? "question_asked" : "permission_asked",
          sessionID: p.sessionID,
          turnID: uid,
          requestID: p.id,
          rootSession
        },
        `${type}:${p.id}`,
        s,
        strict && nativeBound ? createdAtIngress : void 0,
        "assistant_created_lower_bound",
        void 0,
        born
      );
      return;
    }
    if (type === "question.replied" || type === "question.rejected" || type === "permission.replied") {
      if (!id2(p.sessionID) || !id2(p.requestID)) {
        diag("invalid resolution");
        return;
      }
      const s = state(p.sessionID);
      const pending = type.startsWith("question.") ? s.questions : s.permissions;
      const current = pending.delete(p.requestID);
      s.resolved.add(p.requestID);
      if (s.resolved.size > 256) {
        s.cancelled = true;
        s.questions.clear();
        s.permissions.clear();
        s.revision++;
        diag("request_capacity");
      }
      if (current) {
        s.assistant = "";
        s.revision++;
      }
      return;
    }
    if (strict && ["session.updated", "session.deleted"].includes(type) && id2(nativeSessionID(event))) {
      const sid = nativeSessionID(event), s = state(sid), info = p.info;
      if (type === "session.updated" && object(info) && info.id === sid && (p.sessionID === void 0 || p.sessionID === sid) && info.parentID === void 0 && typeof options.location === "string" && info.directory === options.location && object(info.time) && Number.isSafeInteger(info.time.created) && info.time.created >= 0 && Number.isSafeInteger(info.time.updated) && info.time.updated >= 0 && info.time.archived === void 0 && info.time.compacting === void 0 && info.revert === void 0) return;
      s.rootSession = void 0;
      s.scopeMatched = false;
      s.cancelled = true;
      s.questions.clear();
      s.permissions.clear();
      s.revision++;
      return;
    }
    if (strict && type === "session.compacted") {
      if (id2(p.sessionID)) {
        const s = state(p.sessionID);
        s.compaction = true;
        s.sourceError = void 0;
        s.questions.clear();
        s.permissions.clear();
        s.revision++;
      }
      return;
    }
    if (type === "session.error") {
      if (!id2(p.sessionID) || !object(p.error) || p.messageID !== void 0 && !id2(p.messageID)) {
        diag("invalid session.error");
        return;
      }
      const s = state(p.sessionID), uid = s.user, epoch = s.turnEpoch;
      if (!uid || s.admitted.has("idle")) return;
      if (!strict) {
        s.errorPending++;
        s.revision++;
        const messages2 = await latest(p.sessionID, deadline);
        s.errorPending--;
        if (!messages2 || sessions.get(p.sessionID) !== s || s.turnEpoch !== epoch || s.user !== uid || s.admitted.has("idle") || !currentTurn(messages2, uid)) return;
        if (p.messageID && !messages2.some((m) => m.role === "assistant" && m.id === p.messageID && m.parentID === uid)) {
          diag("unmatched error message");
          return;
        }
        const rootSession = await rootStatus(p.sessionID, deadline);
        if (rootSession === void 0 || sessions.get(p.sessionID) !== s || s.turnEpoch !== epoch || s.user !== uid || s.admitted.has("idle")) return;
        if (p.error.name === "ContextOverflowError") {
          s.overflowPending = true;
          s.revision++;
          if (s.idleObserved) await idle(p.sessionID, born, deadline);
          return;
        }
        s.cancelled = true;
        s.revision++;
        if (/abort|cancel/i.test(String(p.error.name ?? ""))) return;
        await emit({ kind: "terminal_error", sessionID: p.sessionID, turnID: uid, rootSession }, "error", s, void 0, void 0, void 0, born);
        return;
      }
      s.questions.clear();
      s.permissions.clear();
      s.revision++;
      if (p.messageID && p.messageID !== s.activeAssistant) {
        diag("unmatched error message");
        return;
      }
      if (/abort|cancel/i.test(String(p.error.name ?? ""))) {
        s.cancelled = true;
        s.questions.clear();
        s.permissions.clear();
        s.revision++;
        return;
      }
      const candidate = s.activeAssistant;
      if (!candidate || s.compaction || s.ambiguousAssistant) {
        diag("unmatched error message");
        return;
      }
      s.sourceError = { messageID: candidate, name: p.error.name };
      s.questions.clear();
      s.permissions.clear();
      s.errorPending++;
      s.revision++;
      const messages = await latest(p.sessionID, deadline);
      s.errorPending--;
      if (!messages || sessions.get(p.sessionID) !== s || s.turnEpoch !== epoch || s.user !== uid || s.admitted.has("idle") || !currentTurn(messages, uid)) return;
      if (p.error.name === "ContextOverflowError") s.overflowPending = true;
      if (s.idleObserved) await idle(p.sessionID, born, deadline);
      return;
    }
    await emit(
      { kind: "unknown", nativeType: type, ...id2(p.sessionID) ? { sessionID: p.sessionID } : {} },
      `unknown:${type}:${id2(p.sessionID) ? p.sessionID : ""}`,
      void 0,
      void 0,
      void 0,
      void 0,
      born
    );
  }
  return {
    observe(event) {
      if (disposed) return Promise.resolve();
      if (strict && runtime !== "supported") {
        diag(runtime === "unsupported" ? "runtime_unsupported" : "runtime_unverified");
        return Promise.resolve();
      }
      const relevant = ["session.idle", "session.error", "question.asked", "permission.asked"].includes(event?.type) || event?.type === "session.status" && event.properties?.status?.type === "idle" || event?.type === "message.updated" && event.properties?.info?.error != null;
      const control = ["message.updated", "question.replied", "question.rejected", "permission.replied", "session.compacted"].includes(event?.type) || strict && ["session.updated", "session.deleted"].includes(event?.type) || event?.type === "session.status" && event.properties?.status?.type !== "idle";
      const sid = nativeSessionID(event);
      if ((relevant || !control) && activeJobs >= 256) {
        if (id2(sid)) {
          const s = state(sid);
          s.cancelled = true;
          s.questions.clear();
          s.permissions.clear();
          s.revision++;
        }
        for (const owned of handoffs) if (!owned.relevant()) owned.controller.abort();
        diag("job_capacity");
        return Promise.resolve();
      }
      const counted = relevant || !control;
      if (counted) {
        activeJobs++;
        if (id2(sid)) jobsBySession.set(sid, (jobsBySession.get(sid) ?? 0) + 1);
      }
      const born = now();
      const work = observe(event, born, born + timeoutMs);
      for (const owned of handoffs) if (!owned.relevant()) owned.controller.abort();
      return work.finally(() => {
        if (counted) {
          activeJobs--;
          const remaining = (jobsBySession.get(sid) ?? 1) - 1;
          if (remaining) jobsBySession.set(sid, remaining);
          else jobsBySession.delete(sid);
        }
      });
    },
    dispose() {
      disposed = true;
      sessions.clear();
      lifetime.abort();
      core.invalidate();
      for (const owned of handoffs) owned.controller.abort();
    }
  };
}

// node_modules/universal-agent-plugins-opencode-events/v2-reducer.js
var object2 = (x) => x !== null && typeof x === "object" && !Array.isArray(x);
var id3 = (x) => typeof x === "string" && x.length > 0 && !/[\u0000-\u001f]/u.test(x) && new TextEncoder().encode(x).length <= 256;
var location = (x) => object2(x) && typeof x.directory === "string" && x.directory.length > 0 && (x.workspaceID === void 0 || id3(x.workspaceID));
var sameLocation = (a, b) => location(a) && location(b) && a.directory === b.directory && a.workspaceID === b.workspaceID;
var bounded = (value, fallback, min, max) => Math.max(min, Math.min(max, Number.isInteger(value) ? value : fallback));
var retainedAdd = (set, value, limit) => {
  set.add(value);
  if (set.size > limit) set.delete(set.values().next().value);
};
var supported = /* @__PURE__ */ new Set([
  "session.created",
  "session.moved",
  "session.deleted",
  "session.inbox.enqueued",
  "session.inbox.delivered",
  "session.inbox.cancelled",
  "session.inbox.delivery.changed",
  "session.execution.started",
  "session.execution.succeeded",
  "session.execution.failed",
  "session.execution.interrupted",
  "session.step.started",
  "session.step.ended",
  "session.step.failed",
  "session.retry.scheduled",
  "session.compaction.started",
  "session.compaction.ended",
  "session.compaction.failed",
  "form.created",
  "form.replied",
  "form.cancelled",
  "permission.asked",
  "permission.replied"
]);
function createV2Reducer(options, strict = false, fence2 = () => {
}) {
  if (!strict && (typeof options?.emit !== "function" || typeof options.client?.get !== "function" || typeof options.client?.context !== "function" || !location(options.location))) {
    throw new TypeError("emit, native get/context and location required");
  }
  const own = strict ? options.location : { directory: options.location.directory, workspaceID: options.location.workspaceID };
  const maxSessions = bounded(options.maxSessions, 512, 1, 512);
  const timeout = bounded(options.lookupTimeoutMs, 2e3, 100, 1e4);
  const sessions = /* @__PURE__ */ new Map(), eventIDs = /* @__PURE__ */ new Map();
  const core = createCore({ ...options, onDiagnostic(code) {
    diag(code);
    if (strict && ["job_capacity", "lookup_capacity", "lookup_timeout"].includes(code)) fence2(code);
  } });
  let checkpoint;
  const now = core.now;
  const diagnosticCodes = /* @__PURE__ */ new Set();
  let disposed = false, lifecycle = 0;
  const diag = (code) => {
    if (diagnosticCodes.has(code)) return;
    diagnosticCodes.add(code);
    try {
      options.onDiagnostic?.(code);
    } catch {
    }
  };
  function invalidate(s) {
    s.generation++;
    s.revision++;
    sessions.delete(s.sid);
    core.recheck();
  }
  const activeOwned = (s) => ["root", "pending", "new"].includes(s.ownership) && (s.admissions.size > 0 || s.started && !s.result && !s.interrupted && (!s.terminal || s.verifying.size > 0));
  function state(sid) {
    if (sessions.has(sid)) {
      const existing = sessions.get(sid);
      sessions.delete(sid);
      sessions.set(sid, existing);
      return existing;
    }
    if (sessions.size === maxSessions) {
      if (strict) {
        const ended = [...sessions.values()].find((s2) => s2.result || s2.interrupted || s2.blocked);
        if (ended && !core.busy()) invalidate(ended);
        else {
          fence2("session_capacity");
          return;
        }
      }
      const records = strict ? [] : [...sessions.values()];
      const victim = records.find((s2) => s2.ownership === "rejected" || s2.ownership === "child") ?? records.find((s2) => !activeOwned(s2) && !s2.verifying.size) ?? records[0];
      if (victim && activeOwned(victim)) diag("session_capacity");
      if (victim) invalidate(victim);
    }
    const s = {
      sid,
      generation: 0,
      ownership: "new",
      epoch: 0,
      revision: 0,
      started: false,
      user: "",
      assistant: "",
      final: "",
      retry: false,
      interrupted: false,
      compacting: false,
      compactedUser: "",
      blocked: false,
      admissionsOverflow: false,
      terminal: "",
      result: "",
      admissions: /* @__PURE__ */ new Map(),
      pending: /* @__PURE__ */ new Map(),
      resolved: /* @__PURE__ */ new Set(),
      runID: "",
      nativeTerminal: void 0,
      sequence: void 0,
      nativeScope: void 0,
      admitted: /* @__PURE__ */ new Set(),
      liveAssistants: /* @__PURE__ */ new Set(),
      attempted: /* @__PURE__ */ new Set(),
      verifying: /* @__PURE__ */ new Set()
    };
    sessions.set(sid, s);
    return s;
  }
  const ownershipToken = (s) => ({ s, generation: s.generation, lifecycle });
  const ownedToken = (token) => !disposed && lifecycle === token.lifecycle && sessions.get(token.s.sid) === token.s && token.s.generation === token.generation;
  const semanticToken = (s) => ({ ...ownershipToken(s), epoch: s.epoch, revision: s.revision });
  const current = (token) => ownedToken(token) && token.s.epoch === token.epoch && token.s.revision === token.revision;
  const liveWork = (s) => s.started && Boolean(s.user) && !s.result && !s.interrupted && !s.blocked;
  function lookup(call) {
    const controller = new AbortController(), epoch = lifecycle;
    return core.lookup(call, {
      signal: controller.signal,
      isCurrent: () => !disposed && epoch === lifecycle,
      metadataDeadline: now() + Math.min(timeout, 2e3)
    });
  }
  function ensureOwnership(s) {
    if (strict || s.ownership !== "new") return;
    s.ownership = "pending";
    const token = ownershipToken(s);
    void lookup((signal) => options.client.get({ sessionID: s.sid }, { signal })).then((info) => {
      if (!ownedToken(token)) return;
      if (info === void 0) {
        s.ownership = "unverified";
        diag("ownership_unverified");
        return;
      }
      if (!object2(info) || info.id !== s.sid || info.parentID !== void 0 && !id3(info.parentID) || !sameLocation(info.location, own)) {
        s.ownership = "rejected";
        diag("ownership_unverified");
        return;
      }
      s.ownership = info.parentID === void 0 ? "root" : "child";
      schedule(s);
    });
  }
  async function context(s, handoff) {
    const rows = await core.lookup((signal) => options.client.context({ sessionID: s.sid }, { signal }), handoff);
    if (!Array.isArray(rows) || rows.length > 4096) {
      diag("context_unverified");
      return;
    }
    const seen = /* @__PURE__ */ new Set(), metadata = [];
    for (const row of rows) {
      if (!object2(row) || !id3(row.id) || seen.has(row.id) || typeof row.type !== "string") {
        diag("context_unverified");
        return;
      }
      seen.add(row.id);
      metadata.push({
        id: row.id,
        type: row.type,
        finish: row.finish,
        completed: Number.isFinite(row.time?.completed),
        failed: row.error != null
      });
    }
    return metadata;
  }
  function associated(s, rows) {
    const lastUser = rows.findLast((row) => row.type === "user");
    return lastUser ? lastUser.id === s.user : s.compactedUser === s.user && Boolean(s.user);
  }
  async function emit(s, key, fact, handoff) {
    if (disposed || s.ownership !== "root" || s.admitted.has(key)) return;
    if (s.admitted.size >= 2048) {
      s.blocked = true;
      diag("admission_capacity");
      return;
    }
    s.admitted.add(key);
    await core.emit({ version: 1, sessionID: s.sid, turnID: s.user, rootSession: true, ...fact }, handoff);
  }
  function schedule(s) {
    if (disposed || !strict && s.ownership !== "root" || !liveWork(s)) return;
    const candidates = [...s.pending.values()].filter((candidate) => !s.admitted.has(`request:${candidate.kind}:${candidate.id}`));
    if (s.terminal && !s.result) candidates.push({ kind: s.terminal, id: "", messageID: s.final });
    for (const candidate of candidates) {
      if (candidate.kind === "success" && (!s.final || s.retry || s.compacting || s.pending.size || s.admissions.size)) continue;
      const flight = `${s.epoch}:${candidate.kind}:${candidate.id}`;
      if (s.verifying.has(flight)) continue;
      const attempt = `${s.epoch}:${s.user}:${candidate.kind}:${candidate.id}:${candidate.messageID ?? ""}`;
      if (s.attempted.has(attempt)) continue;
      if (s.attempted.size >= 2048) {
        s.blocked = true;
        diag("verification_capacity");
        return;
      }
      s.attempted.add(attempt);
      const token = semanticToken(s);
      s.verifying.add(flight);
      const final = s.nativeTerminal;
      const relevant = () => strict && final && ["success", "failure"].includes(candidate.kind) ? ownedToken(token) && s.epoch === token.epoch && !s.blocked && !s.interrupted && s.nativeTerminal === final : current(token);
      const work = core.submit(s.sid, relevant, async (handoff) => {
        if (strict) await verifyStrict(s, candidate, handoff, final);
        else await verify(s, candidate, token, handoff);
      }, candidate.ingress ?? s.terminalIngress ?? now());
      if (!work) {
        s.blocked = true;
        s.pending.clear();
      }
      void work?.finally(() => {
        s.verifying.delete(flight);
        if (!strict && ownedToken(token) && !current(token)) {
          s.attempted.delete(attempt);
          schedule(s);
        }
      });
    }
  }
  async function verify(s, candidate, token, handoff) {
    const rows = await context(s, handoff);
    if (!current(token)) return;
    if (!rows || !liveWork(s) || !associated(s, rows)) return;
    if (candidate.kind === "success") {
      if (s.terminal !== "success" || s.result || s.retry || s.compacting || s.pending.size || s.admissions.size || !s.final) return;
      const index = rows.findIndex((row) => row.id === s.final && row.type === "assistant");
      const answer = rows[index];
      if (!answer || answer.finish !== "stop" || !answer.completed || answer.failed || rows.slice(index + 1).some((row) => row.type !== "idle")) return;
      s.result = "success";
      await emit(s, `terminal:${s.epoch}`, { kind: "turn_idle_verified", messageID: s.final }, handoff);
    } else if (candidate.kind === "failure") {
      if (s.terminal !== "failure" || s.result) return;
      s.result = "failure";
      await emit(s, `terminal:${s.epoch}`, { kind: "terminal_error" }, handoff);
    } else {
      if (s.pending.get(candidate.id) !== candidate || s.resolved.has(candidate.id)) return;
      if (candidate.messageID && (!s.liveAssistants.has(candidate.messageID) || !rows.some((row) => row.type === "assistant" && row.id === candidate.messageID))) return;
      await emit(s, `request:${candidate.kind}:${candidate.id}`, { kind: candidate.kind, requestID: candidate.id }, handoff);
    }
  }
  function matches(proof, run) {
    return object2(proof) && proof.sessionID === run.sessionID && proof.turnID === run.turnID && proof.location === own && proof.rootSession === true;
  }
  async function verifyStrict(s, candidate, handoff, final) {
    const run = Object.freeze({
      sessionID: s.sid,
      turnID: s.runID,
      userID: s.user,
      location: own,
      observedLocation: s.nativeScope,
      terminalEventID: final?.id,
      terminalCreated: final?.created,
      terminalSequence: final?.sequence
    });
    const scope2 = await core.lookup((signal) => options.native.session(run, signal), handoff);
    if (!handoff.isCurrent() || !matches(scope2, run)) {
      diag("root_execution_unverified");
      return;
    }
    s.ownership = "root";
    const terminal = candidate.kind === "success" || candidate.kind === "failure";
    if (terminal) {
      if (s.compacting || !candidate.messageID || !final) {
        diag("terminal_assistant_unverified");
        return;
      }
      const answer = await core.lookup((signal) => options.native.assistant({ ...run, messageID: candidate.messageID }, signal), handoff);
      if (!handoff.isCurrent() || !matches(answer, run) || answer.messageID !== candidate.messageID || answer.role !== "assistant" || answer.summary !== false || answer.final !== true || answer.outcome !== (candidate.kind === "failure" ? "error" : "success")) {
        diag("terminal_assistant_unverified");
        return;
      }
    } else if (candidate.kind === "question_asked") {
      const source = await core.lookup((signal) => options.native.questionSource({
        ...run,
        requestID: candidate.id,
        messageID: candidate.messageID,
        callID: candidate.callID
      }, signal), handoff);
      if (!handoff.isCurrent() || !matches(source, run) || source.requestID !== candidate.id || source.messageID !== candidate.messageID || source.callID !== candidate.callID || source.role !== "assistant" || source.tool !== "question" || source.summary !== false) {
        diag("question_source_unverified");
        return;
      }
    }
    const kind = terminal ? candidate.kind === "success" ? "turn_idle_verified" : "terminal_error" : candidate.kind;
    const envelope = terminal ? final : candidate;
    let snapshot;
    const takeSnapshot = async () => {
      if (kind === "question_asked") return checkpoint.verify(handoff);
      const pending = await core.lookup((signal) => options.native.currentPermission({ ...run, requestID: candidate.id }, signal), handoff);
      return handoff.isCurrent() && matches(pending, run) && pending.pending === true && pending.requestID === candidate.id && pending.messageID === candidate.messageID && pending.callID === candidate.callID;
    };
    const revalidate = terminal ? void 0 : async () => {
      snapshot ??= takeSnapshot();
      return Boolean(await snapshot && handoff.isCurrent());
    };
    const fact = {
      kind,
      ...kind === "turn_idle_verified" ? { messageID: candidate.messageID } : {},
      ...!terminal ? { requestID: candidate.id } : {},
      provenance: {
        generation: "v2",
        observationID: canonicalID("v2", s.sid, s.runID, kind, terminal ? candidate.messageID : candidate.id),
        nativeEventID: envelope.eventID ?? envelope.id,
        nativeTime: envelope.created,
        timeBasis: "envelope_created"
      }
    };
    if (terminal) s.result = candidate.kind;
    await emit(s, terminal ? `terminal:${s.epoch}` : `request:${kind}:${candidate.id}`, fact, { ...handoff, revalidate });
  }
  function resetFinal(s) {
    s.final = "";
    s.terminal = "";
  }
  function block(s) {
    s.blocked = true;
    resetFinal(s);
    s.revision++;
    diag("metadata_capacity");
  }
  function suppress(sid, code) {
    const s = sessions.get(sid);
    if (s) {
      s.blocked = true;
      s.pending.clear();
      s.revision++;
    }
    diag(code);
  }
  function observe(event) {
    const ingress = now();
    try {
      ingest(event, ingress);
    } finally {
      if (strict) core.recheck();
    }
  }
  function ingest(event, ingress) {
    if (disposed) return;
    if (!object2(event) || typeof event.type !== "string" || !object2(event.data)) {
      if (strict && supported.has(event?.type)) fence2("invalid_native_envelope");
      else diag("invalid_event");
      return;
    }
    const type = event.type;
    let p = event.data, correlation;
    if (strict && checkpoint?.consume(event)) return;
    if (type === "location.shutdown") {
      if (strict) {
        let scope2;
        try {
          scope2 = options.native.correlate(event)?.location;
        } catch {
        }
        if (scope2 === void 0 || scope2 === own) fence2("location_shutdown");
        return;
      }
      if (sameLocation(event.location, own)) dispose();
      return;
    }
    if (!supported.has(type) && !(strict && type.startsWith("session."))) return;
    if (strict) {
      try {
        correlation = options.native.correlate(event);
      } catch {
        if (id3(p.sessionID)) suppress(p.sessionID, "native_mapping_failed");
        else fence2("native_mapping_failed");
        return;
      }
      if (!correlation) return;
      const sid2 = correlation.sessionID;
      if (sid2 === "global") return;
      if (!id3(sid2)) {
        fence2("native_correlation_unverified");
        return;
      }
      if (!id3(event.id) || !timestamp(event.created)) {
        suppress(sid2, "invalid_native_envelope");
        return;
      }
      p = { ...p, sessionID: sid2 };
      if (correlation.messageID) p.assistantMessageID = correlation.messageID;
    }
    const sid = strict ? correlation.sessionID : type === "form.created" ? p.form?.sessionID : p.sessionID;
    if (!id3(sid) || sid === "global") {
      diag("invalid_session");
      return;
    }
    if (type === "session.moved" || type === "session.deleted") {
      const tracked = sessions.get(sid);
      if (tracked) {
        if (strict) {
          tracked.blocked = true;
          tracked.pending.clear();
          tracked.nativeScope = correlation.location;
          tracked.revision++;
        } else invalidate(tracked);
      }
      return;
    }
    if (!strict && event.location !== void 0 && !sameLocation(event.location, own)) return;
    if (strict && correlation.location !== void 0 && correlation.location !== own) {
      const tracked = sessions.get(sid);
      if (tracked) {
        tracked.blocked = true;
        tracked.pending.clear();
        tracked.revision++;
      }
      diag("scope_mismatch");
      return;
    }
    if (event.id !== void 0) {
      if (!id3(event.id)) {
        diag("invalid_event");
        return;
      }
      if (!strict && eventIDs.has(event.id)) return;
      if (!strict) {
        eventIDs.set(event.id, true);
        if (eventIDs.size > 2048) eventIDs.delete(eventIDs.keys().next().value);
      }
    }
    const s = state(sid);
    if (!s) return;
    if (s.ownership === "rejected" || s.ownership === "child") return;
    if (strict) {
      if (type.startsWith("session.step.") && !id3(correlation.messageID) && correlation.compaction !== true) {
        suppress(sid, "native_correlation_unverified");
        return;
      }
      const badCorrelation = ["location", "messageID", "requestID", "callID"].some((key) => correlation[key] !== void 0 && !id3(correlation[key]));
      const seq = correlation.sequence;
      if (badCorrelation || seq !== void 0 && (!object2(seq) || seq.aggregate !== sid || !Number.isSafeInteger(seq.seq) || seq.seq < 0 || seq.version !== void 0 && (!Number.isSafeInteger(seq.version) || seq.version < 0)) || correlation.compaction !== void 0 && typeof correlation.compaction !== "boolean") {
        s.blocked = true;
        s.pending.clear();
        s.revision++;
        diag("native_correlation_unverified");
        return;
      }
      const nativeSequence = seq ? { aggregate: seq.aggregate, seq: seq.seq, ...seq.version !== void 0 ? { version: seq.version } : {} } : void 0;
      if (s.nativeScope !== void 0 && correlation.location === void 0 && s.nativeScope !== own) {
        s.blocked = true;
        diag("scope_mismatch");
        return;
      }
      const fingerprint = JSON.stringify([
        type,
        event.created,
        sid,
        correlation.location,
        correlation.messageID,
        correlation.requestID,
        correlation.callID,
        correlation.compaction,
        nativeSequence,
        id3(p.inboxID) ? p.inboxID : void 0,
        id3(p.item?.type) ? p.item.type : void 0,
        p.item?.delivery === "queue",
        p.delivery === "queue",
        p.finish === "stop"
      ]);
      if (eventIDs.has(event.id)) {
        if (eventIDs.get(event.id) === fingerprint) return;
        const original = sessions.get(JSON.parse(eventIDs.get(event.id))[2]);
        if (original) {
          original.blocked = true;
          original.pending.clear();
          original.revision++;
        }
        s.blocked = true;
        s.pending.clear();
        s.revision++;
        diag("native_identity_contradiction");
        return;
      }
      eventIDs.set(event.id, fingerprint);
      if (eventIDs.size > 2048) eventIDs.delete(eventIDs.keys().next().value);
      if (s.nativeTerminal && ["session.inbox.enqueued", "session.inbox.delivered", "session.step.started"].includes(type)) {
        s.blocked = true;
        s.pending.clear();
        s.revision++;
      }
      if (type.startsWith("session.") && (!object2(seq) || seq.aggregate !== sid || !Number.isSafeInteger(seq.seq) || seq.seq < 0)) {
        s.blocked = true;
        s.pending.clear();
        s.revision++;
        diag("invalid_native_sequence");
        return;
      }
      if (seq) {
        if (s.sequence && seq.seq <= s.sequence.seq) {
          s.blocked = true;
          s.pending.clear();
          s.revision++;
          diag("native_sequence_contradiction");
          return;
        }
        s.sequence = {
          ...nativeSequence,
          id: event.id,
          type,
          created: event.created,
          messageID: correlation.messageID,
          compaction: correlation.compaction
        };
      }
      if (correlation.location !== void 0) s.nativeScope = correlation.location;
      if (["session.retry.scheduled", "session.compaction.started", "session.execution.interrupted"].includes(type) || correlation.compaction === true || type === "session.execution.started") {
        for (const key of s.pending.keys()) s.resolved.add(key);
        s.pending.clear();
      }
      if (correlation.compaction === true) s.compacting = true;
      if (s.resolved.size >= 256) {
        s.blocked = true;
        diag("request_capacity");
        return;
      }
    }
    let changed = true;
    if (type === "session.execution.started") {
      if (s.ownership === "unverified") s.ownership = "new";
      s.runID = strict ? event.id : "";
      s.nativeTerminal = void 0;
      s.epoch++;
      s.started = true;
      s.user = "";
      s.assistant = "";
      resetFinal(s);
      s.retry = false;
      s.interrupted = false;
      s.compacting = false;
      s.compactedUser = "";
      s.result = "";
      s.blocked = s.admissionsOverflow;
      s.admitted.clear();
      s.pending.clear();
      s.liveAssistants.clear();
      s.attempted.clear();
    } else if (type === "session.inbox.enqueued") {
      if (!id3(p.inboxID) || !object2(p.item) || !id3(p.item.type)) {
        diag("invalid_inbox");
        return;
      }
      if (s.admissions.size >= 64 && !s.admissions.has(p.inboxID)) {
        s.admissionsOverflow = true;
        block(s);
      } else s.admissions.set(p.inboxID, { type: p.item.type, delivery: p.item.delivery === "queue" ? "queue" : "steer" });
      resetFinal(s);
    } else if (type === "session.inbox.delivered") {
      if (!id3(p.inboxID)) {
        diag("invalid_inbox");
        return;
      }
      const item = s.admissions.get(p.inboxID);
      s.admissions.delete(p.inboxID);
      if (!item) {
        s.blocked = true;
        diag("unmatched_delivery");
      } else if (item.type === "user" && s.started) {
        s.user = p.inboxID;
        s.assistant = "";
        s.liveAssistants.clear();
        s.compactedUser = "";
        s.retry = false;
      }
      resetFinal(s);
    } else if (type === "session.inbox.cancelled") {
      if (id3(p.inboxID)) s.admissions.delete(p.inboxID);
    } else if (type === "session.inbox.delivery.changed") {
      const admission = s.admissions.get(p.inboxID);
      if (admission && ["steer", "queue"].includes(p.delivery)) admission.delivery = p.delivery;
    } else if (type === "session.step.started") {
      if (strict && correlation.compaction === true) {
        s.compacting = true;
        resetFinal(s);
        s.revision++;
        return;
      }
      if (!id3(p.assistantMessageID) || !liveWork(s)) {
        diag("unmatched_step");
        return;
      }
      if (strict) s.compacting = false;
      if (strict && s.assistant && s.assistant !== p.assistantMessageID) {
        for (const key of s.pending.keys()) s.resolved.add(key);
        s.pending.clear();
      }
      s.assistant = p.assistantMessageID;
      retainedAdd(s.liveAssistants, p.assistantMessageID, 64);
      s.retry = false;
      resetFinal(s);
    } else if (type === "session.step.ended") {
      if (strict && correlation.compaction === true) {
        s.compacting = true;
        resetFinal(s);
        s.revision++;
        return;
      }
      if (!id3(p.assistantMessageID) || p.assistantMessageID !== s.assistant || !liveWork(s)) return;
      s.final = (strict || p.finish === "stop") && !s.retry && !s.compacting ? p.assistantMessageID : "";
    } else if (type === "session.step.failed" || type === "session.retry.scheduled") {
      resetFinal(s);
      s.retry = type === "session.retry.scheduled";
      if (strict && type === "session.step.failed" && !s.compacting && p.assistantMessageID === s.assistant && id3(p.assistantMessageID)) s.final = p.assistantMessageID;
    } else if (type === "session.compaction.started") {
      s.compacting = true;
      resetFinal(s);
    } else if (type === "session.compaction.ended") {
      if (s.compacting && liveWork(s)) s.compactedUser = s.user;
      s.compacting = false;
      resetFinal(s);
    } else if (type === "session.compaction.failed") {
      s.compacting = false;
      resetFinal(s);
    } else if (type === "session.execution.interrupted") {
      s.interrupted = true;
      resetFinal(s);
      s.pending.clear();
    } else if (type === "session.execution.succeeded") {
      if (strict && s.nativeTerminal) {
        s.blocked = true;
        s.revision++;
        diag("native_terminal_contradiction");
        return;
      }
      s.terminal = "success";
    } else if (type === "session.execution.failed") {
      if (strict && s.nativeTerminal) {
        s.blocked = true;
        s.revision++;
        diag("native_terminal_contradiction");
        return;
      }
      if (!strict && !object2(p.error)) {
        diag("invalid_failure");
        return;
      }
      const tag = p.error?._tag ?? p.error?.name ?? p.error?.type;
      if (typeof tag === "string" && /abort|cancel|interrupt/i.test(tag)) {
        s.interrupted = true;
        resetFinal(s);
      } else s.terminal = "failure";
    } else if (type === "form.replied" || type === "form.cancelled" || type === "permission.replied") {
      const rid = strict ? correlation.requestID : type === "permission.replied" ? p.requestID : p.id;
      if (!id3(rid)) {
        diag("invalid_request");
        return;
      }
      retainedAdd(s.resolved, rid, strict ? 256 : 2048);
      s.pending.delete(rid);
      resetFinal(s);
    } else if (type === "form.created" || type === "permission.asked") {
      let rid, messageID, kind;
      if (strict) {
        rid = correlation.requestID;
        messageID = correlation.messageID;
        kind = type === "form.created" ? "question_asked" : "permission_asked";
        if (!id3(rid) || !id3(messageID) || !id3(correlation.callID) || messageID !== s.assistant || !s.liveAssistants.has(messageID)) {
          s.blocked = true;
          s.pending.clear();
          s.revision++;
          diag("request_identity_unverified");
          return;
        }
      } else if (type === "form.created") {
        const form = p.form;
        if (!object2(form) || form.metadata?.kind !== "question" || !id3(form.id) || !Array.isArray(form.fields) || form.fields.length === 0 || !id3(form.metadata?.tool?.messageID) || !id3(form.metadata?.tool?.id)) return;
        rid = form.id;
        messageID = form.metadata.tool.messageID;
        kind = "question_asked";
      } else {
        if (!id3(p.id) || typeof p.action !== "string" || !p.action || !Array.isArray(p.resources) || p.source !== void 0 && (!object2(p.source) || p.source.type !== "tool" || !id3(p.source.messageID) || !id3(p.source.id))) return;
        rid = p.id;
        messageID = p.source?.messageID;
        kind = "permission_asked";
      }
      if (strict && s.pending.has(rid)) {
        const previous = s.pending.get(rid);
        if (previous.eventID !== event.id || previous.created !== event.created || previous.messageID !== messageID || previous.callID !== correlation.callID) {
          suppress(sid, "request_identity_ambiguous");
        }
        return;
      }
      if (strict && type === "form.created" && (!checkpoint?.ready() || !options.native.questionSource)) {
        s.resolved.add(rid);
        diag(!checkpoint?.ready() ? "form_checkpoint_unready" : "question_source_unavailable");
        return;
      }
      if (strict && type === "permission.asked" && !options.native.currentPermission) {
        diag("permission_pending_authority_unavailable");
        return;
      }
      if (!liveWork(s) || s.resolved.has(rid) || s.pending.has(rid) || strict && (s.nativeTerminal || s.compacting || !id3(correlation.callID))) return;
      if (s.pending.size >= 64 || strict && s.pending.size + s.resolved.size >= 256) block(s);
      else s.pending.set(rid, {
        id: rid,
        messageID,
        kind,
        ingress,
        ...strict ? { callID: correlation.callID, eventID: event.id, created: event.created } : {}
      });
      resetFinal(s);
    } else changed = false;
    if (strict && ["session.execution.succeeded", "session.execution.failed"].includes(type)) {
      s.pending.clear();
      s.nativeTerminal = Object.freeze({ id: event.id, created: event.created, sequence: Object.freeze({ ...s.sequence }) });
      s.terminalIngress = ingress;
      if (s.compacting) {
        s.terminal = "";
        diag("compaction_terminal_suppressed");
      }
    }
    if (s.admissionsOverflow && ["session.execution.succeeded", "session.execution.failed", "session.execution.interrupted"].includes(type)) {
      s.admissionsOverflow = false;
      s.admissions.clear();
    }
    if (changed) s.revision++;
    ensureOwnership(s);
    schedule(s);
  }
  function dispose() {
    if (disposed) return;
    disposed = true;
    lifecycle++;
    core.invalidate();
    sessions.clear();
    eventIDs.clear();
  }
  return {
    observe,
    dispose,
    core,
    diag,
    setCheckpoint(value) {
      checkpoint = value;
    },
    invalidate() {
      lifecycle++;
      core.invalidate();
      checkpoint?.invalidate();
      for (const s of sessions.values()) {
        s.blocked = true;
        s.pending.clear();
        s.revision++;
      }
    },
    done: () => core.drain()
  };
}

// node_modules/universal-agent-plugins-opencode-events/checkpoint.js
import { randomBytes } from "node:crypto";
function createCheckpoint(port, core, fail2) {
  let current;
  function invalidate() {
    const state = current;
    if (!state) return;
    state.ready = false;
    for (const resolve of state.waiters.values()) resolve(false);
    state.waiters.clear();
    state.retired.clear();
  }
  function consume(envelope) {
    const state = current;
    if (!state?.registration || state.signal.aborted || envelope.type !== state.registration.type) return false;
    let nonce;
    try {
      nonce = state.registration.read(envelope);
    } catch {
      fail2("checkpoint_schema_unverified");
      return true;
    }
    if (nonce === void 0) {
      fail2("checkpoint_schema_unverified");
      return true;
    }
    if (state.retired.delete(nonce)) return true;
    if (typeof nonce !== "string" || !/^[a-f0-9]{32}$/.test(nonce) || !state.waiters.has(nonce)) {
      fail2("checkpoint_continuity_unverified");
      return true;
    }
    const resolve = state.waiters.get(nonce);
    state.waiters.delete(nonce);
    resolve(true);
    return true;
  }
  async function marker(state, handoff) {
    if (state !== current || !handoff.isCurrent() || !state.registration) return false;
    const result2 = await core.lookup(async (signal) => {
      const nonce = randomBytes(16).toString("hex");
      let finish, wasConsumed = false;
      const consumed = new Promise((resolve) => {
        finish = (value) => {
          if (value) wasConsumed = true;
          resolve(value);
        };
        state.waiters.set(nonce, finish);
      });
      const abort = () => finish(false);
      signal.addEventListener("abort", abort, { once: true });
      try {
        if (signal.aborted) return false;
        await state.registration.emit(nonce);
        return await consumed;
      } finally {
        state.waiters.delete(nonce);
        if (!wasConsumed && !state.signal.aborted && state === current) {
          if (state.retired.size >= 256) fail2("checkpoint_capacity");
          else state.retired.add(nonce);
        }
        signal.removeEventListener("abort", abort);
      }
    }, handoff);
    if (!handoff.isCurrent()) return false;
    if (!result2 || state !== current) {
      if (state === current && !state.signal.aborted) fail2("checkpoint_failed");
      return false;
    }
    return true;
  }
  async function open(signal) {
    invalidate();
    const state = { signal, ready: false, registration: void 0, waiters: /* @__PURE__ */ new Map(), retired: /* @__PURE__ */ new Set() };
    current = state;
    if (!port) {
      core.diag("form_checkpoint_unavailable");
      return;
    }
    const handoff = {
      signal,
      isCurrent: () => current === state && !signal.aborted,
      metadataDeadline: core.now() + 2e3
    };
    const registration = await core.lookup(async () => {
      const owned = await port.register(signal, randomBytes(16).toString("hex"));
      if (!handoff.isCurrent()) {
        await owned?.dispose?.();
        return;
      }
      state.registration = owned;
      return owned;
    }, handoff);
    if (!handoff.isCurrent()) return;
    if (!registration || typeof registration.emit !== "function" || typeof registration.read !== "function" || typeof registration.dispose !== "function" || !id(registration.type)) {
      fail2("checkpoint_registration_unverified");
      return;
    }
    if (await marker(state, handoff)) state.ready = true;
  }
  async function close() {
    invalidate();
    const state = current;
    current = void 0;
    if (state?.registration) {
      try {
        await state.registration.dispose();
      } catch {
        core.diag("checkpoint_dispose_failed");
      }
    }
  }
  return {
    open,
    close,
    consume,
    invalidate,
    ready: () => Boolean(current?.ready && !current.signal.aborted),
    async verify(handoff) {
      const state = current;
      return Boolean(state?.ready && await marker(state, handoff) && state.ready);
    }
  };
}

// node_modules/universal-agent-plugins-opencode-events/v2.js
function createV2Observer(options) {
  if (typeof options?.context?.event?.subscribe !== "function" || typeof options.emit !== "function" || typeof options.runtimeEligibility !== "function" || typeof options.native?.correlate !== "function" || typeof options.native?.session !== "function" || typeof options.native?.assistant !== "function" || !id(options.location)) {
    throw new TypeError("context, native ports, location, runtimeEligibility and emit required");
  }
  const lifetime = new AbortController();
  let connection, started = false, task = Promise.resolve(), replacements = 0;
  function invalidate(reason) {
    reducer.invalidate();
    connection?.abort();
    reducer.diag(reason);
  }
  const reducer = createV2Reducer(options, true, invalidate), core = reducer.core;
  const checkpoint = createCheckpoint(options.checkpoint, core, invalidate);
  reducer.setCheckpoint(checkpoint);
  async function delay(ms) {
    await new Promise((resolve) => {
      const done = () => {
        clearTimeout(timer);
        lifetime.signal.removeEventListener("abort", done);
        resolve();
      };
      const timer = setTimeout(done, ms);
      lifetime.signal.addEventListener("abort", done, { once: true });
      if (lifetime.signal.aborted) done();
    });
  }
  async function read2() {
    while (!lifetime.signal.aborted) {
      connection = new AbortController();
      try {
        const stream = options.context.event.subscribe({ signal: connection.signal });
        const opening = Promise.resolve().then(() => checkpoint.open(connection.signal));
        for await (const event of stream) {
          if (lifetime.signal.aborted || connection.signal.aborted) break;
          reducer.observe(event);
          if (connection.signal.aborted) break;
        }
        if (!lifetime.signal.aborted) invalidate("subscription_ended");
        await opening;
      } catch {
        if (!lifetime.signal.aborted) invalidate("subscription_error");
      }
      connection.abort();
      await checkpoint.close();
      if (lifetime.signal.aborted || replacements >= 3) break;
      await delay([250, 1e3, 2e3][replacements++]);
    }
    if (!lifetime.signal.aborted) core.diag("subscription_replacements_exhausted");
  }
  return {
    start() {
      if (started || lifetime.signal.aborted) return;
      started = true;
      let eligibility;
      try {
        eligibility = options.runtimeEligibility(options.context.app?.version);
      } catch {
      }
      if (eligibility !== "supported") {
        core.diag(eligibility === "unsupported" ? "runtime_unsupported" : "runtime_unverified");
        return;
      }
      task = read2();
    },
    dispose() {
      invalidate("observer_disposed");
      lifetime.abort();
      reducer.dispose();
    },
    async done() {
      await task;
      await core.drain();
    }
  };
}

// owned-host.mjs
import { realpathSync, statSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

// process-registry.mjs
init_protocol();
import { spawn } from "node:child_process";
import path from "node:path";
import { performance as performance2 } from "node:perf_hooks";
var deliveryKeys = process.platform === "win32" ? ["AGENT_NOTIFICATIONS_CONFIG", "USERPROFILE", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP"] : ["AGENT_NOTIFICATIONS_CONFIG", "HOME", "XDG_CONFIG_HOME", "DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR"];
var osKeys = process.platform === "win32" ? ["SystemRoot", "WINDIR"] : [];
var result = (status, output = Buffer.alloc(0)) => Object.freeze({ status, output });
var closedObject = (value, keys) => value && Object.getPrototypeOf(value) === Object.prototype && Reflect.ownKeys(value).every((key) => keys.includes(key));
function absoluteNativePath(value, platform = process.platform) {
  if (typeof value !== "string" || value.includes("\0")) return false;
  if (platform !== "win32") return path.posix.isAbsolute(value);
  if (value.startsWith("\\\\?\\") || value.startsWith("\\\\.\\")) return false;
  return (/^[A-Za-z]:\\/.test(value) || /^\\\\[^\\]+\\[^\\]+\\/.test(value)) && path.win32.normalize(value) === value;
}
function environment(values, allowed) {
  if (!closedObject(values, allowed)) throw new TypeError("invalid_environment");
  for (const [key, value] of Object.entries(values)) {
    if (!allowed.includes(key) || typeof value !== "string" || value.includes("\0") || value.length > 8192)
      throw new TypeError("invalid_environment");
  }
  return { ...values };
}
function createProcessRegistry(configuration) {
  if (!closedObject(configuration, ["executable", "privateCwd", "controlRoot", "osEnv", "deliveryEnv", "origin"]))
    throw new TypeError("invalid_configuration");
  const { executable: executable2, privateCwd, controlRoot: controlRoot2, osEnv = {}, deliveryEnv = {}, origin: origin2 } = configuration;
  if (![executable2, privateCwd, controlRoot2].every((value) => absoluteNativePath(value)) || process.platform === "win32" && !executable2.toLowerCase().endsWith(".exe"))
    throw new TypeError("invalid_paths");
  if (origin2 !== void 0 && !/^[a-f0-9]{64}$/.test(origin2)) throw new TypeError("invalid_origin");
  const nativePID = process.pid, publicExecPath = process.execPath;
  const profileInput = Buffer.from(JSON.stringify({
    protocol: 1,
    hostExecutable: publicExecPath,
    origin: origin2,
    controlRoot: controlRoot2,
    nativePID,
    entry: "serve",
    publicExecPath
  }));
  const base = process.platform === "win32" ? { USERPROFILE: privateCwd, APPDATA: privateCwd, LOCALAPPDATA: privateCwd, TEMP: privateCwd, TMP: privateCwd } : { HOME: privateCwd, XDG_CONFIG_HOME: privateCwd, XDG_RUNTIME_DIR: privateCwd };
  const clockEnv = { ...base, ...environment(osEnv, osKeys) };
  const profileEnv = {
    ...clockEnv,
    AGENT_NOTIFICATIONS_CONTROL_ROOT: controlRoot2,
    AGENT_NOTIFICATIONS_ORIGIN: origin2,
    AGENT_NOTIFICATIONS_NATIVE_PID: String(nativePID),
    AGENT_NOTIFICATIONS_HOST_EXECUTABLE: publicExecPath,
    AGENT_NOTIFICATIONS_HOST_ENTRY: "serve",
    AGENT_NOTIFICATIONS_PUBLIC_EXEC_PATH: publicExecPath
  };
  const eventEnv = { ...profileEnv, ...environment(deliveryEnv, deliveryKeys) };
  const entries = /* @__PURE__ */ new Set(), watchers = /* @__PURE__ */ new Set();
  let disposed = false, disabled = false;
  const status = () => Object.freeze({
    accepting: !disposed && !disabled,
    disposed,
    occupied: [...entries].reduce((sum, entry) => sum + entry.weight, 0),
    unresolved: [...entries].filter((entry) => entry.unresolved).length
  });
  const changed = () => {
    for (const watcher of watchers) watcher();
  };
  const current = (signal, isCurrent) => {
    try {
      return !signal?.aborted && isCurrent() === true && !signal?.aborted;
    } catch {
      return false;
    }
  };
  function handoff(kind, options) {
    if (!closedObject(options, kind !== "event" ? ["signal", "isCurrent", "deadline"] : ["frame", "prepare", "signal", "isCurrent", "deadline"]))
      return Promise.resolve(result("invalid_request"));
    const { frame, prepare, signal, isCurrent, deadline } = options;
    const weight = kind === "profile" ? 2 : 1;
    if (typeof isCurrent !== "function" || signal !== void 0 && !(signal instanceof AbortSignal) || deadline !== void 0 && !Number.isFinite(deadline) || kind === "event" && !(prepare === void 0 && Buffer.isBuffer(frame) && frame.length <= 4096 || frame === void 0 && typeof prepare === "function") || kind === "profile" && (origin2 === void 0 || !absoluteNativePath(publicExecPath) || profileInput.length > 4096))
      return Promise.resolve(result("invalid_request"));
    if (!current(signal, isCurrent)) return Promise.resolve(result("invalidated"));
    if (disposed || disabled) return Promise.resolve(result("registry_unavailable"));
    if (status().occupied + weight > 4) return Promise.resolve(result("capacity_suppressed"));
    if (deadline !== void 0 && deadline <= performance2.now()) return Promise.resolve(result("deadline"));
    let body = kind === "profile" ? Buffer.from(profileInput) : kind === "event" && frame ? Buffer.from(frame) : void 0;
    if (!current(signal, isCurrent)) return Promise.resolve(result("invalidated"));
    if (disposed || disabled) return Promise.resolve(result("registry_unavailable"));
    if (status().occupied + weight > 4) return Promise.resolve(result("capacity_suppressed"));
    if (deadline !== void 0 && deadline <= performance2.now()) return Promise.resolve(result("deadline"));
    const entry = { controller: new AbortController(), unresolved: false, weight };
    entries.add(entry);
    return new Promise((resolve) => {
      let child, afterSpawn, forcedKill = false, proofOutput = true, closed2 = false, failure, output = Buffer.alloc(0), stderrBytes = 0;
      let deadlineTimer, escalationTimer, proofTimer;
      const spawnedAt = performance2.now();
      const stopAt = Math.min(spawnedAt + (kind === "clock" ? 2e3 : kind === "profile" ? 1e4 : 22e3), deadline ?? Infinity);
      const closeBy = stopAt + 3e3;
      const delay = (at) => Math.max(0, at - performance2.now());
      const kill = (signalName) => {
        try {
          child.kill(signalName);
        } catch {
        }
      };
      const unproved = () => {
        if (closed2) return;
        entry.unresolved = true;
        disabled = true;
        output = Buffer.alloc(0);
        changed();
      };
      function terminate(reason) {
        if (closed2 || failure) return;
        failure = reason;
        if (kind !== "profile" || !["aborted", "invalidated", "deadline"].includes(reason)) {
          proofOutput = false;
          output = Buffer.alloc(0);
        }
        entry.controller.abort();
        clearTimeout(deadlineTimer);
        kill("SIGTERM");
        const now = performance2.now();
        escalationTimer = setTimeout(() => {
          if (!closed2) {
            forcedKill = true;
            kill("SIGKILL");
          }
        }, delay(Math.min(now + 1e3, closeBy)));
        entry.proofBy = Math.min(now + 3e3, closeBy);
        proofTimer = setTimeout(unproved, delay(entry.proofBy));
      }
      const abort = () => terminate("aborted");
      entry.stop = abort;
      try {
        afterSpawn = prepare?.();
        if (prepare && typeof afterSpawn !== "function" || entry.controller.signal.aborted || signal?.aborted || disposed || disabled)
          throw new TypeError("invalid_preparation");
        child = spawn(executable2, [kind === "clock" ? "opencode-clock" : kind === "profile" ? "opencode-runtime-profile" : "opencode-event", "--protocol", "1"], {
          shell: false,
          windowsHide: true,
          cwd: privateCwd,
          stdio: ["pipe", "pipe", "pipe"],
          env: kind === "clock" ? clockEnv : kind === "profile" ? profileEnv : eventEnv
        });
      } catch {
        entries.delete(entry);
        clearTimeout(deadlineTimer);
        clearTimeout(escalationTimer);
        clearTimeout(proofTimer);
        changed();
        resolve(result("spawn_failed"));
        return;
      }
      let postFailure = false;
      if (afterSpawn) {
        try {
          body = afterSpawn();
          if (!Buffer.isBuffer(body) || body.length > 4096) postFailure = true;
        } catch {
          postFailure = true;
        }
      }
      child.on("error", () => {
        proofOutput = false;
        terminate("spawn_failed");
      });
      for (const stream of [child.stdin, child.stdout, child.stderr])
        stream.on("error", () => {
          proofOutput = false;
          terminate("stream_error");
        });
      child.stdout.on("data", (chunk) => {
        if (closed2 || failure && !(kind === "profile" && proofOutput)) return;
        if (output.length + chunk.length > 1024) {
          proofOutput = false;
          output = Buffer.alloc(0);
          terminate("output_limit");
          return;
        }
        output = Buffer.concat([output, chunk]);
      });
      child.stderr.on("data", (chunk) => {
        if (closed2 || failure && !(kind === "profile" && proofOutput)) return;
        stderrBytes = Math.min(1025, stderrBytes + chunk.length);
        if (stderrBytes > 1024) {
          proofOutput = false;
          output = Buffer.alloc(0);
          terminate("output_limit");
        }
      });
      child.once("close", (code) => {
        if (performance2.now() > (entry.proofBy ?? closeBy)) unproved();
        closed2 = true;
        clearTimeout(deadlineTimer);
        clearTimeout(escalationTimer);
        clearTimeout(proofTimer);
        signal?.removeEventListener("abort", abort);
        let innerProof = kind !== "profile";
        if (kind === "profile" && code === 0 && !forcedKill && proofOutput && !entry.unresolved) {
          try {
            profileReceipt(output);
            innerProof = true;
          } catch {
          }
        }
        if (!innerProof) {
          entry.unresolved = true;
          disabled = true;
        }
        if (innerProof) entries.delete(entry);
        const valid = current(signal, isCurrent);
        const outcome = entry.unresolved ? "ipc_termination_unproved" : !valid ? "invalidated" : failure ?? (performance2.now() > stopAt ? "deadline" : code === 0 ? "ok" : "exited");
        resolve(result(outcome, outcome === "ok" ? output : void 0));
        changed();
      });
      deadlineTimer = setTimeout(() => terminate("deadline"), delay(stopAt));
      signal?.addEventListener("abort", abort, { once: true });
      if (postFailure) terminate("invalidated");
      if (!current(signal, isCurrent)) terminate("invalidated");
      if (!failure) {
        try {
          child.stdin.end(body);
        } catch {
          terminate("stream_error");
        }
      }
    });
  }
  function drain(options = {}) {
    if (!closedObject(options, ["timeoutMs"])) throw new TypeError("invalid_timeout");
    const { timeoutMs = 3e3 } = options;
    if (!Number.isFinite(timeoutMs) || timeoutMs < 0 || timeoutMs > 3e3) throw new TypeError("invalid_timeout");
    return new Promise((resolve) => {
      let timer;
      const finish = () => {
        clearTimeout(timer);
        watchers.delete(check);
        const snapshot = status();
        resolve(Object.freeze({
          ...snapshot,
          reaped: snapshot.occupied === 0,
          status: snapshot.occupied === 0 ? "drained" : snapshot.unresolved ? "ipc_termination_unproved" : "pending"
        }));
      };
      const check = () => {
        if (entries.size === 0 || status().unresolved) finish();
      };
      watchers.add(check);
      timer = setTimeout(finish, timeoutMs);
      check();
    });
  }
  function cancel() {
    for (const entry of entries) entry.controller.abort();
    for (const entry of entries) entry.stop();
    return drain();
  }
  function dispose() {
    disposed = true;
    for (const entry of entries) entry.controller.abort();
    for (const entry of entries) entry.stop();
    return drain();
  }
  return Object.freeze({
    profile: (options) => handoff("profile", options),
    clock: (options) => handoff("clock", options),
    event: (options) => handoff("event", options),
    status,
    drain,
    cancel,
    dispose
  });
}

// owned-host.mjs
init_protocol();
var executable = "__AGENT_NOTIFICATIONS_EXECUTABLE__";
var controlRoot = "__AGENT_NOTIFICATIONS_CONTROL_ROOT__";
var origin = "__AGENT_NOTIFICATIONS_ORIGIN__";
var identity2 = (name) => {
  if (!absoluteNativePath(name) || realpathSync(name) !== name) throw new TypeError("binding_unverified");
  const stat = statSync(name, { bigint: true });
  return Object.freeze({ name, dev: stat.dev, ino: stat.ino, size: stat.isDirectory() ? void 0 : stat.size, mtime: stat.isDirectory() ? void 0 : stat.mtimeNs, mode: stat.mode });
};
var equal = (a, b) => ["name", "dev", "ino", "size", "mtime", "mode"].every((key) => a[key] === b[key]);
async function prepareOwnedHost(directory2, generation, appVersion) {
  let registry, privateCwd;
  try {
    if (!/^[a-f0-9]{64}$/.test(origin) || !absoluteNativePath(directory2)) return;
    const files = [executable, controlRoot, directory2, process.execPath].map(identity2);
    const isOwned = () => {
      try {
        return files.every((original) => equal(original, identity2(original.name)));
      } catch {
        return false;
      }
    };
    privateCwd = mkdtempSync(join(tmpdir(), "agent-notifications-"));
    const deliveryKeys2 = process.platform === "win32" ? ["AGENT_NOTIFICATIONS_CONFIG", "USERPROFILE", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP"] : ["AGENT_NOTIFICATIONS_CONFIG", "HOME", "XDG_CONFIG_HOME", "DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR"];
    const pick = (keys) => Object.fromEntries(keys.filter((key) => process.env[key] !== void 0).map((key) => [key, process.env[key]]));
    registry = createProcessRegistry({
      executable,
      controlRoot,
      origin,
      privateCwd,
      osEnv: pick(process.platform === "win32" ? ["SystemRoot", "WINDIR"] : []),
      deliveryEnv: pick(deliveryKeys2)
    });
    const response = await registry.profile({ isCurrent: isOwned });
    const receipt = profileReceipt(response.output);
    if (response.status !== "ok" || receipt.semantic !== "eligible" || receipt.generation !== generation || !isOwned() || generation === "v2" && appVersion !== "2.0.21") throw new TypeError("runtime_unverified");
    let disposed = false;
    return Object.freeze({
      registry,
      origin,
      isOwned: () => !disposed && isOwned(),
      runtimeEligibility: () => !disposed && isOwned() ? "supported" : "unverified",
      async dispose() {
        disposed = true;
        const status = await registry.dispose();
        if (status.reaped) {
          try {
            rmSync(privateCwd, { recursive: true });
          } catch {
          }
        }
        return status;
      }
    });
  } catch {
    if (registry) {
      const status = await registry.dispose();
      if (!status.reaped) return;
    }
    if (privateCwd) {
      try {
        rmSync(privateCwd, { recursive: true });
      } catch {
      }
    }
  }
}

// clock-cells.mjs
init_protocol();
import { createHash as createHash2 } from "node:crypto";

// clock-qualification-data.mjs
var clock_qualification_data_default = Object.freeze([
  {
    "protocol": 1,
    "generation": "v1",
    "goos": "linux",
    "goarch": "amd64",
    "images": [
      {
        "version": "1.18.33",
        "imageSHA256": "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"
      },
      {
        "version": "1.18.34",
        "imageSHA256": "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"
      }
    ],
    "algorithmSourceMerkleSHA256": "298fada0b99e4e45d874a2e055a564a46b22b43e185d6540448066af7a31c2d1",
    "sourceKind": "linux-proc-boottime",
    "rawKind": "linux-boottime",
    "nativeReadBoundNS": "103000000",
    "comparisonBoundNS": "430000000",
    "translationBoundNS": "224000000"
  },
  {
    "protocol": 1,
    "generation": "v2",
    "goos": "linux",
    "goarch": "amd64",
    "images": [
      {
        "version": "2.0.21",
        "imageSHA256": "f916986543348d7953d8d43aa048516cdbc3f84f4d0dc9c0c5b9d1da3030cea7"
      }
    ],
    "algorithmSourceMerkleSHA256": "298fada0b99e4e45d874a2e055a564a46b22b43e185d6540448066af7a31c2d1",
    "sourceKind": "linux-proc-boottime",
    "rawKind": "linux-boottime",
    "nativeReadBoundNS": "103000000",
    "comparisonBoundNS": "430000000",
    "translationBoundNS": "224000000"
  }
]);

// clock-cells.mjs
var images = Object.freeze({
  v1: [
    ["1.18.33", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"],
    ["1.18.34", "9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"]
  ],
  v2: [["2.0.21", "f916986543348d7953d8d43aa048516cdbc3f84f4d0dc9c0c5b9d1da3030cea7"]]
});
var hashOK = (value) => typeof value === "string" && /^[a-f0-9]{64}$/.test(value) && !/^0+$/.test(value);
var canonical = (value) => Array.isArray(value) ? `[${value.map(canonical).join(",")}]` : value && typeof value === "object" ? `{${Object.keys(value).sort().map((key) => `${JSON.stringify(key)}:${canonical(value[key])}`).join(",")}}` : JSON.stringify(value);
function describeClockSource(goos, goarch) {
  if (goos === "linux" && ["amd64", "arm64"].includes(goarch))
    return Object.freeze({ sourceKind: "linux-proc-boottime", rawKind: "linux-boottime" });
  if (goos === "darwin" && ["amd64", "arm64"].includes(goarch))
    return Object.freeze({ sourceKind: "darwin-mach-continuous", rawKind: "darwin-monotonic-raw" });
  if (goos === "windows" && goarch === "amd64")
    return Object.freeze({ sourceKind: "windows-interrupt-precise", rawKind: "windows-interrupt-precise" });
  throw new TypeError("qualification_unverified");
}
function describeClockPolicy(row) {
  const keys = [
    "protocol",
    "generation",
    "goos",
    "goarch",
    "images",
    "algorithmSourceMerkleSHA256",
    "sourceKind",
    "rawKind",
    "nativeReadBoundNS",
    "comparisonBoundNS",
    "translationBoundNS"
  ];
  closed(row, [...keys, "sourceWallBoundNS", "originalNativeAge"], keys);
  const descriptor = describeClockSource(row.goos, row.goarch);
  const legacy = row.goos === "linux" && row.goarch === "amd64";
  const pins = legacy ? images[row.generation] : void 0;
  const versions = legacy ? pins?.map((pin) => pin[0]) : row.generation === "v1" ? ["1.18.33"] : row.generation === "v2" ? ["2.0.21"] : void 0;
  if (row.protocol !== 1 || !versions || row.sourceKind !== descriptor.sourceKind || row.rawKind !== descriptor.rawKind || !hashOK(row.algorithmSourceMerkleSHA256) || !Array.isArray(row.images) || row.images.length !== versions.length)
    throw new TypeError("qualification_unverified");
  const nativeReadBoundNS = ns(row.nativeReadBoundNS), comparisonBoundNS = ns(row.comparisonBoundNS), translationBoundNS = ns(row.translationBoundNS);
  let sourceWallBoundNS;
  if (row.goos === "linux") {
    if (Object.hasOwn(row, "sourceWallBoundNS") || row.nativeReadBoundNS !== "103000000" || row.comparisonBoundNS !== "430000000" || row.translationBoundNS !== "224000000")
      throw new TypeError("qualification_unverified");
  } else {
    sourceWallBoundNS = ns(row.sourceWallBoundNS);
    if (nativeReadBoundNS < 3000000n || nativeReadBoundNS > 103000000n || comparisonBoundNS > 2000000000n || 2n * nativeReadBoundNS + translationBoundNS > comparisonBoundNS || sourceWallBoundNS > comparisonBoundNS) throw new TypeError("qualification_unverified");
  }
  for (const [index, image] of row.images.entries()) {
    closed(image, ["version", "imageSHA256"]);
    if (image.version !== versions[index] || !hashOK(image.imageSHA256) || legacy && image.imageSHA256 !== pins[index][1])
      throw new TypeError("qualification_unverified");
  }
  const exceptional = row.goos === "windows" && row.goarch === "amd64" && row.generation === "v1" && row.images.length === 1 && row.images[0].version === "1.18.33" && row.images[0].imageSHA256 === "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c";
  const originalNativeAge = Object.hasOwn(row, "originalNativeAge") ? row.originalNativeAge : "bounded";
  if (exceptional ? originalNativeAge !== "unverified_original_date" : originalNativeAge !== "bounded")
    throw new TypeError("qualification_unverified");
  const prefix = legacy ? "linux-amd64-proc-boottime-v1" : `${row.goos}-${row.goarch}-${row.sourceKind}-v1`;
  const profileID = `${prefix}:${createHash2("sha256").update(canonical(row)).digest("hex")}`;
  return Object.freeze({
    profileID,
    calibrationID: `${profileID}:same-coordinate`,
    generation: row.generation,
    images: Object.freeze(row.images.map((image) => Object.freeze({ ...image }))),
    goos: row.goos,
    goarch: row.goarch,
    sourceKind: descriptor.sourceKind,
    rawKind: descriptor.rawKind,
    originalNativeAge,
    nativeReadBoundNS,
    comparisonBoundNS,
    translationBoundNS,
    ...sourceWallBoundNS === void 0 ? {} : { sourceWallBoundNS }
  });
}
var cells = Object.freeze(clock_qualification_data_default.map(describeClockPolicy));
function selectClockCell(generation) {
  const goos = process.platform === "win32" ? "windows" : process.platform;
  const goarch = process.arch === "x64" ? "amd64" : process.arch;
  const matches = cells.filter((cell) => cell.goos === goos && cell.goarch === goarch && cell.generation === generation);
  return matches.length === 1 ? matches[0] : void 0;
}

// linux-clock.mjs
init_protocol();
import { openSync, closeSync, readSync, fstatSync, statSync as statSync2, statfsSync, readlinkSync, constants } from "node:fs";
var fail = () => {
  throw new TypeError("clock_unavailable");
};
var fixed = ["/proc/uptime", "/proc/sys/kernel/random/boot_id", "/proc/thread-self/ns/time"];
var PROCFS = 0x9fa0n;
var NSFS = 0x6e736673n;
var quantum = 10000000n;
var same = (a, b) => a.dev === b.dev && a.ino === b.ino && a.mode === b.mode;
function held(fd, magic) {
  if (statfsSync(`/proc/thread-self/fd/${fd}`, { bigint: true }).type !== magic) fail();
  const info = fstatSync(fd, { bigint: true });
  if (typeof info.dev !== "bigint" || typeof info.ino !== "bigint" || info.dev <= 0n || info.ino <= 0n) fail();
  return info;
}
function read(fd, size) {
  const buffer = Buffer.alloc(size + 1);
  const count = readSync(fd, buffer, 0, buffer.length, 0);
  if (count === 0 || count > size) fail();
  return buffer.subarray(0, count).toString("utf8");
}
function parseUptime(text) {
  if (typeof text !== "string" || !/^(0|[1-9][0-9]*)\.[0-9]{2} (0|[1-9][0-9]*)\.[0-9]{2}\n$/.test(text)) fail();
  const first = text.slice(0, text.indexOf(" ")).split(".");
  return ns((BigInt(first[0]) * 1000000000n + BigInt(first[1]) * quantum).toString());
}
function createLinuxClock() {
  if (process.platform !== "linux" || !["x64", "arm64"].includes(process.arch)) fail();
  const fds = [];
  let disposed = false, previous;
  try {
    let verify = function() {
      if (disposed) fail();
      for (const [index, fd] of fds.entries()) if (!same(held(fd, index === 2 ? NSFS : PROCFS), identities[index])) fail();
      const current = statSync2(fixed[2], { bigint: true });
      if (!same(current, identities[2]) || statfsSync(fixed[2], { bigint: true }).type !== NSFS || readlinkSync(fixed[2]) !== `time:[${current.ino}]` || read(fds[1], 37) !== `${boot}
`) fail();
    }, sample = function() {
      try {
        verify();
        const lo = parseUptime(read(fds[0], 128));
        const wallMs = Date.now();
        const last = parseUptime(read(fds[0], 128));
        verify();
        if (!Number.isSafeInteger(wallMs) || wallMs <= 0 || last < lo || last - lo > 100000000n || previous && lo < previous.loNS) fail();
        const wallNS = ns((BigInt(wallMs) * 1000000n).toString());
        const hi = ns((last + quantum).toString());
        const result2 = Object.freeze({
          boot,
          domain,
          rawKind: "linux-boottime",
          loNS: lo,
          hiNS: hi,
          wallNS,
          offsetLoNS: wallNS - hi - 2000000n,
          offsetHiNS: wallNS - lo + 2000000n
        });
        if (previous && (result2.offsetLoNS > previous.offsetHiNS + 430000000n || result2.offsetHiNS < previous.offsetLoNS - 430000000n)) fail();
        previous = result2;
        return result2;
      } catch {
        dispose();
        fail();
      }
    }, dispose = function() {
      if (disposed) return;
      disposed = true;
      for (const fd of fds) {
        try {
          closeSync(fd);
        } catch {
        }
      }
    };
    for (const [index, file] of fixed.entries()) {
      fds.push(openSync(file, constants.O_RDONLY | (constants.O_CLOEXEC ?? 524288) | (index === 2 ? 0 : constants.O_NOFOLLOW)));
    }
    const identities = fds.map((fd, index) => held(fd, index === 2 ? NSFS : PROCFS));
    const boot = read(fds[1], 37).trimEnd();
    if (!bootOK(boot) || read(fds[1], 37) !== `${boot}
`) fail();
    const domain = `linux-time:${identities[2].dev}:${identities[2].ino}`;
    return Object.freeze({ sample, dispose });
  } catch {
    for (const fd of fds) {
      try {
        closeSync(fd);
      } catch {
      }
    }
    fail();
  }
}

// platform-clock.mjs
init_native_clock_contract();
async function createPlatformClock({ signal } = {}) {
  if (signal?.aborted) unavailable();
  if (process.platform === "linux") return createLinuxClock();
  if (process.platform === "darwin") return (await Promise.resolve().then(() => (init_darwin_clock(), darwin_clock_exports))).createDarwinClock({ signal });
  if (process.platform === "win32") return (await Promise.resolve().then(() => (init_windows_clock(), windows_clock_exports))).createWindowsClock({ signal });
  unavailable();
}

// prepared-delivery.mjs
import { randomBytes as randomBytes2 } from "node:crypto";
import { performance as performance3 } from "node:perf_hooks";
init_native_clock_contract();
init_protocol();
var bad = () => {
  throw new TypeError("source_unverified");
};
var overlaps = (a, b, bound) => a.offsetLoNS <= b.offsetHiNS + bound && a.offsetHiNS >= b.offsetLoNS - bound;
function anchorMatches(anchor, before, after, policy) {
  const lo = ns(anchor.monoLoNS), hi = ns(anchor.monoHiNS), wall = ns(anchor.wallNS);
  return bootOK(anchor.boot) && domainOK(anchor.domain, anchor.rawKind) && before.rawKind === policy.rawKind && after.rawKind === policy.rawKind && anchor.boot === before.boot && anchor.boot === after.boot && anchor.domain === before.domain && anchor.domain === after.domain && anchor.rawKind === policy.rawKind && ns(anchor.readUncertaintyNS) <= policy.nativeReadBoundNS && before.loNS <= hi && lo < after.hiNS && after.hiNS - before.loNS <= policy.translationBoundNS && overlaps({
    offsetLoNS: wall - hi - policy.nativeReadBoundNS,
    offsetHiNS: wall - lo + policy.nativeReadBoundNS
  }, after, policy.comparisonBoundNS);
}
function createPreparedDelivery({ registry, origin: origin2, policy, isOwned, onInvalidate, sourceFactory = createPlatformClock }) {
  let epoch, source, anchor, activation, pending, last, ingress, disposed = false;
  const originals = /* @__PURE__ */ new Map(), prepared = /* @__PURE__ */ new WeakMap();
  function invalidate(reason = "clock") {
    if (!epoch && !source && !pending) return;
    const attempt = pending;
    pending = void 0;
    epoch = void 0;
    originals.clear();
    anchor = void 0;
    activation = void 0;
    ingress = void 0;
    last = void 0;
    attempt?.controller.abort();
    const retired = source;
    source = void 0;
    try {
      retired?.dispose();
    } catch {
    }
    try {
      onInvalidate?.(reason);
    } catch {
    } finally {
      registry.cancel();
    }
  }
  function sample() {
    try {
      if (disposed || !source || !isOwned()) bad();
      const raw = source.sample();
      if (raw.rawKind !== policy.rawKind) bad();
      const value = withWallOffset(raw, policy.rawKind === "linux-boottime" ? 2000000n : policy.sourceWallBoundNS);
      if (policy.rawKind === "linux-boottime" && (raw.offsetLoNS !== value.offsetLoNS || raw.offsetHiNS !== value.offsetHiNS)) bad();
      if (last && (value.boot !== last.boot || value.domain !== last.domain || value.loNS < last.loNS || !overlaps(last, value, policy.comparisonBoundNS))) bad();
      if (activation && !overlaps(activation, value, policy.comparisonBoundNS)) bad();
      last = value;
      return value;
    } catch {
      invalidate();
      bad();
    }
  }
  const ms = (record) => Number(record.loNS / 1000000n);
  const clock = Object.freeze({ id: policy.sourceKind ?? (policy.rawKind === "linux-boottime" ? "linux-proc-boottime" : policy.rawKind), now() {
    if (ingress) {
      const original = ingress;
      ingress = void 0;
      last = original;
      return ms(original);
    }
    return ms(sample());
  } });
  async function activate() {
    if (disposed) return false;
    if (epoch) {
      if (isOwned()) return true;
      invalidate();
      return false;
    }
    if (pending) return pending.promise;
    const attempt = { controller: new AbortController(), deadline: performance3.now() + 2e3 };
    pending = attempt;
    const isCurrent = () => {
      try {
        return pending === attempt && !disposed && !attempt.controller.signal.aborted && performance3.now() < attempt.deadline && isOwned();
      } catch {
        return false;
      }
    };
    const timer = setTimeout(() => {
      if (pending === attempt) invalidate();
    }, 2e3);
    attempt.promise = (async () => {
      const id6 = randomBytes2(16).toString("hex");
      try {
        const acquired = await new Promise((resolve, reject) => {
          const signal = attempt.controller.signal;
          const abort = () => reject(new TypeError("source_unverified"));
          signal.addEventListener("abort", abort, { once: true });
          let preparing;
          try {
            preparing = sourceFactory({ signal });
          } catch (error) {
            preparing = Promise.reject(error);
          }
          Promise.resolve(preparing).then((value) => {
            signal.removeEventListener("abort", abort);
            if (!isCurrent()) {
              try {
                value?.dispose();
              } catch {
              }
              reject(new TypeError("source_unverified"));
            } else resolve(value);
          }, (error) => {
            signal.removeEventListener("abort", abort);
            reject(error);
          });
        });
        if (!isCurrent()) {
          try {
            acquired?.dispose();
          } catch {
          }
          bad();
        }
        source = acquired;
        activation = void 0;
        if (policy.rawKind !== "linux-boottime" && !policy.images?.some((image) => image.imageSHA256 === source.imageSHA256)) bad();
        const before = sample();
        const r = await registry.clock({ signal: attempt.controller.signal, isCurrent, deadline: attempt.deadline });
        if (!isCurrent() || performance3.now() >= attempt.deadline || r.status !== "ok") bad();
        const after = sample(), original = clockReceipt(r.output);
        if (!anchorMatches(original, before, after, policy) || after.loNS - before.loNS > 2000000000n) bad();
        anchor = original;
        activation = before;
        epoch = Object.freeze({ id: id6, started: before.loNS, after: after.hiNS });
        return true;
      } catch {
        if (pending === attempt) invalidate();
        return false;
      } finally {
        clearTimeout(timer);
        if (pending === attempt) pending = void 0;
      }
    })();
    return attempt.promise;
  }
  function retain(record) {
    if (!epoch) bad();
    for (const [tick2] of originals) if (record.loNS - tick2 > 30000000000n) originals.delete(tick2);
    const tick = record.loNS / 1000000n * 1000000n;
    if (!originals.has(tick)) {
      if (originals.size >= 256) {
        invalidate();
        bad();
      }
      originals.set(tick, Object.freeze({ sample: record, epoch, anchor }));
    }
  }
  function beginIngress(event) {
    const record = sample();
    ingress = record;
    if (["session.idle", "session.error", "question.asked", "permission.asked"].includes(event?.type) || event?.type === "session.status" && event.properties?.status?.type === "idle" || event?.type === "message.updated" && event.properties?.info?.error != null) retain(record);
  }
  function nativeIngress(event) {
    if (["form.created", "permission.asked", "session.execution.succeeded", "session.execution.failed"].includes(event?.type))
      retain(last ?? sample());
  }
  const current = (record, handoff) => {
    if (disposed || epoch !== record.epoch) return false;
    if (!isOwned()) {
      invalidate();
      return false;
    }
    return handoff.clockID === clock.id && !handoff.signal.aborted && handoff.isCurrent();
  };
  async function beforeEmit(event, handoff) {
    try {
      const tick = BigInt(handoff.ingressMonotonicMs) * 1000000n;
      const original = originals.get(tick);
      if (!original || !current(original, handoff) || event.rootSession !== true || !event.provenance) return false;
      const before = sample(), remaining = handoff.metadataDeadline - clock.now();
      if (remaining <= 0) return false;
      const response = await registry.clock({
        signal: handoff.signal,
        isCurrent: () => current(original, handoff),
        deadline: performance3.now() + Math.min(remaining, 2e3)
      });
      if (!current(original, handoff) || response.status !== "ok") return false;
      const after = sample(), calibrationCheck = clockReceipt(response.output);
      if (!anchorMatches(calibrationCheck, before, after, policy) || !overlaps(original.sample, after, policy.comparisonBoundNS) || after.loNS - before.loNS > 2000000000n) bad();
      const birthNS = BigInt(event.provenance.nativeTime) * 1000000n;
      if (birthNS < original.sample.wallNS - 60000000000n || birthNS > original.sample.wallNS + 2000000000n || birthNS < activation.wallNS + (original.epoch.after - activation.loNS)) bad();
      const provenance = Object.freeze({
        sourceEpoch: original.epoch.id,
        epochStartedTickNS: String(original.epoch.started),
        policyID: policy.profileID,
        fence: fence(original.anchor, policy),
        anchor: original.anchor,
        ingressTickNS: String(original.sample.loNS),
        calibration: Object.freeze({
          calibrationID: policy.calibrationID,
          sourceEpoch: original.epoch.id,
          sourceLoNS: String(activation.loNS),
          sourceHiNS: String(original.epoch.after),
          nativeLoNS: original.anchor.monoLoNS,
          nativeHiNS: original.anchor.monoHiNS,
          errorNS: String(policy.translationBoundNS)
        })
      });
      encodeFrame({ protocol: 1, origin: origin2, event, provenance: {
        ...provenance,
        spawnTickNS: String(after.loNS),
        deadlineTickNS: String(after.loNS + 20000000000n)
      } }, policy);
      prepared.set(event, { original, provenance });
      return true;
    } catch {
      invalidate();
      return false;
    }
  }
  function emit(event, handoff) {
    const held2 = prepared.get(event);
    prepared.delete(event);
    if (!held2 || !current(held2.original, handoff)) return Promise.resolve();
    return registry.event({
      signal: handoff.signal,
      isCurrent: () => current(held2.original, handoff),
      prepare() {
        if (!current(held2.original, handoff)) {
          invalidate();
          bad();
        }
        const before = sample();
        if (before.loNS - held2.original.sample.loNS > 30000000000n || ms(before) >= handoff.metadataDeadline || !overlaps(held2.original.sample, before, policy.comparisonBoundNS)) {
          invalidate();
          bad();
        }
        const spawnTick = before.loNS;
        const deadline = ns((spawnTick + 20000000000n).toString());
        return () => {
          const after = sample();
          if (epoch !== held2.original.epoch || handoff.signal.aborted || after.hiNS - before.loNS > 110000000n || after.hiNS - held2.original.sample.loNS > 30000000000n || after.hiNS >= deadline || ms(after) >= handoff.metadataDeadline) {
            invalidate();
            bad();
          }
          return encodeFrame({ protocol: 1, origin: origin2, event, provenance: {
            ...held2.provenance,
            spawnTickNS: String(spawnTick),
            deadlineTickNS: String(deadline)
          } }, policy);
        };
      }
    });
  }
  return Object.freeze({
    clock,
    activate,
    beginIngress,
    nativeIngress,
    beforeEmit,
    emit,
    invalidate,
    allowsBirth(nativeTime) {
      return epoch && Number.isSafeInteger(nativeTime) && nativeTime > 0 && BigInt(nativeTime) * 1000000n >= activation.wallNS + (epoch.after - activation.loNS);
    },
    dispose() {
      disposed = true;
      invalidate();
    },
    ready: () => Boolean(epoch && !disposed)
  });
}

// ipc.mjs
init_protocol();

// native-v1.mjs
var id4 = (x) => typeof x === "string" && x.length > 0 && !/[\u0000-\u001f]/u.test(x) && Buffer.byteLength(x) <= 256;
var stamp = (x) => Number.isSafeInteger(x) && x > 0;
function createNativeV1(client, directory2, fail2) {
  const requests = /* @__PURE__ */ new Map(), users = /* @__PURE__ */ new Map();
  function ingest(event) {
    const p = event?.properties;
    if (event?.type === "message.updated" && p?.info?.role === "user") {
      const { sessionID, id: userID, time } = p.info;
      if (!id4(sessionID) || !id4(userID) || !stamp(time?.created)) {
        fail2();
        return;
      }
      const prior = users.get(sessionID), created = time.created;
      if (prior?.seen.has(userID)) {
        if (prior.seen.get(userID) !== created) fail2();
        return;
      }
      if (prior && created < prior.created) return;
      if (prior?.seen.size >= 512 || !prior && users.size >= 512) {
        fail2();
        return;
      }
      const seen = prior?.seen ?? /* @__PURE__ */ new Map();
      seen.set(userID, created);
      users.set(sessionID, { id: userID, created, seen });
    }
    if (["question.asked", "permission.asked"].includes(event?.type) && id4(p?.id)) {
      if (requests.size >= 256 && !requests.has(p.id)) {
        fail2();
        return;
      }
      const next = {
        sessionID: p.sessionID,
        messageID: p.tool?.messageID ?? p.messageID,
        callID: p.tool?.callID,
        turnID: users.get(p.sessionID)?.id
      };
      const prior = requests.get(p.id);
      if (prior && Object.keys(next).some((key) => prior[key] !== next[key])) {
        fail2();
        return;
      }
      if (!prior) requests.set(p.id, Object.freeze(next));
    }
    if (["question.replied", "question.rejected", "permission.replied"].includes(event?.type)) requests.delete(p?.requestID);
    if (["session.deleted", "session.compacted", "session.error"].includes(event?.type) || event?.type === "session.status" && p?.status?.type === "retry") {
      const sid = p?.sessionID ?? p?.info?.id;
      for (const [key, value] of requests) if (value.sessionID === sid) requests.delete(key);
    }
  }
  async function finalize(event, handoff) {
    if (!handoff.isCurrent() || handoff.signal.aborted || users.get(event.sessionID)?.id !== event.turnID) return false;
    const request = event.requestID ? requests.get(event.requestID) : void 0;
    if (event.requestID && (!request || request.sessionID !== event.sessionID || request.turnID !== event.turnID || !id4(request.messageID) || !id4(request.callID))) return false;
    const result2 = await client.session.get({ path: { id: event.sessionID }, signal: handoff.signal });
    if (!handoff.isCurrent() || handoff.signal.aborted) return false;
    const info = result2?.data ?? result2;
    if (info?.id !== event.sessionID || info.parentID !== void 0 || info.directory !== directory2) return false;
    const snapshot = await client.session.messages({ path: { id: event.sessionID }, query: { limit: 30 }, signal: handoff.signal });
    if (!handoff.isCurrent() || handoff.signal.aborted || users.get(event.sessionID)?.id !== event.turnID || request && requests.get(event.requestID) !== request) return false;
    const rows = Array.isArray(snapshot) ? snapshot : snapshot?.data;
    if (!Array.isArray(rows) || rows.length > 30) return false;
    const messages = rows.map((row) => row?.info ?? row);
    if (!messages.every((row) => id4(row?.id) && row.sessionID === event.sessionID)) return false;
    const answer = messages.findLast((row) => row.role === "assistant" && row.summary !== true);
    const lastUser = messages.findLast((row) => row.role === "user");
    if (lastUser && lastUser.id !== event.turnID || !answer || answer.parentID !== event.turnID || answer.path?.cwd !== directory2 || messages.at(-1) !== answer || !stamp(answer.time?.created)) return false;
    if (event.kind === "turn_idle_verified") return answer.id === event.messageID && answer.finish === "stop" && answer.time?.completed === event.provenance.nativeTime && answer.error == null;
    if (event.kind === "terminal_error") return answer.id === event.provenance.nativeMessageID && answer.time.created === event.provenance.nativeTime && stamp(answer.time?.completed) && answer.error && !/abort|cancel/i.test(answer.error.name ?? "");
    return request?.messageID === answer.id && answer.time.created === event.provenance.nativeTime;
  }
  return Object.freeze({ ingest, finalize, dispose() {
    users.clear();
    requests.clear();
  } });
}

// native-v2.mjs
var id5 = (x) => typeof x === "string" && x.length > 0 && Buffer.byteLength(x) <= 256 && !/[\u0000-\u001f]/u.test(x);
var stamp2 = (x) => Number.isSafeInteger(x) && x > 0;
var cancelled = (error) => /abort|cancel|interrupt/i.test(error?._tag ?? error?.name ?? error?.type ?? "");
var scope = (x) => x && typeof x.directory === "string" ? Object.freeze({ directory: x.directory, workspaceID: x.workspaceID }) : void 0;
var same3 = (a, b) => a && b && a.directory === b.directory && a.workspaceID === b.workspaceID;
var rowTypes = /* @__PURE__ */ new Set([
  "user",
  "assistant",
  "compaction",
  "idle",
  "agent-switched",
  "model-switched",
  "location-switched",
  "synthetic",
  "system",
  "skill",
  "shell"
]);
var followsAssistant = (value, row) => value.slice(value.indexOf(row) + 1).some((item) => ["user", "assistant", "compaction"].includes(item.type));
var ephemeralTypes = /* @__PURE__ */ new Set([
  "session.usage.updated",
  "session.text.delta",
  "session.reasoning.delta",
  "session.tool.input.delta",
  "session.tool.progress",
  "session.compaction.delta"
]);
function createNativeV2(context, ownedLocation, onIngress, onUncertainty) {
  const own = scope(ownedLocation), projectID = ownedLocation?.project?.id, bindings = /* @__PURE__ */ new Map(), projected = /* @__PURE__ */ new WeakMap();
  if (!own || !id5(projectID)) throw new TypeError("scope_unverified");
  function reset() {
    bindings.clear();
  }
  function state(sid) {
    if (!bindings.has(sid)) {
      if (bindings.size >= 512) {
        onUncertainty();
        return;
      }
      bindings.set(sid, { forms: /* @__PURE__ */ new Map(), requests: /* @__PURE__ */ new Map(), inbox: /* @__PURE__ */ new Map() });
    }
    return bindings.get(sid);
  }
  function correlate(event) {
    const { type, data: p } = event;
    if (ephemeralTypes.has(type)) return;
    if (type === "location.shutdown") return { location: scope(event.location)?.directory };
    const sid = type === "form.created" ? p.form?.sessionID : p.sessionID;
    if (!id5(sid) || sid === "global") return;
    if (type === "form.created" && p.form?.metadata?.kind !== "question") return;
    onIngress(event);
    const b = state(sid);
    if (!b) return { sessionID: sid };
    const nativeScope = scope(event.location) ?? scope(p.location);
    if (nativeScope) b.scope = nativeScope;
    if (["session.moved", "session.deleted"].includes(type)) {
      b.run = void 0;
      b.forms.clear();
      b.requests.clear();
    }
    if (type === "session.execution.started") {
      b.run = event.id;
      b.user = void 0;
      b.assistant = void 0;
      b.step = void 0;
      b.terminal = void 0;
      b.forms.clear();
      b.requests.clear();
    }
    if (type === "session.inbox.enqueued" && id5(p.inboxID) && id5(p.item?.type)) {
      if (b.inbox.size >= 64 && !b.inbox.has(p.inboxID)) {
        onUncertainty();
        return { sessionID: sid };
      }
      if (b.inbox.has(p.inboxID) && b.inbox.get(p.inboxID) !== p.item.type) {
        onUncertainty();
        return { sessionID: sid };
      }
      b.inbox.set(p.inboxID, p.item.type);
    }
    if (type === "session.inbox.delivered") {
      const kind = b.inbox.get(p.inboxID);
      b.inbox.delete(p.inboxID);
      if (kind === "user") b.user = p.inboxID;
    }
    if (type === "session.inbox.cancelled") b.inbox.delete(p.inboxID);
    const compaction = type.startsWith("session.compaction.") || p.type === "compaction";
    if (type === "session.step.started") {
      b.assistant = compaction ? void 0 : p.assistantMessageID;
      b.step = void 0;
      b.forms.clear();
      b.requests.clear();
    }
    if (["session.step.ended", "session.step.failed"].includes(type)) b.step = Object.freeze({
      type,
      eventID: event.id,
      messageID: p.assistantMessageID,
      compaction
    });
    if (["session.execution.succeeded", "session.execution.failed"].includes(type)) {
      b.terminal = Object.freeze({ id: event.id, type, created: event.created, run: b.run, assistant: b.assistant, step: b.step });
      b.forms.clear();
      b.requests.clear();
    }
    if (["session.retry.scheduled", "session.execution.interrupted", "session.compaction.started"].includes(type) || compaction) {
      b.forms.clear();
      b.requests.clear();
    }
    let requestID, messageID, callID;
    if (type === "form.created") {
      const form = p.form;
      requestID = form.id;
      messageID = form.metadata?.tool?.messageID;
      callID = form.metadata?.tool?.id;
      if (id5(requestID) && id5(messageID) && id5(callID) && Array.isArray(form.fields) && form.fields.length > 0) {
        if (b.forms.size >= 64) {
          onUncertainty();
          return { sessionID: sid };
        }
        b.forms.set(requestID, Object.freeze({
          requestID,
          messageID,
          callID,
          run: b.run,
          eventID: event.id,
          created: event.created,
          tool: "question"
        }));
      }
    } else if (type === "permission.asked") {
      requestID = p.id;
      if (p.source?.type === "tool") {
        messageID = p.source.messageID;
        callID = p.source.id;
      }
    } else if (["form.replied", "form.cancelled", "permission.replied"].includes(type)) {
      requestID = type === "permission.replied" ? p.requestID : p.id;
      b.forms.delete(requestID);
      b.requests.delete(requestID);
    } else messageID = p.assistantMessageID;
    if (["form.created", "permission.asked"].includes(type) && id5(requestID) && id5(messageID) && id5(callID)) {
      if (b.requests.size >= 64) {
        onUncertainty();
        return { sessionID: sid };
      }
      b.requests.set(requestID, Object.freeze({ eventID: event.id, created: event.created, run: b.run, messageID, callID }));
    }
    const durable = event.durable;
    return {
      sessionID: sid,
      ...nativeScope ? { location: nativeScope.directory } : {},
      ...messageID !== void 0 ? { messageID } : {},
      ...requestID !== void 0 ? { requestID } : {},
      ...callID !== void 0 ? { callID } : {},
      ...compaction ? { compaction: true } : {},
      ...durable ? { sequence: { aggregate: durable.aggregateID, seq: durable.seq, version: durable.version } } : {}
    };
  }
  const binding = (run) => {
    const b = bindings.get(run.sessionID);
    return b && b.run === run.turnID && b.user === run.userID && b.assistant && (!run.terminalEventID || b.terminal?.id === run.terminalEventID && b.terminal?.created === run.terminalCreated) ? b : void 0;
  };
  async function session(run, signal) {
    const b = binding(run);
    if (!b || signal.aborted) return;
    const info = await context.session.get({ sessionID: run.sessionID });
    if (signal.aborted || binding(run) !== b || info?.id !== run.sessionID || info.parentID !== void 0 && !id5(info.parentID)) return;
    const observed = b.scope;
    const native = scope(info.location);
    if (!native || native.directory !== own.directory || info.projectID !== projectID || observed && !same3(observed, own) || own.workspaceID !== void 0 && !same3(observed, own) || native.workspaceID !== void 0 && native.workspaceID !== own.workspaceID) return;
    return Object.freeze({ ...run, location: native.directory, rootSession: info.parentID === void 0 });
  }
  async function rows(run, signal) {
    const b = binding(run);
    if (!b || signal.aborted) return;
    const value = await context.session.context({ sessionID: run.sessionID });
    if (signal.aborted || binding(run) !== b || !Array.isArray(value) || value.length > 4096) return;
    const seen = /* @__PURE__ */ new Set();
    for (const row of value) {
      if (!id5(row?.id) || seen.has(row.id) || !rowTypes.has(row.type) || row.sessionID !== void 0 && row.sessionID !== run.sessionID) return;
      seen.add(row.id);
    }
    if (value.findLast((row) => row.type === "user")?.id !== b.user) return;
    return value;
  }
  async function assistant(run, signal) {
    const proof = await session(run, signal);
    if (!proof?.rootSession) return;
    const b = binding(run), value = await rows(run, signal);
    if (!value || signal.aborted || binding(run) !== b || b.assistant !== run.messageID) return;
    const terminal = b.terminal, step = terminal?.step;
    const row = value.find((item) => item.id === run.messageID);
    const terminalMessageID = terminal?.id.startsWith("evt_") ? `msg_${terminal.id.slice(4)}` : void 0;
    if (row?.type !== "assistant" || !stamp2(row.time?.completed) || !stamp2(row.time?.created) || followsAssistant(value, row) || step?.messageID !== row.id || step.compaction || !terminalMessageID || value.findIndex((item) => item.type === "idle" && item.id === terminalMessageID) <= value.indexOf(row)) return;
    let outcome;
    if (terminal.type === "session.execution.succeeded" && step.type === "session.step.ended" && row.finish === "stop" && row.error == null) outcome = "success";
    if (terminal.type === "session.execution.failed" && row.error && !cancelled(row.error)) outcome = "error";
    if (!outcome) return;
    return Object.freeze({ ...proof, messageID: row.id, role: "assistant", summary: false, final: true, outcome });
  }
  async function questionSource(run, signal) {
    const b = binding(run), form = b?.forms.get(run.requestID);
    if (!form || form.run !== run.turnID || form.messageID !== run.messageID || form.callID !== run.callID) return;
    const proof = await session(run, signal), value = await rows(run, signal);
    if (!proof?.rootSession || !value || signal.aborted || binding(run) !== b || b.forms.get(run.requestID) !== form || !value.some((row) => row.type === "assistant" && row.id === form.messageID && !followsAssistant(value, row))) return;
    const assistant2 = value.find((row) => row.id === form.messageID && row.type === "assistant");
    if (!Array.isArray(assistant2?.content) || assistant2.content.length > 256) return;
    const tools = assistant2.content.filter((part) => part?.type === "tool" && part.id === form.callID);
    if (tools.length !== 1 || tools[0].name !== "question") return;
    return Object.freeze({
      ...proof,
      requestID: form.requestID,
      messageID: form.messageID,
      callID: form.callID,
      role: "assistant",
      tool: tools[0].name,
      summary: false
    });
  }
  async function currentPermission(run, signal) {
    const b = binding(run);
    if (!b || signal.aborted) return;
    const proof = await session(run, signal);
    if (!proof?.rootSession || signal.aborted || binding(run) !== b) return;
    const request = await context.permission.get({ sessionID: run.sessionID, requestID: run.requestID });
    if (!proof?.rootSession || signal.aborted || binding(run) !== b || request?.id !== run.requestID || request.sessionID !== run.sessionID || request.source?.type !== "tool" || request.source.messageID !== b.assistant || !id5(request.source.id)) return;
    return Object.freeze({
      ...proof,
      requestID: request.id,
      messageID: request.source.messageID,
      callID: request.source.id,
      pending: true
    });
  }
  async function finalize(event, handoff) {
    const b = bindings.get(event.sessionID);
    if (!b || !project(event) || !handoff.isCurrent() || handoff.signal.aborted) return false;
    const run = Object.freeze({
      sessionID: event.sessionID,
      turnID: b.run,
      userID: b.user,
      location: own.directory,
      terminalEventID: event.requestID ? void 0 : b.terminal?.id,
      terminalCreated: event.requestID ? void 0 : b.terminal?.created
    });
    const proof = await session(run, handoff.signal);
    if (!proof?.rootSession || !handoff.isCurrent() || handoff.signal.aborted) return false;
    if (!event.requestID) {
      const answer = await assistant({ ...run, messageID: b.assistant }, handoff.signal);
      return handoff.isCurrent() && answer?.final === true && answer.outcome === (event.kind === "terminal_error" ? "error" : "success");
    }
    const value = await rows(run, handoff.signal);
    return handoff.isCurrent() && !handoff.signal.aborted && binding(run) === b && b.requests.has(event.requestID) && value?.some((row) => row.type === "assistant" && row.id === b.assistant && !followsAssistant(value, row));
  }
  function project(event) {
    const b = bindings.get(event.sessionID);
    if (!b || !id5(b.run) || event.turnID !== b.user || event.provenance?.generation !== "v2") return;
    const source = event.requestID ? b.requests.get(event.requestID) : b.terminal;
    if (!source || source.run !== b.run || (source.eventID ?? source.id) !== event.provenance.nativeEventID || source.created !== event.provenance.nativeTime) return;
    let value = projected.get(event);
    if (!value || value.turnID !== b.run) {
      value = Object.freeze({ ...event, turnID: b.run });
      projected.set(event, value);
    }
    return value;
  }
  return Object.freeze({ native: Object.freeze({ correlate, session, assistant, questionSource, currentPermission }), reset, project, finalize });
}
var checkpointNonce = (value) => typeof value === "string" && value.length === 32 && /^[a-f0-9]{32}$/.test(value);
function createRPCCheckpoint(context, ready) {
  return Object.freeze({ async register(signal, namespace) {
    if (!await ready() || signal.aborted) return;
    const id6 = `agent-notifications-${namespace}`;
    const schema = {
      type: "object",
      properties: { nonce: { type: "string", minLength: 32, maxLength: 32 } },
      required: ["nonce"],
      additionalProperties: false
    };
    const registration = await context.rpc.register({ id: id6, methods: {}, events: { checkpoint: { schema } } }, {});
    if (signal.aborted) {
      await registration.dispose();
      return;
    }
    return Object.freeze({
      type: `rpc.${id6}.checkpoint`,
      emit: async (nonce) => {
        if (!checkpointNonce(nonce)) throw new TypeError("checkpoint_nonce_invalid");
        return registration.events.emit("checkpoint", { nonce });
      },
      read: (envelope) => {
        const data = envelope?.data;
        return data && typeof data === "object" && !Array.isArray(data) && Reflect.ownKeys(data).length === 1 && Object.hasOwn(data, "nonce") && checkpointNonce(data.nonce) ? data.nonce : void 0;
      },
      dispose: () => registration.dispose()
    });
  } });
}

// plugin.mjs
var servers = /* @__PURE__ */ new WeakMap();
var setups = /* @__PURE__ */ new WeakMap();
var silent = Object.freeze({ event() {
} });
async function server(input) {
  if (!input?.client || typeof input.client.session?.get !== "function" || typeof input.client.session?.messages !== "function") return silent;
  const existing = servers.get(input.client);
  if (existing) {
    if (existing.directory !== input.directory) {
      existing.retired = true;
      existing.stop?.();
      return silent;
    }
    return existing.starting;
  }
  const holder = { directory: input.directory, retired: false };
  const starting = (async () => {
    const owned = await prepareOwnedHost(input.directory, "v1");
    if (!owned) return silent;
    if (holder.retired) {
      await owned.dispose();
      return silent;
    }
    const policy = selectClockCell("v1");
    if (!policy) {
      await owned.dispose();
      return silent;
    }
    let observer, view, stopped = false;
    const publications = /* @__PURE__ */ new WeakSet();
    const delivery = createPreparedDelivery({
      registry: owned.registry,
      origin: owned.origin,
      policy,
      sourceFactory: createPlatformClock,
      isOwned: owned.isOwned,
      onInvalidate: () => observer?.dispose()
    });
    view = createNativeV1(input.client, input.directory, delivery.invalidate);
    const stop = () => {
      if (stopped) return;
      stopped = true;
      observer?.dispose();
      view.dispose();
      delivery.dispose();
      void owned.dispose();
    };
    holder.stop = stop;
    try {
      if (!await delivery.activate() || holder.retired) {
        stop();
        return silent;
      }
      observer = createObserver({
        client: input.client,
        location: input.directory,
        runtimeEligibility: owned.runtimeEligibility,
        callbackAuthority: "qualified_native_sync",
        clock: delivery.clock,
        beforeEmit: async (event, handoff) => await delivery.beforeEmit(event, handoff) && await view.finalize(event, handoff),
        emit: delivery.emit
      });
      return Object.freeze({ event({ event }) {
        if (stopped) return;
        if (event && typeof event === "object") {
          if (publications.has(event)) return;
          publications.add(event);
        }
        try {
          delivery.beginIngress(event);
          if (event?.type === "location.shutdown" || event?.type === "server.instance.disposed") {
            stop();
            return;
          }
          if (event?.type === "message.updated" && event.properties?.info?.role === "user" && !delivery.allowsBirth(event.properties.info.time?.created)) return;
          view.ingest(event);
          void observer.observe(event).catch(stop);
        } catch {
          stop();
        }
      } });
    } catch {
      stop();
      return silent;
    }
  })();
  holder.starting = starting;
  servers.set(input.client, holder);
  return starting;
}
async function setup(context) {
  if (!context || typeof context.event?.subscribe !== "function" || typeof context.session?.get !== "function" || typeof context.session?.context !== "function" || typeof context.permission?.get !== "function" || typeof context.permission?.list !== "function" || typeof context.rpc?.register !== "function") return;
  if (setups.has(context)) return setups.get(context);
  const starting = (async () => {
    const owned = await prepareOwnedHost(context.location?.directory, "v2", context.app?.version);
    if (!owned) return;
    const policy = selectClockCell("v2");
    if (!policy) {
      await owned.dispose();
      return;
    }
    let observer, view, stopped = false, connection;
    const location2 = Object.freeze({
      directory: context.location.directory,
      workspaceID: context.location.workspaceID,
      projectID: context.location.project?.id
    });
    const isNativeOwned = () => owned.isOwned() && context.app?.version === "2.0.21" && context.location?.directory === location2.directory && context.location?.workspaceID === location2.workspaceID && context.location?.project?.id === location2.projectID;
    const delivery = createPreparedDelivery({
      registry: owned.registry,
      origin: owned.origin,
      policy,
      sourceFactory: createPlatformClock,
      isOwned: isNativeOwned,
      onInvalidate: (reason) => {
        if (reason === "clock") observer?.dispose();
        view?.reset();
        connection?.abort();
      }
    });
    const stop = async () => {
      if (stopped) return;
      stopped = true;
      observer?.dispose();
      delivery.dispose();
      connection?.abort();
      view?.reset();
      return owned.dispose();
    };
    try {
      view = createNativeV2(context, context.location, delivery.nativeIngress, delivery.invalidate);
      if (!await delivery.activate()) {
        await stop();
        return;
      }
      const readerContext = { app: context.app, event: { subscribe({ signal }) {
        connection = new AbortController();
        const abort = () => {
          delivery.invalidate("reader");
          current.abort();
        };
        signal.addEventListener("abort", abort, { once: true });
        const current = connection;
        let iterator;
        const opening = delivery.activate().then((ready) => {
          if (!ready || stopped || signal.aborted || current.signal.aborted) throw new TypeError("reader_unverified");
          iterator = context.event.subscribe({ signal: current.signal })[Symbol.asyncIterator]();
        });
        return { [Symbol.asyncIterator]() {
          return this;
        }, async next() {
          try {
            await opening;
            const item = await iterator.next();
            if (item.done) delivery.invalidate("reader");
            return item;
          } catch {
            delivery.invalidate("reader");
            throw new TypeError("reader_unverified");
          }
        }, async return() {
          signal.removeEventListener("abort", abort);
          delivery.invalidate("reader");
          current.abort();
          await opening.catch(() => {
          });
          return iterator?.return ? iterator.return() : { done: true };
        } };
      } } };
      observer = createV2Observer({
        context: readerContext,
        location: context.location.directory,
        runtimeEligibility: () => isNativeOwned() ? owned.runtimeEligibility() : "unverified",
        native: view.native,
        checkpoint: createRPCCheckpoint(context, delivery.activate),
        clock: delivery.clock,
        beforeEmit: async (event, handoff) => {
          const fact = view.project(event);
          return Boolean(fact && await delivery.beforeEmit(fact, handoff) && await view.finalize(event, handoff));
        },
        emit: (event, handoff) => {
          const fact = view.project(event);
          return fact ? delivery.emit(fact, handoff) : void 0;
        }
      });
      observer.start();
      return stop;
    } catch {
      await stop();
    }
  })();
  setups.set(context, starting);
  return starting;
}
var AgentNotifications = (input) => server(input);
var plugin_default = Object.freeze({ id: "agent-notifications", server, setup });
export {
  AgentNotifications,
  plugin_default as default
};

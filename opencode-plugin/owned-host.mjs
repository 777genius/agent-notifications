import { realpathSync, statSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { absoluteNativePath, createProcessRegistry } from './process-registry.mjs';
import { profileReceipt } from './protocol.mjs';

const executable = "__AGENT_NOTIFICATIONS_EXECUTABLE__";
const controlRoot = "__AGENT_NOTIFICATIONS_CONTROL_ROOT__";
const origin = "__AGENT_NOTIFICATIONS_ORIGIN__";
const identity = (name) => {
  if (!absoluteNativePath(name) || realpathSync(name) !== name) throw new TypeError('binding_unverified');
  const stat = statSync(name, { bigint: true });
  return Object.freeze({ name, dev: stat.dev, ino: stat.ino, size: stat.isDirectory() ? undefined : stat.size, mtime: stat.isDirectory() ? undefined : stat.mtimeNs, mode: stat.mode });
};
const equal = (a, b) => ['name', 'dev', 'ino', 'size', 'mtime', 'mode'].every((key) => a[key] === b[key]);
export async function prepareOwnedHost(directory, generation, appVersion, diagnostics = false) {
  let registry, privateCwd;
  try {
    if (!/^[a-f0-9]{64}$/.test(origin) || !absoluteNativePath(directory)) return;
    const files = [executable, controlRoot, directory, process.execPath].map(identity);
    const isOwned = () => {
      try { return files.every((original) => equal(original, identity(original.name))); } catch { return false; }
    };
    privateCwd = mkdtempSync(join(tmpdir(), 'agent-notifications-'));
    const deliveryKeys = process.platform === 'win32' ?
      ['AGENT_NOTIFICATIONS_CONFIG', 'USERPROFILE', 'HOMEDRIVE', 'HOMEPATH', 'APPDATA', 'LOCALAPPDATA', 'TEMP', 'TMP'] :
      ['AGENT_NOTIFICATIONS_CONFIG', 'HOME', 'XDG_CONFIG_HOME', 'DBUS_SESSION_BUS_ADDRESS', 'XDG_RUNTIME_DIR'];
    const pick = (keys) => Object.fromEntries(keys.filter((key) => process.env[key] !== undefined).map((key) => [key, process.env[key]]));
    registry = createProcessRegistry({ executable, controlRoot, origin, privateCwd,
      diagnostics, osEnv: pick(process.platform === 'win32' ? ['SystemRoot', 'WINDIR'] : []), deliveryEnv: pick(deliveryKeys) });
    const response = await registry.profile({ isCurrent: isOwned });
    const receipt = profileReceipt(response.output);
    // generation is the closed helper contract, not raw semver/range authority.
    // The helper's only V2 image is 2.0.21; ctx.app independently must agree.
    if (response.status !== 'ok' || receipt.semantic !== 'eligible' || receipt.generation !== generation ||
        !isOwned() || generation === 'v2' && appVersion !== '2.0.21') throw new TypeError('runtime_unverified');
    let disposed = false;
    return Object.freeze({ registry, origin, isOwned: () => !disposed && isOwned(),
      runtimeEligibility: () => !disposed && isOwned() ? 'supported' : 'unverified',
      async dispose() {
        disposed = true;
        const status = await registry.dispose();
        if (status.reaped) { try { rmSync(privateCwd, { recursive: true }); } catch {} }
        return status;
      } });
  } catch {
    if (registry) {
      const status = await registry.dispose();
      if (!status.reaped) return;
    }
    if (privateCwd) { try { rmSync(privateCwd, { recursive: true }); } catch {} }
  }
}

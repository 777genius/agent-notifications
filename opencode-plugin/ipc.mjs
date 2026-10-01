import { spawn } from 'node:child_process';
import path from 'node:path';

// The product installer replaces this exact token with an owned absolute path.
const executable = '__AGENT_NOTIFICATIONS_EXECUTABLE__';
const controlRoot = '__AGENT_NOTIFICATIONS_CONTROL_ROOT__';
const maxWireBytes = 4096;
const maxReceiptBytes = 1024;
// Desktop delivery and one webhook POST are sequential, each bounded to 10 seconds.
const processTimeoutMs = 25000;

const commonEnvironment = ['AGENT_NOTIFICATIONS_CONFIG'];
const posixEnvironment = ['HOME', 'XDG_CONFIG_HOME', 'DBUS_SESSION_BUS_ADDRESS', 'XDG_RUNTIME_DIR'];
const windowsEnvironment = ['SystemRoot', 'WINDIR', 'USERPROFILE', 'HOMEDRIVE', 'HOMEPATH', 'APPDATA', 'LOCALAPPDATA', 'TEMP', 'TMP'];

function absoluteNativePath(value, platform) {
  if (typeof value !== 'string' || value.includes('\0')) return false;
  if (platform !== 'win32') return path.posix.isAbsolute(value);
  // Reject root-relative, drive-relative and device namespace paths. The
  // installer owns ordinary canonical drive or UNC paths only.
  if (value.startsWith('\\\\?\\') || value.startsWith('\\\\.\\')) return false;
  const drive = /^[A-Za-z]:\\/.test(value);
  const unc = /^\\\\[^\\]+\\[^\\]+\\/.test(value);
  return (drive || unc) && path.win32.normalize(value) === value;
}

export async function forward(event, spawnProcess = spawn, binary = executable, root = controlRoot, platform = process.platform) {
  const body = Buffer.from(JSON.stringify(event));
  if (body.length > maxWireBytes || !absoluteNativePath(binary, platform) || !absoluteNativePath(root, platform) || (platform === 'win32' && !binary.toLowerCase().endsWith('.exe'))) return 'invalid_plugin';
  return new Promise((resolve) => {
    let settled = false;
    let timer;
    const finish = (result) => { if (!settled) { settled = true; clearTimeout(timer); resolve(result); } };
    let child;
    try {
      child = spawnProcess(binary, ['opencode-event', '--protocol', '1'], {
        shell: false,
        windowsHide: true,
        stdio: ['pipe', 'pipe', 'pipe'],
        env: { ...Object.fromEntries([...commonEnvironment, ...(platform === 'win32' ? windowsEnvironment : posixEnvironment)]
          .filter((key) => process.env[key] !== undefined)
          .map((key) => [key, process.env[key]])), AGENT_NOTIFICATIONS_CONTROL_ROOT: root },
      });
    } catch { finish('spawn_failed'); return; }
    let stdout = Buffer.alloc(0), stderrBytes = 0;
    timer = setTimeout(() => { child.kill(); finish('timeout'); }, processTimeoutMs);
    child.on('error', () => { clearTimeout(timer); finish('spawn_failed'); });
    child.stdout.on('data', (chunk) => {
      stdout = Buffer.concat([stdout, chunk]);
      if (stdout.length > maxReceiptBytes) { child.kill(); finish('invalid_receipt'); }
    });
    child.stderr.on('data', (chunk) => {
      stderrBytes += chunk.length;
      if (stderrBytes > maxReceiptBytes) { child.kill(); finish('invalid_receipt'); }
    });
    child.stdin.on('error', () => { child.kill(); finish('write_failed'); });
    child.on('close', (code) => {
      clearTimeout(timer);
      if (settled) return;
      if (code !== 0) { finish('rejected'); return; }
      try {
        const receipt = JSON.parse(stdout.toString('utf8'));
        finish(['submitted', 'rejected', 'suppressed', 'unknown'].includes(receipt.status) ? receipt.status : 'invalid_receipt');
      } catch { finish('invalid_receipt'); }
    });
    child.stdin.end(body);
  });
}

// V2 ingress keeps reducing native events while deliveries are in progress.
// Hold capacity until the child closes, including after a timeout requests kill.
export function createBoundedForwarder({ limit = 16, spawnProcess = spawn, binary = executable, root = controlRoot, platform = process.platform } = {}) {
  if (!Number.isInteger(limit) || limit < 1 || limit > 64) throw new RangeError('invalid delivery limit');
  let active = 0;
  let disposed = false;
  return {
    async forward(event) {
      if (disposed) return 'suppressed';
      if (active >= limit) return 'capacity';
      return forward(event, (...args) => {
        const child = spawnProcess(...args);
        active++;
        child.once('close', () => { active--; });
        return child;
      }, binary, root, platform);
    },
    dispose() { disposed = true; },
  };
}

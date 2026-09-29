import { spawn } from 'node:child_process';

// The product installer replaces this exact token with an owned absolute path.
const executable = '__AGENT_NOTIFICATIONS_EXECUTABLE__';
const controlRoot = '__AGENT_NOTIFICATIONS_CONTROL_ROOT__';
const maxWireBytes = 4096;
const maxReceiptBytes = 1024;
// Linux desktop and one webhook POST are sequential, each bounded to 10 seconds.
const processTimeoutMs = 25000;

export async function forward(event, spawnProcess = spawn, binary = executable, root = controlRoot) {
  const body = Buffer.from(JSON.stringify(event));
  if (body.length > maxWireBytes || !binary.startsWith('/') || binary.includes('\0') || !root.startsWith('/') || root.includes('\0')) return 'invalid_plugin';
  return new Promise((resolve) => {
    let settled = false;
    let timer;
    const finish = (result) => { if (!settled) { settled = true; clearTimeout(timer); resolve(result); } };
    let child;
    try {
      child = spawnProcess(binary, ['opencode-event', '--protocol', '1'], {
        shell: false,
        stdio: ['pipe', 'pipe', 'pipe'],
        env: { ...Object.fromEntries(['HOME', 'XDG_CONFIG_HOME', 'DBUS_SESSION_BUS_ADDRESS', 'XDG_RUNTIME_DIR', 'AGENT_NOTIFICATIONS_CONFIG']
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

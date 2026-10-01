'use strict';
// Exact CLI-installed PTY only. No global resolution, transcript, or terminal framework.
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const readline = require('node:readline');
const {createRequire} = require('node:module');
let child, deadline, killDeadline, sequence, pattern, forced = false;
let tail = '', total = 0, permissionSeen = false, stopped = false;
const diagnostics = new Set();
const emit = data => process.stdout.write(JSON.stringify(data) + '\n');
const check = (ok, code) => { if (!ok) throw new Error(code); };
const hash = file => crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex');
const contains = (file, root) => {
  const rel = path.relative(root, file);
  return rel === '' || (!rel.startsWith('..' + path.sep) && rel !== '..' && !path.isAbsolute(rel));
};
function stop() {
  if (!child || stopped) return;
  stopped = true;
  forced = true;
  // node-pty handle owns exactly the process started below, including ConPTY.
  try {
    if (process.platform === 'win32') child.kill();
    else process.kill(-child.pid, 'SIGTERM'); // forkpty owns this new session/group.
  } catch { emit({error: 'PTY_stop_failed'}); }
  killDeadline = setTimeout(() => {
    try {
      if (process.platform === 'win32') child.kill();
      else process.kill(-child.pid, 'SIGKILL');
    } catch {}
    emit({error: 'PTY_shutdown_unconfirmed'});
    process.exitCode = 1;
    process.stdin.destroy();
  }, 2500);
}
function start(m) {
  check(!child, 'duplicate_start');
  const root = fs.realpathSync(m.installRoot);
  check(root === m.installRoot && fs.existsSync(path.join(root, '.an-gemini-TEST')), 'TEST_installation_required');
  const exe = fs.realpathSync(m.executable);
  check(exe === m.executable && contains(exe, root), 'CLI_physical_path_required');
  let cliPackage;
  for (let p = path.dirname(exe); contains(p, root); p = path.dirname(p)) {
    const file = path.join(p, 'package.json');
    if (fs.existsSync(file) && JSON.parse(fs.readFileSync(file)).name === '@google/gemini-cli') {
      cliPackage = file; break;
    }
    if (p === path.dirname(p)) break;
  }
  check(cliPackage, 'CLI_package_missing');
  const cli = JSON.parse(fs.readFileSync(cliPackage));
  check(cli.version === '0.62.0' && cli.optionalDependencies['@lydell/node-pty'] === '1.1.0', 'CLI_PTY_pin_mismatch');
  const req = createRequire(cliPackage);
  const entry = fs.realpathSync(req.resolve('@lydell/node-pty'));
  check(contains(entry, root), 'PTY_outside_explicit_installation');
  let pkg;
  for (let p = path.dirname(entry); contains(p, root); p = path.dirname(p)) {
    const file = path.join(p, 'package.json');
    if (fs.existsSync(file) && JSON.parse(fs.readFileSync(file)).name === '@lydell/node-pty') { pkg = file; break; }
    if (p === path.dirname(p)) break;
  }
  check(pkg && JSON.parse(fs.readFileSync(pkg)).version === '1.1.0', 'PTY_identity_mismatch');
  const pty = req(entry); // Loads the CLI's packaged native platform module, never npm scripts.
  const addons = Object.keys(require.cache).filter(x => x.endsWith('.node'));
  check(addons.length > 0 && addons.every(x => contains(fs.realpathSync(x), root)), 'native_PTY_backend_unverified');
  const allowed = new Set(['HOME','USERPROFILE','GEMINI_CLI_HOME','XDG_CONFIG_HOME','XDG_CACHE_HOME','XDG_DATA_HOME','XDG_STATE_HOME',
    'TMPDIR','TMP','TEMP','GEMINI_CLI_SYSTEM_SETTINGS_PATH','GEMINI_CLI_SYSTEM_DEFAULTS_PATH','GEMINI_CLI_TRUSTED_FOLDERS_PATH',
    'GEMINI_API_KEY','GEMINI_FORCE_FILE_STORAGE','GOOGLE_GEMINI_BASE_URL','TERM','PATH','LANG','LC_ALL','SystemRoot','ComSpec']);
  check(Object.keys(m.env).every(k => allowed.has(k)), 'environment_not_allowlisted');
  const cwd = fs.realpathSync(m.cwd), lab = path.dirname(cwd);
  check(cwd === m.cwd && path.basename(cwd) === 'profile' && fs.existsSync(path.join(lab, '.an-gemini-TEST')), 'TEST_cwd_required');
  check(['HOME','USERPROFILE','GEMINI_CLI_HOME'].every(k => m.env[k] === cwd), 'home_mismatch');
  check(m.env.GEMINI_API_KEY === 'an-gemini-test-not-a-secret', 'synthetic_key_required');
  check(/^http:\/\/127\.0\.0\.1:[0-9]+$/.test(m.env.GOOGLE_GEMINI_BASE_URL), 'loopback_provider_required');
  check(Number.isInteger(m.timeoutMs) && m.timeoutMs >= 60000 && m.timeoutMs <= 240000, 'watchdog_bounds');
  pattern = new RegExp(m.permissionPattern, 'is');
  child = pty.spawn(m.node, [exe, '--model', 'gemini-2.5-flash', '--approval-mode', 'default', '--skip-trust', '--prompt-interactive', 'AN_TEST_PLAIN'], {
    name: 'xterm-256color', cols: 120, rows: 40, cwd, env: m.env
  });
  deadline = setTimeout(() => { emit({error: 'native_watchdog_timeout'}); stop(); }, m.timeoutMs);
  child.onData(data => {
    total += Buffer.byteLength(data);
    if (total > 8 * 1024 * 1024) { emit({error: 'terminal_output_limit'}); stop(); return; }
    // Strip CSI/OSC before matching genuine rendered UI; never send its text.
    tail = (tail + data).slice(-65536);
    const plain = tail.replace(/\x1b\][^\x07]*(?:\x07|\x1b\\)/g, '').replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, '');
    for (const [label, re] of [['auth_error', /Invalid auth|authentication.*error|No authentication/i], ['startup_welcome', /Welcome to Gemini/i], ['startup_theme', /Choose.*theme|Select.*theme/i], ['startup_trust', /trust this folder|Do you trust/i], ['startup_update', /update.*available/i], ['startup_model', /Select.*model|Choose.*model/i], ['startup_terms', /Terms of Service|usage statistics/i], ['startup_continue', /press.*enter|press.*key/i], ['startup_error', /Error:|error occurred|not supported/i], ['prompt_seen', /AN_TEST_PLAIN/i], ['turn_response_seen', /TEST turn finished/i]]) {
      if (!diagnostics.has(label) && re.test(plain)) {diagnostics.add(label); emit({diagnostic: label});}
    }
    if (sequence && !permissionSeen && pattern.test(plain)) {
      permissionSeen = true;
      emit({permission: sequence});
    }
  });
  child.onExit(({exitCode, signal}) => {
    clearTimeout(deadline); clearTimeout(killDeadline);
    emit({exit: {code: exitCode, signal: signal || 0, forced, terminal_bytes: total}});
    child = undefined;
    process.stdin.destroy();
  });
  emit({ready: {module: '@lydell/node-pty', version: '1.1.0', platform: process.platform, node_version: process.version,
    package_sha256: hash(pkg), entry_sha256: hash(entry), native_backend_sha256: addons.map(hash)}});
}
const input = readline.createInterface({input: process.stdin, crlfDelay: Infinity});
input.on('line', line => {
  try {
    check(Buffer.byteLength(line) <= 65536, 'bridge_input_limit');
    const m = JSON.parse(line);
    if (m.op === 'start') start(m);
    else if (m.op === 'watch') {
      check(child && ['plain','equal','approve','deny','cancel','recovery'].includes(m.case), 'unknown_case');
      sequence = m.case; tail = ''; permissionSeen = false;
    } else if (m.op === 'write') {
      check(child && typeof m.data === 'string' && m.data.length <= 256, 'write_bounds');
      child.write(m.data);
    } else if (m.op === 'stop') stop();
    else throw new Error('unknown_operation');
  } catch {
    emit({error: 'bridge_protocol_or_startup_error', native_child_started: Boolean(child)}); process.exitCode = 1;
    if (child) stop(); else process.stdin.destroy();
  }
});
input.on('close', () => { if (child) stop(); });
process.on('SIGTERM', stop);
process.on('SIGINT', stop);

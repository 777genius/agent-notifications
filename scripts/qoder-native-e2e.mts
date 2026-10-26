/** Managed Qoder lifecycle qualification. Never fabricates a hook payload. */
import { spawn } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import { createServer } from 'node:http';
import { existsSync, mkdirSync, readFileSync, realpathSync, writeFileSync } from 'node:fs';
import { basename, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

// Optional deterministic OpenAI-compatible TEST provider. Configure it only
// through Qoder's supported Custom wizard; this never edits provider settings.
if (process.argv[2] === 'provider' || process.argv[2] === 'provider-check') {
  const selfCheck = process.argv[2] === 'provider-check';
  const port = selfCheck ? 0 : Number(process.argv[3]);
  if (!selfCheck && (!Number.isInteger(port) || port < 1024 || port > 65535)) throw Error('explicit unprivileged loopback port required');
  let count = 0;
  const provider = createServer((req, res) => {
    let body = '';
    req.on('data', chunk => { body += String(chunk); if (body.length > 8_000_000) req.destroy(); });
    req.on('end', () => {
      process.stdout.write(JSON.stringify({ request: ++count, path: req.url, bytes: body.length }) + '\n');
      if (req.method === 'GET' && req.url === '/v1/models') {
        res.setHeader('content-type', 'application/json');
        res.end(JSON.stringify({ object: 'list', data: [{ id: 'TEST-qoder-local', object: 'model', owned_by: 'TEST' }] }));
        return;
      }
      if (req.method !== 'POST' || req.url !== '/v1/chat/completions') {
        res.writeHead(404, { 'content-type': 'application/json' }).end(JSON.stringify({ error: { message: 'TEST endpoint unavailable' } })); return;
      }
      let input: { stream?: boolean; messages?: unknown[] } = {};
      try { input = JSON.parse(body); } catch { res.writeHead(400).end(); return; }
      if (!Array.isArray(input.messages)) { res.writeHead(400).end(); return; }
      const common = { id: `TEST-${count}`, created: Math.floor(Date.now() / 1000), model: 'TEST-qoder-local' };
      if (input.stream) {
        res.setHeader('content-type', 'text/event-stream');
        res.write('data: ' + JSON.stringify({ ...common, object: 'chat.completion.chunk', choices: [{ index: 0, delta: { role: 'assistant', content: 'TEST_QODER_DONE' }, finish_reason: null }] }) + '\n\n');
        res.write('data: ' + JSON.stringify({ ...common, object: 'chat.completion.chunk', choices: [{ index: 0, delta: {}, finish_reason: 'stop' }], usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 } }) + '\n\n');
        res.end('data: [DONE]\n\n');
      } else {
        res.setHeader('content-type', 'application/json');
        res.end(JSON.stringify({ ...common, object: 'chat.completion', choices: [{ index: 0, message: { role: 'assistant', content: 'TEST_QODER_DONE' }, finish_reason: 'stop' }], usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 } }));
      }
    });
  });
  await new Promise<void>(ok => provider.listen(port, '127.0.0.1', ok));
  const bound = provider.address();
  if (!bound || typeof bound === 'string') throw Error('loopback listener unavailable');
  const baseURL = `http://127.0.0.1:${bound.port}/v1`;
  process.stdout.write(`TEST provider ready ${baseURL}\n`);
  if (selfCheck) {
    try {
      const models = await (await fetch(baseURL + '/models')).json() as { data: { id: string }[] };
      if (models.data[0]?.id !== 'TEST-qoder-local') throw Error('model listing mismatch');
      for (const stream of [false, true]) {
        const response = await fetch(baseURL + '/chat/completions', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ stream, messages: [{ role: 'user', content: 'TEST' }] }) });
        const body = await response.text();
        if (response.status !== 200 || !body.includes('TEST_QODER_DONE') || stream && !body.endsWith('data: [DONE]\n\n')) throw Error('completion contract mismatch');
      }
      if ((await fetch(baseURL + '/unknown')).status !== 404) throw Error('unknown endpoint accepted');
      process.stdout.write('TEST provider self-check passed\n');
    } finally { await new Promise<void>(ok => provider.close(() => ok())); }
    process.exit(0);
  }
  await new Promise<void>(ok => {
    process.once('SIGTERM', () => provider.close(() => ok()));
    process.once('SIGINT', () => provider.close(() => ok()));
  });
  process.exit(0);
}

const [cliArg, binaryArg, rootArg, mode = 'lifecycle', nativeAppArg, model] = process.argv.slice(2);
const nativeApp = nativeAppArg === '-' ? undefined : nativeAppArg;
if (!cliArg || !binaryArg || !rootArg || !['lifecycle', 'turn', 'resume-turn'].includes(mode)) {
  throw Error('usage: node qoder-native-e2e.mts QODER_CLI NOTIFICATIONS_BINARY TEST_ROOT [lifecycle|turn|resume-turn] [NATIVE_APP|-] [MODEL]');
}
const turn = mode !== 'lifecycle';
const cli = realpathSync(cliArg), binary = realpathSync(binaryArg), root = resolve(rootArg);
if (!basename(root).startsWith('TEST-')) throw Error('TEST- root required');
// Human login may precede the explicit turn run. Never read authentication files.
if (existsSync(root)) {
  if (!turn || realpathSync(root) !== root) throw Error('existing root requires explicit turn mode and physical TEST path');
} else mkdirSync(root, { mode: 0o700 });
for (const dir of ['home', 'profile', 'project']) {
  const path = join(root, dir);
  if (existsSync(path)) {
    if (!turn || realpathSync(path) !== path) throw Error('existing profile must be a physical TEST directory');
  } else mkdirSync(path, { mode: 0o700 });
}
const control = join(root, 'control');
const env: NodeJS.ProcessEnv = {
  PATH: process.env.PATH, HOME: join(root, 'home'), QODER_CONFIG_DIR: join(root, 'profile'),
  AGENT_NOTIFICATIONS_CONFIG: join(root, 'notifications.json'),
};
const hash = (path: string): string => createHash('sha256').update(readFileSync(path)).digest('hex');
const receipt = join(root, `evidence-${Date.now()}-${randomUUID()}.json`);
const turnSessionID = randomUUID();
const executions: unknown[] = [];
const evidence: Record<string, unknown> = { nativeCLI: cli, binary, binarySHA256: hash(binary),
  harnessSHA256: hash(fileURLToPath(import.meta.url)), cliSHA256: hash(cli), mode, model,
  turnSessionID, nativeTurn: false, desktopObserved: false, executions };
async function run(executable: string, args: string[]): Promise<string> {
  return await new Promise((ok, fail) => {
    const child = spawn(executable, args, { cwd: join(root, 'project'), env, stdio: ['ignore', 'pipe', 'pipe'] });
    let stdout = '', stderr = '';
    child.stdout.on('data', b => { stdout += String(b); });
    child.stderr.on('data', b => { stderr += String(b); });
    let timedOut = false;
    let forceTimer: ReturnType<typeof setTimeout> | undefined;
    const timer = setTimeout(() => {
      timedOut = true; child.kill('SIGTERM');
      forceTimer = setTimeout(() => child.kill('SIGKILL'), 5000);
    }, 120_000);
    child.on('error', fail);
    child.on('close', (code, signal) => {
      clearTimeout(timer); if (forceTimer) clearTimeout(forceTimer);
      let processAbsent = false;
      try { if (child.pid) process.kill(child.pid, 0); } catch { processAbsent = true; }
      executions.push({ executable, args, pid: child.pid, code, signal, timedOut, processClosed: true, processAbsent,
        stdout: stdout.slice(-8000), stderr: stderr.slice(-2000) });
      code === 0 ? ok(stdout) : fail(Error(`native CLI exit ${code}: ${stderr.slice(0, 500)}`));
    });
  });
}
const deliveries: string[] = [];
const server = createServer((req, res) => {
  let body = ''; req.on('data', b => { body += String(b); });
  req.on('end', () => { deliveries.push(body); res.end('ok'); });
});
await new Promise<void>(ok => server.listen(0, '127.0.0.1', ok));
const setup = (action: string): string[] => ['setup-qoder', action,
  '--control-root', control, '--home', env.HOME!, '--config-root', env.QODER_CONFIG_DIR!, '--qoder-executable', cli,
  ...(action === 'remove' ? [] : ['--binary', binary, '--desktop=' + Boolean(nativeApp), '--webhook=true', ...(nativeApp ? ['--native-app', realpathSync(nativeApp)] : [])]),
];
try {
  const address = server.address();
  if (!address || typeof address === 'string') throw Error('loopback endpoint unavailable');
  writeFileSync(env.AGENT_NOTIFICATIONS_CONFIG!, JSON.stringify({ schemaVersion: 2, notifications: {
    desktop: { enabled: Boolean(nativeApp), sound: false, clickToFocus: false },
    webhook: { enabled: true, preset: 'custom', format: 'json', url: `http://127.0.0.1:${address.port}` },
  } }), { mode: 0o600 });
  evidence.version = (await run(cli, ['--version'])).trim();
  const initial = JSON.parse(await run(cli, ['plugins', 'list', '--json']));
  if (mode === 'resume-turn') {
    const status = JSON.parse(await run(binary, ['setup-qoder', 'inspect', '--control-root', control, '--config-root', env.QODER_CONFIG_DIR!]));
    if (status.status !== 'installed' || initial.length !== 1 || initial[0].id !== 'agent-notifications-qoder@local' || !initial[0].enabled) throw Error('resume requires exact managed Qoder consumer and native identity');
    await run(binary, setup('update'));
  } else {
    if (initial.length !== 0) throw Error('fresh profile not empty');
    await run(binary, setup('install'));
  }
  const first = JSON.parse(await run(cli, ['plugins', 'list', '--json']));
  if (first.length !== 1 || first[0].id !== 'agent-notifications-qoder@local' || !first[0].enabled) throw Error('unexpected native identity');
  evidence.install = first;
  // Native settings saves must remain usable after our transaction; in
  // particular, installer lock files must not collide with Qoder's dir locks.
  await run(cli, ['plugins', 'disable', first[0].id, '--scope', 'user']);
  await run(cli, ['plugins', 'enable', first[0].id, '--scope', 'user']);
  const enabled = JSON.parse(await run(cli, ['plugins', 'list', '--json']));
  if (enabled.length !== 1 || enabled[0].id !== first[0].id || !enabled[0].enabled) throw Error('native settings save failed');
  evidence.nativeSettingsSave = true;
  if (turn) {
    evidence.turnOutput = await run(cli, ['-p', '--tools', '', '--strict-mcp-config', '--no-session-persistence', '--session-id', turnSessionID, '--max-model-request-retries', '0', ...(model ? ['--model', model] : []), 'Reply exactly TEST_QODER_DONE']);
    if (String(evidence.turnOutput).trim() !== 'TEST_QODER_DONE') throw Error('native model completion marker missing');
    if (deliveries.length !== 1) throw Error('native Stop notification not observed exactly once');
    const delivered = JSON.parse(deliveries[0]) as Record<string, unknown>;
    if (delivered.agent_source !== 'qoder' || delivered.notification_type !== 'task_complete' || delivered.session_id !== turnSessionID) throw Error('native Stop notification identity mismatch');
    evidence.nativeTurn = true;
  }
  await run(binary, setup('update'));
  evidence.update = JSON.parse(await run(cli, ['plugins', 'list', '--json']));
  await run(binary, setup('remove'));
  if (JSON.parse(await run(cli, ['plugins', 'list', '--json'])).length !== 0) throw Error('native registration remains');
  evidence.outcome = 'passed';
} catch (error) {
  evidence.outcome = 'failed'; evidence.error = String(error); process.exitCode = 1;
} finally {
  server.close();
  writeFileSync(receipt, JSON.stringify({ ...evidence, webhookDeliveries: deliveries.length, webhookBodies: deliveries.map(body => body.slice(0, 8000)) }, null, 2), { flag: 'wx', mode: 0o600 });
  process.stdout.write(receipt + '\n');
}

import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import dc from 'node:diagnostics_channel';
import { createProcessRegistry } from './process-registry.mjs';

const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(predicate) {
  const end = Date.now() + 3000;
  while (!await predicate()) { assert.ok(Date.now() < end, 'real child phase timed out'); await sleep(10); }
}
async function exists(file) { try { await fs.access(file); return true; } catch { return false; } }
const privateMarker = 'PRIVATE_PROMPT_SESSION_ID_NATIVE_ERROR_SENTINEL';
const success = JSON.stringify({ status: 'submitted', reason: privateMarker, desktop: 'submitted', webhook: 'submitted' });

// Same real-executable/shebang and child_process diagnostics boundary as the
// existing registry tests. No spawn, stream, exit or close is replaced.
async function exercise(raw, diagnostics, run, throwingLogger = false) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'TEST-opencode-diagnostics-'));
  const exe = path.join(root, 'receipt.mjs'), children = [], lines = [];
  let registry, settled = false, result;
  const logger = console.error;
  const observed = ({ process: child }) => {
    const record = { child, closed: false, code: undefined };
    children.push(record);
    child.once('close', code => { record.closed = true; record.code = code; });
  };
  dc.subscribe('child_process', observed);
  console.error = line => {
    assert.equal(children.length, 1, 'diagnostic without actual child');
    assert.equal(children[0].closed, true, 'diagnostic preceded actual close');
    lines.push(line);
    if (throwingLogger) throw new Error('private logger failure');
    logger(line); // Also retain actual stderr in the ROOT command log.
  };
  try {
    await fs.writeFile(exe, `#!${process.execPath}\nimport fs from 'node:fs';\nconst root=process.cwd();\nlet bytes=0;process.stdin.on('data',b=>bytes+=b.length);\nprocess.stdin.on('end',()=>{fs.writeFileSync(root+'/input-bytes',String(bytes));process.stdout.write(${JSON.stringify(raw)});fs.writeFileSync(root+'/output-written','');const timer=setInterval(()=>{if(fs.existsSync(root+'/release')){clearInterval(timer);process.exitCode=0;}},10);setTimeout(()=>{clearInterval(timer);process.exitCode=2;},3500).unref();});\n`, { mode: 0o700 });
    const config = { executable: exe, privateCwd: root, controlRoot: root };
    if (diagnostics !== undefined) config.diagnostics = diagnostics;
    registry = createProcessRegistry(config);
    const frame = Buffer.from('private-original-event-frame');
    const response = registry.event({ frame, isCurrent: () => true }).then(value => { settled = true; result = value; });
    await until(() => exists(path.join(root, 'output-written')));
    await run({ root, children, lines, response, frame, pending: () => !settled, result: () => result });
    assert.equal(children.length, 1);
    assert.equal(children[0].closed, true);
    assert.equal(children[0].code, 0);
    assert.equal(result.status, 'ok');
    assert.equal(result.output.toString(), raw, 'diagnostics changed actual receipt');
    assert.equal(await fs.readFile(path.join(root, 'input-bytes'), 'utf8'), String(frame.length));
    assert.equal(registry.status().occupied, 0);
    assert.equal((await registry.dispose()).reaped, true);
  } finally {
    console.error = logger;
    await fs.writeFile(path.join(root, 'release'), '').catch(() => {});
    if (registry) await registry.dispose();
    for (const record of children) if (!record.closed) record.child.kill('SIGTERM');
    await until(() => children.every(record => record.closed));
    dc.unsubscribe('child_process', observed);
    await fs.rm(root, { recursive: true });
  }
}
const focus = { concurrency: false, timeout: 7000 };
const decode = line => {
  assert.ok(line.startsWith('[agent-notifications] '));
  return JSON.parse(line.slice('[agent-notifications] '.length));
};
async function release(f) { await fs.writeFile(path.join(f.root, 'release'), ''); await f.response; await sleep(25); }

test('diagnostics waits for one real close and projects only private-free receipt enums', focus, async () => {
  await exercise(success, true, async f => {
    assert.equal(f.pending(), true);
    assert.equal(f.lines.length, 0, 'stdout is not successful-close authority');
    await release(f);
    assert.equal(f.lines.length, 1);
    assert.equal(f.lines.join('').includes(privateMarker), false);
    const row = decode(f.lines[0]);
    assert.equal(row.ipc, 'ok'); assert.equal(row.childClosure, 'closed');
    assert.equal(row.exitCode, 0); assert.equal(row.forcedKill, false);
    assert.deepEqual(row.receipt, { status: 'submitted', desktop: 'submitted', webhook: 'submitted' });
  });
});

test('malformed duplicate and private-field receipts never become diagnostic submission', focus, async () => {
  for (const raw of [JSON.stringify({ status: 'submitted', prompt: privateMarker }), '{"status":"rejected","status":"submitted"}']) {
    await exercise(raw, true, async f => {
      await release(f); assert.equal(f.lines.length, 1);
      assert.equal(f.lines.join('').includes(privateMarker), false);
      assert.equal(decode(f.lines[0]).receipt, 'invalid');
    });
  }
});

test('diagnostics is off by default without changing actual child receipt or closure', focus, async () => {
  await exercise(success, undefined, async f => { await release(f); assert.deepEqual(f.lines, []); });
});

test('throwing diagnostic logger cannot change real event settlement or reservations', focus, async () => {
  await exercise(success, true, async f => { await release(f); assert.equal(f.lines.length, 1); }, true);
});

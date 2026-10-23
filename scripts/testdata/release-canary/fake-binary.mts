// TEST local process contract, launched by the real canonical Python harness.
import assert from 'node:assert/strict';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
const root = process.cwd();
for (const key of ['HOME', 'CODEX_HOME', 'CLAUDE_CONFIG_DIR', 'TMPDIR', 'APPDATA']) {
  assert.ok(process.env[key]?.startsWith(root + '/'), `non-isolated ${key}`);
}
for (const key of ['GH_TOKEN', 'GITHUB_TOKEN', 'OPENAI_API_KEY', 'ANTHROPIC_API_KEY', 'PYTHONOPTIMIZE', 'NODE_OPTIONS']) {
  assert.equal(process.env[key], undefined, `leaked ${key}`);
}
const config = JSON.parse(readFileSync(process.env.AGENT_NOTIFICATIONS_CONFIG!, 'utf8')) as {
  notifications: { desktop: { enabled: boolean; sound: boolean }; webhook: { url: string } };
};
assert.equal(config.notifications.desktop.enabled, false); assert.equal(config.notifications.desktop.sound, false);
const args = process.argv.slice(2);
const mode = args[0].startsWith('--TEST-') ? args.shift() : undefined;
writeFileSync(join(root, 'TEST-commands.jsonl'), JSON.stringify(args) + '\n', { flag: 'a' });
if (args.join(' ') === 'version') console.log(mode === '--TEST-version' ? 'claude-notifications WRONG' : 'claude-notifications v2.7.19');
else {
  assert.equal(args[0], 'handle-hook'); assert.equal(args[1], 'Stop'); assert.equal(args[2], '--product');
  assert.ok(['claude', 'codex'].includes(args[3]));
  let input = ''; for await (const chunk of process.stdin) input += chunk;
  const payload = JSON.parse(input) as { session_id: string; last_assistant_message: string; transcript_path: string };
  assert.ok(readFileSync(payload.transcript_path, 'utf8').includes(payload.last_assistant_message));
  const seen = join(root, `TEST-seen-${payload.session_id}`);
  if (mode !== '--TEST-missing' && (!existsSync(seen) || mode === '--TEST-duplicate')) {
    const url = new URL(config.notifications.webhook.url); assert.equal(url.hostname, '127.0.0.1');
    const response = await fetch(url, { method: 'POST', body: JSON.stringify({ text: payload.last_assistant_message }) });
    assert.equal(response.status, 200); writeFileSync(seen, 'delivered');
  }
}

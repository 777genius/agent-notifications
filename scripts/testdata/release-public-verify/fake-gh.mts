// Strict TEST-only transport: no network, credentials, releases or provider effects.
import { appendFileSync, readFileSync } from 'node:fs';
const args = process.argv.slice(2);
const endpoint = args.find(value => value.startsWith('repos/777genius/agent-notifications/'));
if (args[0] !== 'api' || !endpoint || args.some(value => ['--method', '-X', '-f', '-F', '--input'].includes(value))) {
  console.error('TEST mock rejects every non-read API command'); process.exit(1);
}
appendFileSync(process.env.TEST_PUBLIC_CALLS!, JSON.stringify(args) + '\n');
const state = JSON.parse(readFileSync(process.env.TEST_PUBLIC_STATE!, 'utf8')) as Record<string, unknown>;
const key = endpoint.slice('repos/777genius/agent-notifications/'.length);
const value = state[key];
if (value === undefined) { console.error(`unexpected TEST read: ${key}`); process.exit(1); }
if (args.includes('Accept: application/octet-stream')) {
  const bytes = value as { bytes: string }; process.stdout.write(Buffer.from(bytes.bytes, 'base64'));
} else {
  if (!args.includes('--include')) { console.error('TEST requires included status'); process.exit(1); }
  process.stdout.write('HTTP/2.0 200 Test\r\nContent-Type: application/json\r\n\r\n' + JSON.stringify(value));
}

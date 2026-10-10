// TEST-only gh replacement: persistent local state, no token/network/provider access.
import { createHash } from 'node:crypto';
import { readFileSync, writeFileSync } from 'node:fs';
import { basename } from 'node:path';
interface Asset { id: number; name: string; size: number; digest?: string; state: string; bytes?: string }
interface State {
  tag: unknown; release: Record<string, unknown> | null; assets: Asset[]; mutations: string[];
  interruptAfterUpload?: boolean; failReads?: number; loseWriteResponse?: boolean;
  missingDigest?: boolean; failUploads?: boolean; uploadAttempts?: number;
}
const path = process.env.TEST_GH_STATE!;
const state = JSON.parse(readFileSync(path, 'utf8')) as State;
const args = process.argv.slice(2);
const save = () => writeFileSync(path, JSON.stringify(state));
function fail(message: string): never { console.error(message); process.exit(1); }
const field = (name: string) => args[args.indexOf(name) + 1];
const body = (value: unknown, status = 200): void => {
  if (args.includes('--include')) process.stdout.write(`HTTP/2.0 ${status} Test\r\nContent-Type: application/json\r\n\r\n`);
  process.stdout.write(JSON.stringify(value)); if (status >= 400) process.exitCode = 1;
};
const fields = new Map(args.filter((_, index) => args[index - 1] === '-f').map(x => { const split = x.indexOf('='); return [x.slice(0, split), x.slice(split + 1)] as const; }));
if (args[0] === 'api') {
  const endpoint = args.find(x => x.startsWith('repos/777genius/agent-notifications/'))?.slice('repos/777genius/agent-notifications/'.length);
  if (!endpoint) fail('explicit correct repository required');
  if (args.includes('POST')) {
    if (endpoint !== 'git/refs' || state.tag) fail('unexpected tag mutation');
    state.tag = { ref: fields.get('ref'), object: { type: 'commit', sha: fields.get('sha') } };
    state.mutations.push('tag'); save(); body(state.tag);
  } else {
    if (state.failReads) { state.failReads--; save(); fail('TEST network uncertainty'); }
    if (endpoint!.startsWith('git/ref/tags/')) body(state.tag ?? {}, state.tag ? 200 : 404);
    else if (endpoint!.startsWith('releases/tags/')) body(state.release ?? {}, state.release ? 200 : 404);
    else if (endpoint === 'releases/17/assets?per_page=100') body(state.assets);
    else if (endpoint!.startsWith('releases/assets/')) {
      const asset = state.assets.find(a => a.id === Number(endpoint!.split('/').at(-1)));
      if (!asset?.bytes) fail('missing TEST remote bytes');
      process.stdout.write(Buffer.from(asset.bytes, 'base64'));
    }
    else fail(`unexpected read ${endpoint}`);
  }
} else if (args[0] === 'release') {
  if (!args.includes('--repo') || field('--repo') !== '777genius/agent-notifications' || args.includes('--clobber')) fail('explicit repo/no clobber required');
  if (args[1] === 'create') {
    if (!state.tag || state.release || !args.includes('--draft') || !args.includes('--latest=false') || !args.includes('--verify-tag')) fail('unsafe draft mutation');
    state.release = { id: 17, tag_name: args[2], target_commitish: field('--target'), draft: true, prerelease: false };
    state.mutations.push('draft'); save(); body(state.release);
  } else if (args[1] === 'upload') {
    state.uploadAttempts = (state.uploadAttempts ?? 0) + 1; save();
    if (!state.release || state.failUploads) fail('TEST upload unavailable');
    const file = args.at(-1)!; const name = basename(file);
    if (state.assets.some(a => a.name === name)) fail('duplicate upload');
    const bytes = readFileSync(file);
    state.assets.push({ id: state.assets.length + 1, name, size: bytes.length,
      digest: state.missingDigest ? undefined : 'sha256:' + createHash('sha256').update(bytes).digest('hex'), state: 'uploaded', bytes: bytes.toString('base64') });
    state.mutations.push(`upload:${name}`);
    if (state.interruptAfterUpload) { state.interruptAfterUpload = false; state.failReads = 1; save(); fail('TEST lost upload response followed by read failure'); }
    if (state.loseWriteResponse) { state.loseWriteResponse = false; save(); fail('TEST lost upload response'); }
    save(); body(state.assets.at(-1));
  } else fail('unexpected release command');
} else fail('unexpected gh command');

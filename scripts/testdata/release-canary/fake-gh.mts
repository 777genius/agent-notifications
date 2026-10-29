// TEST transport only. It has no network or credential access.
import { readFileSync, writeFileSync } from 'node:fs';
export interface FakeRemote {
  release: Record<string, unknown>; tag: unknown; assets: Array<Record<string, unknown>>;
  binary: string; reads: string[]; download?: string; replaceAssetAfterDownload?: boolean;
  reorderAssetsAfterDownload?: boolean;
}
const path = process.env.TEST_RELEASE_CANARY_REMOTE!;
const state = JSON.parse(readFileSync(path, 'utf8')) as FakeRemote;
const args = process.argv.slice(2);
const repository = process.env.TEST_RELEASE_CANARY_REPOSITORY ?? '777genius/agent-notifications';
const endpoint = args.find(value => value.startsWith(`repos/${repository}/`))?.slice(`repos/${repository}/`.length);
if (args[0] !== 'api' || !endpoint || args.includes('--method')) throw new Error('TEST fake-gh accepts read-only API only');
state.reads.push(endpoint);
writeFileSync(path, JSON.stringify(state));
const reply = (value: unknown) => {
  process.stdout.write('HTTP/2.0 200 Test\r\nContent-Type: application/json\r\n\r\n' + JSON.stringify(value));
};
if (endpoint === 'releases/17') reply(state.release);
else if (endpoint.startsWith('git/ref/tags/')) reply(state.tag);
else if (endpoint === 'releases/17/assets?per_page=100') reply(state.assets);
else if (endpoint === `releases/assets/${state.assets.find(asset => String(asset.name).startsWith('claude-notifications-') && asset.id === 71)?.id}`) {
  if (!args.includes('Accept: application/octet-stream')) throw new Error('immutable bytes required');
  if (state.replaceAssetAfterDownload) state.assets = state.assets.map(asset => asset.id === 71 ? { ...asset, id: 72 } : asset);
  if (state.reorderAssetsAfterDownload) state.assets.reverse();
  // Commit TEST server state before streaming: the bounded-download client may
  // intentionally kill this process on oversized bytes, even during a write.
  writeFileSync(path, JSON.stringify(state));
  process.stdout.write(Buffer.from(state.download ?? state.binary, 'base64'));
} else throw new Error(`unexpected TEST API ${endpoint}`);

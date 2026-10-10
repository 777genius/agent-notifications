import { readFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

export type Mode = 'snapshot' | 'candidate' | 'prepared-promotion';
export type Options = { mode: Mode; version?: string; sourceSHA?: string };
type Read = (path: string) => string;
type Channel = { key: string; tag: string; release: string; source: string; ref: string };
const semver = /^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$/;
const sha = /^[0-9a-f]{40}$/;
const platforms = ['darwin/amd64', 'darwin/arm64', 'linux/amd64', 'linux/arm64', 'windows/amd64'];
const locales = ['ar', 'de', 'en', 'es', 'fr', 'hi', 'it', 'ja', 'ko', 'pt', 'ru', 'zh'];
function requireValue(ok: unknown, message: string): asserts ok {
  if (!ok) throw new Error(message);
}
function object(value: unknown): Record<string, unknown> {
  requireValue(value && typeof value === 'object' && !Array.isArray(value), 'Expected JSON object');
  return value as Record<string, unknown>;
}
function json(read: Read, path: string): Record<string, unknown> { return object(JSON.parse(read(path))); }

export function parseChannels(text: string): Channel[] {
  const lines = text.trimEnd().split('\n');
  requireValue(lines.shift() === '# agent-notifications-platform-channels-v1', 'Invalid channel header');
  const seen = new Set<string>();
  const rows = lines.filter(line => !line.startsWith('#')).map(line => {
    const columns = line.split('\t');
    requireValue(columns.length === 6, 'Channel row must have six columns');
    const [os, arch, tag, release, source, ref] = columns as [string, string, string, string, string, string];
    const key = `${os}/${arch}`;
    requireValue(platforms.includes(key) && !seen.has(key), `Invalid or duplicate channel ${key}`);
    seen.add(key);
    requireValue(tag.startsWith('v') && semver.test(tag.slice(1)) && sha.test(release) && sha.test(source), `Invalid channel identity ${key}`);
    requireValue(ref === (os === 'darwin' ? 'release/platform-macos' : 'release/platform-linux-windows'), `Invalid source ref ${key}`);
    return { key, tag, release, source, ref };
  });
  requireValue(rows.length === platforms.length, 'All five channel rows are required');
  return rows;
}

// Snapshot checks compare public documentation to the independent channel index
// and catch source version drift without demanding premature channel activation.
// Candidate checks intentionally allow that published index to remain on old bytes.
export function validatePreflight(read: Read, options: Options, head?: string): void {
  const channels = parseChannels(read('release-channels.tsv'));
  const versionFor = (key: string) => channels.find(row => row.key === key)!.tag.slice(1);
  for (const os of ['darwin', 'linux'])
    requireValue(versionFor(`${os}/amd64`) === versionFor(`${os}/arm64`), `${os} architecture display versions differ`);
  requireValue(versionFor('linux/amd64') === versionFor('windows/amd64'), 'Linux/Windows display versions differ');
  const linux = versionFor('linux/amd64'), mac = versionFor('darwin/amd64');
  const readme = read('README.md');
  for (const [label, version] of [['Linux amd64 / arm64', linux], ['Windows amd64', linux], ['macOS amd64 / arm64', mac]])
    requireValue(readme.includes(`| ${label} | ${version} |`), `README channel version stale: ${label}`);
  for (const locale of locales) {
    const install = object(object(json(read, `landing/locales/${locale}.json`).install).gemini);
    requireValue(typeof install.version === 'string' && install.version.startsWith(`Linux / Windows: ${linux} (`) &&
      install.version.includes(`macOS: ${mac} (`), `${locale} manual channel version stale`);
  }
  const marketplace = json(read, '.claude-plugin/marketplace.json');
  requireValue(Array.isArray(marketplace.plugins), 'Marketplace plugins must be an array');
  const plugins = marketplace.plugins.map(object).filter(plugin => plugin.name === 'claude-notifications-go');
  requireValue(plugins.length === 1, 'Exactly one legacy marketplace plugin is required');
  const runtime = [...read('internal/config/runtime.go').matchAll(/^var ConsumerVersion = "([^"]+)"$/gm)];
  requireValue(runtime.length === 1, 'Exactly one ConsumerVersion is required');
  const versions = [runtime[0]![1], json(read, '.claude-plugin/plugin.json').version,
    json(read, '.codex-plugin/plugin.json').version, object(marketplace.metadata).version, plugins[0]!.version];
  const version = options.mode === 'snapshot' ? versions[0] : options.version;
  requireValue(typeof version === 'string' && semver.test(version), 'Stable --version X.Y.Z or source version is required');
  requireValue(versions.every(value => value === version), 'All five source versions must match the expected version');
  const heading = `## [${version}]`;
  requireValue(read('CHANGELOG.md').split('\n').some(line => line === heading || line.startsWith(`${heading} - `)), 'Missing release changelog heading');
  if (options.mode === 'snapshot') return;
  requireValue(options.sourceSHA && sha.test(options.sourceSHA), 'Explicit lowercase 40-character --source-sha is required');
  if (options.mode === 'candidate') requireValue(head === options.sourceSHA, 'Candidate HEAD differs from --source-sha');
  requireValue(read('.github/workflows/release.yml').split('\n').some(line => line.trim() === `- '!v${options.version}'`), 'Basic release tag must be excluded from full Release workflow');
  if (options.mode === 'prepared-promotion') {
    for (const row of channels) {
      requireValue(row.tag === `v${options.version}`, `Stale promotion version: ${row.key}`);
      requireValue(row.release === options.sourceSHA, `Stale promotion release source: ${row.key}`);
    }
  }
}

export function run(root: string, options: Options): void {
  const read: Read = path => readFileSync(resolve(root, path), 'utf8');
  const head = options.mode === 'candidate' ? execFileSync('git', ['-C', root, 'rev-parse', 'HEAD'], { encoding: 'utf8' }).trim() : undefined;
  validatePreflight(read, options, head);
}
if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  try {
    const flags: Record<string, string> = {};
    for (let i = 2; i < process.argv.length; i += 2) {
      const key = process.argv[i]!, value = process.argv[i + 1];
      requireValue(['--mode', '--version', '--source-sha', '--root'].includes(key) && value && !flags[key], `Invalid argument ${key}`);
      flags[key] = value;
    }
    requireValue(['snapshot', 'candidate', 'prepared-promotion'].includes(flags['--mode'] ?? ''), '--mode snapshot|candidate|prepared-promotion is required');
    run(resolve(flags['--root'] ?? '.'), { mode: flags['--mode'] as Mode, version: flags['--version'], sourceSHA: flags['--source-sha'] });
    console.log(`Release preflight passed (${flags['--mode']})`);
  } catch (error) { console.error(error instanceof Error ? error.message : error); process.exitCode = 1; }
}

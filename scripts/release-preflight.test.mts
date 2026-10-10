import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { validatePreflight, type Options } from './release-preflight.mts';
const C = 'a'.repeat(40), S = 'b'.repeat(40);
const options: Options = { mode: 'candidate', version: '1.49.0', sourceSHA: C };
// Deliberately different candidate and published versions: freezing source must
// not prematurely activate channels or rewrite public instructions.
function fixture(promotion = false): Record<string, string> {
  const published = promotion ? '1.49.0' : '1.48.5';
  const files: Record<string, string> = {
    'internal/config/runtime.go': 'var ConsumerVersion = "1.49.0"\n',
    '.claude-plugin/plugin.json': '{"version":"1.49.0"}',
    '.codex-plugin/plugin.json': '{"version":"1.49.0"}',
    '.claude-plugin/marketplace.json': JSON.stringify({ metadata: { version: '1.49.0' }, plugins: [{ name: 'claude-notifications-go', version: '1.49.0' }] }),
    'CHANGELOG.md': '## [1.49.0] - 2026-10-10\n',
    '.github/workflows/release.yml': "      - '!v1.49.0'\n",
    'README.md': ['Linux amd64 / arm64', 'Windows amd64', 'macOS amd64 / arm64'].map(label => `| ${label} | ${published} | supported |`).join('\n'),
    'release-channels.tsv': '# agent-notifications-platform-channels-v1\n' +
      ['darwin/amd64', 'darwin/arm64', 'linux/amd64', 'linux/arm64', 'windows/amd64'].map(key => {
        const [os, arch] = key.split('/');
        return [os, arch, `v${published}`, promotion ? C : 'c'.repeat(40), S, os === 'darwin' ? 'release/platform-macos' : 'release/platform-linux-windows'].join('\t');
      }).join('\n') + '\n',
  };
  for (const locale of ['ar', 'de', 'en', 'es', 'fr', 'hi', 'it', 'ja', 'ko', 'pt', 'ru', 'zh'])
    files[`landing/locales/${locale}.json`] = JSON.stringify({ install: { gemini: { version: `Linux / Windows: ${published} (Gemini CLI 0.62.0). macOS: ${published} (Claude, Codex CLI, OpenCode)` } } });
  return files;
}
const validate = (files: Record<string, string>, opts = options, head = C) => validatePreflight(path => {
  assert.ok(path in files, `Missing fixture ${path}`); return files[path]!;
}, opts, head);

test('honest candidate retains published old channels and documentation', () => validate(fixture()));
test('prepared promotion binds all five rows to C while distribution source S remains distinct', () =>
  validate(fixture(true), { ...options, mode: 'prepared-promotion' }));
test('ordinary snapshot permits product version changes before release preparation', () => {
  const files = fixture(); delete files['.github/workflows/release.yml'];
  validate(files, { mode: 'snapshot' });
});
test('snapshot detects source version drift or a missing source changelog before long Landing checks', () => {
  for (const path of ['.codex-plugin/plugin.json', 'CHANGELOG.md']) {
    const files = fixture(); files[path] = files[path]!.replaceAll('1.49.0', '1.48.5');
    assert.throws(() => validate(files, { mode: 'snapshot' }));
  }
});
test('stale source versions, missing changelog/tag exclusion and wrong candidate SHA fail early', () => {
  for (const path of ['internal/config/runtime.go', '.claude-plugin/plugin.json', '.codex-plugin/plugin.json', '.claude-plugin/marketplace.json', 'CHANGELOG.md', '.github/workflows/release.yml']) {
    const files = fixture(); files[path] = files[path]!.replaceAll('1.49.0', '1.48.5');
    assert.throws(() => validate(files), path);
  }
  assert.throws(() => validate(fixture(), options, 'd'.repeat(40)), /Candidate HEAD/);
});
test('every stale public manual or README platform version fails', () => {
  for (const path of ['README.md', ...['ar', 'de', 'en', 'es', 'fr', 'hi', 'it', 'ja', 'ko', 'pt', 'ru', 'zh'].map(locale => `landing/locales/${locale}.json`)]) {
    const files = fixture(true); files[path] = files[path]!.replaceAll('1.49.0', '1.48.5');
    assert.throws(() => validate(files, { ...options, mode: 'prepared-promotion' }), /version stale/);
  }
});
test('malformed/duplicate/missing platform rows and invalid immutable source fail', () => {
  for (const mutate of [
    (text: string) => text.replace('darwin\tamd64', 'windows\tarm64'),
    (text: string) => text + text.split('\n')[1] + '\n',
    (text: string) => text.split('\n').filter(line => !line.startsWith('windows')).join('\n'),
    (text: string) => text.replace(S, 'main'),
    (text: string) => text.replace('release/platform-macos', 'main'),
  ]) {
    const files = fixture(); files['release-channels.tsv'] = mutate(files['release-channels.tsv']!);
    assert.throws(() => validate(files));
  }
});
test('validly shaped but stale promotion version or release source fails', () => {
  for (const replacement of ['v1.48.5', 'd'.repeat(40)]) {
    const files = fixture(true);
    files['release-channels.tsv'] = files['release-channels.tsv']!.replace(replacement.startsWith('v') ? 'v1.49.0' : C, replacement);
    assert.throws(() => validate(files, { ...options, mode: 'prepared-promotion' }));
  }
});

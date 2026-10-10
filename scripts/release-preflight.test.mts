import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { validatePreflight, type Options } from './release-preflight.mts';
const C = 'a'.repeat(40), S = 'b'.repeat(40);
const locales = ['ar', 'de', 'en', 'es', 'fr', 'hi', 'it', 'ja', 'ko', 'pt', 'ru', 'zh'];
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
  for (const locale of locales)
    files[`landing/locales/${locale}.json`] = JSON.stringify({ install: {
      platformRelease: 'Qualified release for {os}: {version}.',
      gemini: { version: 'Gemini CLI requires exactly version 0.62.0. Available on Linux / Windows only; other CLI versions are rejected.' },
    } });
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
test('every stale public release heading or README platform version fails', () => {
  for (const path of ['README.md', ...locales.map(locale => `landing/locales/${locale}.json`)]) {
    const files = fixture(true);
    files[path] = path === 'README.md' ? files[path]!.replaceAll('1.49.0', '1.48.5') :
      files[path]!.replace('{version}', '1.48.5');
    assert.throws(() => validate(files, { ...options, mode: 'prepared-promotion' }), /version stale/);
  }
});
test('all twelve shared release headings require exactly the OS and channel-version placeholders', () => {
  for (const locale of locales) {
    for (const heading of [
      'Qualified release for {os}.', 'Qualified release: {version}.',
      '{os}: {version} {version}', '{os}: {version} {other}',
      '{os}: {{version}}', '{os}: {version} (1.48.4)',
    ]) {
      const files = fixture(), path = `landing/locales/${locale}.json`;
      const data = JSON.parse(files[path]!); data.install.platformRelease = heading;
      files[path] = JSON.stringify(data);
      assert.throws(() => validate(files), /platform release version stale or invalid interpolation/, `${locale}: ${heading}`);
    }
  }
});
test('recognized legacy manual release claims must still match both published platform versions', () => {
  const files = fixture();
  for (const locale of locales) {
    const path = `landing/locales/${locale}.json`, data = JSON.parse(files[path]!);
    data.install.gemini.version = 'Linux / Windows: 1.48.5 (Gemini CLI 0.62.0). macOS: 1.48.5 (Claude, Codex CLI, OpenCode)';
    files[path] = JSON.stringify(data);
  }
  validate(files);
  for (const locale of locales) {
    for (const manual of [
      'Linux / Windows: 1.48.4 (Gemini CLI 0.62.0). macOS: 1.48.5 (Claude, Codex CLI, OpenCode)',
      'Linux / Windows: 1.48.5 (Gemini CLI 0.62.0). macOS: 1.48.4 (Claude, Codex CLI, OpenCode)',
      'Linux / Windows: 1.48.5 (Gemini CLI 0.62.0).',
      'macOS: 1.48.5 (Claude, Codex CLI, OpenCode)',
    ]) {
      const changed = { ...files }, path = `landing/locales/${locale}.json`;
      const data = JSON.parse(changed[path]!); data.install.gemini.version = manual;
      changed[path] = JSON.stringify(data);
      assert.throws(() => validate(changed), /manual channel version stale/, `${locale}: ${manual}`);
    }
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

// Exact replacement paragraph from the user-maintained README; host compatibility
// versions elsewhere are unrelated to the product release channel contract.
const canonicalReadme = "The installer selects a release from the [platform channels](docs/PLATFORM_RELEASE_CHANNELS.md) for your OS and architecture. See that page for current versions and the [release notes](https://github.com/777genius/agent-notifications/releases) for validation details and known limitations.";
function canonicalFixture(): Record<string, string> {
  return { ...fixture(), 'README.md': canonicalReadme + '\nOpenCode stable V1 >= 1.18.29 and V2 >= 2.0.0. Codex CLI version 0.162.0; OpenCode release v2.0.0.',
    'docs/PLATFORM_RELEASE_CHANNELS.md': '| Linux amd64 / arm64, Windows amd64 | 1.48.5 | `release/platform-linux-windows` |\n| macOS amd64 / arm64 | 1.48.5 | `release/platform-macos` |' };
}
test('version-free user README delegates current versions to the matching canonical guide', () => validate(canonicalFixture()));
test('version-free README requires the exact canonical guide link', () => {
  for (const readme of ['See platform channels for current versions.', canonicalReadme.replace('docs/PLATFORM_RELEASE_CHANNELS.md', 'docs/INSTALLATION.md')]) {
    const files = canonicalFixture(); files['README.md'] = readme;
    assert.throws(() => validate(files), /canonical platform channel guide link/);
  }
});
test('canonical guide rejects stale Linux or macOS versions, missing rows and duplicates', () => {
  const path = 'docs/PLATFORM_RELEASE_CHANNELS.md';
  for (const mutate of [
    (text: string) => text.replace('Windows amd64 | 1.48.5', 'Windows amd64 | 1.48.4'),
    (text: string) => text.replace('macOS amd64 / arm64 | 1.48.5', 'macOS amd64 / arm64 | 1.48.4'),
    (text: string) => text.split('\n')[0]!,
    (text: string) => text.split('\n')[1]!,
    (text: string) => text + '\n' + text.split('\n')[0],
  ]) {
    const files = canonicalFixture(); files[path] = mutate(files[path]!);
    assert.throws(() => validate(files), /Platform guide channel version stale/);
  }
});
test('canonical link cannot bypass stale, partial or duplicate explicit README platform rows', () => {
  for (const rows of [
    fixture()['README.md']!.replace('Linux amd64 / arm64 | 1.48.5', 'Linux amd64 / arm64 | 1.48.4'),
    '| Linux amd64 / arm64 | 1.48.5 | supported |',
    '| Linux x64 | 1.48.4 | supported |',
    fixture()['README.md']! + '\n| Windows amd64 | 1.48.4 | supported |',
  ]) {
    const files = canonicalFixture(); files['README.md'] += '\n' + rows;
    assert.throws(() => validate(files), /README channel version stale/);
  }
});

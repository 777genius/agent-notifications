import { createHash } from 'node:crypto';
import { readFile, writeFile, mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';

// PR1 exact source artifact. Replace this pin with a published UAP package before merge.
export const uapCommit = '69eea93bcf37f2fec556c649135919a6456c5f0d';
export const uapSourceSHA256 = 'ee3b795a970764d1937fccec6ac8321ca76056f27b4e05e842d721933dc580f7';
const sourceURL = `https://raw.githubusercontent.com/777genius/universal-agent-plugins/${uapCommit}/sdk/opencode-js/index.js`;
const root = fileURLToPath(new URL('.', import.meta.url));
const outputDir = join(root, '..', 'internal', 'opencodeplugin', 'dist');

export async function acquireObserver(fetchSource = fetch) {
  const response = await fetchSource(sourceURL);
  if (!response.ok) throw new Error('UAP observer source unavailable');
  const bytes = Buffer.from(await response.arrayBuffer());
  if (bytes.length > 64 * 1024 || createHash('sha256').update(bytes).digest('hex') !== uapSourceSHA256) {
    throw new Error('UAP observer source digest mismatch');
  }
  return bytes.toString('utf8');
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const source = await acquireObserver();
  const product = await readFile(join(root, 'plugin.mjs'), 'utf8');
  const output = await build({
    stdin: { contents: product, sourcefile: 'plugin.mjs', resolveDir: root, loader: 'js' },
    bundle: true,
    platform: 'node',
    format: 'esm',
    target: 'node22',
    write: false,
    plugins: [{
      name: 'pinned-uap-observer',
      setup(plugin) {
        plugin.onResolve({ filter: /^plugin-kit-ai-opencode-events$/ }, () => ({ path: 'observer', namespace: 'uap' }));
        plugin.onLoad({ filter: /^observer$/, namespace: 'uap' }, () => ({ contents: source, loader: 'js' }));
      },
    }],
  });
  const banner = `// Generated from UAP ${uapCommit}; source sha256 ${uapSourceSHA256}.\n`;
  if (output.outputFiles.length !== 1 ||
      output.outputFiles[0].text.split('"__AGENT_NOTIFICATIONS_EXECUTABLE__"').length !== 2) {
    throw new Error('unexpected plugin executable token');
  }
  await mkdir(outputDir, { recursive: true });
  await writeFile(join(outputDir, 'agent-notifications.js'), banner + output.outputFiles[0].text);
}

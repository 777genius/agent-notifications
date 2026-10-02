import { readFile, writeFile, mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';

const root = fileURLToPath(new URL('.', import.meta.url));
const outputDir = join(root, '..', 'internal', 'opencodeplugin', 'dist');

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const product = await readFile(join(root, 'plugin.mjs'), 'utf8');
  const output = await build({
    stdin: { contents: product, sourcefile: 'plugin.mjs', resolveDir: root, loader: 'js' },
    bundle: true,
    platform: 'node',
    format: 'esm',
    target: 'node22',
    write: false,
  });
  const banner = '// Generated with the UAP observer pinned in package-lock.json.\n';
  if (output.outputFiles.length !== 1 ||
      output.outputFiles[0].text.split('"__AGENT_NOTIFICATIONS_EXECUTABLE__"').length !== 2) {
    throw new Error('unexpected plugin executable token');
  }
  await mkdir(outputDir, { recursive: true });
  await writeFile(join(outputDir, 'agent-notifications.js'), banner + output.outputFiles[0].text);
}

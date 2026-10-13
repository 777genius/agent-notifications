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
    external: ['bun:ffi'],
    format: 'esm',
    target: 'node22',
    write: false,
    metafile: true,
  });
  const banner = '// Generated with the UAP observer pinned in package-lock.json.\n';
  if (output.outputFiles.length !== 1) throw new Error('unexpected_plugin_output');
  for (const token of ['EXECUTABLE', 'CONTROL_ROOT', 'ORIGIN'])
    if (output.outputFiles[0].text.split(`"__AGENT_NOTIFICATIONS_${token}__"`).length !== 2)
      throw new Error('unexpected_plugin_token');
  if (Object.values(output.metafile.outputs).some((file) => file.imports.some((item) => !item.path.startsWith('node:') && item.path !== 'bun:ffi')))
    throw new Error('external_plugin_dependency');
  await mkdir(outputDir, { recursive: true });
  await writeFile(join(outputDir, 'agent-notifications.js'), banner + output.outputFiles[0].text);
}

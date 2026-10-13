import assert from 'node:assert/strict';
import { open } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import path from 'node:path';
import type { Tuple } from './evidence.mts';

export type Acquisition = {
  official_url: string;
  actual_url: string;
  bytes: number;
  sha256: string;
};

// The fixed archive URL and transport grammar are an acquisition boundary.
// No caller-supplied URL, architecture, credential or retry is admitted.
export async function acquireOfficialArchive(root: string, tuple: Tuple): Promise<Acquisition> {
  const initial = 'https://persistent.oaistatic.com/codex-app-prod/ChatGPT-x64.msix';
  let url = initial;
  const deadline = Date.now()+120000;
  const destination = path.join(root,'client.msix');
  const h = await open(destination,'wx');
  let total = 0;
  try {
    for (let redirects = 0; redirects <= 5; ++redirects) {
      assert(Date.now() < deadline);
      const response = await fetch(url,{redirect:'manual',signal:AbortSignal.timeout(deadline-Date.now())});
      if ([301,302,303,307,308].includes(response.status)) {
        assert(redirects < 5);
        const target = new URL(response.headers.get('location')!,url);
        assert(target.protocol === 'https:'
          && (target.hostname === 'persistent.oaistatic.com' || target.hostname.endsWith('.oaistatic.com')));
        await response.body?.cancel();
        url = target.href;
        continue;
      }
      assert(response.ok && response.body);
      const reader = response.body.getReader();
      for (;;) {
        const item = await reader.read();
        if (item.done) break;
        total += item.value.length;
        assert(total <= 1073741824 && Date.now() < deadline);
        await h.writeFile(item.value);
      }
      assert(total > 0);
      await h.sync();
      break;
    }
  } finally {
    await h.close();
  }
  const digest = createHash('sha256');
  const raw = await open(destination,'r');
  try {
    const stat = await raw.stat();
    assert(stat.isFile() && stat.nlink === 1 && stat.size === total);
    digest.update(await raw.readFile());
  } finally {
    await raw.close();
  }
  return {official_url:initial,actual_url:url,bytes:total,sha256:digest.digest('hex')};
}

// TEST-only official acquisition; import has no execution or effect.
import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { open } from 'node:fs/promises';
export const sourceURL = 'https://persistent.oaistatic.com/codex-app-prod/ChatGPT-arm64.msix';
const maxPackage = 1024 * 1024 * 1024;
export async function hash(path: string): Promise<string> {
  const digest = createHash('sha256'); let bytes = 0;
  for await (const chunk of createReadStream(path, { highWaterMark: 65536 })) {
    if (!Buffer.isBuffer(chunk) || (bytes += chunk.length) > maxPackage) throw new Error('hash input outside bound');
    digest.update(chunk);
  }
  return digest.digest('hex');
}
export async function download(path: string, evidence: Record<string, unknown>): Promise<string> {
  const controller = new AbortController(), timer = setTimeout(() => controller.abort(), 120000);
  let current = new URL(sourceURL);
  const redirects: string[] = [];
  const file = await open(path, 'wx', 0o600);
  try {
    for (let attempt = 0; attempt < 6; attempt++) {
      if (current.protocol !== 'https:' || current.username || current.password) throw new Error('invalid source redirect');
      const response = await fetch(current, { redirect: 'manual', signal: controller.signal });
      redirects.push(current.origin + current.pathname); evidence.redirects = redirects;
      if ([301, 302, 303, 307, 308].includes(response.status)) {
        const location = response.headers.get('location'); await response.body?.cancel();
        if (!location) throw new Error('redirect location absent'); current = new URL(location, current); continue;
      }
      if (response.status !== 200 || !response.body) { await response.body?.cancel(); throw new Error('official download unavailable'); }
      const declared = response.headers.get('content-length');
      if (declared && (!/^\d+$/.test(declared) || Number(declared) > maxPackage)) {
        await response.body.cancel(); throw new Error('declared package size outside bound');
      }
      const digest = createHash('sha256'), reader = response.body.getReader(); let bytes = 0;
      try {
        while (true) {
          const chunk = await reader.read(); if (chunk.done) break;
          bytes += chunk.value.length;
          if (bytes > maxPackage) throw new Error('package download exceeded bound');
          digest.update(chunk.value); await file.writeFile(chunk.value);
        }
      } finally { await reader.cancel(); }
      if (!bytes || declared && bytes !== Number(declared)) throw new Error('package download incomplete');
      await file.sync(); evidence.packageBytes = bytes; evidence.etag = response.headers.get('etag');
      return digest.digest('hex');
    }
    throw new Error('redirect budget exhausted');
  } finally { controller.abort(); clearTimeout(timer); await file.close(); }
}

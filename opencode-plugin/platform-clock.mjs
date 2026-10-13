import { createLinuxClock } from './linux-clock.mjs';
import { unavailable } from './native-clock-contract.mjs';

// Preparation only: callers must already hold a closed compiled clock policy.
// The returned sampling/disposal seam remains synchronous on every platform.
export async function createPlatformClock({ signal } = {}) {
  if (signal?.aborted) unavailable();
  if (process.platform === 'linux') return createLinuxClock();
  if (process.platform === 'darwin') return (await import('./darwin-clock.mjs')).createDarwinClock({ signal });
  if (process.platform === 'win32') return (await import('./windows-clock.mjs')).createWindowsClock({ signal });
  unavailable();
}

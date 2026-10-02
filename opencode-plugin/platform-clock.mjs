import { createLinuxClock } from './linux-clock.mjs';
import { unavailable } from './native-clock-contract.mjs';

// Source checkpoint dispatch only. Qualification cells and Linux preparation
// stay closed and unchanged. No port here grants a prepared delivery policy.
export async function createPlatformClock() {
  if (process.platform === 'linux') return createLinuxClock();
  if (process.platform === 'darwin') return (await import('./darwin-clock.mjs')).createDarwinClock();
  if (process.platform === 'win32') return (await import('./windows-clock.mjs')).createWindowsClock();
  unavailable();
}

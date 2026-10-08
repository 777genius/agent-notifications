// CI-only validation of restored bytes; never executes or deploys package code.
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { closeSync, fstatSync, lstatSync, openSync, readSync, realpathSync, writeFileSync } from 'node:fs';
import type { Stats } from 'node:fs';
import { basename, dirname, join, resolve } from 'node:path';

const [outArg, packagesArg, lockArg, dotnetArg, ...extra] = process.argv.slice(2);
const out = resolve(outArg ?? '.');
const targets = [
  {id: 'Microsoft.WindowsAppSDK.Runtime', folder: 'microsoft.windowsappsdk.runtime', version: '2.5.1', prefix: 'runtime'},
  {id: 'Microsoft.WindowsAppSDK.Foundation', folder: 'microsoft.windowsappsdk.foundation', version: '2.3.12', prefix: 'foundation'},
] as const;
let anyFailure = false;
for (const target of targets) {
const report: Record<string, unknown> = { status: 'failed', sourceSHA: process.env.SOURCE_SHA,
  runID: process.env.GITHUB_RUN_ID, job: process.env.GITHUB_JOB, image: process.env.ImageVersion,
  scope: `CI-restored ${target.prefix === 'runtime' ? 'runtime' : 'Foundation'} package only`, packageVerified: false, runtimeCodeExecuted: false,
  localArchiveQualified: false, automaticRetry: false, steps: [] };
let fd: number | undefined;
let before: Stats | undefined;
let packagePath: string | undefined;
let lockPath: string | undefined;
let exitCode = 1;
let admitted = false;
function physical(path: string, directory: boolean): void {
  const st = lstatSync(path);
  if (st.isSymbolicLink() || (directory ? !st.isDirectory() : !st.isFile() || st.nlink !== 1)
      || realpathSync(path).toLowerCase() !== path.toLowerCase()) throw new Error('input redirected/nonregular');
}
function boundedRead(path: string, cap: number): Buffer {
  physical(path, false);
  const handle = openSync(path, 'r');
  try {
    if (fstatSync(handle).size > cap) throw new Error('input exceeds bound');
    const bytes = Buffer.alloc(cap + 1); let length = 0, count: number;
    while (length < bytes.length && (count = readSync(handle, bytes, length, bytes.length - length, length)) !== 0) length += count;
    if (length > cap) throw new Error('input grew beyond bound');
    return bytes.subarray(0, length);
  } finally { closeSync(handle); }
}
const hashFile = (path: string, cap: number): string => createHash('sha256').update(boundedRead(path, cap)).digest('hex');
function archiveHash(handle: number): string {
  const hash = createHash('sha256'), buffer = Buffer.alloc(1 << 20);
  let position = 0, count: number;
  while ((count = readSync(handle, buffer, 0, buffer.length, position)) !== 0) {
    position += count;
    if (position > 170 * 1024 * 1024) throw new Error('archive grew beyond bound');
    hash.update(buffer.subarray(0, count));
  }
  if (position !== before?.size) throw new Error('archive length changed');
  return hash.digest('hex');
}
function run(dotnet: string, phase: string, args: string[], timeout: number): string {
  const env = Object.fromEntries(['SystemRoot', 'WINDIR', 'PATH', 'TEMP', 'TMP', 'ProgramFiles', 'ProgramFiles(x86)']
    .filter(key => process.env[key] !== undefined).map(key => [key, process.env[key]]));
  Object.assign(env, { DOTNET_CLI_UI_LANGUAGE: 'en-US', DOTNET_CLI_HOME: out,
    DOTNET_GENERATE_ASPNET_CERTIFICATE: 'false', DOTNET_CLI_TELEMETRY_OPTOUT: '1',
    DOTNET_ADD_GLOBAL_TOOLS_TO_PATH: 'false', DOTNET_NOLOGO: '1' });
  const child = spawnSync(dotnet, args, { cwd: out, env, timeout, maxBuffer: 65_536, windowsHide: true });
  const stdout = child.stdout ?? Buffer.alloc(0), stderr = child.stderr ?? Buffer.alloc(0);
  for (const [label, data] of [['stdout', stdout], ['stderr', stderr]] as const) {
    writeFileSync(join(out, `${target.prefix}-${phase}.${label}`), data.subarray(0, 65_536), { flag: 'wx' });
  }
  (report.steps as unknown[]).push({ phase, pid: child.pid, status: child.status, signal: child.signal,
    errorCode: child.error ? (child.error as NodeJS.ErrnoException).code ?? 'error' : null,
    collected: !child.error && !child.signal && child.status !== null,
    stdoutTruncated: stdout.length > 65_536, stderrTruncated: stderr.length > 65_536,
    stdoutSHA256: createHash('sha256').update(stdout.subarray(0, 65_536)).digest('hex'),
    stderrSHA256: createHash('sha256').update(stderr.subarray(0, 65_536)).digest('hex') });
  if (child.error || child.signal || child.status !== 0 || stdout.length + stderr.length > 65_536) throw new Error(`${phase} failed/unknown/oversized`);
  return stdout.toString('utf8') + '\n' + stderr.toString('utf8');
}
try {
  if (!outArg || out !== join(resolve(process.env.RUNNER_TEMP ?? '.'), 'TEST-windows-appsdk-build')) throw new Error('owned output guard refused');
  physical(out, true); admitted = true;
  if (extra.length || !packagesArg || !lockArg || !dotnetArg || process.platform !== 'win32'
      || process.arch !== 'arm64' || process.env.CI !== 'true' || process.env.GITHUB_ACTIONS !== 'true'
      || process.env.GITHUB_REPOSITORY !== '777genius/agent-notifications' || !/^[0-9a-f]{40}$/.test(process.env.SOURCE_SHA ?? '')
      || !/^v24\./.test(process.version)) throw new Error('owned CI guard refused');
  const packages = resolve(packagesArg), dotnet = resolve(dotnetArg);
  if (packages !== join(resolve(process.env.RUNNER_TEMP!), 'TEST-windows-appsdk-packages') || basename(dotnet).toLowerCase() !== 'dotnet.exe') throw new Error('unexpected input path');
  for (const path of [packages, join(packages, target.folder), join(packages, target.folder, target.version)]) physical(path, true);
  report.dotnetSHA256 = hashFile(dotnet, 32 << 20);
  const version = run(dotnet, 'version', ['--version'], 10_000).trim();
  if (!/^(?:1[0-9]|[2-9][0-9])\.\d+\.\d+$/.test(version)) throw new Error('installed stable .NET SDK10+ unavailable');
  report.dotnetSDK = version;
  lockPath = resolve(lockArg); const lockBytes = boundedRead(lockPath, 1 << 20);
  report.lockSHA256 = createHash('sha256').update(lockBytes).digest('hex');
  const locked = JSON.parse(lockBytes.toString('utf8')) as { dependencies?: Record<string, Record<string, { resolved?: string; contentHash?: string }>> };
  const entry = locked.dependencies?.['native,Version=v0.0']?.[target.id];
  if (entry?.resolved !== target.version || !entry.contentHash || !/^[A-Za-z0-9+/]{86}==$/.test(entry.contentHash)) throw new Error('runtime lock missing/invalid');
  report.expectedContentHash = entry.contentHash;
  packagePath = join(packages, target.folder, target.version, `${target.folder}.${target.version}.nupkg`);
  physical(join(dirname(packagePath), '.nupkg.metadata'), false); physical(packagePath, false);
  fd = openSync(packagePath, 'r'); before = fstatSync(fd);
  if (before.size <= 0 || before.size > 170 * 1024 * 1024) throw new Error('archive size invalid');
  report.archiveBytes = before.size; report.archiveSHA256Before = archiveHash(fd);
  const config = join(out, `${target.prefix}-verify.nuget.config`);
  writeFileSync(config, '<configuration><packageSources><clear /></packageSources></configuration>\n', { flag: 'wx' });
  const output = run(dotnet, 'verify', ['nuget', 'verify', packagePath, '--all', '--verbosity', 'normal', '--configfile', config], 120_000);
  const hashes = [...output.matchAll(/^Content hash: ([A-Za-z0-9+/]{86}==)\r?$/gm)];
  report.actualContentHash = hashes.length === 1 ? hashes[0]?.[1] : null;
  const success = `Successfully verified package '${target.id}.${target.version}'.`;
  if (hashes.length !== 1 || hashes[0]?.[1] !== entry.contentHash || output.split(/\r?\n/).filter(line => line === success).length !== 1
      || output.includes('Package signature validation failed.')) throw new Error('NuGet content/signature proof absent');
  report.verificationReturned = true; exitCode = 0;
} catch { report.failure = 'runtime validation failed or prerequisite unavailable'; }
finally {
  try {
    if (fd !== undefined && before && packagePath) {
      physical(packagePath, false);
      const held = fstatSync(fd), named = lstatSync(packagePath);
      const same = [held, named].every(st => st.dev === before!.dev && st.ino === before!.ino && st.size === before!.size
        && st.mtimeMs === before!.mtimeMs && st.ctimeMs === before!.ctimeMs);
      report.archiveSHA256After = archiveHash(fd);
      report.archiveUnchanged = same && report.archiveSHA256After === report.archiveSHA256Before;
      if (!report.archiveUnchanged) exitCode = 1;
    }
    if (lockPath && hashFile(lockPath, 1 << 20) !== report.lockSHA256) exitCode = 1;
  } catch { report.integrityUnknown = true; exitCode = 1; }
  if (fd !== undefined) { try { closeSync(fd); } catch { report.closeUnknown = true; exitCode = 1; } }
  report.packageVerified = exitCode === 0; report.status = exitCode === 0 ? 'verified' : 'failed';
  let data = JSON.stringify(report, null, 2) + '\n';
  if (Buffer.byteLength(data) > 16_384) {
    exitCode = 1;
    data = JSON.stringify({ status: 'failed', packageVerified: false, failure: 'verification evidence exceeds bound',
      originalEvidenceSHA256: createHash('sha256').update(data).digest('hex'), runtimeCodeExecuted: false,
      localArchiveQualified: false, automaticRetry: false }) + '\n';
  }
  if (admitted) { physical(out, true); writeFileSync(join(out, `${target.prefix}-verification.json`), data, { flag: 'wx' }); }
  if (exitCode !== 0) anyFailure = true;
}

}
process.exitCode = anyFailure ? 1 : 0;

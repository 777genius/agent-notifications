import { createHash } from 'node:crypto';
import { existsSync, lstatSync, readFileSync, realpathSync } from 'node:fs';
import { basename, dirname, join, resolve } from 'node:path';

export type Json = Record<string, unknown>;

export type BootstrapExpectedIdentity = {
  sourceSHA: string;
  observerSHA256: string;
  imageVersion: string;
  runnerLabel: string;
};

export type BootstrapBindOptions = {
  suppliedRoot: string;
  runnerTemp: string;
  currentRoot: string;
  currentNonce: string;
  observerPath: string;
  expected: BootstrapExpectedIdentity;
};

export type BootstrapObservedIdentity = {
  sourceSHA: string;
  observerSHA256: string;
  userSid: string;
  session: number;
};

export type BoundSameJobBootstrap = {
  root: string;
  nonce: string;
  handoff: Json;
  members: ReadonlyMap<string, string>;
  digests: ReadonlyMap<string, string>;
  summary: {
    nonce: string;
    session: number;
    userSid: string;
    sourceSHA: string;
    handoffSHA256: string;
    evidenceSHA256: string;
    recordsSHA256: string;
    observerSHA256: string;
  };
};

function fileHash(path: string): string {
  return createHash('sha256').update(readFileSync(path)).digest('hex');
}

function memberLimit(name: string): number {
  if (name === 'oobe-setup-records.json') return 6_291_456;
  if (name === 'evidence.json') return 131_072;
  if (name === '.owned-test-root') return 256;
  return 65_536;
}

export function requireSameJobBootstrapMode(
  mode: string,
  composedModes: readonly string[],
  suppliedRoot: string | undefined,
): boolean {
  const composed = composedModes.includes(mode);
  if (composed && !suppliedRoot) throw new Error('same-job bootstrap handoff required in composed mode');
  if (!composed && suppliedRoot) throw new Error('unexpected bootstrap in unrelated mode');
  return composed;
}

export function bindSameJobBootstrap(options: BootstrapBindOptions): BoundSameJobBootstrap {
  const supplied = resolve(options.suppliedRoot);
  const path = realpathSync(supplied);
  const st = lstatSync(supplied);
  const name = basename(path);
  const id = name.slice('navigation-windows-test-'.length);
  if (!st.isDirectory() || st.isSymbolicLink() || dirname(path) !== realpathSync(options.runnerTemp)
      || !/^navigation-windows-test-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(name)
      || path === options.currentRoot || id === options.currentNonce) {
    throw new Error('fresh sibling bootstrap TEST root required');
  }

  const members = new Map<string, string>();
  const digests = new Map<string, string>();
  function bytes(memberName: string): Buffer {
    const file = join(path, memberName);
    if (!existsSync(file)) throw new Error('bounded immutable bootstrap member required');
    const fs = lstatSync(file);
    if (!fs.isFile() || fs.isSymbolicLink() || fs.nlink !== 1 || realpathSync(file) !== file
        || fs.size < 1 || fs.size > memberLimit(memberName)) {
      throw new Error('bounded immutable bootstrap member required');
    }
    const data = readFileSync(file);
    if (data.length !== fs.size) throw new Error('bootstrap member changed');
    members.set(memberName, file);
    digests.set(memberName, createHash('sha256').update(data).digest('hex'));
    return data;
  }

  const marker = bytes('.owned-test-root').toString('utf8');
  const handoff = JSON.parse(bytes('same-job-bootstrap.json').toString('utf8')) as Json;
  const result = JSON.parse(bytes('evidence.json').toString('utf8')) as Json;
  const packet = bytes('oobe-setup-records.json');
  const expected = options.expected;
  if (marker !== 'TEST navigation Windows ' + id + '\n' || handoff.root !== path || handoff.nonce !== id
      || handoff.runID !== process.env.GITHUB_RUN_ID || handoff.job !== process.env.GITHUB_JOB
      || handoff.attempt !== '1' || handoff.sourceSHA !== expected.sourceSHA
      || handoff.observerSHA256 !== expected.observerSHA256
      || handoff.evidenceSHA256 !== digests.get('evidence.json')
      || handoff.recordsSHA256 !== digests.get('oobe-setup-records.json')
      || typeof handoff.userSid !== 'string' || !/^S-1-5-[0-9-]{1,160}$/.test(handoff.userSid)
      || !Number.isInteger(handoff.session) || Number(handoff.session) <= 0
      || result.status !== 'guest_setup_complete' || result.root !== path || result.nonce !== id
      || result.sourceSHA !== expected.sourceSHA || result.binarySHA256 !== expected.observerSHA256
      || result.imageVersion !== expected.imageVersion || result.runnerLabel !== expected.runnerLabel
      || result.oobeSetupQualified !== true || result.captureQualified !== true
      || result.oobeSetupActorCollected !== true || result.captureActorCollected !== true
      || result.invokeEffectUncertain !== false || result.nativeEffectUncertain !== false
      || result.showAttempts !== 0 || result.noKeyboardOrPointerInputProved !== true
      || result.setupRecordErrors || result.setupRecordPublicationError
      || result.setupRecordsSHA256 !== digests.get('oobe-setup-records.json')) {
    throw new Error('bootstrap custody/terminal proof absent');
  }

  const terminal = result.oobeSetupResult as Json | undefined;
  const capture = result['desktop-capture.json'] as Json | undefined;
  if (!terminal || terminal.setupQualified !== true || terminal.ownedOOBEWindowGone !== true
      || terminal.invokeEffectUncertain !== false || terminal.acceptCallsEntered !== 1
      || terminal.invokeCallsEntered !== terminal.invokeCallsReturned
      || terminal.progressionsObserved !== terminal.invokeCallsReturned
      || !capture || capture.session !== handoff.session || capture.sourceSHA !== expected.sourceSHA
      || capture.binarySHA256 !== expected.observerSHA256 || !Array.isArray(result.steps)
      || result.steps.length !== 4 || result.steps.some((step: unknown, index: number) => {
        const actor = step as Json;
        return actor.mode !== ['center-policy', 'preflight', 'oobe-setup', 'desktop-capture'][index]
          || actor.status !== 0 || actor.signal !== null || actor.error
          || !Number.isInteger(actor.pid) || Number(actor.pid) <= 0;
      })) {
    throw new Error('known collected bootstrap actors required');
  }

  const records = JSON.parse(packet.toString('utf8')) as Json;
  const setupRecords = records.records as Json | undefined;
  if (records.nonce !== id || records.actorPID !== (result.steps[2] as Json).pid
      || !setupRecords || typeof setupRecords !== 'object' || Array.isArray(setupRecords)
      || Object.keys(setupRecords).length !== result.setupRecordCount || Number(result.setupRecordCount) > 198
      || Object.entries(setupRecords).some(([key, rawValue]: [string, unknown]) => {
        const value = rawValue as Json;
        return !/^oobe-setup-(intent|result|after-preflight|[0-4]-(armed|returned|progress|(before|after-[0-7])-(preflight|after-preflight|intent|uia)))\.json$/.test(key)
          || !value || value.pid !== records.actorPID || value.nonce !== id
          || Buffer.byteLength(JSON.stringify(value)) > (key.endsWith('-uia.json') ? 65_536 : 16_384);
      })) {
    throw new Error('bounded bootstrap packet binding absent');
  }

  return {
    root: path,
    nonce: id,
    handoff,
    members,
    digests,
    summary: {
      nonce: id,
      session: handoff.session as number,
      userSid: handoff.userSid as string,
      sourceSHA: expected.sourceSHA,
      handoffSHA256: digests.get('same-job-bootstrap.json')!,
      evidenceSHA256: digests.get('evidence.json')!,
      recordsSHA256: digests.get('oobe-setup-records.json')!,
      observerSHA256: expected.observerSHA256,
    },
  };
}

export function revalidateSameJobBootstrap(
  binding: BoundSameJobBootstrap,
  observed: BootstrapObservedIdentity,
  observerPath: string,
): void {
  if (observed.sourceSHA !== binding.summary.sourceSHA
      || !/^S-1-5-[0-9-]{1,160}$/.test(observed.userSid)
      || !Number.isInteger(observed.session) || observed.session <= 0
      || observed.userSid !== binding.summary.userSid || observed.session !== binding.summary.session
      || observed.observerSHA256 !== binding.summary.observerSHA256
      || fileHash(observerPath) !== observed.observerSHA256) {
    throw new Error('bootstrap runtime identity changed');
  }
  for (const [name, path] of binding.members) {
    const st = lstatSync(path);
    if (!st.isFile() || st.isSymbolicLink() || st.nlink !== 1 || realpathSync(path) !== path
        || st.size < 1 || st.size > memberLimit(name) || fileHash(path) !== binding.digests.get(name)) {
      throw new Error('bootstrap custody changed before submission effects');
    }
  }
}

// Windows client CI only. Native source is compiled from this exact checkout.
import { spawnSync } from 'node:child_process';
import { randomUUID, createHash } from 'node:crypto';
import { mkdirSync, writeFileSync, readFileSync, existsSync, realpathSync, copyFileSync, statSync, lstatSync, readdirSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { setTimeout as delay } from 'node:timers/promises';

type Json = Record<string, unknown>;
type Step = { mode: string; pid: number; status: number | null; signal: string | null;
  stdout: string; stderr: string; error?: string; exitedAt: number };
const evidence: Json = { status: 'failed', nativeCallbackQualified: false, navigationQualified: false,
  scope: 'Windows client native TEST cold toast COM callback only', clientRouteTested: false,
  showAttempts: 0, showAttemptMeaning: 'native Show call entered, not OS acceptance', submissionIntent: false,
  showCallOutcome: 'not_started', nativeEffectUncertain: false,
  steps: [], sourceSHA: process.env.NAVIGATION_SOURCE_SHA ?? null,
  runnerLabel: process.env.NAVIGATION_WINDOWS_RUNNER ?? null,
  imageVersion: process.env.ImageVersion ?? null, nodeVersion: process.version };
const steps: Step[] = [];
let root: string | undefined;
let binary: string | undefined;
let nonce: string | undefined;
let installed = false;
let exitCode = 1;
let setupActor: Step | undefined;
const setupRecords: Record<string, Json> = {};
const setupNames = ['oobe-setup-intent.json', 'oobe-setup-result.json', 'oobe-setup-after-preflight.json',
  ...Array.from({ length: 5 }, (_, index) => [
    ...['armed', 'returned', 'progress'].map(phase => `oobe-setup-${index}-${phase}.json`),
    ...['before', ...Array.from({ length: 8 }, (_, observation) => `after-${observation}`)]
      .flatMap(phase => ['preflight', 'after-preflight', 'intent', 'uia']
      .map(kind => `oobe-setup-${index}-${phase}-${kind}.json`)),
  ]).flat()];
function read(name: string): Json {
  if (!root) throw new Error('owned root absent');
  const path = join(root, name);
  const st = lstatSync(path);
  if (!st.isFile() || st.isSymbolicLink() || st.nlink !== 1 || realpathSync(path) !== path
      || st.size > (name.endsWith('-uia.json') ? 65_536 : 16_384)) throw new Error(`invalid bounded ${name}`);
  const value: unknown = JSON.parse(readFileSync(path, 'utf8'));
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(`invalid ${name}`);
  return value as Json;
}
function run(mode: string, timeout: number): Step {
  if (!root || !binary || !nonce) throw new Error('owned fixture absent');
  const selectedDiagnostic = mode === 'desktop-capture' || mode === 'oobe-preflight' || mode === 'oobe-setup';
  const env: NodeJS.ProcessEnv = selectedDiagnostic ? Object.fromEntries([
    'SystemRoot', 'WINDIR', 'PATH', 'TEMP', 'TMP', 'CI', 'GITHUB_ACTIONS', 'GITHUB_REPOSITORY',
    'GITHUB_EVENT_NAME', 'GITHUB_RUN_ATTEMPT', 'NAVIGATION_SOURCE_SHA', 'NAVIGATION_WINDOWS_RUNNER',
    'NAVIGATION_WINDOWS_DESKTOP_CAPTURE_TEST', 'NAVIGATION_WINDOWS_OOBE_PREFLIGHT_TEST', 'NAVIGATION_WINDOWS_OOBE_SETUP_TEST',
  ].filter(key => process.env[key] !== undefined).map(key => [key, process.env[key]])) : { ...process.env };
  env.AGENT_NOTIFY_NAVIGATION_WINDOWS_E2E = '1';
  // The follow-up is read-only and only runs after the setup actor was collected/qualified.
  if (mode === 'desktop-capture' && evidence.oobeSetupQualified === true) env.NAVIGATION_WINDOWS_DESKTOP_CAPTURE_TEST = '1';
  const result = spawnSync(binary, [mode, root, nonce], { cwd: root, encoding: 'utf8',
    env, timeout, maxBuffer: 65_536, windowsHide: selectedDiagnostic });
  const step: Step = { mode, pid: result.pid, status: result.status, signal: result.signal,
    stdout: result.stdout ?? '', stderr: result.stderr ?? '', exitedAt: Date.now(),
    ...(result.error ? { error: result.error.message } : {}) };
  steps.push(step); evidence.steps = steps;
  return step;
}
function requireSuccess(step: Step): void {
  if (step.error || step.signal || step.status !== 0) throw new Error(`${step.mode} failed (${step.status}): ${step.error ?? step.stderr}`);
}
function observeShow(sender: Step): void {
  if (!root || !nonce) throw new Error('owned fixture absent');
  const stages = readdirSync(root).filter(name => /^sender-stage-\d+\.json$/.test(name));
  if (stages.length > 32) throw new Error('too many native stage records');
  evidence.senderStages = stages.sort((a, b) => Number(a.match(/\d+/)?.[0]) - Number(b.match(/\d+/)?.[0])).map(name => {
    const stage = read(name);
    if (stage.pid !== sender.pid || stage.nonce !== nonce || typeof stage.phase !== 'string' || stage.phase.length > 64) {
      throw new Error('native stage correlation invalid');
    }
    return stage;
  });
  for (const name of ['show-outcome.json', 'sender-failure.json']) {
    if (!existsSync(join(root, name))) continue;
    const record = read(name); evidence[name] = record;
    if (record.pid !== sender.pid || record.nonce !== nonce || typeof record.showCallEntered !== 'boolean'
        || typeof record.showCallReturned !== 'boolean' || (record.showCallReturned && !record.showCallEntered)) {
      throw new Error('native Show evidence correlation/state invalid');
    }
    evidence.showAttempts = record.showCallEntered ? 1 : 0;
    evidence.showReturned = record.showCallReturned;
    evidence.showCallOutcome = record.showCallEntered ? (record.showCallReturned ? 'returned' : 'entered_error') : 'not_called';
    evidence.noShowProved = !record.showCallEntered;
    evidence.nativeEffectUncertain = record.showCallEntered && !record.showCallReturned;
  }
  if (sender.status === 3 && existsSync(join(root, 'sender.json'))) {
    const record = read('sender.json');
    if (record.pid !== sender.pid || record.nonce !== nonce || record.showCalledAtReceipt !== false
        || typeof record.notificationSetting !== 'number' || record.notificationSetting === 0) {
      throw new Error('disabled native Setting evidence invalid');
    }
    evidence.showAttempts = 0; evidence.noShowProved = true; evidence.nativeEffectUncertain = false;
    evidence.showCallOutcome = 'not_called';
  }
  // Missing terminal native evidence (crash/timeout/write failure) remains unknown.
  if (evidence.showAttempts === null) { evidence.nativeEffectUncertain = true; evidence.showCallOutcome = 'unknown'; }
}
function collectSetupRecords(): void {
  if (!root || !setupActor) return;
  for (const name of setupNames) {
    if (!existsSync(join(root, name))) continue;
    try {
      const record = read(name);
      if (record.pid !== setupActor.pid || record.nonce !== nonce) throw new Error('setup actor correlation invalid');
      setupRecords[name] = record;
    } catch (error: unknown) {
      evidence.setupRecordErrors = [...(evidence.setupRecordErrors as string[] ?? []), `${name}: ${String(error)}`];
      evidence.oobeSetupQualified = false; evidence.invokeEffectUncertain = true;
    }
  }
}
function setupRecord(name: string): Json {
  const record = setupRecords[name];
  if (!record || record.sourceSHA !== evidence.sourceSHA || record.binarySHA256 !== evidence.binarySHA256
      || record.architecture !== 'ARM64') throw new Error(`missing/source-mismatched setup ${name}`);
  return record;
}
function setupSnapshot(prefix: string, entered: number, allowDisabled = false): Json {
  const census = setupRecord(`${prefix}-uia.json`), intent = setupRecord(`${prefix}-intent.json`);
  if (census.snapshotReadOnly !== true || census.readOnly !== false || census.diagnosticOnly !== false
      || census.actorInvocationsBeforeSnapshot !== entered || census.invokeAttempted !== (entered > 0)
      || census.available !== true || census.rootStable !== true || census.walkCompleted !== true
      || census.truncated !== false || census.deadlineExpired !== false || census.errors !== 0 || census.providerSkips !== 0
      || census.verifiedOwners !== 1 || census.privacyPanes !== 1 || census.matchingPrivacyButtons !== 1
      || ![0, 1].includes(Number(census.eligiblePrivacyButtons)) || !allowDisabled && census.eligiblePrivacyButtons !== 1
      || census.sameUserSession !== true || census.verifiedImageLeaf !== 'wwahost.exe'
      || census.windowClass !== 'Windows.UI.Core.CoreWindow' || census.censusAttempts !== 1
      || census.showAttempts !== 0 || census.inputAttempted !== false || census.installAttempted !== false
      || census.launchAttempted !== false || census.retryAllowed !== false
      || census.projection !== 'selected_foreground_UIA_properties'
      || census.imageAuthority !== 'kernel_image_under_Windows_not_signature_qualification'
      || census.setupImagePathKind !== 'exact_Windows_System32_WWAHost_kernel_image'
      || !Number.isInteger(census.foregroundPID) || Number(census.foregroundPID) <= 0 || Number(census.foregroundPID) > 0xffffffff
      || !Number.isInteger(census.foregroundHWND) || Number(census.foregroundHWND) <= 0
      || typeof census.foregroundCreatedUtcTicks !== 'string' || !/^[1-9][0-9]{0,19}$/.test(census.foregroundCreatedUtcTicks)
      || !Number.isInteger(census.session) || Number(census.session) <= 0
      || !Number.isInteger(census.elapsedMs) || Number(census.elapsedMs) < 0 || Number(census.elapsedMs) >= 5000
      || !Array.isArray(census.rows) || census.rows.length < 1 || census.rows.length > 512
      || census.rows.length !== census.count || census.visited !== census.count
      || intent.foregroundPID !== census.foregroundPID || intent.foregroundCreatedUtcTicks !== census.foregroundCreatedUtcTicks) {
    throw new Error('complete bound OOBE setup census required');
  }
  for (const kind of ['preflight', 'after-preflight']) {
    const guard = setupRecords[`${prefix}-${kind}.json`];
    if (!guard || guard.ready !== true || guard.connectionState !== 0 || guard.connectionStateKnown !== true
        || guard.session !== census.session) throw new Error('fresh setup desktop guard invalid');
  }
  const rows = census.rows as Json[], privateIndices = new Map<number, number>(), semantic: unknown[][] = [];
  const buttons: Json[] = []; let panes = 0;
  for (const [index, row] of rows.entries()) {
    const parent = Number(row.parentIndex);
    if (row.index !== index || !Number.isInteger(row.parentIndex) || parent < -1 || parent >= index
        || !Number.isInteger(row.depth) || Number(row.depth) > 12 || Number(row.depth) < 0
        || (parent === -1 ? index !== 0 || row.depth !== 0 : row.depth !== Number(rows[parent]?.depth) + 1)
        || row.providerPID !== census.foregroundPID || row.providerCreatedUtcTicks !== census.foregroundCreatedUtcTicks
        || row.verifiedImageLeaf !== 'wwahost.exe'
        || [row.elementName, row.automationId, row.className].some(value => typeof value !== 'string' || value.length > 120)
        || [row.nameTruncated, row.automationIdTruncated, row.classNameTruncated,
          row.enabled, row.offscreen, row.invokePatternAvailable].some(value => typeof value !== 'boolean')
        || !Number.isInteger(row.controlType) || !Array.isArray(row.propertyHRESULTs)
        || row.propertyHRESULTs.length !== 10 || row.propertyHRESULTs.some(value => value !== 0)
        || !Array.isArray(row.rectangle) || row.rectangle.length !== 4
        || row.rectangle.some(value => !Number.isInteger(value) || Math.abs(Number(value)) > 1048576)
        || Number(row.rectangle[0]) > Number(row.rectangle[2]) || Number(row.rectangle[1]) > Number(row.rectangle[3])) {
      throw new Error('setup census row not fully verified');
    }
    if (index === 0 && (row.elementName !== 'Microsoft account' || row.controlType !== 50032 || row.nameTruncated !== false)) {
      throw new Error('actual OOBE root role missing');
    }
    const pane = row.elementName === 'Choose privacy settings for your device'
      && row.className === 'Internet Explorer_Server' && row.controlType === 50033
      && row.nameTruncated === false && row.classNameTruncated === false && row.enabled === true && row.offscreen === false;
    if (pane) panes++;
    if (!pane && !privateIndices.has(parent)) continue;
    const privateParent = pane ? -1 : privateIndices.get(parent)!;
    privateIndices.set(index, semantic.length);
    semantic.push([privateParent, row.elementName, row.automationId, row.className, row.controlType,
      row.enabled, row.offscreen, row.rectangle, row.nameTruncated, row.automationIdTruncated,
      row.classNameTruncated, row.invokePatternAvailable]);
    if (row.automationId === 'OobeSettingsAcceptButton' && row.controlType === 50000
        && row.offscreen === false && row.invokePatternAvailable === true && row.nameTruncated === false
        && row.automationIdTruncated === false && Number(row.rectangle[0]) < Number(row.rectangle[2])
        && Number(row.rectangle[1]) < Number(row.rectangle[3])
        && ['Next, tab through all privacy settings to continue', 'Accept', 'Accept these privacy settings']
          .includes(String(row.elementName))) buttons.push(row);
  }
  const digest = createHash('sha256').update(JSON.stringify(semantic)).digest('hex');
  if (panes !== 1 || buttons.length !== 1 || digest !== census.semanticStateSHA256
      || census.eligiblePrivacyButtons !== Number(buttons[0]?.enabled)) {
    throw new Error('independent privacy state/unique button binding failed');
  }
  return { ...census, selectedButton: buttons[0] };
}
function verifySetup(step: Step): void {
  collectSetupRecords();
  if (evidence.setupRecordErrors) throw new Error('partial setup evidence invalid');
  const result = setupRecord('oobe-setup-result.json'), intent = setupRecord('oobe-setup-intent.json');
  evidence.oobeSetupResult = result;
  const count = Number(result.invokeCallsEntered), next = Number(result.nextCallsEntered);
  if (!Number.isInteger(count) || count < 0 || count > 5 || !Number.isInteger(next) || next < 0 || next > 4
      || !Number.isInteger(result.acceptCallsEntered) || Number(result.acceptCallsEntered) < 0 || Number(result.acceptCallsEntered) > 1
      || count !== next + Number(result.acceptCallsEntered) || typeof result.invokeEffectUncertain !== 'boolean'
      || !Number.isInteger(result.invokeCallsReturned) || Number(result.invokeCallsReturned) < 0 || Number(result.invokeCallsReturned) > count
      || !Number.isInteger(result.progressionsObserved) || Number(result.progressionsObserved) < 0
      || Number(result.progressionsObserved) > Number(result.invokeCallsReturned)) throw new Error('terminal setup state invalid');
  evidence.invokeEffectUncertain = result.invokeEffectUncertain;
  evidence.invokeCallsEntered = result.invokeCallsEntered;
  if (intent.maxNextCalls !== 4 || intent.maxAcceptCalls !== 1 || intent.helperBudgetMs !== 60000
      || intent.cooperativeUIBudgetMs !== 5000 || intent.maxAfterObservations !== 8
      || intent.afterObservationBudgetMs !== 5000 || intent.retryAllowed !== false) throw new Error('finite setup intent invalid');
  requireSuccess(step);
  if (result.setupQualified !== true || result.phase !== 'complete' || result.readOnly !== false
      || !Number.isInteger(next) || next < 0 || next > 4 || result.acceptCallsEntered !== 1 || count !== next + 1
      || result.invokeCallsReturned !== count || result.progressionsObserved !== count || result.invokeBoundaryArmed !== true
      || result.invokeEffectUncertain !== false || result.ownedOOBEWindowGone !== true || result.lastErrorHRESULT !== 0
      || !Number.isInteger(result.elapsedMs) || Number(result.elapsedMs) < 0 || Number(result.elapsedMs) >= 60000
      || result.showAttempts !== 0 || result.keyboardOrPointerInputAttempted !== false || result.registryWriteAPIAttempted !== false
      || result.installAttempted !== false || result.launchAttempted !== false || result.retryAllowed !== false
      || result.atomicUIBindingQualified !== false || result.centerOpenedProved !== false
      || result.nativeCallbackQualified !== false || result.navigationQualified !== false || result.processQuiescenceQualified !== false) {
    throw new Error('known finite setup completion required');
  }
  let previous: Json | undefined; let afterCensuses = 0;
  for (let index = 0; index < count; index++) {
    const prefix = `oobe-setup-${index}`, before = setupSnapshot(`${prefix}-before`, index);
    const armed = setupRecord(`${prefix}-armed.json`), returned = setupRecord(`${prefix}-returned.json`);
    const progress = setupRecord(`${prefix}-progress.json`), accept = index === next, action = accept ? 'Accept' : 'Next';
    const button = before.selectedButton as Json;
    if ([armed, returned, progress].some(record => record.step !== index || record.action !== action)
        || armed.invokeBoundaryArmed !== true || armed.invokeCallEntered !== false || armed.retryAllowed !== false
        || armed.setupImagePathKind !== 'exact_Windows_System32_WWAHost_kernel_image'
        || armed.targetAutomationId !== button.automationId || armed.targetName !== button.elementName
        || (accept ? !['Accept', 'Accept these privacy settings'].includes(String(button.elementName))
          : button.elementName !== 'Next, tab through all privacy settings to continue')
        || armed.beforeStateSHA256 !== before.semanticStateSHA256 || progress.beforeStateSHA256 !== before.semanticStateSHA256
        || ['foregroundPID', 'foregroundCreatedUtcTicks', 'foregroundHWND'].some(key => armed[key] !== before[key])
        || returned.invokeCallEntered !== true || returned.invokeCallReturned !== true || returned.hresult !== 0
        || progress.progressionObserved !== true || progress.ownedOOBEWindowGone !== accept
        || previous && (previous.semanticStateSHA256 !== before.semanticStateSHA256
          || ['foregroundPID', 'foregroundCreatedUtcTicks', 'foregroundHWND'].some(key => previous![key] !== before[key]))) {
      throw new Error('setup intent/return/fresh progression chain invalid');
    }
    if (accept) {
      if (progress.afterStateSHA256 !== null || progress.afterSnapshotPrefix !== null
          || progress.afterObservationCount !== 0 || progress.afterObservationElapsedMs !== 0) {
        throw new Error('Accept requires window disappearance, not guessed subtree');
      }
    } else {
      const observations = Number(progress.afterObservationCount);
      if (!Number.isInteger(observations) || observations < 1 || observations > 8
          || !Number.isInteger(progress.afterObservationElapsedMs) || Number(progress.afterObservationElapsedMs) < 0
          || Number(progress.afterObservationElapsedMs) >= 5000
          || progress.afterSnapshotPrefix !== `${prefix}-after-${observations - 1}`) throw new Error('finite observation bounds invalid');
      let deadline: number | undefined;
      for (let observation = 0; observation < observations; observation++) {
        const after = setupSnapshot(`${prefix}-after-${observation}`, index + 1, true), button = after.selectedButton as Json;
        if (!Number.isSafeInteger(after.startedBootMs) || !Number.isSafeInteger(after.observationDeadlineBootMs)
            || Number(after.observationDeadlineBootMs) <= Number(after.startedBootMs)
            || Number(after.observationDeadlineBootMs) - Number(after.startedBootMs) > 5000
            || Number(after.elapsedMs) >= Number(after.observationDeadlineBootMs) - Number(after.startedBootMs)
            || deadline !== undefined && after.observationDeadlineBootMs !== deadline
            || after.semanticStateSHA256 === before.semanticStateSHA256
            || ['foregroundPID', 'foregroundCreatedUtcTicks', 'foregroundHWND'].some(key => after[key] !== before[key])
            || button.enabled !== (observation === observations - 1)) {
          throw new Error('same-owner changed disabled transition/absolute deadline unproved');
        }
        deadline = Number(after.observationDeadlineBootMs); afterCensuses++;
        if (observation === observations - 1) {
          if (progress.afterStateSHA256 !== after.semanticStateSHA256) throw new Error('fresh eligible progression hash mismatch');
          previous = after;
        }
      }
    }
  }
  if (result.afterCensusesAttempted !== afterCensuses) throw new Error('actual indexed observation count mismatch');
  const finalGuard = setupRecords['oobe-setup-after-preflight.json'];
  if (!finalGuard || finalGuard.ready !== true || finalGuard.connectionStateKnown !== true || finalGuard.connectionState !== 0
      || createHash('sha256').update(readFileSync(binary!)).digest('hex') !== evidence.binarySHA256) {
    throw new Error('final fresh desktop/binary guard unproved');
  }
  evidence.oobeSetupQualified = true; evidence.invokeEffectUncertain = false;
}
async function main(): Promise<void> {
  if (process.env.AGENT_NOTIFY_NAVIGATION_WINDOWS_E2E !== '1' || process.env.CI !== 'true'
      || process.env.GITHUB_ACTIONS !== 'true' || process.platform !== 'win32') {
    throw new Error('requires explicit opt-in on disposable GitHub Windows CI');
  }
  if (process.env.NAVIGATION_WINDOWS_RUNNER !== 'windows-11-vs2026-arm') {
    throw new Error('qualification requires explicit supported Windows 11 client label');
  }
  if (Number(process.versions.node.split('.')[0]) !== 24) throw new Error('requires Node 24');
  nonce = randomUUID();
  root = join(realpathSync(process.env.RUNNER_TEMP ?? tmpdir()), `navigation-windows-test-${nonce}`);
  mkdirSync(root, { recursive: false });
  writeFileSync(join(root, '.owned-test-root'), `TEST navigation Windows ${nonce}\n`, { flag: 'wx' });
  const built = realpathSync(resolve(process.argv[2] ?? ''));
  if (!statSync(built).isFile()) throw new Error('compiled native probe absent');
  binary = join(root, 'navigation-native-probe.exe'); copyFileSync(built, binary);
  evidence.root = root; evidence.nonce = nonce;
  evidence.binarySHA256 = createHash('sha256').update(readFileSync(binary)).digest('hex');
  const diagnosticFlag = process.env.NAVIGATION_WINDOWS_PREFLIGHT_ONLY;
  if (diagnosticFlag !== undefined && diagnosticFlag !== '0' && diagnosticFlag !== '1') {
    throw new Error('invalid preflight-only flag');
  }
  // Missing flag stays read-only; native submission requires the explicit '0' mode.
  const diagnosticOnly = diagnosticFlag !== '0';
  const surfaceFlag = process.env.NAVIGATION_WINDOWS_CENTER_SURFACE;
  if (surfaceFlag !== undefined && surfaceFlag !== '0' && surfaceFlag !== '1') throw new Error('invalid surface flag');
  const surfaceOnly = surfaceFlag === '1';
  const taskbarFlag = process.env.NAVIGATION_WINDOWS_TASKBAR_UIA;
  if (taskbarFlag !== undefined && taskbarFlag !== '0' && taskbarFlag !== '1') throw new Error('invalid taskbar flag');
  const taskbarOnly = taskbarFlag === '1';
  const captureFlag = process.env.NAVIGATION_WINDOWS_DESKTOP_CAPTURE_TEST;
  if (captureFlag !== undefined && captureFlag !== '0' && captureFlag !== '1') throw new Error('invalid desktop capture flag');
  const captureOnly = captureFlag === '1';
  const oobeFlag = process.env.NAVIGATION_WINDOWS_OOBE_PREFLIGHT_TEST;
  if (oobeFlag !== undefined && oobeFlag !== '0' && oobeFlag !== '1') throw new Error('invalid OOBE preflight flag');
  const oobeOnly = oobeFlag === '1';
  const setupFlag = process.env.NAVIGATION_WINDOWS_OOBE_SETUP_TEST;
  if (setupFlag !== undefined && setupFlag !== '0' && setupFlag !== '1') throw new Error('invalid OOBE setup flag');
  const setupOnly = setupFlag === '1';
  const sampleFlag = process.env.NAVIGATION_WINDOWS_PUBLIC_SAMPLE_TEST;
  if (sampleFlag !== undefined && sampleFlag !== '0' && sampleFlag !== '1') throw new Error('invalid public-sample flag');
  const publicSample = sampleFlag === '1';
  if (publicSample && diagnosticOnly) throw new Error('public sample requires explicit native TEST submission');
  evidence.nativeSubmissionProfile = publicSample ? 'public_sample_one_show' : 'strict_setting';
  if (publicSample) evidence.scope = 'explicit TEST public-sample Show, readiness unqualified, cold COM callback only';
  evidence.notifierReadinessQualified = false;

  if ((surfaceOnly || taskbarOnly || captureOnly || oobeOnly || setupOnly) && !diagnosticOnly
      || Number(surfaceOnly) + Number(taskbarOnly) + Number(captureOnly) + Number(oobeOnly) + Number(setupOnly) > 1) {
    throw new Error('selected TEST modes are exclusive');
  }
  if ((captureOnly || oobeOnly || setupOnly || publicSample) && (process.env.GITHUB_REPOSITORY !== '777genius/agent-notifications'
      || process.env.GITHUB_EVENT_NAME !== 'workflow_dispatch' || process.env.GITHUB_RUN_ATTEMPT !== '1'
      || process.arch !== 'arm64'
      || !/^[0-9a-f]{40}$/.test(process.env.NAVIGATION_SOURCE_SHA ?? ''))) {
    throw new Error('selected foreground/capture requires first explicit manual TEST job and exact source');
  }
  evidence.diagnosticOnly = diagnosticOnly && !setupOnly; evidence.readOnly = diagnosticOnly && !setupOnly;
  if (diagnosticOnly) {
    evidence.scope = 'Windows TEST read-only Center policy and input desktop preflight';
    evidence.noShowProved = true;
  }
  const policyStep = run('center-policy', 10_000);
  if (existsSync(join(root, 'center-policy.json'))) evidence.centerPolicy = read('center-policy.json');
  requireSuccess(policyStep);
  const policy = evidence.centerPolicy as Json | undefined;
  if (!policy || policy.pid !== policyStep.pid || policy.nonce !== nonce || policy.lookupComplete !== true
      || policy.registryView !== 'native64' || policy.effectiveShellPolicyProved !== false
      || typeof policy.configuredDisabled !== 'boolean') throw new Error('Center policy evidence invalid');
  const preflight = run('preflight', 15_000);
  if (existsSync(join(root, 'preflight.json'))) evidence.preflight = read('preflight.json');
  const desktop = evidence.preflight as Json | undefined;
  if (!desktop || desktop.pid !== preflight.pid || desktop.nonce !== nonce) throw new Error('desktop preflight correlation invalid');
  if (preflight.status === 3) {
    evidence.status = 'unavailable'; throw new Error('Windows client/input desktop/Shell prerequisite unavailable');
  }
  requireSuccess(preflight);
  if (desktop.ready !== true || desktop.connectionStateKnown !== true || desktop.connectionState !== 0
      || desktop.connectionStateQuerySucceeded !== true || desktop.connectionStateError !== 0) {
    throw new Error('active connected desktop preflight not ready');
  }
  if (diagnosticOnly) {
    if (setupOnly) {
      evidence.scope = 'Windows disposable TEST finite privacy OOBE guest setup only';
      evidence.oobeSetupModeAttempted = true; evidence.oobeSetupQualified = false;
      evidence.invokeEffectUncertain = true; evidence.invokeCallsEntered = null;
      evidence.centerOpenedProved = false; evidence.processQuiescenceQualified = false;
      setupActor = run('oobe-setup', 75_000);
      evidence.oobeSetupActorCollected = !setupActor.error && !setupActor.signal && setupActor.status !== null;
      verifySetup(setupActor);
    }
    if (captureOnly || setupOnly) {
      if (captureOnly) evidence.scope = 'Windows disposable TEST primary-desktop pixels and bounded Windows-owner metadata only';
      evidence.captureModeAttempted = true; evidence.captureQualified = false;
      const step = run('desktop-capture', 15_000);
      evidence.captureActorCollected = !step.error && !step.signal && step.status !== null;
      for (const name of ['capture-preflight.json', 'capture-after-preflight.json', 'desktop-capture-intent.json', 'desktop-capture.json']) {
        if (!existsSync(join(root, name))) continue;
        const record = read(name); evidence[name] = record;
        if (record.pid !== step.pid || record.nonce !== nonce) throw new Error('capture report correlation invalid');
      }
      requireSuccess(step);
      const capture = evidence['desktop-capture.json'] as Json | undefined;
      const intent = evidence['desktop-capture-intent.json'] as Json | undefined;
      for (const name of ['capture-preflight.json', 'capture-after-preflight.json']) {
        const record = evidence[name] as Json | undefined;
        if (!record || record.ready !== true || record.connectionState !== 0 || record.connectionStateKnown !== true
            || record.session !== capture?.session) throw new Error('capture fresh desktop guards unproved');
      }
      if (!capture || !intent || intent.captureAttempts !== 1 || intent.sourceSHA !== evidence.sourceSHA
          || capture.sourceSHA !== evidence.sourceSHA || capture.captureAttempts !== 1 || capture.pixelFile !== 'desktop-capture.png'
          || capture.binarySHA256 !== evidence.binarySHA256 || capture.architecture !== 'ARM64'
          || capture.captureScope !== 'primary_monitor_physical_GDI_pixels_non_atomic_metadata'
          || capture.coordinateContract !== 'per_monitor_aware_v2_MM_TEXT' || capture.primaryMonitorStable !== true
          || !Number.isInteger(capture.originX) || !Number.isInteger(capture.originY)
          || Number(capture.originX) < -2147483648 || Number(capture.originX) > 2147483647
          || Number(capture.originY) < -2147483648 || Number(capture.originY) > 2147483647 || capture.shellStillLive !== true
          || capture.diagnosticOnly !== true || capture.readOnly !== true || capture.showAttempts !== 0 || capture.inputAttempted !== false
          || capture.launchAttempted !== false || capture.installAttempted !== false || capture.retryAllowed !== false
          || capture.centerOpenedProved !== false || capture.navigationQualified !== false || capture.nativeCallbackQualified !== false
          || !Number.isInteger(capture.elapsedMs) || Number(capture.elapsedMs) < 0 || Number(capture.elapsedMs) >= 5000
          || !Number.isInteger(capture.width) || !Number.isInteger(capture.height)
          || Number(capture.width) < 1 || Number(capture.width) > 2048 || Number(capture.height) < 1 || Number(capture.height) > 2048) {
        throw new Error('capture terminal scope or bounds unproved');
      }
      const path = join(root, 'desktop-capture.png'), st = lstatSync(path);
      if (!st.isFile() || st.isSymbolicLink() || st.nlink !== 1 || realpathSync(path) !== path || st.size < 33 || st.size > 8_388_608) {
        throw new Error('bounded regular owned PNG required');
      }
      const pixels = readFileSync(path), pixelSHA256 = createHash('sha256').update(pixels).digest('hex');
      if (pixels.length !== st.size || pixels.length !== capture.pixelBytes || pixelSHA256 !== capture.pixelSHA256
          || !pixels.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]))
          || pixels.readUInt32BE(8) !== 13 || pixels.toString('ascii', 12, 16) !== 'IHDR'
          || pixels.readUInt32BE(16) !== capture.width || pixels.readUInt32BE(20) !== capture.height) {
        throw new Error('PNG bytes/dimensions do not bind native receipt');
      }
      const metadata = capture.metadata as Json | undefined;
      if (!metadata || metadata.projection !== 'visible_or_foreground_same_user_windows_oobe_or_shell'
          || !Array.isArray(metadata.windows) || metadata.windows.length > 32 || Number(metadata.visited) > 128
          || Buffer.byteLength(JSON.stringify(metadata)) > 7000) throw new Error('capture metadata outside bound');
      evidence.capturePixelSHA256 = pixelSHA256; evidence.capturePixelBytes = pixels.length;
      if (createHash('sha256').update(readFileSync(binary)).digest('hex') !== evidence.binarySHA256) {
        throw new Error('capture executable changed across snapshot');
      }
      evidence.captureQualified = true; evidence.noKeyboardOrPointerInputProved = true;
      if (captureOnly) { evidence.noInputProved = true; evidence.inputEffectUncertain = false; }
    }
    if (oobeOnly) {
      evidence.scope = 'Windows disposable TEST selected foreground UIA property census only';
      evidence.oobeModeAttempted = true; evidence.oobeCensusObserved = false;
      evidence.oobeObservationUncertain = true; evidence.selectorActionQualified = false;
      const step = run('oobe-preflight', 15_000);
      evidence.oobeActorCollected = !step.error && !step.signal && step.status !== null;
      for (const name of ['oobe-preflight.json', 'oobe-after-preflight.json', 'oobe-intent.json', 'oobe-uia.json']) {
        if (!existsSync(join(root, name))) continue;
        const record = read(name); evidence[name] = record;
        if (record.pid !== step.pid || record.nonce !== nonce) throw new Error('OOBE report correlation invalid');
      }
      requireSuccess(step);
      const census = evidence['oobe-uia.json'] as Json | undefined, intent = evidence['oobe-intent.json'] as Json | undefined;
      if (!census || !intent || census.sourceSHA !== evidence.sourceSHA || intent.sourceSHA !== evidence.sourceSHA
          || census.binarySHA256 !== evidence.binarySHA256 || census.architecture !== 'ARM64'
          || census.projection !== 'selected_foreground_UIA_properties'
          || census.imageAuthority !== 'kernel_image_under_Windows_not_signature_qualification'
          || census.diagnosticOnly !== true || census.readOnly !== true || census.censusAttempts !== 1 || intent.censusAttempts !== 1
          || census.showAttempts !== 0 || intent.showAttempts !== 0 || census.inputAttempted !== false || intent.inputAttempted !== false
          || census.invokeAttempted !== false || census.launchAttempted !== false || census.installAttempted !== false
          || census.retryAllowed !== false || census.selectorActionQualified !== false || census.negativeIsAbsenceProof !== false
          || census.sameUserSession !== true || census.centerOpenedProved !== false
          || census.nativeCallbackQualified !== false || census.navigationQualified !== false
          || census.available !== true || census.rootStable !== true || !Array.isArray(census.rows)
          || census.rows.length !== census.count || Buffer.byteLength(JSON.stringify(census)) > 65_536
          || census.foregroundPID !== intent.foregroundPID || census.foregroundCreatedUtcTicks !== intent.foregroundCreatedUtcTicks
          || typeof census.foregroundCreatedUtcTicks !== 'string' || !/^[1-9][0-9]{0,19}$/.test(census.foregroundCreatedUtcTicks)
          || typeof census.verifiedImageLeaf !== 'string' || census.verifiedImageLeaf.length < 1 || census.verifiedImageLeaf.length > 120 || /[\\/]/.test(census.verifiedImageLeaf)
          || typeof census.windowClass !== 'string' || census.windowClass.length < 1 || census.windowClass.length > 120
          || !Number.isInteger(census.foregroundHWND) || Number(census.foregroundHWND) <= 0
          || !Number.isInteger(census.foregroundPID) || Number(census.foregroundPID) <= 0
          || Number(census.foregroundPID) > 0xffffffff || !Number.isInteger(census.session) || Number(census.session) <= 0) {
        throw new Error('OOBE native authority/scope or bounds invalid');
      }
      for (const key of ['walkCompleted', 'truncated', 'deadlineExpired']) {
        if (typeof census[key] !== 'boolean') throw new Error('OOBE observation state invalid');
      }
      for (const [key, max] of [['visited', 512], ['count', 512], ['verifiedOwners', 16], ['providerSkips', 512],
        ['errors', 16_384], ['elapsedMs', 15_000]] as const) {
        if (!Number.isInteger(census[key]) || Number(census[key]) < 0 || Number(census[key]) > max) {
          throw new Error('OOBE observation bounds invalid');
        }
      }
      if (Number(census.visited) < census.rows.length || census.walkCompleted && (census.truncated !== false
          || census.deadlineExpired !== false || census.errors !== 0 || census.providerSkips !== 0
          || census.rows.length === 0 || Number(census.elapsedMs) >= 5000)) throw new Error('OOBE completeness claim invalid');
      for (const name of ['oobe-preflight.json', 'oobe-after-preflight.json']) {
        const record = evidence[name] as Json | undefined;
        if (!record || record.ready !== true || record.connectionState !== 0 || record.connectionStateKnown !== true
            || record.session !== census.session) throw new Error('OOBE fresh desktop guards unproved');
      }
      const rows = census.rows as Json[];
      for (const [index, row] of rows.entries()) {
        const parent = row.parentIndex;
        if (row.index !== index || !Number.isInteger(parent) || typeof parent !== 'number' || parent < -1 || parent >= index
            || !Number.isInteger(row.depth) || Number(row.depth) < 0 || Number(row.depth) > 12
            || (parent === -1 ? index !== 0 || row.depth !== 0 : row.depth !== Number(rows[parent]?.depth) + 1)
            || !Number.isInteger(row.providerPID) || Number(row.providerPID) <= 0 || Number(row.providerPID) > 0xffffffff
            || typeof row.providerCreatedUtcTicks !== 'string' || !/^[1-9][0-9]{0,19}$/.test(row.providerCreatedUtcTicks)
            || typeof row.verifiedImageLeaf !== 'string' || row.verifiedImageLeaf.length < 1 || row.verifiedImageLeaf.length > 120 || /[\\/]/.test(row.verifiedImageLeaf)
            || [row.elementName, row.automationId, row.className].some(value => value !== null && (typeof value !== 'string' || value.length > 120))
            || [row.nameTruncated, row.automationIdTruncated, row.classNameTruncated].some(value => typeof value !== 'boolean')
            || row.controlType !== null && !Number.isInteger(row.controlType)
            || [row.enabled, row.offscreen, row.invokePatternAvailable].some(value => value !== null && typeof value !== 'boolean')
            || row.rectangle !== null && (!Array.isArray(row.rectangle) || row.rectangle.length !== 4
              || row.rectangle.some(value => !Number.isInteger(value) || Math.abs(Number(value)) > 1048576)
              || Number(row.rectangle[0]) > Number(row.rectangle[2]) || Number(row.rectangle[1]) > Number(row.rectangle[3]))
            || !Array.isArray(row.propertyHRESULTs) || row.propertyHRESULTs.length !== 10
            || row.propertyHRESULTs.some(hr => !Number.isInteger(hr) || hr < -2147483648 || hr > 2147483647)
            || Object.keys(row).some(key => !['index', 'parentIndex', 'depth', 'providerPID', 'providerCreatedUtcTicks',
              'verifiedImageLeaf', 'elementName', 'automationId', 'className', 'nameTruncated', 'automationIdTruncated',
              'classNameTruncated', 'controlType', 'enabled', 'offscreen', 'invokePatternAvailable', 'rectangle', 'propertyHRESULTs'].includes(key))) {
          throw new Error('OOBE row contract invalid');
        }
      }
      if (!rows[0] || rows[0].providerPID !== census.foregroundPID
          || rows[0].providerCreatedUtcTicks !== census.foregroundCreatedUtcTicks
          || rows[0].verifiedImageLeaf !== census.verifiedImageLeaf) throw new Error('OOBE root row kernel binding invalid');
      if (createHash('sha256').update(readFileSync(binary)).digest('hex') !== evidence.binarySHA256) {
        throw new Error('OOBE executable changed across census');
      }
      evidence.oobeCensusObserved = true; evidence.oobeSnapshotComplete = census.walkCompleted;
      evidence.oobeObservationUncertain = false; evidence.noInputProved = true; evidence.inputEffectUncertain = false;
    }
    if (taskbarOnly) {
      evidence.scope = 'Windows TEST read-only owned taskbar UIA identifier projection';
      const step = run('taskbar-uia', 15_000);
      for (const name of ['taskbar-preflight.json', 'taskbar-uia.json']) {
        if (!existsSync(join(root, name))) continue;
        const record = read(name); evidence[name] = record;
        if (record.pid !== step.pid || record.nonce !== nonce) throw new Error('taskbar report correlation invalid');
      }
      const taskbar = evidence['taskbar-uia.json'] as Json | undefined;
      if (!taskbar || taskbar.diagnosticOnly !== true || taskbar.readOnly !== true || taskbar.inputAttempted !== false || taskbar.showAttempts !== 0
          || taskbar.centerActionQualified !== false || taskbar.negativeIsAbsenceProof !== false
          || taskbar.projection !== 'owned_taskbar_identifiers' || !Array.isArray(taskbar.rows)
          || taskbar.rows.length !== taskbar.count || taskbar.rows.length > 128
          || Buffer.byteLength(JSON.stringify(taskbar.rows).slice(1, -1)) > 10000) throw new Error('taskbar projection invalid');
      for (const key of ['available', 'rootStable', 'walkCompleted', 'truncated', 'deadlineExpired']) {
        if (typeof taskbar[key] !== 'boolean') throw new Error('taskbar observation state invalid');
      }
      for (const [key, max] of [['visited', 128], ['verifiedOwners', 16], ['providerSkips', 128], ['errors', 1024]] as const) {
        if (!Number.isInteger(taskbar[key]) || Number(taskbar[key]) < 0 || Number(taskbar[key]) > max) {
          throw new Error('taskbar observation bounds invalid');
        }
      }
      if (Number(taskbar.visited) < taskbar.rows.length || taskbar.walkCompleted && (taskbar.available !== true
          || taskbar.rootStable !== true || taskbar.truncated !== false || taskbar.deadlineExpired !== false
          || taskbar.errors !== 0 || taskbar.providerSkips !== 0 || taskbar.rows.length === 0)) {
        throw new Error('taskbar completeness claim invalid');
      }
      evidence.noInputProved = true; evidence.inputEffectUncertain = false;
      if (step.status === 3 && !step.error && !step.signal) evidence.status = 'unavailable';
      requireSuccess(step);
      const fresh = evidence['taskbar-preflight.json'] as Json | undefined;
      if (!fresh || fresh.ready !== true || fresh.connectionState !== 0 || fresh.connectionStateKnown !== true
          || taskbar.available !== true) throw new Error('taskbar prerequisite evidence invalid');
      const rows = taskbar.rows as Json[];
      for (const [index, row] of rows.entries()) {
        const parent = row.parentIndex;
        if (row.index !== index || !Number.isInteger(parent) || typeof parent !== 'number' || parent < -1 || parent >= index
            || !Number.isInteger(row.depth) || typeof row.depth !== 'number' || row.depth < 0 || row.depth > 16
            || (parent === -1 ? index !== 0 || row.depth !== 0 : row.depth !== Number(rows[parent]?.depth) + 1)
            || !Number.isInteger(row.providerPID) || Number(row.providerPID) <= 0
            || !Array.isArray(row.propertyHRESULTs) || row.propertyHRESULTs.length !== 6
            || row.propertyHRESULTs.some(hr => !Number.isInteger(hr) || hr < -2147483648 || hr > 2147483647)
            || [row.automationId, row.className].some(value => value !== null && (typeof value !== 'string' || value.length > 128))
            || row.controlType !== null && !Number.isInteger(row.controlType)
            || row.offscreen !== null && typeof row.offscreen !== 'boolean'
            || Object.keys(row).some(key => !['index', 'parentIndex', 'depth', 'providerPID', 'automationId', 'className',
              'controlType', 'offscreen', 'propertyHRESULTs'].includes(key))) throw new Error('taskbar row contract invalid');
      }
    }
    if (surfaceOnly) {
      if (policy.configuredDisabled) throw new Error('Center disable policy configured; no input attempted');
      evidence.scope = 'Windows TEST single Win+N Shell surface diagnostic, no native Show';
      evidence.inputEffectUncertain = true;
      const surfaceStep = run('center-surface', 15_000);
      for (const name of ['surface-preflight.json', 'center-surface-before.json', 'center-surface-rejected.json', 'center-surface-intent.json', 'center-surface.json']) {
        if (existsSync(join(root, name))) {
          const record = read(name); evidence[name] = record;
          if (record.pid !== surfaceStep.pid || record.nonce !== nonce) throw new Error('surface report correlation invalid');
        }
      }
      const surface = evidence['center-surface.json'] as Json | undefined;
      const rejected = evidence['center-surface-rejected.json'] as Json | undefined;
      if (surfaceStep.status === 3 && !surfaceStep.error && !surfaceStep.signal && rejected?.inputAttempted === false
          && rejected.showAttempts === 0 && !evidence['center-surface-intent.json'] && !surface) {
        evidence.inputEffectUncertain = false; evidence.noInputProved = true; evidence.status = 'unavailable';
      }
      if (surface && surface.pid === surfaceStep.pid && surface.nonce === nonce
          && typeof surface.keyReleaseUnknown === 'boolean') evidence.inputEffectUncertain = surface.keyReleaseUnknown;
      requireSuccess(surfaceStep);
      const fresh = evidence['surface-preflight.json'] as Json | undefined;
      const intent = evidence['center-surface-intent.json'] as Json | undefined;
      if (!fresh || fresh.ready !== true || fresh.connectionState !== 0 || fresh.connectionStateKnown !== true
          || !intent || intent.chordIntent !== 1 || intent.showAttempts !== 0) throw new Error('surface prerequisite evidence invalid');
      if (!surface || surface.pid !== surfaceStep.pid || surface.nonce !== nonce || surface.chordAttempts !== 1
          || surface.chordAccepted !== true || surface.keyReleaseUnknown !== false || surface.showAttempts !== 0
          || surface.centerOpenedProved !== false || surface.diagnosticOnly !== true) throw new Error('surface evidence invalid');
    }
    evidence.status = setupOnly ? 'guest_setup_complete' : 'diagnostic_complete'; evidence.noShowProved = true; exitCode = 0;
    return;
  }
  if (policy.configuredDisabled) {
    evidence.status = 'unavailable'; evidence.noShowProved = true;
    throw new Error('Center disable policy configured; no settings changed or submission attempted');
  }
  // This records submission intent only. Native terminal records establish the Show count.
  installed = true; evidence.submissionIntent = true; evidence.showAttempts = null;
  evidence.showCallOutcome = 'unknown'; evidence.nativeEffectUncertain = true;
  writeFileSync(join(root, 'submission-attempted'), nonce, { flag: 'wx' });
  const sender = run(publicSample ? 'send-public-sample' : 'send', 20_000);
  if (existsSync(join(root, 'sender.json'))) evidence.sender = read('sender.json');
  if (existsSync(join(root, 'aumid-identity.json'))) evidence.aumidIdentity = read('aumid-identity.json');
  observeShow(sender);
  if (sender.status === 3) {
    evidence.status = 'unavailable';
    throw new Error('native notifier Setting is not Enabled; settings are not changed');
  }
  requireSuccess(sender);
  const registeredIdentity = evidence.aumidIdentity as Json | undefined;
  if (!registeredIdentity || registeredIdentity.pid !== sender.pid || registeredIdentity.nonce !== nonce
      || registeredIdentity.aumid !== `AgentNotify.Navigation.TEST.${nonce}`
      || registeredIdentity.newKey !== true || registeredIdentity.displayNameMatches !== true
      || registeredIdentity.customActivatorMatches !== true) {
    throw new Error('unique native AUMID registry identity not proved');
  }
  evidence.submitted = read('submitted.json');
  const senderReceipt = evidence.sender as Json;
  if (senderReceipt.submissionProfile !== evidence.nativeSubmissionProfile
      || senderReceipt.readinessQualified !== !publicSample
      || (publicSample ? senderReceipt.notificationSetting !== null : senderReceipt.notificationSetting !== 0)) {
    throw new Error('native submission profile/readiness receipt mismatch');
  }
  if (senderReceipt.pid !== sender.pid || senderReceipt.nonce !== nonce || senderReceipt.showCalledAtReceipt !== false
      || evidence.showAttempts !== 1 || evidence.showReturned !== true) {
    throw new Error('native sender correlation mismatch');
  }
  evidence.notifierReadinessQualified = !publicSample;
  evidence.senderExitedBeforeInvoke = true; evidence.senderExitedAt = sender.exitedAt;
  if (publicSample) {
    const show = read('show-outcome.json');
    if (senderReceipt.aumid !== `AgentNotify.Navigation.TEST.${nonce}` || senderReceipt.session !== desktop?.session
        || typeof senderReceipt.userSid !== 'string' || !/^S-1-[0-9-]{1,184}$/.test(senderReceipt.userSid)
        || typeof senderReceipt.creationTicks !== 'string' || !/^[0-9]{15,20}$/.test(senderReceipt.creationTicks)
        || senderReceipt.executable !== binary || senderReceipt.executableSHA256 !== evidence.binarySHA256
        || senderReceipt.sourceSHA !== evidence.sourceSHA || senderReceipt.submissionProfile !== 'public_sample_one_show'
        || senderReceipt.notificationSetting !== null || senderReceipt.readinessQualified !== false
        || createHash('sha256').update(readFileSync(binary!)).digest('hex') !== evidence.binarySHA256
        || show.pid !== sender.pid || show.nonce !== nonce || show.showCallEntered !== true || show.showCallReturned !== true) {
      throw new Error('public sample collected sender authority invalid');
    }
    const senderBirth = BigInt(senderReceipt.creationTicks as string) / 10000n - 11644473600000n;
    if (senderBirth < 0n || senderBirth > BigInt(sender.exitedAt)) throw new Error('native sender birth exceeds collected exit');
    const authority = { ...senderReceipt, schema: 1, collected: true, exitCode: sender.status, collectedAt: sender.exitedAt,
      senderReceiptSHA256: createHash('sha256').update(readFileSync(join(root, 'sender.json'))).digest('hex'),
      showOutcomeSHA256: createHash('sha256').update(readFileSync(join(root, 'show-outcome.json'))).digest('hex') };
    const bytes = JSON.stringify(authority);
    if (Buffer.byteLength(bytes) > 16_384) throw new Error('public sample authority exceeds bound');
    writeFileSync(join(root, 'public-sample-invoke-authority.json'), bytes, { flag: 'wx', flush: true });
    evidence.publicSampleInvokeAuthoritySHA256 = createHash('sha256').update(bytes).digest('hex');
    evidence.invokeEffectUncertain = true;
  }
  // No controller call to callback mode/CoCreateInstance. OS is the only cold-server launcher.
  const invoke = run('invoke', 30_000);
  if (publicSample && existsSync(join(root, 'ui-invoke-intent.json'))) evidence.uiInvokeIntent = read('ui-invoke-intent.json');
  if (existsSync(join(root, 'ui-invoke.json'))) evidence.uiInvoke = read('ui-invoke.json');
  requireSuccess(invoke);
  if ((evidence.uiInvoke as Json).invokeHRESULT !== 0) throw new Error('UI provider did not accept native Invoke');
  if (publicSample) {
    const intent = evidence.uiInvokeIntent as Json | undefined, returned = evidence.uiInvoke as Json | undefined;
    if (!intent || !returned || intent.nonce !== nonce || intent.pid !== invoke.pid
        || intent.authorityProfile !== 'public_sample_one_show' || intent.selectionKind !== 'toast_default'
        || intent.controlType !== 50007 || intent.specSHA256 !== evidence.publicSampleInvokeAuthoritySHA256
        || intent.invokeBoundaryArmed !== true || intent.invokeCallEntered !== false
        || intent.exactTitleVerified !== true || intent.offscreen !== false || intent.enabled !== true
        || !Number.isInteger(intent.providerPID) || Number(intent.providerPID) <= 0 || Number(intent.providerPID) > 0xffffffff
        || typeof intent.providerCreationTicks !== 'string' || !/^[0-9]{15,20}$/.test(intent.providerCreationTicks)
        || typeof intent.providerImagePathKind !== 'string'
        || !['windows_explorer', 'system32_shellhost', 'systemapps_shellexperiencehost', 'systemapps_cbs_shellhost'].includes(intent.providerImagePathKind)) {
      throw new Error('public sample row Invoke authority invalid');
    }
    const runtime = (value: unknown) => Array.isArray(value) && value.length >= 1 && value.length <= 64
      && value.every(part => Number.isInteger(part) && Number(part) >= -2147483648 && Number(part) <= 2147483647);
    if (![intent.runtimeID, intent.titleRuntimeID, intent.containerRuntimeID].every(runtime)
        || !Array.isArray(intent.titleRuntimeIDs) || intent.titleRuntimeIDs.length < 1 || intent.titleRuntimeIDs.length > 512
        || !intent.titleRuntimeIDs.every(runtime)
        || !intent.titleRuntimeIDs.some(value => JSON.stringify(value) === JSON.stringify(intent.titleRuntimeID))
        || new Set(intent.titleRuntimeIDs.map(value => JSON.stringify(value))).size !== intent.titleRuntimeIDs.length) {
      throw new Error('public sample row title identity set invalid');
    }
    for (const key of ['nonce', 'pid', 'authorityProfile', 'selectionKind', 'controlType', 'specSHA256', 'invokeDeadlineBootMs', 'providerPID',
      'providerCreationTicks', 'providerImagePathKind', 'runtimeID', 'titleRuntimeID', 'titleRuntimeIDs', 'containerRuntimeID',
      'exactTitleVerified', 'offscreen', 'enabled']) {
      if (JSON.stringify(intent[key]) !== JSON.stringify(returned[key])) throw new Error('public sample Invoke binding changed');
    }
    if (returned.invokeCallEntered !== true || returned.invokeCallReturned !== true
        || !Number.isSafeInteger(intent.invokeDeadlineBootMs) || Number(intent.invokeDeadlineBootMs) <= 0
        || !Number.isSafeInteger(returned.returnedBootMs) || Number(returned.returnedBootMs) <= 0
        || Number(returned.returnedBootMs) >= Number(intent.invokeDeadlineBootMs)) throw new Error('public sample timely Invoke terminal absent');
    evidence.invokeEffectUncertain = false;
    evidence.publicSampleDefaultRowInvokeObserved = true;
  }
  for (let i = 0; i < 100 && !existsSync(join(root, 'callback.json')); i++) await delay(100);
  if (!existsSync(join(root, 'callback.json'))) throw new Error('no OS-launched COM callback receipt');
  const callback = read('callback.json'); evidence.callback = callback;
  const expectedAUMID = `AgentNotify.Navigation.TEST.${nonce}`;
  if (callback.matches !== true || callback.nonce !== nonce || callback.aumid !== expectedAUMID
      || typeof callback.pid !== 'number' || callback.pid <= 0 || callback.pid === sender.pid
      || callback.pid === invoke.pid || callback.pid === process.pid
      || typeof callback.startedAt !== 'number' || callback.startedAt < sender.exitedAt) {
    throw new Error('cold callback identity/correlation/post-sender-exit contract violated');
  }
  evidence.status = 'qualified'; evidence.nativeCallbackQualified = true;
  evidence.actualNativeUIInvoke = true; evidence.freshCallbackPID = true; exitCode = 0;
}
try {
  await main();
} catch (error: unknown) {
  evidence.error = error instanceof Error ? error.message : String(error);
} finally {
  if (root && installed) {
    // Only the UUID registration/shortcut/toast history. Native callback exits itself in <=30s.
    const cleanup = run('cleanup', 15_000);
    if (existsSync(join(root, 'cleanup.json'))) evidence.cleanup = read('cleanup.json');
    const cleanupRecord = evidence.cleanup as Json | undefined;
    // When registration reached its complete readback, exact key absence is mandatory.
    const identityCleanupMissing = evidence.aumidIdentity !== undefined
      && cleanupRecord?.ownAumidIdentityRemoved !== true;
    if (cleanup.error || cleanup.status !== 0 || cleanup.signal || identityCleanupMissing) {
      evidence.cleanupFailed = true; evidence.nativeCallbackQualified = false;
      evidence.status = 'failed'; exitCode = 1;
    }
  }
  if (root) {
    if (setupActor) {
      collectSetupRecords();
      const packet = `${JSON.stringify({ nonce, actorPID: setupActor.pid, records: setupRecords })}\n`;
      if (Buffer.byteLength(packet) <= 6_291_456) {
        try {
          writeFileSync(join(root, 'oobe-setup-records.json'), packet, { flag: 'wx' });
          evidence.setupRecordCount = Object.keys(setupRecords).length;
          evidence.setupRecordsSHA256 = createHash('sha256').update(packet).digest('hex');
        } catch (error: unknown) { evidence.setupRecordPublicationError = String(error); evidence.oobeSetupQualified = false; exitCode = 1; }
      } else { evidence.setupRecordPublicationError = 'bounded packet overflow'; evidence.oobeSetupQualified = false; exitCode = 1; }
      if (evidence.setupRecordErrors || evidence.oobeSetupQualified !== true) { evidence.status = 'failed'; exitCode = 1; }
    }
    for (const name of ['center-policy.json', 'preflight.json', 'capture-preflight.json', 'capture-after-preflight.json', 'desktop-capture-intent.json', 'desktop-capture.json', 'oobe-preflight.json', 'oobe-after-preflight.json', 'oobe-intent.json', 'oobe-uia.json', 'shortcut-location.json', 'aumid-identity.json', 'sender.json', 'sender-failure.json', 'show-outcome.json', 'submitted.json', 'callback-started.json', 'callback.json', 'public-sample-invoke-authority.json', 'ui-invoke-intent.json', 'ui-candidate.json', 'ui-invoke.json', 'center-open.json']) {
      if (!existsSync(join(root, name))) continue;
      try { evidence[name] = read(name); } catch (error: unknown) { evidence[`${name}ReadError`] = String(error); }
    }
    for (const name of readdirSync(root).filter(name => /^(shortcut-readback|shell-identity|shell-notify|shell-readiness-\d+)\.json$/.test(name)).slice(0, 20)) {
      try { evidence[name] = read(name); } catch (error: unknown) { evidence[`${name}ReadError`] = String(error); }
    }
    writeFileSync(join(root, 'evidence.json'), `${JSON.stringify(evidence, null, 2)}\n`, { flag: 'wx' });
    // A single known path is the workflow artifact handoff, including failed/unavailable reports.
    if (process.env.GITHUB_OUTPUT) writeFileSync(process.env.GITHUB_OUTPUT, `evidence_root=${root}\n`, { flag: 'a' });
  }
  console.log(JSON.stringify(evidence, null, 2)); process.exitCode = exitCode;
}

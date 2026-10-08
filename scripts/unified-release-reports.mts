/** Draft-only all-platform report gate. No builds, native actions or publication. */
import { strict as assert } from 'node:assert';
import { createHash } from 'node:crypto';
import { lstatSync, readFileSync, readdirSync, realpathSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

export const cells = ['linux/amd64', 'linux/arm64', 'windows/amd64', 'darwin/amd64', 'darwin/arm64']
  .flatMap(target => ['1.18.33', '2.0.21'].map(version => target + '/' + version)).concat('linux/amd64/1.18.34');
type Row = Record<string, unknown>;
export type Binding = {candidate: string; operator: string; signingRun: string; parent: string; adapter: string};
export function validate(rows: Row[], binding: Binding): void {
  assert.equal(rows.length, 11, 'eleven fresh reports required');
  const seen = new Set<string>();
  for (const row of rows) {
    const identity = `${String(row.os)}/${String(row.arch)}/${String(row.version)}`;
    assert(cells.includes(identity) && !seen.has(identity), 'missing, duplicate or unexpected cell');
    seen.add(identity);
    assert.equal(row.status, 'installed_business_lifecycle_observed');
    assert.equal(row.suite, 'business');
    assert.equal(row.businessBodyComplete, true);
    assert.equal(row.actualProviderTransactions, 21);
    assert.equal(row.actualClosedSubmittedChildren, 14);
    assert.equal(row.unifiedReleaseTag, 'v1.48.4');
    assert.equal(row.candidateCommit, binding.candidate);
    assert.equal(row.unifiedCandidate, binding.candidate);
    assert.equal(row.unifiedOperatorSHA, binding.operator);
    assert.equal(row.unifiedSigningRun, binding.signingRun);
    assert.equal(row.unifiedParentSHA256, binding.parent);
    assert.equal(row.unifiedAdapterSHA256, binding.adapter);
    for (const flag of ['fullNativeQualified', 'clockQualified', 'sourceEpochQualified', 'timePolicyQualified',
      'platformLifetimeQualified', 'visibleDesktopQualified', 'installedQualificationGranted', 'coldStartQualified']) {
      assert.equal(row[flag], false, `business reports must not grant ${flag}`);
    }
    for (const hash of ['candidateSHA256', 'embeddedSHA256', 'manifestSHA256', 'sdkArchiveSHA256', 'businessPrerequisitesSHA256']) {
      assert(typeof row[hash] === 'string' && /^[a-f0-9]{64}$/.test(row[hash]), 'actual artifact/proof hash required');
    }
    if (row.os === 'windows') {
      const custody = row.unifiedWindowsRenderCustody as Row[];
      assert(Array.isArray(custody) && custody.length === 3, 'three actual Windows registrations required');
      assert.deepEqual(custody.map(item => item.phase), ['install', 'update', 'reinstall']);
      for (const item of custody) {
        assert.equal(item.binarySHA256, row.candidateSHA256);
        assert.equal(item.canonicalGitAssetSHA256, row.embeddedSHA256);
        assert.equal(item.transform, 'none');
        assert.equal(item.lineEndings, 'LF');
        assert.equal(item.originBound, true);
        assert.equal(item.rawInstalledEquality, true);
        assert.equal(item.renderedInstalledSHA256, item.actualLedgerBundleSHA256);
      }
    }
  }
}
function physical(path: string): string {
  path = resolve(path); assert.equal(realpathSync(path), path); assert(!lstatSync(path).isSymbolicLink()); return path;
}
function hash(path: string): string { return createHash('sha256').update(readFileSync(physical(path))).digest('hex'); }
function main(): void {
  const [reportsArg, distArg, helperArg, sourceArg] = process.argv.slice(2);
  assert(reportsArg && distArg && helperArg && sourceArg);
  const reports = physical(reportsArg), dist = physical(distArg), helper = physical(helperArg), source = physical(sourceArg);
  const binding = {candidate: process.env.RELEASE_CANDIDATE_SHA ?? '', operator: process.env.GITHUB_SHA ?? '',
    signingRun: process.env.RELEASE_SIGNING_RUN ?? '', parent: process.env.AN_EVIDENCE_SHA256 ?? '',
    adapter: hash(join(import.meta.dirname, 'unified-release-qualification.mts'))};
  assert(/^[a-f0-9]{40}$/.test(binding.candidate) && /^[a-f0-9]{40}$/.test(binding.operator));
  assert(/^[1-9][0-9]*$/.test(binding.signingRun) && /^[a-f0-9]{64}$/.test(binding.parent));
  const rows = readdirSync(reports).map(name => JSON.parse(readFileSync(physical(join(reports, name, 'native-report.json')), 'utf8')) as Row);
  validate(rows, binding);
  for (const target of ['linux-amd64', 'linux-arm64', 'windows-amd64', 'darwin-amd64', 'darwin-arm64']) {
    const suffix = target.startsWith('windows') ? '.exe' : '';
    for (const command of ['claude-notifications', 'sound-preview', 'list-devices', 'list-sounds']) {
      assert(lstatSync(physical(join(dist, command + '-' + target + suffix))).size > 0);
    }
    assert(lstatSync(physical(join(dist, 'agent-notify-portable-' + target + '.zip'))).size > 0);
    const binary = hash(join(dist, 'claude-notifications-' + target + suffix));
    assert(rows.filter(row => `${row.os}-${row.arch}` === target).every(row => row.candidateSHA256 === binary));
  }
  assert(lstatSync(physical(join(dist, 'claude-notifications-windows-amd64-focus.exe'))).size > 0);
  const custody = JSON.parse(readFileSync(physical(join(helper, 'ClaudeNotifier-smoke.custody.json')), 'utf8')) as Row;
  assert.equal(custody.sourceSHA, binding.candidate); assert.equal(custody.workflowSHA, binding.candidate);
  assert.equal(custody.runID, binding.signingRun); assert.equal(custody.signatureVerified, true);
  assert.equal(custody.signingTeam, '86399583GS'); assert.equal(custody.archiveSHA256, hash(join(helper, 'ClaudeNotifier-smoke.app.zip')));
  assert(lstatSync(physical(join(source, 'internal/thirdpartynotices/THIRD_PARTY_NOTICES.txt'))).size > 0);
  process.stdout.write(JSON.stringify({status: 'complete_draft_inputs_observed', candidate: binding.candidate,
    cells: 11, platforms: 5, publicationAuthorized: false, fullNativeQualified: false, visibleDesktopQualified: false}) + '\n');
}
if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) main();

import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { cells, validate } from './unified-release-reports.mts';
const binding = {candidate: '1'.repeat(40), operator: '2'.repeat(40), signingRun: '123', parent: '3'.repeat(64), adapter: '4'.repeat(64)};
function reports(): Record<string, unknown>[] {
  return cells.map(identity => {
    const [os, arch, version] = identity.split('/');
    const hash = '5'.repeat(64);
    return {os, arch, version, status: 'installed_business_lifecycle_observed', suite: 'business', businessBodyComplete: true,
      actualProviderTransactions: 21, actualClosedSubmittedChildren: 14, unifiedReleaseTag: 'v1.48.4',
      candidateCommit: binding.candidate, unifiedCandidate: binding.candidate, unifiedOperatorSHA: binding.operator,
      unifiedSigningRun: binding.signingRun, unifiedParentSHA256: binding.parent, unifiedAdapterSHA256: binding.adapter,
      fullNativeQualified: false, clockQualified: false, sourceEpochQualified: false, timePolicyQualified: false,
      platformLifetimeQualified: false, visibleDesktopQualified: false, installedQualificationGranted: false, coldStartQualified: false,
      candidateSHA256: hash, embeddedSHA256: hash, manifestSHA256: hash, sdkArchiveSHA256: hash, businessPrerequisitesSHA256: hash,
      unifiedWindowsRenderCustody: ['install', 'update', 'reinstall'].map(phase => ({phase, binarySHA256: hash,
        canonicalGitAssetSHA256: hash, transform: 'none', lineEndings: 'LF', originBound: true,
        rawInstalledEquality: true, renderedInstalledSHA256: hash, actualLedgerBundleSHA256: hash}))};
  });
}
test('draft gate accepts eleven bound business cells without granting full native qualification', () => validate(reports(), binding));
test('draft gate rejects missing/duplicate cells, old custody, unclosed effects and false grants', () => {
  const missing = reports(); missing.pop(); assert.throws(() => validate(missing, binding));
  const duplicate = reports(); duplicate[10] = duplicate[0]!; assert.throws(() => validate(duplicate, binding));
  for (const [field, value] of [['unifiedCandidate', '0'.repeat(40)], ['unifiedParentSHA256', '0'.repeat(64)],
    ['fullNativeQualified', true], ['actualClosedSubmittedChildren', 13], ['actualProviderTransactions', 22],
    ['status', 'unqualified'], ['businessBodyComplete', false]] as const) {
    const altered = reports(); altered[0]![field] = value; assert.throws(() => validate(altered, binding));
  }
  const windows = reports(); windows.find(row => row.os === 'windows')!.unifiedWindowsRenderCustody = [];
  assert.throws(() => validate(windows, binding));
});
test('draft gate rejects a Windows registration with both installed SHA fields omitted', () => {
  const rows = reports();
  const custody = rows.find(row => row.os === 'windows')!.unifiedWindowsRenderCustody as Record<string, unknown>[];
  delete custody[0]!.renderedInstalledSHA256;
  delete custody[0]!.actualLedgerBundleSHA256;
  assert.throws(() => validate(rows, binding), 'absent hashes must not prove installed byte equality');
});
test('draft gate independently requires two lowercase SHA-256 installed hashes', () => {
  for (const value of [undefined, null, '', '5'.repeat(63), 'A'.repeat(64), 'g'.repeat(64), 123]) {
    const rows = reports();
    const custody = rows.find(row => row.os === 'windows')!.unifiedWindowsRenderCustody as Record<string, unknown>[];
    custody[0]!.renderedInstalledSHA256 = value;
    custody[0]!.actualLedgerBundleSHA256 = value;
    assert.throws(() => validate(rows, binding), 'equal invalid values must not prove installed byte equality');
  }
});

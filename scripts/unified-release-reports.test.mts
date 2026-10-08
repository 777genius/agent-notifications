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

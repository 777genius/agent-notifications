import { createHash } from 'node:crypto';
import { closed, ns } from './protocol.mjs';
import compiledRows from './clock-qualification-data.mjs';

// Parent fills COMPILED rows only after actual embedded-Bun/Go qualification.
// No env/config/IPC/file/callback registers authority. Final output/proof hashes
// belong in an external manifest and do not enter this acyclic semantic ID.
const images = Object.freeze({
  v1: [['1.18.33', '0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427'],
    ['1.18.34', '9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2']],
  v2: [['2.0.21', 'f916986543348d7953d8d43aa048516cdbc3f84f4d0dc9c0c5b9d1da3030cea7']],
});
const hashOK = (value) => typeof value === 'string' && /^[a-f0-9]{64}$/.test(value) && !/^0+$/.test(value);
const canonical = (value) => Array.isArray(value) ? `[${value.map(canonical).join(',')}]` :
  value && typeof value === 'object' ? `{${Object.keys(value).sort().map((key) => `${JSON.stringify(key)}:${canonical(value[key])}`).join(',')}}` : JSON.stringify(value);
// This descriptor names implemented coordinates, never image/clock authority.
export function describeClockSource(goos, goarch) {
  if (goos === 'linux' && ['amd64', 'arm64'].includes(goarch))
    return Object.freeze({ sourceKind: 'linux-proc-boottime', rawKind: 'linux-boottime' });
  if (goos === 'darwin' && ['amd64', 'arm64'].includes(goarch))
    return Object.freeze({ sourceKind: 'darwin-mach-continuous', rawKind: 'darwin-monotonic-raw' });
  if (goos === 'windows' && goarch === 'amd64')
    return Object.freeze({ sourceKind: 'windows-interrupt-precise', rawKind: 'windows-interrupt-precise' });
  throw new TypeError('qualification_unverified');
}
// Pure data seam. Describing a row DOES NOT register it. New image hashes and
// complete source-wall/Q/T proofs remain parent-owned compiled evidence.
export function describeClockPolicy(row) {
  const keys = ['protocol', 'generation', 'goos', 'goarch', 'images', 'algorithmSourceMerkleSHA256',
    'sourceKind', 'rawKind', 'nativeReadBoundNS', 'comparisonBoundNS', 'translationBoundNS'];
  closed(row, [...keys, 'sourceWallBoundNS', 'originalNativeAge'], keys);
  const descriptor = describeClockSource(row.goos, row.goarch);
  const legacy = row.goos === 'linux' && row.goarch === 'amd64';
  const pins = legacy ? images[row.generation] : undefined;
  const versions = legacy ? pins?.map(pin => pin[0]) :
    row.generation === 'v1' ? ['1.18.33'] : row.generation === 'v2' ? ['2.0.21'] : undefined;
  if (row.protocol !== 1 || !versions || row.sourceKind !== descriptor.sourceKind || row.rawKind !== descriptor.rawKind ||
      !hashOK(row.algorithmSourceMerkleSHA256) || !Array.isArray(row.images) || row.images.length !== versions.length)
    throw new TypeError('qualification_unverified');
  const nativeReadBoundNS = ns(row.nativeReadBoundNS), comparisonBoundNS = ns(row.comparisonBoundNS),
    translationBoundNS = ns(row.translationBoundNS);
  let sourceWallBoundNS;
  if (row.goos === 'linux') {
    // Preserve the proc recipe and Linux amd64 semantic IDs byte for byte.
    if (Object.hasOwn(row, 'sourceWallBoundNS') || row.nativeReadBoundNS !== '103000000' ||
        row.comparisonBoundNS !== '430000000' || row.translationBoundNS !== '224000000')
      throw new TypeError('qualification_unverified');
  } else {
    // Mandatory explicit source premise: API quantum or sampled drift is not Q.
    sourceWallBoundNS = ns(row.sourceWallBoundNS);
    if (nativeReadBoundNS < 3000000n || nativeReadBoundNS > 103000000n ||
        comparisonBoundNS > 2000000000n || 2n * nativeReadBoundNS + translationBoundNS > comparisonBoundNS ||
        sourceWallBoundNS > comparisonBoundNS) throw new TypeError('qualification_unverified');
  }
  for (const [index, image] of row.images.entries()) {
    closed(image, ['version', 'imageSHA256']);
    if (image.version !== versions[index] || !hashOK(image.imageSHA256) || legacy && image.imageSHA256 !== pins[index][1])
      throw new TypeError('qualification_unverified');
  }
  const exceptional = row.goos === 'windows' && row.goarch === 'amd64' && row.generation === 'v1' &&
    row.images.length === 1 && row.images[0].version === '1.18.33' &&
    row.images[0].imageSHA256 === '52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c';
  const originalNativeAge = Object.hasOwn(row, 'originalNativeAge') ? row.originalNativeAge : 'bounded';
  if (exceptional ? originalNativeAge !== 'unverified_original_date' : originalNativeAge !== 'bounded')
    throw new TypeError('qualification_unverified');
  // Hash the explicit exception; absent legacy metadata keeps canonical IDs.
  // Entry is independently enforced by the owned serve binding, never IPC.
  const prefix = legacy ? 'linux-amd64-proc-boottime-v1' : `${row.goos}-${row.goarch}-${row.sourceKind}-v1`;
  const profileID = `${prefix}:${createHash('sha256').update(canonical(row)).digest('hex')}`;
  return Object.freeze({ profileID, calibrationID: `${profileID}:same-coordinate`, generation: row.generation,
    images: Object.freeze(row.images.map(image => Object.freeze({ ...image }))),
    goos: row.goos, goarch: row.goarch, sourceKind: descriptor.sourceKind, rawKind: descriptor.rawKind,
    originalNativeAge, nativeReadBoundNS, comparisonBoundNS, translationBoundNS,
    ...(sourceWallBoundNS === undefined ? {} : { sourceWallBoundNS }) });
}
const cells = Object.freeze(compiledRows.map(describeClockPolicy));
export function selectClockCell(generation) {
  const goos = process.platform === 'win32' ? 'windows' : process.platform;
  const goarch = process.arch === 'x64' ? 'amd64' : process.arch;
  const matches = cells.filter(cell => cell.goos === goos && cell.goarch === goarch && cell.generation === generation);
  return matches.length === 1 ? matches[0] : undefined;
}

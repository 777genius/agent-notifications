import { createHash } from 'node:crypto';
import { closed } from './protocol.mjs';
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
// Pure data seam. Describing a row DOES NOT register it. The algorithm Merkle
// excludes qualification-row data, generated dist, binary/pack/output/proof files.
export function describeClockPolicy(row) {
  closed(row, ['protocol', 'generation', 'goos', 'goarch', 'images', 'algorithmSourceMerkleSHA256',
    'sourceKind', 'rawKind', 'nativeReadBoundNS', 'comparisonBoundNS', 'translationBoundNS']);
  const pins = images[row.generation];
  if (row.protocol !== 1 || row.goos !== 'linux' || row.goarch !== 'amd64' || !pins ||
      row.sourceKind !== 'linux-proc-boottime' || row.rawKind !== 'linux-boottime' ||
      row.nativeReadBoundNS !== '103000000' || row.comparisonBoundNS !== '430000000' ||
      row.translationBoundNS !== '224000000' || !hashOK(row.algorithmSourceMerkleSHA256) ||
      !Array.isArray(row.images) || row.images.length !== pins.length) throw new TypeError('qualification_unverified');
  for (const [index, image] of row.images.entries()) {
    closed(image, ['version', 'imageSHA256']);
    if (image.version !== pins[index][0] || image.imageSHA256 !== pins[index][1]) throw new TypeError('qualification_unverified');
  }
  const profileID = `linux-amd64-proc-boottime-v1:${createHash('sha256').update(canonical(row)).digest('hex')}`;
  return Object.freeze({ profileID, calibrationID: `${profileID}:same-coordinate`, generation: row.generation,
    rawKind: 'linux-boottime', nativeReadBoundNS: 103000000n, comparisonBoundNS: 430000000n, translationBoundNS: 224000000n });
}
const cells = Object.freeze(compiledRows.map(describeClockPolicy));
export function selectClockCell(generation) {
  return process.platform === 'linux' && process.arch === 'x64' ? cells.find((cell) => cell.generation === generation) : undefined;
}

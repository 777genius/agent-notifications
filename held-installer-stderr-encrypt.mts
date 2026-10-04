// TEST-only one failed installer diagnostic. Node stdlib crypto; no private key.
import { constants, createHash, createPublicKey, publicEncrypt } from 'node:crypto';

const KEY_ID = 'TEST-AN-Windows-installer-sealed-failure-key-r1';
const KEY_SHA256 = '686ad7bb936635572301fa3c03c23706b7a7de225068330a8258b2b0f4664acf';
const PUBLIC_KEY = `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAqhbBvs8c9aCuC6E+1qCX
XECCU4PYgiJNbBd66rjkaJLN1M84+OS1PrXvN0AJp796rnmzXH6X4RFIKZJ5v28l
O8PGpIHi4Z4pkJD6A1k5dw59Xiocvl1wRsDHXGFrpHPb8A3EZRIelJ4I/89lg8Oa
U9qVxKxN08OuQLdiKgAgOgvnfOsBTGuPqPiHGFmnRuPYkx2MGINDMaSrDfIbKtJ8
0VTxxgryWyto2gaYn+zl9jbISTjwQqSK5S+g/dMxjskWPQBO8BQkN1CTiCc9jaf8
JG3+46nJcMt7tSiVHzfIZ7l1gyeibNXSdwM8feZV3o4nXxLuEQy24cCvEegi6TbE
9QIDAQAB
-----END PUBLIC KEY-----
`;

type SealedRecord = {
  schema: 1;
  keyID: string;
  publicKeySHA256: string;
  plaintextPrefixBytes: number;
  ciphertextBase64: string;
};

async function main(): Promise<void> {
  const pieces: Buffer[] = [];
  let count = 0;
  for await (const chunk of process.stdin) {
    const bytes = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk as string);
    count += bytes.byteLength;
    if (count > 190) throw new Error('input_bound');
    pieces.push(bytes);
  }
  if (createHash('sha256').update(PUBLIC_KEY).digest('hex') !== KEY_SHA256) throw new Error('key_binding');
  const key = createPublicKey(PUBLIC_KEY);
  if (key.asymmetricKeyType !== 'rsa' || key.asymmetricKeyDetails?.modulusLength !== 2048) throw new Error('key_shape');
  const ciphertext = publicEncrypt({ key, padding: constants.RSA_PKCS1_OAEP_PADDING, oaepHash: 'sha256' }, Buffer.concat(pieces, count));
  if (ciphertext.byteLength !== 256) throw new Error('ciphertext_bound');
  const record: SealedRecord = { schema: 1, keyID: KEY_ID, publicKeySHA256: KEY_SHA256,
    plaintextPrefixBytes: count, ciphertextBase64: ciphertext.toString('base64') };
  process.stdout.write(JSON.stringify(record) + '\n');
}

void main().catch(() => { process.exitCode = 1; });

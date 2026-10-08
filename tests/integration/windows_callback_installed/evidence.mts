import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
export const sha = (bytes: Uint8Array): string => createHash('sha256').update(bytes).digest('hex');
export type Tuple = {source: string; nonce: string};
export type Receiver = Tuple & {
  snapshot_sha: string; reference: string; generation: string; selected_full_name: string;
  collected: boolean; receiver_exit_code: number; click_boot_ms: number;
  receiver_collected_boot_ms: number; receiver_birth: string; receiver_pid: number;
};
export function derivedURI(raw: string): string {
  assert(raw !== '.' && raw !== '..' && raw.length > 0);
  return 'codex://threads/' + [...Buffer.from(raw)].map(b =>
    (b >= 65 && b <= 90) || (b >= 97 && b <= 122) || (b >= 48 && b <= 57) || [45,46,95,126].includes(b)
      ? String.fromCharCode(b) : '%' + b.toString(16).toUpperCase().padStart(2,'0')).join('');
}
export function envelope(bytes: Buffer, kind: number, count: number): string[] {
  assert(bytes.length >= 12 && bytes.length <= 65536);
  assert.equal(bytes.subarray(0,12).toString('hex'), '574e4342303030310100'+kind.toString(16).padStart(2,'0')+'00');
  const decoder = new TextDecoder('utf-8',{fatal:true});
  const fields: string[] = []; let at = 12;
  for (let i = 0; i < count; ++i) {
    assert(at+4 <= bytes.length); const length = bytes.readUInt32LE(at); at += 4;
    assert(length && length <= bytes.length-at);
    const text = decoder.decode(bytes.subarray(at,at+length));
    assert(!/[\u0000-\u001f\u007f-\u009f]/u.test(text)); fields.push(text); at += length;
  }
  assert.equal(at,bytes.length); return fields;
}
export function callbackJoin(tuple: Tuple, receiver: Receiver, snapshot: string[], record: string[], expectedAttempt: string,
  intent: string, terminal: string, drained: string, query: string, launch: string, worker: string,
  names: ReadonlySet<string>): {attempt: string; uri_derived: string} {
  assert.equal(receiver.source,tuple.source); assert.equal(receiver.nonce,tuple.nonce);
  assert(receiver.collected && receiver.receiver_exit_code === 0 && receiver.receiver_pid > 0);
  assert(receiver.receiver_birth.match(/^\d+$/));
  assert(receiver.receiver_collected_boot_ms > receiver.click_boot_ms
    && receiver.receiver_collected_boot_ms < receiver.click_boot_ms+70000);
  const m = /^WinAttempt1 ([a-f0-9]{32}) (\d+) (\d+) ([a-f0-9]{32}) ([a-f0-9]{64})\n$/.exec(intent); assert(m);
  const [attempt, entry, deadline, reference, digest] = m.slice(1) as [string,string,string,string,string];
  assert(/^[a-f0-9]{32}$/.test(expectedAttempt)); assert.equal(attempt,expectedAttempt);
  const d = BigInt(deadline), e = BigInt(entry);
  assert(d > e && d-e <= 30000n && e >= BigInt(receiver.click_boot_ms));
  assert.equal(reference,record[1]); assert.equal(digest,record[4]); assert.equal(digest,receiver.snapshot_sha);
  assert.equal(record[0],snapshot[0]); assert.equal(receiver.generation,snapshot[0]);
  assert.equal(receiver.reference,reference); assert.equal(receiver.selected_full_name,snapshot[9]);
  assert.equal(terminal,'accepted effect_entered=1 target_confirmed=0 full_name_atomic=0\n');
  assert.equal(worker,'worker_returned=1 operations_completion_known=1\n');
  assert(!names.has(attempt+'.late') && !names.has(attempt+'.collection-late'));
  const drain = /^actual_operation_completion=1 collected_boot_ms=(\d+) deadline_boot_ms=(\d+)\n$/.exec(drained); assert(drain);
  assert.equal(drain[2],deadline); assert(BigInt(drain[1]!) >= e && BigInt(drain[1]!) < d);
  const q = /^query (\d+)\n$/.exec(query), l = /^launch (\d+)\n$/.exec(launch); assert(q && l);
  assert(BigInt(q[1]!) <= d && BigInt(l[1]!) <= d && BigInt(q[1]!) > e && BigInt(l[1]!) > e);
  return {attempt,uri_derived:derivedURI(record[3]!)};
}

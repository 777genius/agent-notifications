import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import {parseJSON, validateFrame, encodeFrame, clockReceipt, profileReceipt, ns} from './protocol.mjs';
const fixture=fs.readFileSync(new URL('./fixtures/private-frame.json',import.meta.url));
const policy=Object.freeze({generation:'v2',profileID:'independent-fixture-only',calibrationID:'fixture-calibration',rawKind:'linux-boottime',nativeReadBoundNS:103000000n,comparisonBoundNS:430000000n,translationBoundNS:224000000n});
// Literal authored independently from the frozen Go field contract, NOT a consumer-generated oracle.
test('private literal keeps unchanged neutral fact, exact native time and int64 strings',()=>{
 const value=parseJSON(fixture); const roundtrip=parseJSON(encodeFrame(value,policy));
 assert.deepEqual(roundtrip,value); assert.equal(roundtrip.event.provenance.nativeEventID,'evt_terminal');
 assert.equal(roundtrip.provenance.ingressTickNS,'1000010000000');
});
// Red if aliases/omissions/number rounding can become time or incarnation authority.
test('closed private frame rejects missing, unknown, mismatched and overflow authority',()=>{
 const changes=[x=>delete x.origin,x=>x.extra=true,x=>x.event.text='PRIVATE_SENTINEL',x=>delete x.event.provenance,
  x=>delete x.provenance.calibration,x=>x.provenance.ingressTickNS=1000010000000,
  x=>x.provenance.spawnTickNS='9223372036854775808',x=>x.provenance.spawnTickNS='01000020000000',
  x=>x.event.provenance.nativeTime=Number.MAX_SAFE_INTEGER,x=>x.event.provenance.timeBasis='receipt_time',
  x=>x.provenance.calibration.nativeLoNS='1000000500001',x=>x.provenance.calibration.errorNS='0',
  x=>x.provenance.sourceEpoch=null,x=>x.provenance.policyID='caller-selected',x=>x.event.provenance.nativeEventID='',
  x=>x.provenance.epochStartedTickNS='1000010000000',x=>x.provenance.deadlineTickNS=x.provenance.spawnTickNS];
 for(const change of changes){const value=parseJSON(fixture);change(value);assert.throws(()=>validateFrame(value,policy));}
 const v1=parseJSON(fixture);v1.event.kind='terminal_error';delete v1.event.messageID;
 v1.event.provenance={generation:'v1',observationID:'original-error',nativeTime:1700000000123,timeBasis:'assistant_created_lower_bound'};
 assert.throws(()=>validateFrame(v1,{...policy,generation:'v1'}));v1.event.provenance.nativeMessageID='actual-final-assistant';validateFrame(v1,{...policy,generation:'v1'});
});
// Red if JSON.parse masks duplicate keys or consumes a second IPC frame.
test('closed JSON rejects duplicates, trailing frames, unsafe numeric syntax, depth and capacity',()=>{
 for(const s of ['{"protocol":1,"protocol":1}','{} {}','{"protocol":1e0}','{"protocol":1.0}','{"n":9007199254740993}',
  '['.repeat(10)+'0'+']'.repeat(10),'['+Array(100).fill('0').join(',')+']']) assert.throws(()=>parseJSON(Buffer.from(s)));
 assert.throws(()=>parseJSON(Buffer.alloc(4097)));
 assert.throws(()=>ns('9223372036854775808'));
});
// Red if sanitized helper output is mistaken for an eligible runtime or a clock-policy grant.
test('clock and profile receipts are closed prerequisites; malformed proof is unusable',()=>{
 const r=profileReceipt(Buffer.from('{"protocol":1,"semantic":"unverified","generation":"none","resourceClosure":"reaped_or_not_started"}'));
 assert.equal(r.semantic,'unverified');
 for(const s of ['{"protocol":1,"semantic":"eligible","generation":"none","resourceClosure":"reaped_or_not_started"}',
  '{"protocol":1,"semantic":"unverified","generation":"none","resourceClosure":"reaped_or_not_started","version":"2.0.21"}']) assert.throws(()=>profileReceipt(Buffer.from(s)));
 const good='{"protocol":1,"boot":"11111111-2222-3333-4444-555555555555","clockDomain":"linux-time:4:4026531834","clockKind":"linux-boottime","monoLoNs":"1000000","monoHiNs":"2000000","wallUnixNs":"1700000000000000000","uncertaintyNs":"4000000"}';
 assert.equal(clockReceipt(Buffer.from(good)).monoLoNS,'1000000');
 for(const s of [good.replace('4000000','3000000'),good.replace('linux-boottime','linux-monotonic'),good.replace('1000000','01000000')])assert.throws(()=>clockReceipt(Buffer.from(s)));
});

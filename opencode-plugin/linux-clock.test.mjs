import test from 'node:test';
import assert from 'node:assert/strict';
import {createLinuxClock, parseUptime} from './linux-clock.mjs';
import {selectClockCell} from './clock-cells.mjs';
// Red if a numeric conversion rounds, accepts a fabricated grammar, or changes
// the containing interval of the kernel's exact two-decimal floor.
test('proc grammar has exact BigInt ns and rejects truncation/noncanonical/overflow samples',()=>{
 assert.equal(parseUptime('1234567890.12 456.00\n'),1234567890120000000n);
 for(const s of ['123.1 0.00\n','01.00 0.00\n','123.00 0.00','123.00 0.00\nextra','-1.00 0.00\n',
  '9223372036.86 0.00\n','1e3.00 0.00\n','1.00 00.00\n']) assert.throws(()=>parseUptime(s));
});
// Ordinary Node checks API availability and its OWN actual proc/ns identity.
// This is not a Bun/image qualification or a native source-policy grant.
test('real held proc/ns fds have bounded intervals and disposal closes authority',()=>{
 const selected=[selectClockCell('v1'),selectClockCell('v2')];
 const c=createLinuxClock();
 try {
  const a=c.sample(), b=c.sample();
  assert.equal(typeof a.loNS,'bigint');assert.ok(a.hiNS-a.loNS>=10000000n && a.hiNS-a.loNS<=110000000n);
  assert.equal(a.boot,b.boot);assert.equal(a.domain,b.domain);assert.ok(b.loNS>=a.loNS);
  assert.match(a.domain,/^linux-time:[1-9][0-9]*:[1-9][0-9]*$/);
 }finally{c.dispose();}
 assert.throws(()=>c.sample());assert.deepEqual([selectClockCell('v1'),selectClockCell('v2')],selected);
});
// Red if a caller-supplied manifest can register authority, change the fixed
// error budget, inherit an unknown image or bypass the closed evidence schema.
test('manifest description is immutable acyclic data and cannot enable a production clock cell',async()=>{
 const {describeClockPolicy}=await import('./clock-cells.mjs');
 const row={protocol:1,generation:'v2',goos:'linux',goarch:'amd64',images:[
  {version:'2.0.21',imageSHA256:'f916986543348d7953d8d43aa048516cdbc3f84f4d0dc9c0c5b9d1da3030cea7'}],
  algorithmSourceMerkleSHA256:'d'.repeat(64),sourceKind:'linux-proc-boottime',rawKind:'linux-boottime',
  nativeReadBoundNS:'103000000',comparisonBoundNS:'430000000',translationBoundNS:'224000000'};
 const selected=[selectClockCell('v1'),selectClockCell('v2')];
 const policy=describeClockPolicy(row);assert.ok(Object.isFrozen(policy));assert.equal(policy.comparisonBoundNS,430000000n);
 assert.deepEqual([selectClockCell('v1'),selectClockCell('v2')],selected);
 assert.equal(selectClockCell('unknown'),undefined);
 for(const change of [m=>m.supported=true,m=>m.goBinarySHA256='a'.repeat(64),m=>m.comparisonBoundNS='2000000000',
  m=>m.images[0].version='2.0.22',m=>m.images[0].imageSHA256='a'.repeat(64),
  m=>m.algorithmSourceMerkleSHA256='0'.repeat(64)]){
  const m=structuredClone(row);change(m);assert.throws(()=>describeClockPolicy(m));
  assert.deepEqual([selectClockCell('v1'),selectClockCell('v2')],selected);
 }
});

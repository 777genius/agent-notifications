// These failures prevent a malformed contract from reaching any release executable.
import { strict as assert } from 'node:assert';
import { validateInputs, type Inputs } from './inputs.mts';
const input: Inputs = {
  candidateSHA:'a'.repeat(40),operatorSHA:'b'.repeat(40),version:'2.0.0',
  testThread:'12345678-1234-1234-1234-123456789abc',signingRun:'123',signingAttempt:'2',
  nativeCustodyJSON:'/private/tmp/TEST-fixture/native.json',nativeCustodySHA256:'c'.repeat(64),
  draftBinarySHA256:'d'.repeat(64),portableZipSHA256:'e'.repeat(64),nativeZipSHA256:'f'.repeat(64),signingRunJSONSHA256:'0'.repeat(64),
  root:'/private/tmp/TEST-an-codex-12345678-1234-1234-1234-123456789abc',source:'/private/tmp/TEST-fixture/source',
  portableZip:'/private/tmp/TEST-fixture/portable.zip',draftBinary:'/private/tmp/TEST-fixture/binary',nativeZip:'/private/tmp/TEST-fixture/native.zip',
  signingRunJSON:'/private/tmp/TEST-fixture/run.json',codex:'/private/tmp/TEST-fixture/codex',modelPort:52956,webhookPort:52957,
};
assert.equal(validateInputs(input).version,'2.0.0');
for(const patch of [
  {candidateSHA:'a'.repeat(39)},{operatorSHA:'unknown'},{version:'latest'},
  {testThread:'real-project'},{signingRun:'0'},{signingAttempt:'1.0'},
  {draftBinarySHA256:'missing'},{nativeCustodySHA256:undefined},
  {root:'/Users/user/real-project'},{root:'/private/tmp/TEST-an-codex-../escape'},
  {codex:'codex'},{source:'/private/tmp/TEST-fixture/\0escape'},
  {modelPort:1024},{webhookPort:52956},{extra:'unknown'},
]) assert.throws(()=>validateInputs({...input,...patch}));
const {nativeZipSHA256,...missing}=input;assert.throws(()=>validateInputs(missing));
for(const value of [null,[],42,'{}'])assert.throws(()=>validateInputs(value));
console.log('PASS: generic version, exact source/hash/run custody, private TEST namespace, absolute paths, ports, and missing/unknown input rejection; no runtime executed');

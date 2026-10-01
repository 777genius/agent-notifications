import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { gunzipSync } from 'node:zlib';
const root=new URL('.',import.meta.url);
// Closed proof of the independently reviewed, unpublished npm pack; no ignored-file dependency.
const proof={archiveSHA256:"eb4beb60c9f04ce25db49b9f9aef1a74276c26c7644685105023e7c5456a8407",archiveSRI:"sha512-iETCpLXjJdhZnplc6lD6l33UlGESlcFBBdU2VIAsP1yx+hhEzt6XtMGEelh1W4m4QH4lZIfQTwrswuM+IIEZ3g==",published:false,installedVersion:'0.3.0',installedPackedFiles:{
 "checkpoint.js":"96a1776b0663cf2047d2649a77080fe97d852168b0c7654c03ef343391b5c779",
 "index.js":"8e0c343aa9ea29bfce3e4d53c0d979ffc447af96c36b10c5253e3a766a6454a5",
 "observer-core.js":"f0a2b78055b027c9b8ef1cc1d833162543dbf0841e0f53276e943a009d201843",
 "v1.js":"807f6a003512cc7162e585d280d4d9697017fd61a24d97cfe2e173bdbf714acc",
 "v2-reducer.js":"13ab95e5e4065e0f695caa4baec327d803297ad502f554d44627c9b1d6e98799",
 "v2.js":"a68e7e9fa0e51ad2ae5ceac6619800b7f2b03f4897b3738afd63878749688198",
 "package.json":"e625ce669c8d20124345c27cccc4a06db989916e4ed3c0ad21a0f01ce22e7df5",
 "README.md":"3e2e5edec9bf7467e6382e3d76877b4f81c011e8c126b0fd9ebe22a8f728c41d",
 "common.d.ts":"85e055aae784c9b0c81c9d47d778f1e538de059adf6bb3eb6cf62309af79d1cd",
 "index.d.ts":"d91a6f761a7efa6682fec47b51ebd357bf26f7d374df2c501774385378ad604c",
 "v1.d.ts":"397f20a7b76e29365d9fe1030af5e6dba0e45d3c013485ee00065b6a2e574964",
 "v2.d.ts":"f4996b21c2cf5e2b355ebb066cdb3cd939e9291db748c4c3c475c526cec03c8b"
}};
const archive=await readFile(new URL('vendor/universal-agent-plugins-opencode-events-0.3.0.tgz',root));
const sha=(bytes)=>createHash('sha256').update(bytes).digest('hex');
assert.equal(sha(archive),'eb4beb60c9f04ce25db49b9f9aef1a74276c26c7644685105023e7c5456a8407');
assert.equal(sha(archive),proof.archiveSHA256);
assert.equal(`sha512-${createHash('sha512').update(archive).digest('base64')}`,proof.archiveSRI);
assert.equal(archive.length,22870);
const tar=gunzipSync(archive,{maxOutputLength:128*1024}),packed=new Map();
for(let at=0;at+512<=tar.length && tar[at]!==0;){
 const header=tar.subarray(at,at+512),name=header.subarray(0,100).toString('utf8').split('\0')[0],sizeText=header.subarray(124,136).toString('ascii');
 assert.match(sizeText,/^[0-7]{10} \0$/);const size=parseInt(sizeText,8),end=at+512+size;
 assert.equal(header[156],48);assert.ok(name.startsWith('package/') && Object.hasOwn(proof.installedPackedFiles,name.slice(8)) && !packed.has(name) && end<=tar.length);
 assert.equal(sha(tar.subarray(at+512,end)),proof.installedPackedFiles[name.slice(8)],name);packed.set(name,true);
 at=Math.ceil(end/512)*512;
}
assert.equal(packed.size,12);
const pkg=JSON.parse(await readFile(new URL('package.json',root)));
const lock=JSON.parse(await readFile(new URL('package-lock.json',root)));
assert.equal(pkg.devDependencies.esbuild,'0.28.2');
const dependency='file:vendor/universal-agent-plugins-opencode-events-0.3.0.tgz';
assert.equal(pkg.dependencies['universal-agent-plugins-opencode-events'],dependency);
assert.equal(lock.packages[''].dependencies['universal-agent-plugins-opencode-events'],dependency);
assert.equal(lock.packages['node_modules/universal-agent-plugins-opencode-events'].resolved,dependency);
assert.equal(lock.packages['node_modules/universal-agent-plugins-opencode-events'].integrity,proof.archiveSRI);
for(const [name,hash] of Object.entries(proof.installedPackedFiles))
 assert.equal(sha(await readFile(new URL(`node_modules/universal-agent-plugins-opencode-events/${name}`,root))),hash,name);
const legacy=await import('universal-agent-plugins-opencode-events');
const v1=await import('universal-agent-plugins-opencode-events/v1');
const v2=await import('universal-agent-plugins-opencode-events/v2');
assert.equal(typeof legacy.createObserver,'function');assert.equal(typeof legacy.createV2Observer,'function');
assert.equal(typeof v1.createObserver,'function');assert.equal(typeof v2.createV2Observer,'function');
// Published root shape is still a push API. It is NOT strict reader authority.
const push=legacy.createV2Observer({client:{get:async()=>undefined,context:async()=>[]},location:{directory:'/TEST-packed-root'},emit(){}});
assert.equal(typeof push.observe,'function');assert.equal(typeof push.dispose,'function');push.dispose();
process.stdout.write(JSON.stringify({status:'verified',archiveSHA256:sha(archive),archiveSRI:proof.archiveSRI,
 installedPackedFiles:Object.keys(proof.installedPackedFiles).length,lockSHA256:sha(await readFile(new URL('package-lock.json',root))),
 nodeVersion:process.version,published:false})+'\n');

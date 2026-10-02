"""Inert parser/custody regressions. No native/model/installer/notification launch."""
import importlib.util
import io
import tarfile
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest

repo = pathlib.Path(__file__).resolve().parents[3]
spec = importlib.util.spec_from_file_location('native',repo/'scripts/opencode-native-e2e.py')
r = importlib.util.module_from_spec(spec)
spec.loader.exec_module(r)
artifacts = repo/'.task-tools/artifacts/laneF-source'
artifacts.mkdir(parents=True,exist_ok=True)

class FixtureTests(unittest.TestCase):
    def test_duplicate_manifest_key_fails_before_any_launch(self):
        with tempfile.TemporaryDirectory(prefix='TEST-parser-',dir=artifacts) as d:
            path=pathlib.Path(d)/'manifest.json'
            path.write_text('{"purpose":1,"purpose":2}')
            with self.assertRaisesRegex(r.Unqualified,'duplicate_manifest_key'):
                r.load_manifest(path,'linux','amd64','1.18.33')

    def test_null_template_has_no_candidate_authority(self):
        with self.assertRaisesRegex(r.Unqualified,'candidate_revision_missing'):
            r.load_manifest(pathlib.Path(__file__).with_name('manifest.template.json'),'linux','amd64','1.18.33')

    def test_changed_artifact_is_rejected(self):
        with tempfile.TemporaryDirectory(prefix='TEST-hash-',dir=artifacts) as d:
            root=pathlib.Path(d)
            artifact=root/'image'
            artifact.write_bytes(b'original bytes')
            record={'path':'image','sha256':r.digest(artifact)}
            self.assertEqual(r.checked_file(root,record),artifact)
            artifact.write_bytes(b'changed bytes')
            with self.assertRaisesRegex(r.Unqualified,'artifact_hash_mismatch'):
                r.checked_file(root,record)

    def test_artifact_escape_rejected(self):
        with tempfile.TemporaryDirectory(prefix='TEST-path-',dir=artifacts) as d:
            root=pathlib.Path(d)
            record={'path':'../outside','sha256':'0'*64}
            with self.assertRaisesRegex(r.Unqualified,'artifact_path_escape'):
                r.checked_file(root,record)

    def test_canonical_native_terminal_discrimination(self):
        # Independent envelope examples: interrupt's step.failed is not permanent
        # terminal failure; compaction's succeeded alone is not ordinary completion.
        failed={'type':'session.step.failed','data':{'executionID':'run','assistantMessageID':'assistant'}}
        self.assertFalse(r.native_final([failed,{'type':'session.execution.interrupted','data':{'executionID':'run'}}],'error',True))
        self.assertFalse(r.native_final([{'type':'session.execution.succeeded','data':{'executionID':'run'}}],'completion',True))
        self.assertFalse(r.native_final([failed,{'type':'session.execution.failed','data':{'executionID':'other'}}],'error',True))
        self.assertTrue(r.native_final([failed,{'type':'session.execution.failed','data':{'executionID':'run'}}],'error',True))

    def test_supplied_official_bytes_cover_all_eleven_template_cells(self):
        source=json.loads((pathlib.Path(__file__).with_name('reviewed-host-custody.json')).read_text())
        template=json.loads(pathlib.Path(__file__).with_name('manifest.template.json').read_text())
        normalized={(c['os'],'amd64' if c['arch']=='x64' else c['arch'],c['version']):c for c in source['cells']}
        normalized[('linux','amd64','1.18.34')]=source['linuxCurrentRetained']
        self.assertEqual(len(normalized),11)
        self.assertEqual({(c['os'],c['arch'],c['version']) for c in template['cells']},set(normalized))
        self.assertEqual(len(template['cells']),11)
        ledger=json.loads(pathlib.Path(__file__).with_name('host-pins.json').read_text())
        self.assertEqual({(c['os'],c['arch'],c['version']) for c in ledger['cells']},set(normalized))
        self.assertEqual(len(ledger['cells']),11)
        for cell in template['cells']+ledger['cells']:
            expected=normalized[(cell['os'],cell['arch'],cell['version'])]
            self.assertEqual(cell.get('archive',{}).get('sha256',cell.get('archiveSHA256')),expected['archiveSHA256'])
            self.assertEqual(cell['archiveSRI'],expected['sri'])
            self.assertEqual(cell['executableSHA256'],expected['binarySHA256'])
        self.assertIsNone(template['candidateCommit'])

    def test_copied_fork_message_cannot_be_fresh_completion(self):
        events=[{'type':'session.step.ended','data':{'executionID':'run','assistantMessageID':'copied'}},
                {'type':'session.execution.succeeded','data':{'executionID':'run'}}]
        self.assertTrue(r.native_final(events,'completion',True))
        self.assertFalse(r.native_final(events,'completion',True,('copied',)))

    def test_terminal_step_without_matching_final_assistant_projection_is_insufficient(self):
        info={'id':'final','sessionID':'root','role':'assistant','parentID':'human',
              'time':{'completed':1234},'error':{'name':'APIError'}}
        self.assertTrue(r.ordinary_projection([{'info':info}],{'final':{'parentID':'human','completed':1234}},'root',True,'MessageAbortedError'))
        self.assertFalse(r.ordinary_projection([],{'final':{'parentID':'human','completed':1234}},'root',True,'MessageAbortedError'))
        self.assertFalse(r.ordinary_projection([info],{'different'},'root',True,'MessageAbortedError'))
        self.assertFalse(r.ordinary_projection([info],{'final':{'parentID':'human','completed':1234}},'other',True,'MessageAbortedError'))
        for change in ({'parentID':'other-human'},{'time':{'completed':9999}},{'time':{}},{'summary':True},{'role':'user'},{'error':{'name':'MessageAbortedError'}}):
            self.assertFalse(r.ordinary_projection([{**info,**change}],{'final':{'parentID':'human','completed':1234}},'root',True,'MessageAbortedError'))


    def test_v1_observed_final_metadata_must_equal_independent_projection(self):
        # RED: equal IDs can hide a different parent/time after projection lookup.
        observed={'type':'message.updated','properties':{'info':{
            'id':'final','sessionID':'root','role':'assistant','parentID':'human-a',
            'time':{'completed':1234}}}}
        projection={'id':'final','sessionID':'root','role':'assistant',
                    'parentID':'human-a','time':{'completed':1234}}
        # Exercise the actual history lookup boundary with independently supplied
        # native/projection envelopes; this transport substitution returns no grant.
        original=r.request
        key=b'private-parser-correlation'
        try:
            r.request=lambda *args,**kwargs: [projection]
            sid=r.private_redact('root',key)
            r.final_projection('http://127.0.0.1',repo,'root',sid,r.private_redact([observed],key),'completion',False,{},key)
            for wrong in ({'parentID':'human-b'},{'time':{'completed':9999}}):
                r.request=lambda *args,**kwargs: [{**projection,**wrong}]
                with self.assertRaisesRegex(r.Unqualified,'matching_final_ordinary_assistant_projection_missing'):
                    r.final_projection('http://127.0.0.1',repo,'root',sid,r.private_redact([observed],key),'completion',False,{},key)
        finally:
            r.request=original

    def test_external_evidence_binding_is_acyclic_and_rejects_changed_bytes(self):
        # RED: a tracked final manifest asks its own commit to name itself. External
        # parent evidence is sealed AFTER source selection, independent of HEAD.
        with tempfile.TemporaryDirectory(prefix='TEST-parent-input-',dir=artifacts) as d:
            external=pathlib.Path(d)/'parent-manifest.json'
            external.write_text('{"candidateCommit":"e18304e1976e260458b8fe485f267780d873eb93"}\n')
            sha=r.digest(external)
            r.checked_parent_manifest(external,sha)
            with self.assertRaisesRegex(r.Unqualified,'external_parent_manifest_required'):
                r.checked_parent_manifest(pathlib.Path(__file__).with_name('manifest.template.json'),
                                          r.digest(pathlib.Path(__file__).with_name('manifest.template.json')))
            external.write_bytes(external.read_bytes()+b' ')
            with self.assertRaisesRegex(r.Unqualified,'parent_manifest_hash_mismatch'):
                r.checked_parent_manifest(external,sha)


    def test_real_external_archive_staging_changes_no_source_and_checks_hash(self):
        # RED: externally sealed parent bytes cannot be staged without rewriting a
        # tracked input. This is a real inert tar IO boundary, no SDK/build copies.
        spec=importlib.util.spec_from_file_location('ci_inputs',pathlib.Path(__file__).with_name('ci_inputs.py'))
        ci=importlib.util.module_from_spec(spec)
        spec.loader.exec_module(ci)
        source=repo/'scripts/opencode-native-e2e.py'
        source_sha=r.digest(source)
        with tempfile.TemporaryDirectory(prefix='TEST-staged-evidence-',dir=artifacts) as d:
            root=pathlib.Path(d)
            archive=root/'parent.tar.gz'
            manifest=json.dumps({'candidateCommit':'e18304e1976e260458b8fe485f267780d873eb93',
                                 'buildRevision':'e18304e1976e260458b8fe485f267780d873eb93',
                                 'sourceSHA256':source_sha,'purpose':'inert input IO only'}).encode()
            with tarfile.open(archive,'w:gz') as tar:
                for name,body in (('manifest.json',manifest),('source-review.txt',source_sha.encode())):
                    item=tarfile.TarInfo(name); item.size=len(body); tar.addfile(item,io.BytesIO(body))
            seal=r.digest(archive)
            staged,sha=ci.stage_parent_archive(archive,seal,root/'external')
            self.assertEqual(staged.read_bytes(),manifest)
            self.assertEqual(sha,r.digest(staged))
            r.checked_parent_manifest(staged,sha)
            binding=json.loads(staged.read_text())
            revision=binding['candidateCommit']
            metadata='test-input: go1.27.0\n\tbuild\tvcs.revision='+revision+'\n\tbuild\tvcs.modified=false\n'
            r.verify_source_binding(binding,revision,metadata,'')
            with self.assertRaises(r.Unqualified):
                r.verify_source_binding(binding,'0'*40,metadata,'')
            self.assertEqual(r.digest(source),source_sha)
            archive.write_bytes(archive.read_bytes()+b'changed')
            with self.assertRaisesRegex(ci.r.Unqualified,'parent_archive_hash_mismatch'):
                ci.stage_parent_archive(archive,seal,root/'wrong')
            self.assertFalse((root/'wrong').exists())

    def test_exact_source_and_clean_real_build_metadata_remain_required(self):
        # Inert Go metadata parser examples, no native binary/build/profile grant.
        revision='e18304e1976e260458b8fe485f267780d873eb93'
        manifest={'candidateCommit':revision,'buildRevision':revision}
        build='test-input: go1.27.0\n\tbuild\tvcs.revision='+revision+'\n\tbuild\tvcs.modified=false\n'
        r.verify_source_binding(manifest,revision,build,'')
        for head,metadata,dirty in (
            ('0'*40,build,''), (revision,build.replace(revision,'0'*40),''),
            (revision,build.replace('modified=false','modified=true'),''),
            (revision,build,' M scripts/opencode-native-e2e.py\n'),
            (revision,build,'untracked-product.go\n')):
            with self.assertRaises(r.Unqualified):
                r.verify_source_binding(manifest,head,metadata,dirty)

    def test_owned_inert_child_is_awaited(self):
        with tempfile.TemporaryDirectory(prefix='TEST-child-',dir=artifacts) as d:
            owner=r.Owned()
            child=owner.launch([sys.executable,'-B','-c','import sys; sys.stdin.buffer.read()'],pathlib.Path(d),{},'inert',stdin=subprocess.PIPE)
            owner.close()
            self.assertIsNotNone(child.returncode)
            self.assertTrue(child.stdin.closed)

if __name__=='__main__':
    unittest.main()

"""Inert parser/custody regressions. No native/model/installer/notification launch."""
import importlib.util
import ast
import copy
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

def wire(kind, seq, session='root', **data):
    # Independently transcribed schema/session-event + schema/event envelopes.
    return {'id':'evt_'+str(seq),'created':1700000000000+seq,'type':kind,
            'durable':{'aggregateID':session,'seq':seq,'version':1},
            'data':{'sessionID':session,**data}}


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
        began=wire('session.execution.started',10)
        failed=wire('session.step.failed',12,assistantMessageID='assistant')
        self.assertFalse(r.native_final([began,failed,wire('session.execution.interrupted',13)],'error',True))
        self.assertFalse(r.native_final([began,wire('session.execution.succeeded',13)],'completion',True))
        self.assertFalse(r.native_final([began,failed,wire('session.execution.failed',13,'other')],'error',True))
        self.assertTrue(r.native_final([began,failed,wire('session.execution.failed',13)],'error',True))

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
        events=[wire('session.execution.started',10),wire('session.step.ended',12,assistantMessageID='copied'),
                wire('session.execution.succeeded',13)]
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


class DriverContractTests(unittest.TestCase):
    """Pure examples from frozen public routes, core projections and tool renderers.
    These envelopes are parser inputs, never injected into a live native reader.
    """
    def test_manual_routes_are_distinct_and_never_request_auto_resume(self):
        self.assertEqual(r.compaction_request('root/a',False),('/session/root%2Fa/summarize',
                         {'providerID':'p0','modelID':'p0-compaction','auto':False}))
        self.assertEqual(r.compaction_request('root/a',True),('/api/session/root%2Fa/compact',{}))

    def test_manual_summary_requires_new_same_root_native_metadata_and_control_parent(self):
        # V1 compaction.ts creates a summary assistant and a compaction user part.
        user={'info':{'id':'control','sessionID':'root','role':'user'},
              'parts':[{'type':'compaction','sessionID':'root','messageID':'control','auto':False}]}
        info={'id':'summary','sessionID':'root','role':'assistant','parentID':'control',
              'summary':True,'finish':'stop','time':{'completed':4321}}
        observed={'type':'message.updated','properties':{'info':copy.deepcopy(info)}}
        history=[user,{'info':info}]
        native=[observed,{'type':'session.idle','properties':{'sessionID':'root'}}]
        self.assertEqual(r.summary_projection(history,native,'root',False),'summary')
        self.assertFalse(r.native_final(native,'completion',False))
        self.assertIsNone(r.summary_projection(history,native,'other',False))
        self.assertIsNone(r.summary_projection(history,native,'root',False,('summary',)))
        for change in ({'parentID':'old-human'},{'summary':False},{'time':{'completed':999}},
                       {'finish':None},{'error':{'name':'APIError'}},{'sessionID':'other'}):
            self.assertIsNone(r.summary_projection([user,{'info':{**info,**change}}],native,'root',False))
        for change in ({'auto':True},{'messageID':'other'},{'sessionID':'other'}):
            wrong=copy.deepcopy(user);wrong['parts'][0].update(change)
            self.assertIsNone(r.summary_projection([wrong,{'info':info}],native,'root',False))

    def test_unbound_v2_summary_schema_cannot_qualify_execution_success(self):
        # Even with reviewed public source, an unbound enum/wire window cannot
        # turn an isolated stored record or execution success into proof.
        stored={'id':'compact-input','type':'compaction','status':'completed',
                'reason':'manual','summary':'## Objective\n- manual summary'}
        native=[{'type':'session.execution.succeeded','data':{'sessionID':'root','executionID':'run'}}]
        self.assertIsNone(r.summary_projection([stored],native,'root',True,admitted='compact-input'))
        self.assertFalse(r.native_final(native,'completion',True))

    def test_child_needs_created_parent_projection_and_actual_tool_result_not_fork_lineage(self):
        for v2 in (False,True):
            projection={'id':'child','parentID':'parent','agent':'an-e2e-child',
                        **({'location':{'directory':'TEST-project'}} if v2 else {'directory':'TEST-project'})}
            created={'type':'session.created',**({'id':'evt_child','created':1700000000000,'durable':{'aggregateID':'child','seq':0,'version':1},
                       'data':{'sessionID':'child','parentID':'parent','agent':'an-e2e-child','location':{'directory':'TEST-project'}}} if v2 else
                       {'properties':{'info':{'id':'child','parentID':'parent'}}})}
            self.assertTrue(r.child_ancestry(projection,[created],'parent','child','child','TEST-project',v2))
            for field,value in (('parentID',None),('parentID','other'),('id','parent'),('agent','other-agent')):
                self.assertFalse(r.child_ancestry({**projection,field:value},[created],'parent','child','child','TEST-project',v2))
            self.assertFalse(r.child_ancestry(projection,[created],'parent','child','different-result','TEST-project',v2))
            self.assertFalse(r.child_ancestry(projection,[{**created,'type':'session.forked'}],
                                            'parent','child','child','TEST-project',v2))
            # Event lineage alone cannot grant ancestry to a public root projection.
            self.assertFalse(r.child_ancestry({**projection,'parentID':None},[created],
                                            'parent','child','child','TEST-project',v2))

    def test_provider_only_calls_advertised_builtin_foreground_tool_and_exact_test_agent(self):
        import provider as p
        for v2,name,field in ((False,'task','subagent_type'),(True,'subagent','agent')):
            tool={'type':'function','function':{'name':name,'description':'Available subagents:\n- an-e2e-child: Private TEST',
                  'parameters':{'type':'object','properties':{k:{'type':'string'} for k in ('description','prompt',field)},
                                'required':['description','prompt',field],'additionalProperties':False}}}
            call=p.child_call([tool],v2)
            args=json.loads(call['function']['arguments'])
            self.assertEqual(call['function']['name'],name)
            self.assertEqual(args[field],'an-e2e-child')
            self.assertEqual(set(args),{'description','prompt',field})
            for mutate in ('name','description','required','field_type'):
                wrong=copy.deepcopy(tool)
                if mutate=='name': wrong['function']['name']='shell'
                if mutate=='description': wrong['function']['description']='Available subagents:\n- general: default'
                if mutate=='required': wrong['function']['parameters']['required'].append('model')
                if mutate=='field_type': wrong['function']['parameters']['properties'][field]={'type':'number'}
                with self.assertRaises(ValueError): p.child_call([wrong],v2)
            with self.assertRaises(ValueError): p.child_call([tool,tool],v2)

    def test_real_tool_response_must_bind_call_and_complete_before_parent_release(self):
        import provider as p
        for v2,content in ((False,'<task id="child" state="completed">\n<task_result>\nreply\n</task_result>\n</task>'),
                           (True,'<subagent sessionID="child" state="completed">\nreply\n</subagent>')):
            message={'role':'tool','tool_call_id':'call_an_test_child','content':content}
            self.assertEqual(p.child_result([message],v2),'child')
            self.assertEqual(p.child_response({'messages':[message]},v2,2),('resume',None,'child'))
            for change in ({'tool_call_id':'other'},{'role':'assistant'},
                           {'content':content.replace('completed','running')}):
                with self.assertRaises(ValueError): p.child_result([{**message,**change}],v2)
            with self.assertRaises(ValueError): p.child_result([message,message],v2)
            with self.assertRaises(ValueError): p.child_response({'messages':[message]},v2,3)
        request={'messages':[{'role':'user','content':'AN_TEST_CHILD_REPLY_ONLY. Return a short text answer. Do not call tools.'}]}
        self.assertEqual(p.child_response(request,True,1),('child',None,None))
        with self.assertRaises(ValueError): p.child_response({'messages':[{'role':'user','content':'ordinary root'}]},True,1)

    def test_summary_provider_distinguishes_native_generation_and_v1_conversation_envelope(self):
        import provider as p
        # Frozen V1 compaction tests assert one user LLM message and these
        # conversation/anchor literals. No native envelope is injected here.
        content = ('Here is the conversation so far:\n<conversation>\n'
                   '[User]: older context\n</conversation>\nCreate a new anchored summary')
        body = {'model':'p0-compaction','messages':[{'role':'user','content':content}]}
        self.assertTrue(p.summary_request(body,False))
        self.assertFalse(p.summary_request(body,True))
        for changed in (content.replace('Create a new anchored summary','ordinary reply'),
                        content.replace('<conversation>',''),
                        content.replace('Here is the conversation so far:',''),
                        content.replace('</conversation>','')):
            self.assertFalse(p.summary_request({'messages':[{'role':'user','content':changed}]},False))
        self.assertFalse(p.summary_request({'messages':[{'role':'system','content':content}]},False))
        v2 = {'messages':[{'role':'user','content':
              'You MUST summarize the conversation above into a structured summary that will be given to another agent to resume the work.'}]}
        self.assertTrue(p.summary_request(v2,True))
        self.assertFalse(p.summary_request(v2,False))

    def test_reviewed_source_gate_and_generation_specific_summary_templates(self):
        import provider as p
        expected = {False:['## Objective','## Important Details','## Work State','### Completed',
                           '### Active','### Blocked','## Next Move','## Relevant Files'],
                    True:['## Objective','## Requirements','## Decisions','## Work State','### Completed',
                          '### Active','### Blocked','## Next Move','## Relevant Files','## Important Context']}
        for v2, headings in expected.items():
            self.assertEqual([line for line in p.summary_response(v2).splitlines() if line.startswith('#')],headings)
        for version in ('1.18.33','1.18.34','2.0.21'):
            self.assertEqual(len(r.reviewed_driver_contract(version)['versions'][version]),40)
        with tempfile.TemporaryDirectory(prefix='TEST-driver-source-',dir=artifacts) as d:
            path=pathlib.Path(d)/'contracts.json'
            path.write_bytes(pathlib.Path(__file__).with_name('driver-contracts.json').read_bytes()+b' ')
            with self.assertRaisesRegex(r.Unqualified,'reviewed_driver_source_contract_required'):
                r.reviewed_driver_contract('2.0.21',path)

    def test_v2_manual_summary_joins_admission_typed_projection_and_sparse_native_window(self):
        native=[wire('session.execution.started',10),
                wire('session.compaction.started',14,reason='manual',inputID='compact-input',recent=''),
                wire('session.compaction.ended',16,reason='manual',text='private summary',recent='',model={'id':'m','providerID':'p'}),
                wire('session.execution.succeeded',18)]
        stored={'id':'compact-input','type':'compaction','status':'completed','reason':'manual',
                'summary':'private summary','recent':'','model':{'id':'m','providerID':'p'},'time':{'created':1700000000014}}
        types=('session.compaction.started','session.compaction.ended')
        def result(history=None,events=None,**kwargs):
            return r.summary_projection([stored] if history is None else history,native if events is None else events,
                                        'root',True,admitted='compact-input',compaction_types=types,**kwargs)
        self.assertEqual(result(),'compact-input')
        self.assertFalse(r.native_final(native,'completion',True))
        self.assertIsNone(result(excluded=('compact-input',)))
        for change in ({'summary':'old summary'},{'reason':'auto'},{'recent':'different'},
                       {'type':'assistant'},{'status':'running'},{'id':'copied'},
                       {'model':{'id':'different','providerID':'p'}},{'error':{'type':'provider.failed'}}):
            self.assertIsNone(result([{**stored,**change}]))
        for mutate in ('root','aggregate','missing_durable','admission','reverse_seq','old_end','failure','duplicate_start','ordinary_step'):
            events=copy.deepcopy(native)
            if mutate=='root': events[2]['data']['sessionID']='other'
            if mutate=='aggregate': events[2]['durable']['aggregateID']='other'
            if mutate=='missing_durable': events[2].pop('durable')
            if mutate=='admission': events[1]['data']['inputID']='old-input'
            if mutate=='reverse_seq': events[2]['durable']['seq']=13
            if mutate=='old_end': events[2]['durable']['seq']=9
            if mutate=='failure': events.insert(3,wire('session.compaction.failed',17,reason='manual'))
            if mutate=='duplicate_start': events.insert(2,wire('session.compaction.started',15,reason='manual',inputID='compact-input'))
            if mutate=='ordinary_step': events.insert(3,wire('session.step.ended',17,assistantMessageID='ordinary'))
            self.assertIsNone(result(events=events),mutate)

    def test_v2_typed_final_assistant_is_bound_to_real_session_window(self):
        native=[wire('session.execution.started',20),wire('session.step.ended',24,assistantMessageID='final',finish='stop'),
                wire('session.execution.succeeded',26)]
        message={'id':'final','type':'assistant','agent':'an-e2e-child','content':[{'type':'text','text':'reply'}],
                 'finish':'stop','time':{'created':1700000000021,'completed':1700000000024}}
        self.assertTrue(r.v2_ordinary_projection([message],native,'root','completion'))
        self.assertFalse(r.v2_ordinary_projection([message],native,'other','completion'))
        for change in ({'id':'copied'},{'type':'compaction'},{'time':{}},{'error':{'type':'provider.failed'}},{'finish':'tool-calls'}):
            self.assertFalse(r.v2_ordinary_projection([{**message,**change}],native,'root','completion'))
        self.assertFalse(r.native_final([{'type':'session.step.ended','data':{'executionID':'invented','assistantMessageID':'final'}},
                                        {'type':'session.execution.succeeded','data':{'executionID':'invented'}}],'completion',True))
        failed=[wire('session.execution.started',20),wire('session.step.failed',24,assistantMessageID='final',error={'type':'provider.failed'}),
                wire('session.execution.failed',26,error={'type':'provider.failed'})]
        self.assertTrue(r.v2_ordinary_projection([{**message,'error':{'type':'provider.failed'}}],failed,'root','error'))
        self.assertFalse(r.v2_ordinary_projection([{**message,'error':{'type':'interrupted'}}],failed,'root','error'))
        original=r.request
        try:
            def respond(*args,**kwargs):
                self.assertEqual(args[2],'/api/session/root/message?limit=200&order=asc')
                return {'data':[message],'cursor':{}}
            r.request=respond
            self.assertEqual(r.read_history('http://127.0.0.1',repo,'root',True,{}),[message])
            r.request=lambda *a,**kw: {'data':[message],'cursor':{'next':'more'}}
            with self.assertRaisesRegex(r.Unqualified,'typed_message_page_incomplete_or_invalid'):
                r.read_history('http://127.0.0.1',repo,'root',True,{})
        finally: r.request=original

    def test_v2_test_child_uses_plural_config_and_native_created_agent_location(self):
        import provider as p
        config=p.child_config(True)
        self.assertNotIn('agent',config)
        self.assertEqual(set(config['agents']),{'an-e2e-child'})
        child=config['agents']['an-e2e-child']
        self.assertEqual(child['mode'],'subagent')
        self.assertEqual(child['permissions'],[{'action':'*','resource':'*','effect':'deny'}])
        self.assertIn({'action':'subagent','resource':'an-e2e-child','effect':'allow'},config['permissions'])
        projection={'id':'child','parentID':'parent','agent':'an-e2e-child','location':{'directory':'TEST-project'}}
        created=wire('session.created',0,'child',parentID='parent',agent='an-e2e-child',location={'directory':'TEST-project'})
        args=('parent','child','child','TEST-project',True)
        self.assertTrue(r.child_ancestry(projection,[created],*args))
        for change in ({'agent':'ambient'},{'location':{'directory':'other'}},{'parentID':None}):
            wrong=copy.deepcopy(created);wrong['data'].update(change)
            self.assertFalse(r.child_ancestry(projection,[wrong],*args))
        wrong=copy.deepcopy(created);wrong['durable']['aggregateID']='parent'
        self.assertFalse(r.child_ancestry(projection,[wrong],*args))

    def test_child_native_tool_identity_requires_same_call_parent_child_and_execution(self):
        v1={'type':'message.part.updated','properties':{'part':{'type':'tool','sessionID':'parent',
            'messageID':'assistant','callID':'call','tool':'task','state':{'status':'completed',
            'metadata':{'parentSessionId':'parent','sessionId':'child'}}}}}
        self.assertTrue(r.child_tool_identity([v1],'parent','child','call','an-e2e-child','task',False))
        for field in ('callID','sessionID','tool'):
            wrong=copy.deepcopy(v1);wrong['properties']['part'][field]='other'
            self.assertFalse(r.child_tool_identity([wrong],'parent','child','call','an-e2e-child','task',False))
        wrong=copy.deepcopy(v1);wrong['properties']['part']['state']['metadata']['sessionId']='sibling'
        self.assertFalse(r.child_tool_identity([wrong],'parent','child','call','an-e2e-child','task',False))
        native=[wire('session.tool.input.started',4,'parent',id='call',assistantMessageID='assistant',name='subagent'),
                wire('session.tool.called',5,'parent',id='call',assistantMessageID='assistant',executed=True,input={'agent':'an-e2e-child'}),
                wire('session.tool.success',9,'parent',id='call',assistantMessageID='assistant',executed=True,
                     metadata={'sessionID':'child','status':'completed'})]
        native[-1]['durable']['version']=2
        args=('parent','child','call','an-e2e-child','subagent',True)
        self.assertTrue(r.child_tool_identity(native,*args))
        for mutate in ('call','message','child','agent','not_executed','version','reversed','parent'):
            wrong=copy.deepcopy(native)
            if mutate=='call': wrong[-1]['data']['id']='other'
            if mutate=='message': wrong[-1]['data']['assistantMessageID']='other'
            if mutate=='child': wrong[-1]['data']['metadata']['sessionID']='sibling'
            if mutate=='agent': wrong[1]['data']['input']['agent']='general'
            if mutate=='not_executed': wrong[-1]['data']['executed']=False
            if mutate=='version': wrong[-1]['durable']['version']=1
            if mutate=='reversed': wrong[1]['durable']['seq']=10
            if mutate=='parent': wrong[-1]['durable']['aggregateID']='child'
            self.assertFalse(r.child_tool_identity(wrong,*args),mutate)

    def test_permission_nonexecution_requires_correlated_native_state_not_output_hash(self):
        # Regression: an actually executed shell output may hash differently
        # from the literal sentinel. That must never establish non-execution.
        key=b'independent-parser-fixture'
        def probe(native,session,call,tool,v2):
            if hasattr(r,'permission_nonexecution'):
                return r.permission_nonexecution(native,session,call,tool,v2)
            # RED executes the actual old pure assertion, isolated with AST.
            # No host/provider functions, mocked observations or copied expected
            # implementation are used. The external facts below are independent.
            tree=ast.parse(pathlib.Path(r.__file__).read_text())
            old=next(n for n in ast.walk(tree) if isinstance(n,ast.Call)
                     and isinstance(n.func,ast.Name) and n.func.id=='require'
                     and len(n.args)==2 and isinstance(n.args[1],ast.Constant)
                     and n.args[1].value=='harmless_shell_executed')
            expression=compile(ast.Expression(old.args[0]),'<actual-old-permission-guard>','eval')
            return bool(eval(expression,{'private_redact':r.private_redact,'json':json,
                                         'native':native,'key':key}))
        marker=r.private_redact('P0_OWNED_TEST',key)
        differently_hashed=r.private_redact('P0_OWNED_TEST\n',key)
        self.assertNotEqual(marker,differently_hashed)
        native=[wire('session.tool.input.started',4,'root',id='permission-call',
                     assistantMessageID='assistant',name='shell'),
                wire('session.tool.failed',8,'root',id='permission-call',
                     assistantMessageID='assistant',executed=False,error={'type':'permission.denied'})]
        native[-1]['durable']['version']=2
        args=('root','permission-call','shell',True)
        self.assertTrue(probe(native,*args))
        executed=copy.deepcopy(native)
        executed[-1]['data'].update(executed=True,content=[{'type':'text','text':differently_hashed}])
        self.assertNotIn(marker,json.dumps(executed))
        self.assertFalse(probe(executed,*args))
        for mutate in ('call','session','assistant','tool','missing_executed','version','reversed'):
            wrong=copy.deepcopy(native)
            if mutate=='call': wrong[-1]['data']['id']='other'
            if mutate=='session': wrong[-1]['durable']['aggregateID']='other'
            if mutate=='assistant': wrong[-1]['data']['assistantMessageID']='other'
            if mutate=='tool': wrong[0]['data']['name']='other'
            if mutate=='missing_executed': del wrong[-1]['data']['executed']
            if mutate=='version': wrong[-1]['durable']['version']=1
            if mutate=='reversed': wrong[0]['durable']['seq']=9
            self.assertFalse(probe(wrong,*args),mutate)
        self.assertFalse(probe(native[:1],*args))
        self.assertFalse(probe(native,'root','permission-call','shell',False))
        called=wire('session.tool.called',6,'root',id='permission-call',
                    assistantMessageID='assistant',executed=True,input={'command':differently_hashed})
        self.assertFalse(probe([native[0],called,native[1]],*args))

    def test_v2_prompt_admission_is_pending_until_actual_foreground_child_result(self):
        # V2 protocol/groups/session.ts392-409 and core/session/session.ts146-179
        # return an inbox admission after execution.wake, without awaitIdle.
        # A completed HTTP request alone therefore cannot end this observation.
        self.assertFalse(r.child_wait_ready(True,False,False,[]))
        self.assertFalse(r.child_wait_ready(True,True,False,[]))
        self.assertTrue(r.child_wait_ready(True,False,True,[]))
        # Source-confirmed V1 blocking /message keeps its existing fail-fast exit.
        self.assertTrue(r.child_wait_ready(False,False,False,[]))
        self.assertFalse(r.child_wait_ready(False,True,False,[]))
        for v2 in (False,True):
            self.assertTrue(r.child_wait_ready(v2,True,False,['provider_schema_gap']))
            self.assertTrue(r.child_wait_ready(v2,False,False,['transport_error']))

    def test_summary_provider_requires_canonical_native_prompt_not_model_name(self):
        import provider as p
        self.assertTrue(p.summary_request({'messages':[{'role':'user','content':[{'type':'text','text':
                        'You MUST summarize the conversation above into a structured summary that will be given to another agent to resume the work.'}]}]}))
        self.assertFalse(p.summary_request({'model':'p0-compaction','messages':[{'role':'user','content':'ordinary user turn'}]}))
        key=b'private-inert-correlation'
        raw={'reason':'manual','type':'compaction','summary':'private text','parentID':'private-parent'}
        clean=p.redact(raw,key)
        self.assertEqual(clean['reason'],'manual')
        self.assertEqual(clean['type'],'compaction')
        self.assertNotEqual(clean['summary'],raw['summary'])
        self.assertNotEqual(clean['parentID'],raw['parentID'])

if __name__=='__main__':
    unittest.main()

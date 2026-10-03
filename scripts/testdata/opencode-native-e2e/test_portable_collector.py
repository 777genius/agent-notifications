"""ROOT-only pure diagnostic transport negatives, no native/helper/model."""
import json
import copy
import unittest
import pathlib
import tempfile
from types import SimpleNamespace
from portable import PortableOwned
from portable import diagnostic, PREFIX
from portable_permission import joined_rejection


class ClosedDiagnostic(unittest.TestCase):
    def record(self):
        return {'protocol':1,'kind':'event','ipc':'ok','childClosure':'closed',
                'exitCode':0,'forcedKill':False,
                'receipt':{'status':'submitted','desktop':'submitted','webhook':'submitted'}}

    def encode(self, row):
        return PREFIX + json.dumps(row)

    def test_actual_closed_record_keeps_original_receipt(self):
        row = self.record()
        self.assertEqual(diagnostic(self.encode(row)), row)
        self.assertIsNone(diagnostic('unrelated native log'))

    def test_closed_invalidated_is_separate_and_never_submission(self):
        # Actual producer six-field post-Wait record, not a submitted receipt.
        from portable import Observation
        denied={'protocol':1,'kind':'event','ipc':'invalidated','childClosure':'closed',
                'exitCode':0,'forcedKill':False}
        self.assertEqual(diagnostic(self.encode(denied)),denied)  # Old parser RED.
        for change in ({'receipt':None},{'receipt':self.record()['receipt']},
                       {'extra':'secret'},{'ipc':'deadline'},{'kind':'clock'},
                       {'protocol':True},{'exitCode':False},{'exitCode':1},
                       {'forcedKill':True},{'forcedKill':0},{'childClosure':'unproved'}):
            with self.subTest(change=change):
                with self.assertRaises(ValueError):diagnostic(self.encode({**denied,**change}))
        missing=dict(denied);missing.pop('exitCode')
        with self.assertRaises(ValueError):diagnostic(self.encode(missing))
        def require(value,code):
            if not value:raise ValueError(code)
        with tempfile.TemporaryDirectory(prefix='TEST-closed-denied-') as d:
            root=pathlib.Path(d);log=root/'host-private.log'
            observation=Observation.__new__(Observation)
            observation.args=SimpleNamespace(version='2.0.21')
            observation.require=require
            observation.cursors,observation.partial,observation.rows={},{},[]
            observation.closedDenied=[]
            log.write_text(self.encode(self.record())+'\n'+self.encode(denied)+'\n')
            self.assertEqual(observation.update(root,closed=True),[self.record()])
            self.assertEqual(observation.closedDenied,[denied])
            self.assertEqual(observation.update(root,closed=True),[self.record()])
            with log.open('a') as stream:stream.write(self.encode(denied)+'\n')
            with self.assertRaisesRegex(ValueError,'unexpected_closed_denied_event'):
                observation.update(root,closed=True)
            observation.args.version='1.18.33'
            observation.cursors,observation.partial,observation.rows={},{},[]
            observation.closedDenied=[]
            log.write_text(self.encode(denied)+'\n')
            with self.assertRaisesRegex(ValueError,'closed_denied_native_generation_mismatch'):
                observation.update(root,closed=True)

    def test_extra_secret_duplicate_and_incomplete_records_deny(self):
        row=self.record();row['receipt']['reason']='PRIVATE_SECRET'
        with self.assertRaises(ValueError):diagnostic(self.encode(row))
        raw=self.encode(self.record()).replace('"protocol": 1','"protocol": 1, "protocol": 1')
        with self.assertRaises(ValueError):diagnostic(raw)
        with self.assertRaises(ValueError):diagnostic(self.encode(self.record())[:-1])
        with self.assertRaises(ValueError):diagnostic(self.encode(self.record())+PREFIX)


class PublicFinalization(unittest.TestCase):
    # Regression: direct stop skips POST, a false reply is accepted, or finally
    # repeats an uncertain POST / restores success after a disposal failure.
    def fixture(self, reply=True, v2=False):
        directory = tempfile.TemporaryDirectory(prefix='TEST-portable-dispose-')
        self.addCleanup(directory.cleanup)
        root = pathlib.Path(directory.name)
        (root/'tmp').mkdir()
        registry = root/'tmp/agent-notifications-owned'
        registry.mkdir()
        calls = []
        class Host:
            alive = True
            def poll(self): return None if self.alive else 0
        host = Host()
        class Gate:
            def set(self): calls.append('release')
        provider = SimpleNamespace(records=[], gaps=[], child_release=Gate())
        class Owner:
            servers = [provider]
            def launch(self, *args, **kwargs): return host
            def stop(self, process): calls.append('stop');process.alive=False
            def close(self): calls.append('close');host.alive=False
        def require(value, code):
            if not value: raise ValueError(code)
        def request(base, project, path, **kwargs):
            calls.append('POST')
            self.assertEqual((base,project,path),('http://127.0.0.1:1',root,'/global/dispose'))
            self.assertEqual(kwargs['method'],'POST')
            self.assertIs(kwargs['v2'],False)
            self.assertTrue(0 < kwargs['timeout'] <= 3)
            if reply is True: registry.rmdir()
            return reply
        report = {}
        owner = PortableOwned(Owner(),root,report,require)
        owner.bind(request,'http://127.0.0.1:1',root,v2,{})
        owner.launch(['opencode','serve'],root,{},'host')
        return owner,host,calls,report

    def test_public_dispose_precedes_restart_and_final_close_once(self):
        owner,host,calls,report=self.fixture();owner.stop(host);owner.close()
        self.assertEqual(calls,['release','POST','stop','close'])
        self.assertEqual(len(report['portablePublicDisposal']),1)
        self.assertIs(report['portablePublicDisposal'][0]['allHelperWaitEOFObserved'],False)
        owner,host,calls,_=self.fixture();owner.close()
        self.assertEqual(calls,['release','POST','close'])

    def test_uncertain_or_nonliteral_dispose_never_retries_or_restores_success(self):
        owner,host,calls,report=self.fixture(reply=1)
        with self.assertRaisesRegex(ValueError,'public_dispose_boolean_required'):owner.stop(host)
        with self.assertRaisesRegex(ValueError,'public_dispose_boolean_required'):owner.close()
        self.assertEqual(calls,['release','POST','close'])
        self.assertEqual(report['portablePublicDisposal'],[])

    def test_v2_keeps_existing_owned_stop_without_v1_endpoint(self):
        owner,host,calls,report=self.fixture(v2=True);owner.stop(host);owner.close()
        self.assertEqual(calls,['stop','close'])
        self.assertEqual(report['portablePublicDisposal'],[])


class PermissionMetadata(unittest.TestCase):
    # Regression: a genuine optional-undefined error property is captured as a
    # sentinel but omitted from JSON history; defined metadata is never ignored.
    def test_optional_undefined_metadata_preserves_strict_permission_join(self):
        pending={'id':'R','sessionID':'S','permission':'bash','tool':{'messageID':'A','callID':'C'}}
        run={'id':'P','type':'tool','sessionID':'S','messageID':'A','callID':'C','tool':'bash',
             'state':{'status':'running','input':{'command':'command-hmac'},'time':{'start':100}}}
        failed=copy.deepcopy(run);failed['state'].update(status='error',error='error-hmac',time={'start':100,'end':110})
        info={'id':'A','sessionID':'S','role':'assistant','parentID':'U','time':{'created':90,'completed':120}}
        native=[{'type':'message.updated','properties':{'info':{'id':'U','sessionID':'S','role':'user'}}},
                {'type':'message.part.updated','properties':{'part':run}},
                {'type':'permission.asked','properties':copy.deepcopy(pending)},
                {'type':'permission.replied','properties':{'sessionID':'S','requestID':'R','reply':'reject'}},
                {'type':'message.part.updated','properties':{'part':failed}},
                {'type':'message.updated','properties':{'info':copy.deepcopy(info)}}]
        history=[{'info':info,'parts':[copy.deepcopy(failed)]}]
        args=(native,history,pending,'S','C','bash','command-hmac')
        self.assertTrue(joined_rejection(*args))
        failed['state']['metadata']='[unsupported]'
        original=copy.deepcopy(args)
        self.assertTrue(joined_rejection(*args))  # Old code rejects this observed shape.
        self.assertEqual(args,original)
        for name,change in (
            ('defined-native-metadata',lambda n,h:n[4]['properties']['part']['state'].update(metadata={'detail':'native'})),
            ('running-metadata-present',lambda n,h:n[1]['properties']['part']['state'].update(metadata={})),
            ('history-metadata-present',lambda n,h:h[0]['parts'][0]['state'].update(metadata={})),
            ('null-native-metadata',lambda n,h:n[4]['properties']['part']['state'].update(metadata=None)),
            ('error-mismatch',lambda n,h:h[0]['parts'][0]['state'].update(error='different')),
            ('input-mismatch',lambda n,h:h[0]['parts'][0]['state']['input'].update(command='different')),
            ('time-mismatch',lambda n,h:h[0]['parts'][0]['state']['time'].update(end=111)),
            ('error-before-reject',lambda n,h:n.__setitem__(slice(3,5),[n[4],n[3]])),
            ('output-present',lambda n,h:n[4]['properties']['part']['state'].update(output='unexpected')),
            ('identity-mismatch',lambda n,h:h[0]['parts'][0].update(messageID='other')),
        ):
            with self.subTest(name=name):
                v=copy.deepcopy(args);change(v[0],v[1]);self.assertIsNone(joined_rejection(*v))
        v=copy.deepcopy(args)
        for state in (v[0][1]['properties']['part']['state'],v[0][4]['properties']['part']['state'],v[1][0]['parts'][0]['state']):
            state['metadata']={'detail':'same'}
        self.assertTrue(joined_rejection(*v))
        v[1][0]['parts'][0]['state']['metadata']['detail']='different'
        self.assertIsNone(joined_rejection(*v))


if __name__ == '__main__':unittest.main()

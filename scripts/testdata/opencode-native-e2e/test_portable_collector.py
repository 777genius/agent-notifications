"""ROOT-only pure diagnostic transport negatives, no native/helper/model."""
import json
import unittest
import pathlib
import tempfile
from types import SimpleNamespace
from portable import PortableOwned
from portable import diagnostic, PREFIX


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


if __name__ == '__main__':unittest.main()

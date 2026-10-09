"""Provider-permitted pure receipt checks; no process/kernel/runtime proof.

These checks prevent unsupported cleanup claims. The actual hang/leak/descriptor
regression remains lifecycle.py, which ONLY root executes outside the provider.
"""
import copy
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('TEST_linux_contract',Path(__file__).resolve().parents[1]/'linux_contract.py')
harness = importlib.util.module_from_spec(spec)
spec.loader.exec_module(harness)
lifecycle_spec = importlib.util.spec_from_file_location('TEST_lifecycle',Path(__file__).with_name('lifecycle.py'))
lifecycle = importlib.util.module_from_spec(lifecycle_spec); lifecycle_spec.loader.exec_module(lifecycle)


class CleanupReceipt(unittest.TestCase):
    def setUp(self):
        self.observation = {'errors':[], 'resourcesClosed':True,
            'naturalCompletion':False,'forcedContainment':True,
            'children':[{'joined':True,'pidAbsent':True,'birth':9,'freshBirth':None}],
            'tasks':[{'pidAbsent':True,'birth':10,'freshBirth':None}],
            'pumps':[{'joined':True}]}

    def test_completion_requires_each_independent_observation(self):
        self.assertTrue(harness.cleanup_complete(self.observation))
        for collection,field in (('children','joined'),('children','pidAbsent'),
                                 ('tasks','pidAbsent'),('pumps','joined')):
            evidence = copy.deepcopy(self.observation)
            evidence[collection][0][field] = False
            self.assertFalse(harness.cleanup_complete(evidence),(collection,field))
        for patch in ({'errors':[{'operation':'identity','error':'FileNotFoundError'}]},
                      {'resourcesClosed':False}):
            evidence = dict(self.observation,**patch)
            self.assertFalse(harness.cleanup_complete(evidence))

    def test_fresh_reuse_preserves_the_replacement(self):
        evidence = copy.deepcopy(self.observation)
        for field in ('children','tasks'):
            row = evidence[field][0]; row.update(pidAbsent=False,freshBirth=row['birth'])
        self.assertFalse(harness.cleanup_complete(evidence))
        for field in ('children','tasks'):
            evidence[field][0]['freshBirth'] += 1
        self.assertTrue(harness.cleanup_complete(evidence))
        evidence['children'][0]['birth'] = None
        self.assertFalse(harness.cleanup_complete(evidence))

    def test_cleanup_does_not_replace_first_failure(self):
        first = TimeoutError('TEST setup never became ready')
        error = harness.ContractFailure(first,{'complete':False},'TEST setup')
        self.assertIs(error.first_failure,first)
        self.assertFalse(error.cleanup['complete'])


class FailureBoundaryReceipt(unittest.TestCase):
    def setUp(self):
        self.actor = {'qualifiedActor':True,'phase':'tracer-spawn','ready':True,
                      'actorMode':'fork','absentTracer':'/TEST/intentional-absent',
                      'stderrPath':'/TEST/fork-None.stderr'}
        self.failure = {'type':'FileNotFoundError','message':'missing',
                        'filename':'/TEST/intentional-absent'}

    def test_missing_actor_or_import_is_never_expected_spawn_refusal(self):
        self.assertTrue(lifecycle.setup_boundary('spawn-failure',self.failure,self.actor,'tracer-spawn'))
        for patch in ({'qualifiedActor':False},{'ready':False},{'phase':'prerequisite'}):
            self.assertFalse(lifecycle.setup_boundary('spawn-failure',self.failure,dict(self.actor,**patch),'tracer-spawn'))
        for first in (dict(self.failure,filename='/TEST/missing-actor'),
                      dict(self.failure,type='ModuleNotFoundError'),
                      dict(self.failure,type='AssertionError')):
            self.assertFalse(lifecycle.setup_boundary('spawn-failure',first,self.actor,'tracer-spawn'))

    def test_cleanup_cannot_qualify_an_unrelated_original_boundary(self):
        self.assertFalse(lifecycle.setup_boundary('spawn-failure',self.failure,self.actor,'actor-readiness'))
        actor = dict(self.actor,phase='actor-readiness',actorMode='ready-eof',ready=False)
        eof = {'type':'AssertionError','message':'fixture readiness EOF'}
        self.assertTrue(lifecycle.setup_boundary('ready-eof',eof,actor,'actor-readiness'))
        for message in ('held credential/monitor deadline','I/O operation on closed file','fixture readiness bytes'):
            self.assertFalse(lifecycle.setup_boundary('ready-eof',dict(eof,message=message),actor,'actor-readiness'))


if __name__ == '__main__': unittest.main()

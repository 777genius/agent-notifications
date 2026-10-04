"""TEST-only checker supplier. Mandatory actual candidate pins; product runtime bytes stay unchanged."""
import hashlib, importlib.util, json, os, pathlib, re, stat, subprocess, sys, types

P = pathlib.Path
PRODUCT_HEAD = os.environ['TEST_PRODUCT_HEAD']
OLD_CHECKER = os.environ['TEST_PRODUCT_CHECKER_SHA256']
assert re.fullmatch('[0-9a-f]{40}', PRODUCT_HEAD)
assert re.fullmatch('[0-9a-f]{64}', OLD_CHECKER)
SUPPLIER = '2697133dfc9d4a5d19b3cc6355a9d3a37873513ad3b8f68f083b53c7e56d3991'
root = P.cwd().resolve()
checker = root / 'scripts/testdata/opencode-native-e2e/test_fixture.py'
source = P(__file__).resolve().with_name('inert-test-fixture.py')

def digest(path):
    assert not path.is_symlink() and stat.S_ISREG(path.lstat().st_mode)
    return hashlib.sha256(path.read_bytes()).hexdigest()

def product_unchanged():
    assert os.environ['TEST_PRODUCT_HEAD'] == PRODUCT_HEAD
    assert subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=root, text=True).strip() == PRODUCT_HEAD
    subprocess.run(['git', 'diff', '--no-ext-diff', '--quiet', 'HEAD', '--'], cwd=root, check=True)
    assert digest(checker) == OLD_CHECKER
    assert digest(root / 'scripts/opencode-native-e2e.py') == os.environ['TEST_HARNESS_SHA256']

product_unchanged()
# Reuse the exact independently reviewed Windows canonical/physical boundary.
adapter = P(__file__).resolve().with_name('held-native-adapter.py')
assert digest(adapter) == '883db339908acf870123a445f099f1479fb446382ebbe7c275b398de5f14b967'
spec = importlib.util.spec_from_file_location('inert_source_custody', adapter)
custody = importlib.util.module_from_spec(spec)
spec.loader.exec_module(custody)  # Guarded module: main/native execution is not invoked.
before = custody.source_inventory(root)
assert not source.is_symlink() and stat.S_ISREG(source.lstat().st_mode) and 0 < source.stat().st_size < 128 * 1024
supplier_bytes = source.read_bytes()
assert hashlib.sha256(supplier_bytes).hexdigest() == SUPPLIER
original_main, original_argv, original_path = sys.modules['__main__'], sys.argv, sys.path[:]
module = types.ModuleType('__main__')
module.__file__ = str(checker)
module.__package__ = None
sys.modules['__main__'] = module
sys.argv = [str(checker)]
sys.path[0] = str(checker.parent)
receipt = {'status': 'failed', 'productHead': PRODUCT_HEAD, 'originalProductCheckerSHA256': OLD_CHECKER,
           'testSupplierSHA256': SUPPLIER, 'runnerSHA256': digest(P(__file__)),
           'preparedTestCount': 28, 'actualTestCountSource': 'unittest CI log',
           'businessPhasesStarted': False, 'qualificationGranted': False}
try:
    exec(compile(supplier_bytes, str(checker), 'exec'), module.__dict__)
    raise RuntimeError('pinned_checker_did_not_exit_via_unittest')
except SystemExit as outcome:
    receipt['testExitCode'] = outcome.code
    if outcome.code in (None, 0):
        receipt['status'] = 'inert_checker_pass'
    raise
finally:
    sys.modules['__main__'], sys.argv, sys.path[:] = original_main, original_argv, original_path
    product_unchanged()
    after = custody.source_inventory(root)
    assert after == before
    receipt.update(trackedProductBytesUnchanged=True, canonicalSourceInventorySHA256=before[0],
                   actualSourceFileCount=before[1], physicalSourceInventorySHA256=before[2],
                   canonicalAndPhysicalSourceUnchanged=True)
    artifacts = root / '.task-tools/artifacts'
    artifacts.mkdir(parents=True, exist_ok=True)
    (artifacts / 'inert-checker-supplier.json').write_text(json.dumps(receipt, sort_keys=True) + '\n')
    print(json.dumps(receipt, sort_keys=True), flush=True)

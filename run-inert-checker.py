"""TEST-only checker supplier. Product runtime bytes and A5A assets stay unchanged."""
import hashlib, json, os, pathlib, stat, subprocess, sys, types

P = pathlib.Path
PRODUCT_HEAD = 'a5a30b10bc2bfc378a07b70ba5fbe155ff120a33'
OLD_CHECKER = 'f4552aabfdd59129c669016099c37b72b2dda0c986090a14c2d76536acddb921'
SUPPLIER = '8cbaa51017c0b355d0362019b2eaf82be6d97a0c27087cf70f73175eaed5d2ec'
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
    receipt['trackedProductBytesUnchanged'] = True
    artifacts = root / '.task-tools/artifacts'
    artifacts.mkdir(parents=True, exist_ok=True)
    (artifacts / 'inert-checker-supplier.json').write_text(json.dumps(receipt, sort_keys=True) + '\n')
    print(json.dumps(receipt, sort_keys=True), flush=True)

import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).parents[1] / 'ci-checkout-release.sh'


class CheckoutTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        root = Path(self.temp.name)
        self.repo = root / 'work'
        self.repo.mkdir()
        self.remote = root / 'origin.git'
        subprocess.run(['git', 'init', '-q', '--bare', str(self.remote)], check=True)
        self.git('init', '-q', '-b', 'main')
        self.git('config', 'user.name', 'Local CI test')
        self.git('config', 'user.email', 'test@example.invalid')
        self.git('config', 'commit.gpgsign', 'false')
        self.git('config', 'tag.gpgsign', 'false')
        (self.repo / 'source').write_text('immutable release source')
        self.git('add', 'source')
        self.git('commit', '-qm', 'release source')
        self.source = self.git('rev-parse', 'HEAD')
        self.git('tag', '-a', 'v0.1.1', '-m', 'local test tag')
        (self.repo / 'scripts').mkdir()
        shutil.copy2(SCRIPT, self.repo / 'scripts/ci-checkout-release.sh')
        self.git('add', 'scripts')
        self.git('commit', '-qm', 'recovery configuration')
        self.config = self.git('rev-parse', 'HEAD')
        self.git('remote', 'add', 'origin', str(self.remote))
        self.git('push', '-q', 'origin', 'main', 'refs/tags/v0.1.1')
        self.tag_object = self.git('rev-parse', 'refs/tags/v0.1.1')

    def git(self, *args):
        return subprocess.check_output(['git', *args], cwd=self.repo, text=True, stderr=subprocess.PIPE).strip()

    def checkout(self, **values):
        env = {k: v for k, v in os.environ.items() if not k.startswith(('CI_', 'OBTOP_'))}
        env.update(CI_COMMIT_REF_PROTECTED='true', CI_COMMIT_REF_NAME='main', CI_COMMIT_SHA=self.config)
        env.update(values)
        return subprocess.run(['sh', 'scripts/ci-checkout-release.sh'], cwd=self.repo,
                              env=env, text=True, capture_output=True, timeout=30)

    def test_recovery_checks_out_existing_tag_without_moving_it(self):
        result = self.checkout(OBTOP_RELEASE_TAG='v0.1.1')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.git('rev-parse', 'HEAD'), self.source)
        self.assertEqual(self.git('rev-parse', 'refs/tags/v0.1.1'), self.tag_object)
        self.assertEqual(self.git('status', '--porcelain'), '')

    def test_tag_pipeline_checks_exact_source(self):
        result = self.checkout(CI_COMMIT_TAG='v0.1.1', CI_COMMIT_REF_NAME='v0.1.1', CI_COMMIT_SHA=self.source)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.git('rev-parse', 'HEAD'), self.source)

    def test_tag_pipeline_rejects_wrong_commit(self):
        result = self.checkout(CI_COMMIT_TAG='v0.1.1')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.git('rev-parse', 'HEAD'), self.config)

    def test_recovery_rejects_other_branch(self):
        result = self.checkout(OBTOP_RELEASE_TAG='v0.1.1', CI_COMMIT_REF_NAME='feature')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('protected main', result.stderr)

    def test_unprotected_ref_cannot_publish(self):
        result = self.checkout(OBTOP_RELEASE_TAG='v0.1.1', CI_COMMIT_REF_PROTECTED='false')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('protected CI ref', result.stderr)

    def test_conflicting_release_inputs_fail(self):
        result = self.checkout(CI_COMMIT_TAG='v0.1.1', OBTOP_RELEASE_TAG='v0.2.0')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('conflicting', result.stderr)

    def test_debug_trace_is_rejected(self):
        result = self.checkout(OBTOP_RELEASE_TAG='v0.1.1', CI_DEBUG_TRACE='true')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('CI_DEBUG_TRACE', result.stderr)


if __name__ == '__main__':
    unittest.main()

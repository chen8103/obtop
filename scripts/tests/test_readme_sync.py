import base64
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("readme_release", Path(__file__).parents[1] / "publish-release.py")
release = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(release)

OLD_README = """# obtop

Keep this introduction.

当前发布版本为 **v0.1.2**，属于试用阶段。

### 下载与安装 v0.1.2

Old installation instructions.

`make clean` keeps its existing explanation.

## SDK

Keep this v0.1.0 example.
"""
TAG = "v0.2.0"


def remote_file(text):
    data = text.encode()
    return {"type": "file", "encoding": "base64", "size": len(data),
            "sha": hashlib.sha1(b"blob " + str(len(data)).encode() + b"\0" + data).hexdigest(),
            "content": base64.b64encode(data).decode()}


class ReadmeRenderingTests(unittest.TestCase):
    def test_bootstrap_preserves_surrounding_public_content(self):
        result = release.updated_readme(OLD_README, TAG)
        self.assertTrue(result.startswith("# obtop\n\nKeep this introduction.\n\n"))
        self.assertTrue(result.endswith(OLD_README.split("`make clean`", 1)[1]))
        self.assertIn("obtop_v0.2.0_linux_amd64.tar.gz", result)
        self.assertIn("obtop_v0.2.0_darwin_arm64.tar.gz", result)
        self.assertIn("releases/download/v0.2.0/SHA256SUMS", result)
        self.assertIn('shasum -a 256 -c -', result)
        self.assertNotIn("Old installation instructions", result)
        self.assertNotIn("gitlab.qiyi", result)

    def test_managed_update_preserves_manual_text_outside_block(self):
        managed = release.updated_readme(OLD_README, TAG)
        edited = managed.replace("Keep this introduction.", "New manual introduction.")
        result = release.updated_readme(edited, "v0.3.0")
        self.assertIn("New manual introduction.", result)
        self.assertIn("Keep this v0.1.0 example.", result)
        self.assertIn("obtop_v0.3.0_linux_amd64.tar.gz", result)
        self.assertNotIn("obtop_v0.2.0_linux_amd64.tar.gz", result)

    def test_same_version_is_idempotent(self):
        managed = release.updated_readme(OLD_README, TAG)
        self.assertEqual(release.updated_readme(managed, TAG), managed)

    def test_old_release_cannot_downgrade_homepage(self):
        managed = release.updated_readme(OLD_README, "v0.10.0")
        self.assertEqual(release.updated_readme(managed, "v0.2.0"), managed)
        self.assertEqual(release.updated_readme(OLD_README, "v0.1.1"), OLD_README)

    def test_unknown_or_ambiguous_layout_requires_manual_review(self):
        managed = release.updated_readme(OLD_README, TAG)
        for text in ["# custom README", OLD_README + OLD_README,
                     managed.replace("<!-- obtop-downloads:end -->", ""),
                     managed + "<!-- obtop-downloads:start -->",
                     "<!-- obtop-downloads:end -->\n" + managed.replace("<!-- obtop-downloads:end -->", ""),
                     managed.replace("version=v0.2.0", "version=invalid")]:
            with self.subTest(text=text[:40]):
                with self.assertRaises(release.ReleaseError):
                    release.updated_readme(text, "v0.3.0")

    def test_invalid_tag_never_reaches_template(self):
        for tag in ["v01.2.3", "v0.2.0/../main", "v0.2.0\n", "$(id)"]:
            with self.subTest(tag=tag):
                with self.assertRaises(release.ReleaseError):
                    release.updated_readme(OLD_README, tag)

    def test_generated_readme_size_is_checked_before_write(self):
        text = 'x' * (release.README_LIMIT - len(OLD_README.encode()) - 100) + '\n' + OLD_README
        with self.assertRaisesRegex(release.ReleaseError, 'size limit'):
            release.updated_readme(text, TAG)


class ReadmeWritingTests(unittest.TestCase):
    def setUp(self):
        self.github = release.GitHub({})

    def test_update_uses_blob_sha_and_reads_back_exact_contents(self):
        before = remote_file(OLD_README)
        after = remote_file(release.updated_readme(OLD_README, TAG))
        with patch.object(self.github, "api", side_effect=[before, after]) as read:
            with patch.object(self.github, "gh", return_value="") as write:
                self.github.sync_readme(TAG)
        body = json.loads(write.call_args.kwargs['data'])
        self.assertEqual(body['branch'], 'main')
        self.assertEqual(body['sha'], before['sha'])
        self.assertEqual(base64.b64decode(body['content']).decode(), release.updated_readme(OLD_README, TAG))
        self.assertIn('repos/chen8103/obtop/contents/README.md', write.call_args.args)
        self.assertIn('PUT', write.call_args.args)
        self.assertEqual(read.call_count, 2)
        write.assert_called_once()

    def test_dry_run_only_reads(self):
        with patch.object(self.github, "api", return_value=remote_file(OLD_README)):
            with patch.object(self.github, "gh") as write:
                self.github.sync_readme(TAG, dry_run=True)
        write.assert_not_called()

    def test_same_or_newer_readme_does_not_create_commit(self):
        for tag in [TAG, 'v0.1.9']:
            with patch.object(self.github, "api", return_value=remote_file(release.updated_readme(OLD_README, TAG))):
                with patch.object(self.github, "gh") as write:
                    self.github.sync_readme(tag)
            write.assert_not_called()

    def test_timeout_applied_is_confirmed_without_retry(self):
        after = remote_file(release.updated_readme(OLD_README, TAG))
        with patch.object(self.github, "api", side_effect=[remote_file(OLD_README), after]):
            with patch.object(self.github, "gh", side_effect=subprocess.TimeoutExpired('gh', 180)) as write:
                self.github.sync_readme(TAG)
        write.assert_called_once()

    def test_timeout_not_applied_fails_without_retry(self):
        with patch.object(self.github, "api", return_value=remote_file(OLD_README)):
            with patch.object(self.github, "gh", side_effect=subprocess.TimeoutExpired('gh', 180)) as write:
                with self.assertRaisesRegex(release.ReleaseError, "not confirmed"):
                    self.github.sync_readme(TAG)
        write.assert_called_once()

    def test_concurrent_edit_is_not_overwritten_or_retried(self):
        concurrent = OLD_README.replace('Keep this introduction.', 'Concurrent edit.')
        with patch.object(self.github, "api", side_effect=[remote_file(OLD_README), remote_file(concurrent)]):
            with patch.object(self.github, "gh", side_effect=release.ReleaseError('HTTP conflict')) as write:
                with self.assertRaisesRegex(release.ReleaseError, "not confirmed"):
                    self.github.sync_readme(TAG)
        write.assert_called_once()

    def test_invalid_file_fails_before_write(self):
        valid = remote_file(OLD_README)
        for value in [None, dict(valid, type='symlink'), dict(valid, encoding='none'),
                      dict(valid, size=1024 * 1024), dict(valid, content='not base64!')]:
            with self.subTest(value=value):
                with patch.object(self.github, "api", return_value=value):
                    with patch.object(self.github, "gh") as write:
                        with self.assertRaises(release.ReleaseError):
                            self.github.sync_readme(TAG)
                write.assert_not_called()


class WorkflowOrderTests(unittest.TestCase):
    def main(self, *, fail_publish=False, dry_run=False, default_branch='main'):
        root = release.ROOT.resolve()
        commit, tree = 'a' * 40, 'b' * 40
        answers = {('git', 'rev-parse', '--show-toplevel'): str(root),
                   ('git', 'rev-parse', 'HEAD'): commit,
                   ('git', 'rev-parse', 'HEAD^{tree}'): tree,
                   ('git', 'status', '--porcelain', '--untracked-files=normal'): '',
                   ('git', 'cat-file', '-t', f'refs/tags/{TAG}'): 'tag',
                   ('git', 'rev-parse', f'refs/tags/{TAG}^{{commit}}'): commit}
        events = []
        def publish(*args):
            events.append('release_verified')
            if fail_publish:
                raise release.ReleaseError('release verification failed')
        with patch.dict(release.os.environ, {'OBTOP_GITHUB_TOKEN': 'unit-test-placeholder'}, clear=True):
            with patch.object(release.sys, 'argv', ['publish-release.py', '--tag', TAG] + (['--dry-run'] if dry_run else [])):
                with patch.object(release, 'run', side_effect=lambda args, **kwargs: answers[tuple(args)]):
                    with patch.object(release, 'validate_packages', return_value=[]):
                        with patch.object(release, 'GitHub') as cls:
                            github = cls.return_value
                            github.gh.return_value = json.dumps({'permissions': {'push': True}, 'default_branch': default_branch})
                            github.sync_readme.side_effect = lambda *args, **kwargs: events.append('readme')
                            with patch.object(release, 'publish', side_effect=publish):
                                if fail_publish or default_branch != 'main':
                                    with self.assertRaises(release.ReleaseError):
                                        release.main()
                                else:
                                    release.main()
        return events, github

    def test_readme_follows_successful_release_verification(self):
        events, github = self.main()
        self.assertEqual(events, ['release_verified', 'readme'])
        github.sync_readme.assert_called_once_with(TAG, dry_run=False)

    def test_failed_release_never_syncs_readme(self):
        events, github = self.main(fail_publish=True)
        self.assertEqual(events, ['release_verified'])
        github.sync_readme.assert_not_called()

    def test_workflow_dry_run_propagates_read_only_mode(self):
        _, github = self.main(dry_run=True)
        github.sync_readme.assert_called_once_with(TAG, dry_run=True)

    def test_unexpected_default_branch_stops_before_publication(self):
        events, github = self.main(default_branch='another-branch')
        self.assertEqual(events, [])
        github.sync_readme.assert_not_called()


if __name__ == '__main__':
    unittest.main()

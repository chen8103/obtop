import base64
import copy
import importlib.util
import io
import json
from pathlib import Path
import unittest
from unittest.mock import patch
import urllib.error

SPEC = importlib.util.spec_from_file_location('project_readme', Path(__file__).parents[1] / 'sync-project-readme.py')
sync = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(sync)
TAG, SOURCE = 'v0.2.0', 'a' * 40
README = '''# obtop

Keep the introduction.

<!-- obtop-project-release:start -->
<!-- obtop-project-release:version=v0.1.3 -->
Version v0.1.3
git switch --detach v0.1.3
dist/v0.1.3/obtop_v0.1.3_linux_amd64.tar.gz
https://github.com/chen8103/obtop/releases/download/v0.1.3/obtop_v0.1.3_darwin_arm64.tar.gz
https://github.com/chen8103/obtop/releases/download/v0.1.3/SHA256SUMS
"$HOME/.local/opt/obtop/v0.1.3/obtop" version
<!-- obtop-project-release:end -->

Keep historical v0.1.2 documentation.
'''


def file_value(text, commit='b' * 40):
    return dict(file_path='README.md', encoding='base64', size=len(text.encode()),
                content=base64.b64encode(text.encode()).decode(), last_commit_id=commit)


class FakeGitLab(sync.GitLab):
    def __init__(self):
        super().__init__('unit-test-placeholder')
        self.current = README
        self.file_commit = 'b' * 40
        self.tag_commit = SOURCE
        self.release_value = dict(tag_name=TAG, draft=False, prerelease=False,
                                  body=f'<!-- obtop-source:{SOURCE} -->',
                                  assets=[dict(name=f'obtop_{TAG}_{p}.tar.gz') for p in ['linux_amd64', 'darwin_arm64']] + [dict(name='SHA256SUMS')])
        self.events = []
        self.apply_write = True
        self.write_error = False
        self.concurrent_text = None

    def api(self, method, path, body=None):
        if method == 'GET' and path == f'repository/tags/{TAG}':
            self.events.append('tag')
            return {'commit': {'id': self.tag_commit}}
        if method == 'GET' and path == 'repository/files/README.md?ref=main':
            self.events.append('readme')
            return file_value(self.current, self.file_commit)
        if method == 'POST' and path == 'repository/commits':
            self.events.append(('write', copy.deepcopy(body)))
            if self.concurrent_text is not None:
                self.current = self.concurrent_text
            elif self.apply_write:
                self.current = base64.b64decode(body['actions'][0]['content']).decode()
                self.file_commit = 'c' * 40
            if self.write_error:
                raise sync.SyncError('simulated request failure')
            return {'id': self.file_commit}
        raise AssertionError((method, path))

    def public_release(self, tag):
        self.events.append('release')
        return copy.deepcopy(self.release_value)


class RenderingTests(unittest.TestCase):
    def test_updates_all_release_examples_and_preserves_history(self):
        result = sync.updated_project_readme(README, TAG)
        self.assertIn('git switch --detach v0.2.0', result)
        self.assertIn('obtop_v0.2.0_linux_amd64.tar.gz', result)
        self.assertIn('obtop_v0.2.0_darwin_arm64.tar.gz', result)
        self.assertIn('releases/download/v0.2.0/SHA256SUMS', result)
        self.assertIn('$HOME/.local/opt/obtop/v0.2.0', result)
        self.assertIn('Keep historical v0.1.2 documentation.', result)
        self.assertTrue(result.startswith('# obtop\n\nKeep the introduction.'))

    def test_same_version_is_idempotent_and_old_version_cannot_downgrade(self):
        current = sync.updated_project_readme(README, 'v0.10.0')
        self.assertEqual(sync.updated_project_readme(current, 'v0.10.0'), current)
        self.assertEqual(sync.updated_project_readme(current, TAG), current)

    def test_ambiguous_markers_or_mixed_versions_stop(self):
        for text in [README.replace(sync.END, ''), README + sync.START,
                     README.replace('Version v0.1.3', 'Version v0.1.2'), '# custom README']:
            with self.subTest(text=text[:30]):
                with self.assertRaises(sync.SyncError):
                    sync.updated_project_readme(text, TAG)

    def test_invalid_tag_is_rejected(self):
        for tag in ['v01.2.3', 'v0.2.0/../main', 'v0.2.0\n', '$(id)']:
            with self.subTest(tag=tag):
                with self.assertRaises(sync.SyncError):
                    sync.updated_project_readme(README, tag)

    def test_generated_output_cannot_exceed_limit(self):
        padded = 'x' * (sync.LIMIT-len(README.encode())-10) + '\n' + README
        with self.assertRaisesRegex(sync.SyncError, 'size limit'):
            sync.updated_project_readme(padded, 'v0.200000.0')


class SynchronizationTests(unittest.TestCase):
    def setUp(self):
        environment = patch.dict(sync.os.environ, {}, clear=True)
        environment.start()
        self.addCleanup(environment.stop)

    def test_commit_updates_only_readme_with_file_lease_then_reads_back(self):
        client = FakeGitLab()
        client.sync(TAG, SOURCE)
        writes = [e[1] for e in client.events if isinstance(e, tuple)]
        self.assertEqual(len(writes), 1)
        body = writes[0]
        self.assertEqual(body['branch'], 'main')
        self.assertEqual(len(body['actions']), 1)
        action = body['actions'][0]
        self.assertEqual(action['action'], 'update')
        self.assertEqual(action['file_path'], 'README.md')
        self.assertEqual(action['last_commit_id'], 'b' * 40)
        self.assertEqual(action['encoding'], 'base64')
        self.assertNotIn('[skip ci]', body['commit_message'])
        self.assertNotIn('force', body)
        self.assertEqual(client.current, sync.updated_project_readme(README, TAG))
        self.assertEqual(client.events[-1], 'readme')

    def test_dry_run_never_writes(self):
        client = FakeGitLab();client.sync(TAG, SOURCE, dry_run=True)
        self.assertFalse(any(isinstance(e, tuple) for e in client.events))
        self.assertEqual(client.current, README)

    def test_retry_after_confirmation_creates_no_duplicate_commit(self):
        client = FakeGitLab();client.sync(TAG, SOURCE);client.sync(TAG, SOURCE)
        self.assertEqual(sum(isinstance(e, tuple) for e in client.events), 1)

    def test_old_version_has_no_write(self):
        client = FakeGitLab();client.current = sync.updated_project_readme(README, 'v0.10.0')
        client.sync(TAG, SOURCE)
        self.assertFalse(any(isinstance(e, tuple) for e in client.events))

    def test_unconfirmed_error_never_retries_write(self):
        client = FakeGitLab();client.apply_write=False;client.write_error=True
        with self.assertRaisesRegex(sync.SyncError, 'not confirmed'):
            client.sync(TAG, SOURCE)
        self.assertEqual(sum(isinstance(e, tuple) for e in client.events), 1)

    def test_applied_timeout_is_confirmed_by_readback(self):
        client = FakeGitLab();client.write_error=True;client.sync(TAG, SOURCE)
        self.assertEqual(sum(isinstance(e, tuple) for e in client.events), 1)

    def test_concurrent_manual_edit_is_not_overwritten(self):
        client = FakeGitLab();client.write_error=True
        client.concurrent_text=README.replace('Keep the introduction.', 'Concurrent manual edit.')
        with self.assertRaisesRegex(sync.SyncError, 'not confirmed'):
            client.sync(TAG, SOURCE)
        self.assertIn('Concurrent manual edit.', client.current)
        self.assertEqual(sum(isinstance(e, tuple) for e in client.events), 1)

    def test_wrong_source_or_unpublished_release_never_writes(self):
        for change in ['tag', 'draft', 'prerelease', 'source', 'assets']:
            client=FakeGitLab()
            if change=='tag': client.tag_commit='d' * 40
            elif change=='draft': client.release_value['draft']=True
            elif change=='prerelease': client.release_value['prerelease']=True
            elif change=='source': client.release_value['body']='another source'
            else: client.release_value['assets']=[]
            with self.subTest(change=change):
                with self.assertRaises(sync.SyncError): client.sync(TAG, SOURCE)
                self.assertFalse(any(isinstance(e, tuple) for e in client.events))

    def test_bad_file_encoding_or_commit_is_rejected_before_write(self):
        for value in [dict(file_value(README), content='not base64!'),
                      dict(file_value(README), size=1024*1024),
                      dict(file_value(README), last_commit_id=None),
                      dict(file_value(README), last_commit_id='invalid')]:
            client=FakeGitLab();real=client.api
            def api(method,path,body=None):
                return value if path.startswith('repository/files/') else real(method,path,body)
            with self.subTest(value=value):
                with patch.object(client,'api',side_effect=api):
                    with self.assertRaises(sync.SyncError): client.sync(TAG,SOURCE)


class AuthorizationTests(unittest.TestCase):
    def test_missing_token_fails(self):
        with self.assertRaisesRegex(sync.SyncError,'OBTOP_GITLAB_TOKEN'): sync.GitLab('')

    def test_ci_requires_protected_project_and_matching_tag(self):
        base={'CI':'true','CI_PROJECT_ID':'102071','CI_COMMIT_REF_PROTECTED':'true',
              'CI_COMMIT_TAG':TAG,'CI_COMMIT_SHA':SOURCE,'CI_COMMIT_REF_NAME':TAG}
        for change in [{'CI_PROJECT_ID':'1'},{'CI_COMMIT_REF_PROTECTED':'false'},
                       {'CI_COMMIT_TAG':'v0.3.0'},{'CI_COMMIT_SHA':'d'*40},{'CI_DEBUG_TRACE':'true'}]:
            with self.subTest(change=change), patch.dict(sync.os.environ,dict(base,**change),clear=True):
                with self.assertRaises(sync.SyncError): sync.validate_context(TAG,SOURCE)

    def test_protected_main_recovery_is_explicit(self):
        env={'CI':'true','CI_PROJECT_ID':'102071','CI_COMMIT_REF_PROTECTED':'true',
             'CI_COMMIT_REF_NAME':'main','OBTOP_RELEASE_TAG':TAG,'CI_COMMIT_SHA':'d'*40}
        with patch.dict(sync.os.environ,env,clear=True):
            sync.validate_context(TAG,SOURCE)
        env.pop('OBTOP_RELEASE_TAG')
        with patch.dict(sync.os.environ,env,clear=True):
            with self.assertRaises(sync.SyncError): sync.validate_context(TAG,SOURCE)

    def test_api_error_never_echoes_secret_response_body(self):
        client=sync.GitLab('unit-test-placeholder')
        error=urllib.error.HTTPError(sync.API,403,'Forbidden',{},io.BytesIO(b'credential-reflection'))
        with patch.object(client.opener,'open',side_effect=error):
            with self.assertRaises(sync.SyncError) as caught:
                client.api('GET','repository/files/README.md?ref=main')
        self.assertIn('403',str(caught.exception))
        self.assertNotIn('credential-reflection',str(caught.exception))
        self.assertNotIn('unit-test-placeholder',str(caught.exception))


if __name__=='__main__': unittest.main()

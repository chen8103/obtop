#!/usr/bin/env python3
"""Update only obtop's GitLab main README after a verified public release."""
import argparse
import base64
import json
import os
import re
import subprocess
import sys
import urllib.error
import urllib.request

API = 'http://gitlab.qiyi.domain/api/v4/projects/102071/'
START = '<!-- obtop-project-release:start -->'
END = '<!-- obtop-project-release:end -->'
VERSION = re.compile(r'v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\Z')
VERSION_REF = re.compile(r'v[0-9]+\.[0-9]+\.[0-9]+')
LIMIT = 256 * 1024


class SyncError(Exception):
    pass


def validate_context(tag, source):
    if not isinstance(tag, str) or len(tag) > 64 or not VERSION.fullmatch(tag):
        raise SyncError('tag must be vMAJOR.MINOR.PATCH')
    if not re.fullmatch(r'[0-9a-f]{40}', source):
        raise SyncError('source commit must be a full Git SHA')
    if os.environ.get('CI_DEBUG_TRACE') == 'true':
        raise SyncError('disable CI_DEBUG_TRACE before using credentials')
    if os.environ.get('CI'):
        if os.environ.get('CI_PROJECT_ID') != '102071' or os.environ.get('CI_COMMIT_REF_PROTECTED') != 'true':
            raise SyncError('project README sync requires the protected obtop project ref')
        ci_tag = os.environ.get('CI_COMMIT_TAG')
        if ci_tag:
            if ci_tag != tag or os.environ.get('CI_COMMIT_SHA') != source:
                raise SyncError('tag pipeline does not match release source')
        elif (os.environ.get('CI_COMMIT_REF_NAME') != 'main' or
              os.environ.get('OBTOP_RELEASE_TAG') != tag):
            raise SyncError('recovery requires protected main and an explicit release tag')


def updated_project_readme(text, tag):
    if not VERSION.fullmatch(tag) or len(tag) > 64:
        raise SyncError('tag must be vMAJOR.MINOR.PATCH')
    if len(text.encode()) > LIMIT or text.count(START) != 1 or text.count(END) != 1:
        raise SyncError('invalid project README size or markers; manual review required')
    start, end = text.index(START), text.index(END) + len(END)
    block = text[start:end]
    versions = re.findall(r'^<!-- obtop-project-release:version=(\S+) -->$', block, re.MULTILINE)
    if (start >= end or len(versions) != 1 or len(versions[0]) > 64 or
            not VERSION.fullmatch(versions[0])):
        raise SyncError('invalid project README version marker; manual review required')
    current = versions[0]
    if tuple(map(int, current[1:].split('.'))) >= tuple(map(int, tag[1:].split('.'))):
        return text
    if set(VERSION_REF.findall(block)) != {current}:
        raise SyncError('mixed versions in project README release block; manual review required')
    result = text[:start] + VERSION_REF.sub(lambda match: tag, block) + text[end:]
    if len(result.encode()) > LIMIT:
        raise SyncError('generated project README exceeds the size limit')
    return result


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class GitLab:
    def __init__(self, token):
        if not token or any(c.isspace() for c in token):
            raise SyncError('configure protected/masked OBTOP_GITLAB_TOKEN for obtop README updates')
        self.token = token
        # Fixed internal endpoint; never forward this credential to a redirect or proxy.
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())

    def api(self, method, path, body=None):
        headers = {'PRIVATE-TOKEN': self.token}
        data = None
        if body is not None:
            data = json.dumps(body).encode()
            headers['Content-Type'] = 'application/json'
        request = urllib.request.Request(API + path, data=data, headers=headers, method=method)
        try:
            with self.opener.open(request, timeout=30) as response:
                value = response.read(2 * LIMIT + 1)
            if len(value) > 2 * LIMIT:
                raise SyncError('GitLab response exceeds the size limit')
            return json.loads(value)
        except urllib.error.HTTPError as error:
            code = error.code
            error.close()
            raise SyncError(f'GitLab request failed (HTTP {code}); no write retry attempted') from None
        except (urllib.error.URLError, OSError, ValueError):
            raise SyncError('GitLab request failed; no write retry attempted') from None

    def public_release(self, tag):
        env = dict(os.environ, GH_HOST='github.com', GH_PROMPT_DISABLED='1')
        env.pop('GH_DEBUG', None)
        if os.environ.get('OBTOP_GITHUB_TOKEN'):
            env['GH_TOKEN'] = os.environ['OBTOP_GITHUB_TOKEN']
        try:
            result = subprocess.run(['gh', 'api', '--hostname', 'github.com',
                                     f'repos/chen8103/obtop/releases/tags/{tag}'],
                                    text=True, capture_output=True, env=env, timeout=60)
        except (OSError, subprocess.TimeoutExpired):
            raise SyncError('GitHub release read failed') from None
        if result.returncode or len(result.stdout.encode()) > 2 * LIMIT:
            raise SyncError('GitHub release read failed')
        return json.loads(result.stdout)

    def readme(self):
        value = self.api('GET', 'repository/files/README.md?ref=main')
        if (not isinstance(value, dict) or value.get('file_path') != 'README.md' or
                value.get('encoding') != 'base64' or not isinstance(value.get('size'), int) or
                not 0 <= value['size'] <= LIMIT or not isinstance(value.get('content'), str) or
                len(value['content']) > 2 * LIMIT or
                not isinstance(value.get('last_commit_id'), str) or
                not re.fullmatch(r'[0-9a-f]{40}', value.get('last_commit_id', ''))):
            raise SyncError('invalid project README file metadata')
        try:
            data = base64.b64decode(''.join(value['content'].splitlines()), validate=True)
            text = data.decode('utf-8')
        except (ValueError, UnicodeError):
            raise SyncError('invalid project README encoding') from None
        if len(data) != value['size'] or len(data) > LIMIT:
            raise SyncError('invalid project README content size')
        return value['last_commit_id'], text

    def sync(self, tag, source, dry_run=False):
        validate_context(tag, source)
        remote_tag = self.api('GET', f'repository/tags/{tag}')
        commit = remote_tag.get('commit') if isinstance(remote_tag, dict) else None
        if not isinstance(commit, dict) or commit.get('id') != source:
            raise SyncError('GitLab tag does not match the release source')
        public = self.public_release(tag)
        expected = {f'obtop_{tag}_{p}.tar.gz' for p in ['linux_amd64', 'darwin_arm64']} | {'SHA256SUMS'}
        if (not isinstance(public, dict) or public.get('tag_name') != tag or public.get('draft') is not False or
                public.get('prerelease') is not False or
                not isinstance(public.get('body'), str) or not isinstance(public.get('assets'), list) or
                not all(isinstance(a, dict) and isinstance(a.get('name'), str) for a in public['assets']) or
                f'<!-- obtop-source:{source} -->' not in public.get('body', '') or
                not expected.issubset({a['name'] for a in public.get('assets', [])})):
            raise SyncError('matching public release and all required assets must exist before README sync')
        last_commit, current = self.readme()
        desired = updated_project_readme(current, tag)
        if desired == current:
            print('Project README already current or newer; no commit needed.')
            return
        if dry_run:
            print(f'DRY RUN: would sync GitLab main README release examples to {tag}.')
            return
        body = {'branch': 'main', 'commit_message': f'docs: sync {tag} project README release examples',
                'actions': [{'action': 'update', 'file_path': 'README.md',
                             'encoding': 'base64', 'content': base64.b64encode(desired.encode()).decode(),
                             'last_commit_id': last_commit}]}
        try:
            self.api('POST', 'repository/commits', body)
        except SyncError:
            print('Project README write returned an error; checking GitLab before continuing (no retry).')
        confirmed_commit, actual = self.readme()
        if actual != desired:
            raise SyncError('project README update was not confirmed; inspect permissions or concurrent edits before retrying')
        print(f'Project README synchronized and verified: {confirmed_commit}')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--tag', required=True)
    parser.add_argument('--source-commit', required=True)
    parser.add_argument('--dry-run', action='store_true')
    args = parser.parse_args()
    validate_context(args.tag, args.source_commit)
    client = GitLab(os.environ.get('OBTOP_GITLAB_TOKEN'))
    client.sync(args.tag, args.source_commit, dry_run=args.dry_run)


if __name__ == '__main__':
    try:
        main()
    except (SyncError, OSError, ValueError, KeyError, TypeError) as error:
        print('project README error: ' + (str(error) if isinstance(error, SyncError) else type(error).__name__), file=sys.stderr)
        sys.exit(1)

#!/usr/bin/env python3
"""Publish verified packages to one fixed GitHub repository, without private history."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tarfile
import tempfile

REPO = "chen8103/obtop"
ROOT = Path(__file__).resolve().parent.parent
VERSION = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\Z")


class ReleaseError(Exception):
    pass


def run(args, *, data=None, env=None, optional=False):
    result = subprocess.run(args, input=data, text=True, capture_output=True,
                            cwd=ROOT, env=env, timeout=180)
    if result.returncode:
        # Never echo subprocess output: authentication errors can contain secrets.
        if optional and "(HTTP 404)" in result.stderr:
            return None
        raise ReleaseError(f"{args[0]} {args[1]} failed (exit {result.returncode}); no write retry attempted")
    return result.stdout.strip()


def digest(path):
    with path.open("rb") as stream:
        value = hashlib.sha256()
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            value.update(block)
        return value.hexdigest()


def validate_packages(tag, commit):
    directory = ROOT / "dist" / tag
    names = [f"obtop_{tag}_{platform}.tar.gz" for platform in ("linux_amd64", "darwin_arm64")]
    for name in names + ["SHA256SUMS", "SOURCE_COMMIT"]:
        path = directory / name
        if path.is_symlink() or not path.is_file() or path.stat().st_size > 100 * 1024 * 1024:
            raise ReleaseError(f"missing or invalid artifact: {name}")
    if (directory / "SOURCE_COMMIT").read_text().strip() != commit:
        raise ReleaseError("artifacts were built from a different source commit")
    checksums = (directory / "SHA256SUMS").read_text()
    if len(checksums) > 1024:
        raise ReleaseError("invalid checksum manifest")
    expected = {}
    for line in checksums.splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  (?:\./)?([^/]+)", line)
        if not match or match[2] not in names or match[2] in expected:
            raise ReleaseError("invalid or duplicate checksum entry")
        expected[match[2]] = match[1]
    if set(expected) != set(names):
        raise ReleaseError("both platform checksums are required")
    for name in names:
        if digest(directory / name) != expected[name]:
            raise ReleaseError(f"checksum mismatch: {name}")
        with tarfile.open(directory / name, "r|gz") as archive:
            seen = set()
            for member in archive:
                if (member.name not in {"README.md", "obtop"} or member.name in seen or
                        not member.isfile() or member.size > 100 * 1024 * 1024):
                    raise ReleaseError(f"unexpected archive contents: {name}")
                seen.add(member.name)
                if member.name == "obtop":
                    if not member.mode & 0o111:
                        raise ReleaseError(f"binary is not executable: {name}")
                    magic = archive.extractfile(member).read(4)
                    expected_magic = b"\x7fELF" if "linux_amd64" in name else b"\xcf\xfa\xed\xfe"
                    if magic != expected_magic:
                        raise ReleaseError(f"wrong binary format: {name}")
            if seen != {"README.md", "obtop"}:
                raise ReleaseError(f"incomplete archive: {name}")
    return [directory / name for name in names + ["SHA256SUMS"]]


class GitHub:
    def __init__(self, env):
        self.env = env

    def gh(self, *args, optional=False):
        return run(["gh", *args], env=self.env, optional=optional)

    def api(self, path):
        value = self.gh("api", "--hostname", "github.com", f"repos/{REPO}/{path}", optional=True)
        return json.loads(value) if value is not None else None

    def tag_tree(self, tag):
        ref = self.api(f"git/ref/tags/{tag}")
        if ref is None:
            return None
        obj = ref["object"]
        for _ in range(5):
            if obj["type"] == "commit":
                return self.api(f"git/commits/{obj['sha']}")["tree"]["sha"]
            if obj["type"] != "tag":
                break
            obj = self.api(f"git/tags/{obj['sha']}")["object"]
        raise ReleaseError("unsupported GitHub tag object")

    def release(self, tag):
        release = self.api(f"releases/tags/{tag}")
        if release is not None:
            return release
        # GitHub's tag endpoint can return 404 for an authenticated draft lookup.
        # The release list includes drafts visible to this credential.
        for page in range(1, 11):
            releases = self.api(f"releases?per_page=100&page={page}")
            if releases is None:
                raise ReleaseError("release listing was not available")
            for candidate in releases:
                if candidate["tag_name"] == tag:
                    return candidate
            if len(releases) < 100:
                return None
        raise ReleaseError("release lookup exceeded its page limit")

    def verify_downloads(self, tag, paths):
        with tempfile.TemporaryDirectory(prefix="obtop-release-verify-") as directory:
            for path in paths:
                self.gh("release", "download", tag, "--repo", REPO, "--pattern", path.name,
                        "--dir", directory)
                if digest(Path(directory) / path.name) != digest(path):
                    raise ReleaseError(f"download checksum mismatch: {path.name}")

    def write_and_read(self, args, tag):
        # A timeout/error is an unknown outcome. Always read before deciding next steps.
        try:
            self.gh(*args)
        except (ReleaseError, subprocess.TimeoutExpired):
            print("Write returned an error; checking GitHub before continuing (no retry).")
        return self.release(tag)


def ensure_tag(github, tag, commit, tree):
    remote = github.tag_tree(tag)
    if remote is not None:
        if remote != tree:
            raise ReleaseError("GitHub tag has different source content; refusing to overwrite")
        return
    # A standalone snapshot keeps private GitLab commit history out of public GitHub.
    env = dict(github.env)
    for role in ("AUTHOR", "COMMITTER"):
        env[f"GIT_{role}_NAME"] = "obtop release bot"
        env[f"GIT_{role}_EMAIL"] = "obtop-release@users.noreply.github.com"
        env[f"GIT_{role}_DATE"] = run(["git", "show", "-s", "--format=%cI", commit])
    snapshot = run(["git", "-c", "commit.gpgsign=false", "commit-tree", tree],
                   data=f"obtop {tag}\n\nSource commit: {commit}\n", env=env)
    try:
        run(["git", "-c", "credential.helper=", "-c",
             "credential.https://github.com.helper=!gh auth git-credential", "push",
             f"https://github.com/{REPO}.git", f"{snapshot}:refs/tags/{tag}"], env=env)
    except (ReleaseError, subprocess.TimeoutExpired):
        print("Tag push returned an error; checking GitHub (no retry).")
    if github.tag_tree(tag) != tree:
        raise ReleaseError("GitHub source tag was not confirmed")


def publish(github, tag, commit, tree, paths, dry_run):
    remote_tree = github.tag_tree(tag)
    if remote_tree is not None and remote_tree != tree:
        raise ReleaseError("GitHub tag has different source content; refusing to overwrite")
    release = github.release(tag)
    if release and remote_tree is None:
        raise ReleaseError("existing release has no source tag; manual review required")
    marker = f"<!-- obtop-source:{commit} -->"
    if release and release["draft"] and marker not in release.get("body", ""):
        raise ReleaseError("existing draft belongs to another source; manual review required")
    if release:
        existing = [p for p in paths if p.name in {a["name"] for a in release["assets"]}]
        github.verify_downloads(tag, existing)
        if not release["draft"]:
            if len(existing) != len(paths):
                raise ReleaseError("published release is incomplete; refusing to modify it")
            print(f"Already published and verified: {release['html_url']}")
            return
    if dry_run:
        print(f"DRY RUN: source={commit}, tree={tree}, GitHub={REPO}, tag={tag}")
        print("Would sync a source snapshot, upload/verify 3 assets in a draft, then publish.")
        return
    ensure_tag(github, tag, commit, tree)
    if release is None:
        notes_path = ROOT / "docs" / f"release-{tag}.md"
        notes = notes_path.read_text() if notes_path.is_file() else (
            f"# obtop {tag}\n\nLinux amd64 和 macOS arm64 安装包，解压后直接运行。\n"
            "下载后使用 SHA256SUMS 校验。Linux 执行测试及交叉编译通过；"
            "macOS 运行验证与 OceanBase 现场验证不包含在自动构建中。\n")
        notes += f"\n\nSource commit: `{commit}`\n\nSource tree: `{tree}`\n\n{marker}\n"
        with tempfile.TemporaryDirectory(prefix="obtop-release-notes-") as directory:
            note_file = Path(directory) / "notes.md"
            note_file.write_text(notes)
            release = github.write_and_read(
                ["release", "create", tag, "--repo", REPO, "--verify-tag", "--draft",
                 "--title", f"obtop {tag}", "--notes-file", str(note_file)], tag)
        if not release or not release["draft"] or marker not in release.get("body", ""):
            raise ReleaseError("draft creation was not confirmed")
    for path in paths:
        if path.name not in {a["name"] for a in release["assets"]}:
            release = github.write_and_read(
                ["release", "upload", tag, "--repo", REPO, str(path)], tag)
            if not release or not release["draft"] or path.name not in {a["name"] for a in release["assets"]}:
                raise ReleaseError(f"draft upload was not confirmed: {path.name}")
    github.verify_downloads(tag, paths)
    # Recheck tag identity immediately before making the release public.
    if github.tag_tree(tag) != tree:
        raise ReleaseError("GitHub tag changed during upload")
    release = github.write_and_read(
        ["release", "edit", tag, "--repo", REPO, "--draft=false"], tag)
    if not release or release["draft"] or release["tag_name"] != tag:
        raise ReleaseError("publication was not confirmed; inspect the draft before rerunning")
    print(f"Published and verified: {release['html_url']}")


def main():
    global ROOT
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--tag", default=os.environ.get("CI_COMMIT_TAG"), required=False)
    parser.add_argument("--source-dir", type=Path, default=ROOT,
                        help="checked-out release source; allows a newer CI controller for recovery")
    parser.add_argument("--dry-run", action="store_true", help="read-only remote preflight")
    args = parser.parse_args()
    if not args.tag or not VERSION.fullmatch(args.tag):
        raise ReleaseError("--tag must be vMAJOR.MINOR.PATCH")
    if os.environ.get("CI") and os.environ.get("CI_COMMIT_REF_PROTECTED") != "true":
        raise ReleaseError("CI release requires a protected tag")
    if os.environ.get("CI_DEBUG_TRACE") == "true":
        raise ReleaseError("disable CI_DEBUG_TRACE before using release credentials")
    ROOT = args.source_dir.resolve()
    if Path(run(["git", "rev-parse", "--show-toplevel"])).resolve() != ROOT:
        raise ReleaseError("--source-dir must be a Git worktree root")
    env = dict(os.environ, GH_HOST="github.com", GH_PROMPT_DISABLED="1", GIT_TERMINAL_PROMPT="0",
               GIT_TRACE="0", GIT_TRACE_CURL="0", GIT_CURL_VERBOSE="0")
    token = os.environ.get("OBTOP_GITHUB_TOKEN")
    if token:
        env["GH_TOKEN"] = token
    elif not args.dry_run or os.environ.get("CI"):
        raise ReleaseError("configure protected/masked OBTOP_GITHUB_TOKEN with repository Contents read/write")
    commit = run(["git", "rev-parse", "HEAD"])
    tree = run(["git", "rev-parse", "HEAD^{tree}"])
    if run(["git", "status", "--porcelain", "--untracked-files=normal"]):
        raise ReleaseError("publication requires a clean worktree")
    if (run(["git", "cat-file", "-t", f"refs/tags/{args.tag}"]) != "tag" or
            run(["git", "rev-parse", f"refs/tags/{args.tag}^{{commit}}"]) != commit):
        raise ReleaseError("annotated release tag must point to HEAD")
    paths = validate_packages(args.tag, commit)
    github = GitHub(env)
    repo = json.loads(github.gh("api", "--hostname", "github.com", f"repos/{REPO}"))
    if not repo.get("permissions", {}).get("push"):
        raise ReleaseError("GitHub token lacks repository write access")
    publish(github, args.tag, commit, tree, paths, args.dry_run)


if __name__ == "__main__":
    try:
        main()
    except (ReleaseError, OSError, ValueError, KeyError, tarfile.TarError, subprocess.TimeoutExpired) as error:
        # Exception messages from tools/OS may include credentials; do not print raw details.
        message = str(error) if isinstance(error, ReleaseError) else type(error).__name__
        print(f"release error: {message}", file=sys.stderr)
        sys.exit(1)

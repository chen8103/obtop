import copy
import importlib.util
import io
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("release", Path(__file__).parents[1] / "publish-release.py")
release = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(release)
TAG, COMMIT, TREE = "v0.2.0", "a" * 40, "b" * 40
NAMES = [f"obtop_{TAG}_{p}.tar.gz" for p in ("linux_amd64", "darwin_arm64")] + ["SHA256SUMS"]


class FakeGitHub:
    def __init__(self, draft=True, assets=(), tree=TREE):
        self.state = dict(draft=draft, tag_name=TAG, html_url="https://github.com/chen8103/obtop/releases/tag/" + TAG,
                          body=f"<!-- obtop-source:{COMMIT} -->", assets=[dict(name=n) for n in assets])
        self.tree = tree
        self.events = []
        self.bad_download = False
        self.confirm_upload = True

    def tag_tree(self, tag):
        return self.tree

    def release(self, tag):
        return copy.deepcopy(self.state)

    def verify_downloads(self, tag, paths):
        self.events.append(("verify", [p.name for p in paths]))
        if self.bad_download:
            raise release.ReleaseError("download checksum mismatch")

    def write_and_read(self, args, tag):
        self.events.append((args[1], args))
        if args[1] == "create":
            self.state = dict(draft=True, tag_name=tag, html_url="https://github.com/chen8103/obtop/releases/tag/" + tag,
                              body=Path(args[-1]).read_text(), assets=[])
        if args[1] == "upload" and self.confirm_upload:
            self.state["assets"].append(dict(name=Path(args[-1]).name))
        if args[1] == "edit":
            self.state["draft"] = False
        return self.release(tag)


class PublishingTests(unittest.TestCase):
    def setUp(self):
        self.paths = [Path(n) for n in NAMES]

    def publish(self, github, dry=False):
        with patch.object(release, "ensure_tag") as ensure:
            release.publish(github, TAG, COMMIT, TREE, self.paths, dry)
            return ensure

    def test_dry_run_never_writes(self):
        github = FakeGitHub(assets=NAMES[:1])
        ensure = self.publish(github, True)
        ensure.assert_not_called()
        self.assertEqual([e[0] for e in github.events], ["verify"])

    def test_new_draft_preflight_never_writes(self):
        github = FakeGitHub(tree=None)
        github.state = None
        ensure = self.publish(github, True)
        ensure.assert_not_called()
        self.assertEqual(github.events, [])

    def test_tag_conflict_fails_before_writes(self):
        github = FakeGitHub(tree="c" * 40)
        with self.assertRaisesRegex(release.ReleaseError, "different source"):
            self.publish(github)
        self.assertEqual(github.events, [])

    def test_existing_release_without_source_tag_fails(self):
        github = FakeGitHub(assets=NAMES, tree=None)
        with self.assertRaisesRegex(release.ReleaseError, "no source tag"):
            self.publish(github)
        self.assertEqual(github.events, [])

    def test_first_release_uploads_and_verifies_before_publish(self):
        github = FakeGitHub(tree=None)
        github.state = None
        # The source tag exists after ensure_tag; no actual network writes in tests.
        with tempfile.TemporaryDirectory() as directory:
            with patch.object(release, "ROOT", Path(directory)):
                with patch.object(github, "tag_tree", side_effect=[None, TREE]):
                    self.publish(github)
        self.assertEqual([e[0] for e in github.events], ["create", "upload", "upload", "upload", "verify", "edit"])
        self.assertFalse(github.state["draft"])

    def test_tag_changed_during_upload_is_not_published(self):
        github = FakeGitHub(assets=NAMES)
        with patch.object(github, "tag_tree", side_effect=[TREE, "c" * 40]):
            with self.assertRaisesRegex(release.ReleaseError, "changed"):
                self.publish(github)
        self.assertFalse(any(e[0] == "edit" for e in github.events))

    def test_published_release_is_read_only(self):
        github = FakeGitHub(draft=False, assets=NAMES)
        ensure = self.publish(github)
        ensure.assert_not_called()
        self.assertEqual([e[0] for e in github.events], ["verify"])

    def test_incomplete_published_release_is_not_modified(self):
        github = FakeGitHub(draft=False, assets=NAMES[:1])
        with self.assertRaisesRegex(release.ReleaseError, "incomplete"):
            self.publish(github)
        self.assertEqual([e[0] for e in github.events], ["verify"])

    def test_partial_draft_resumes_without_replacing_assets(self):
        github = FakeGitHub(assets=NAMES[:1])
        self.publish(github)
        self.assertEqual([e[0] for e in github.events], ["verify", "upload", "upload", "verify", "edit"])
        self.assertFalse(github.state["draft"])

    def test_corrupted_existing_asset_is_not_replaced(self):
        github = FakeGitHub(assets=NAMES[:1])
        github.bad_download = True
        with self.assertRaisesRegex(release.ReleaseError, "checksum"):
            self.publish(github)
        self.assertFalse(any(e[0] in {"upload", "edit"} for e in github.events))

    def test_unconfirmed_upload_never_publishes(self):
        github = FakeGitHub()
        github.confirm_upload = False
        with self.assertRaisesRegex(release.ReleaseError, "not confirmed"):
            self.publish(github)
        self.assertFalse(any(e[0] == "edit" for e in github.events))

    def test_foreign_draft_is_not_modified(self):
        github = FakeGitHub()
        github.state["body"] = "someone else's draft"
        with self.assertRaisesRegex(release.ReleaseError, "another source"):
            self.publish(github)
        self.assertEqual(github.events, [])

    def test_uncertain_write_reads_without_retry(self):
        github = release.GitHub({})
        with patch.object(github, "gh", side_effect=subprocess.TimeoutExpired("gh", 180)) as write:
            with patch.object(github, "release", return_value={"draft": True}) as read:
                self.assertEqual(github.write_and_read(["release", "create"], TAG), {"draft": True})
        write.assert_called_once()
        read.assert_called_once_with(TAG)

    def test_draft_lookup_falls_back_to_authenticated_list(self):
        github = release.GitHub({})
        draft = {"tag_name": TAG, "draft": True, "id": 123}
        with patch.object(github, "api", side_effect=[None, [draft]]) as api:
            self.assertEqual(github.release(TAG), draft)
        self.assertEqual(api.call_args_list[-1].args[0], 'releases?per_page=100&page=1')

    def test_missing_release_ends_at_last_list_page(self):
        github = release.GitHub({})
        with patch.object(github, "api", side_effect=[None, []]) as api:
            self.assertIsNone(github.release(TAG))
        self.assertEqual(api.call_count, 2)

    def test_draft_lookup_paginates(self):
        github = release.GitHub({})
        draft = {"tag_name": TAG, "draft": True}
        with patch.object(github, "api", side_effect=[None, [{"tag_name": "other"}] * 100, [draft]]):
            self.assertEqual(github.release(TAG), draft)

    def test_unprotected_ci_fails_before_credentials_or_network(self):
        with patch.dict(release.os.environ, {"CI": "true", "CI_COMMIT_REF_PROTECTED": "false"}, clear=True):
            with patch.object(release.sys, "argv", ["publish-release.py", "--tag", TAG]):
                with self.assertRaisesRegex(release.ReleaseError, "protected tag"):
                    release.main()

    def test_snapshot_has_no_private_history_parent(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            def git(*args):
                return subprocess.check_output(["git", *args], cwd=root, text=True).strip()
            git("init", "-q")
            git("config", "user.name", "Local test")
            git("config", "user.email", "local@example.invalid")
            (root / "source").write_text("first version")
            git("add", "source")
            git("commit", "-qm", "private historical commit")
            (root / "source").write_text("public version")
            git("commit", "-qam", "release source")
            tree, commit = git("rev-parse", "HEAD^{tree}"), git("rev-parse", "HEAD")
            github = release.GitHub(dict(release.os.environ))
            pushes = []
            real_run = release.run
            def intercept(args, **kwargs):
                if "push" in args:
                    pushes.append(args[-1].split(":")[0])
                    return ""
                return real_run(args, **kwargs)
            with patch.object(release, "ROOT", root), patch.object(release, "run", side_effect=intercept):
                with patch.object(github, "tag_tree", side_effect=[None, tree]):
                    release.ensure_tag(github, TAG, commit, tree)
            self.assertEqual(len(pushes), 1)
            self.assertEqual(git("rev-parse", pushes[0] + "^{tree}"), tree)
            self.assertNotIn("\nparent ", git("cat-file", "-p", pushes[0]))


class PackageTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.directory = self.root / "dist" / TAG
        self.directory.mkdir(parents=True)
        self.root_patch = patch.object(release, "ROOT", self.root)
        self.root_patch.start()
        self.addCleanup(self.root_patch.stop)
        (self.directory / "SOURCE_COMMIT").write_text(COMMIT + "\n")
        for name in NAMES[:2]:
            magic = b"\x7fELF" if "linux" in name else b"\xcf\xfa\xed\xfe"
            self.archive(name, magic)
        self.checksums()

    def archive(self, name, binary, extra=None):
        with tarfile.open(self.directory / name, "w:gz") as archive:
            for entry, data in [("obtop", binary), ("README.md", b"readme")]:
                member = tarfile.TarInfo(entry)
                member.size, member.mode = len(data), 0o755 if entry == "obtop" else 0o644
                archive.addfile(member, io.BytesIO(data))
            if extra:
                archive.addfile(tarfile.TarInfo(extra))

    def checksums(self):
        (self.directory / "SHA256SUMS").write_text("".join(
            f"{release.digest(self.directory / name)}  ./{name}\n" for name in NAMES[:2]))

    def test_valid_packages(self):
        paths = release.validate_packages(TAG, COMMIT)
        self.assertEqual([p.name for p in paths], NAMES)

    def test_wrong_source_commit(self):
        with self.assertRaisesRegex(release.ReleaseError, "different source"):
            release.validate_packages(TAG, "d" * 40)

    def test_tampered_archive(self):
        (self.directory / NAMES[0]).write_bytes(b"tampered")
        with self.assertRaisesRegex(release.ReleaseError, "checksum mismatch"):
            release.validate_packages(TAG, COMMIT)

    def test_path_traversal_manifest(self):
        (self.directory / "SHA256SUMS").write_text("a" * 64 + "  ../outside\n")
        with self.assertRaisesRegex(release.ReleaseError, "invalid"):
            release.validate_packages(TAG, COMMIT)

    def test_duplicate_checksum(self):
        path = self.directory / "SHA256SUMS"
        path.write_text(path.read_text() + path.read_text().splitlines()[0] + "\n")
        with self.assertRaisesRegex(release.ReleaseError, "duplicate"):
            release.validate_packages(TAG, COMMIT)

    def test_wrong_platform(self):
        self.archive(NAMES[0], b"\xcf\xfa\xed\xfe")
        self.checksums()
        with self.assertRaisesRegex(release.ReleaseError, "wrong binary"):
            release.validate_packages(TAG, COMMIT)

    def test_archive_path_traversal(self):
        self.archive(NAMES[0], b"\x7fELF", extra="../outside")
        self.checksums()
        with self.assertRaisesRegex(release.ReleaseError, "unexpected archive"):
            release.validate_packages(TAG, COMMIT)

    def test_symlink_package(self):
        path = self.directory / NAMES[0]
        path.unlink()
        path.symlink_to(self.directory / NAMES[1])
        with self.assertRaisesRegex(release.ReleaseError, "invalid artifact"):
            release.validate_packages(TAG, COMMIT)


if __name__ == "__main__":
    unittest.main()

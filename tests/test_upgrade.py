import base64
import fcntl
import hashlib
import json
import os
import platform
import pty
import shutil
import socket
import subprocess
import tarfile
import tempfile
import unittest
from http.server import BaseHTTPRequestHandler
from pathlib import Path

from test_install import FAKE_CURL, FAKE_UNAME, GO_CACHES
from test_superfind import SUPERFIND, serve

REPO = Path(__file__).resolve().parent.parent
TESTDATA = REPO / "internal" / "release" / "testdata"
MACHINES = {"x86_64": "amd64", "amd64": "amd64", "arm64": "arm64", "aarch64": "arm64"}
PLATFORM = f"{platform.system().lower()}_{MACHINES[platform.machine()]}"
OLD, NEW = "0.12.0", "0.13.0"
KEY_ID = "F8D8C9C1F5F2C355"
BUILT = {}
ARCHIVES = {}


def setUpModule():
    if not shutil.which("go"):
        raise unittest.SkipTest("the upgrade tests build superfind with Go")
    folder = Path(tempfile.mkdtemp(prefix="superfind-upgrade-")).resolve()
    BUILT["folder"] = folder
    env = {**os.environ, **GO_CACHES}
    for version in (OLD, NEW):
        subprocess.run(["go", "build", "-tags", "testkeys", "-buildvcs=false", "-ldflags", f"-X main.version={version}",
                        "-o", str(folder / f"superfind-{version}"), "."], cwd=REPO, env=env, check=True)
    subprocess.run(["go", "build", "-o", str(folder / "sign"), "./internal/release/cmd/sign"], cwd=REPO, env=env, check=True)


def tearDownModule():
    if "folder" in BUILT:
        shutil.rmtree(BUILT["folder"], ignore_errors=True)


def built(version):
    return BUILT["folder"] / f"superfind-{version}"


def closed_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def archive(version, binary=None, prints=None):
    key = (version, str(binary), prints)
    if key not in ARCHIVES:
        work = Path(tempfile.mkdtemp())
        source = Path(binary) if binary else work / "superfind"
        if not binary:
            source.write_text(f"#!/bin/sh\necho 'superfind {prints or version}'\n")
            source.chmod(0o755)
        with tarfile.open(work / "archive.tar.gz", "w:gz", compresslevel=1) as tar:
            tar.add(source, arcname="superfind")
            tar.add(REPO / "LICENSE", arcname="LICENSE")
        ARCHIVES[key] = (work / "archive.tar.gz").read_bytes()
        shutil.rmtree(work, ignore_errors=True)
    return ARCHIVES[key]


class Releases:
    def __init__(self, test):
        self.dir = test.root / "releases"
        self.dir.mkdir()
        self.latest = None
        self.requests = []
        releases = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_HEAD(self):
                releases.requests.append(f"HEAD {self.path}")
                if self.path == "/latest" and releases.latest:
                    self.send_response(302)
                    self.send_header("Location", f"/tag/v{releases.latest}")
                else:
                    self.send_response(404)
                self.end_headers()

            def do_GET(self):
                releases.requests.append(f"GET {self.path}")
                asset = releases.dir / self.path.removeprefix("/download/")
                if not self.path.startswith("/download/") or not asset.is_file():
                    self.send_response(404)
                    self.end_headers()
                    return
                body = asset.read_bytes()
                self.send_response(200)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

        self.url = serve(test, Handler)

    def publish(self, version, binary=None, prints=None, signed_as=None, minisig=True):
        folder = self.dir / f"v{version}"
        folder.mkdir()
        name = f"superfind_{version}_{PLATFORM}.tar.gz"
        (folder / name).write_bytes(archive(version, binary, prints))
        (folder / "checksums.txt").write_text(f"{hashlib.sha256((folder / name).read_bytes()).hexdigest()}  {name}\n")
        env = {"SUPERFIND_SIGNING_KEY": base64.b64encode((TESTDATA / "test.key").read_bytes()).decode(), "PATH": os.environ["PATH"]}
        subprocess.run([str(BUILT["folder"] / "sign"), "-pubkey", str(TESTDATA / "test.pub"), signed_as or version, str(folder)],
                       env=env, check=True, capture_output=True)
        if not minisig:
            (folder / "checksums.txt.minisig").unlink()
        return folder


class Upgrade(unittest.TestCase):
    def setUp(self):
        self.root = Path(tempfile.mkdtemp()).resolve()
        self.addCleanup(shutil.rmtree, self.root, ignore_errors=True)
        self.home = self.root / "home"
        self.home.mkdir()
        self.bin = self.home / ".local" / "bin"
        self.state = self.home / ".local" / "state" / "superfind"
        self.releases = Releases(self)

    def install(self, version):
        self.bin.mkdir(parents=True, exist_ok=True)
        shutil.copy(built(version), self.bin / "superfind")
        self.state.mkdir(parents=True, exist_ok=True)
        (self.state / "install.json").write_text(json.dumps({"method": "script", "path": str(self.bin / "superfind")}) + "\n")

    def env(self, url=None, **extra):
        return {"HOME": str(self.home), "PATH": os.environ["PATH"], "XDG_CACHE_HOME": str(self.home / ".cache"),
                "SUPERFIND_RELEASES_URL": url or self.releases.url, "SUPERFIND_TELEMETRY": "off", "SUPERFIND_CHECKIN_URL": "http://127.0.0.1:1/v1/checkin", **extra}

    def upgrade(self, *args, binary=None, url=None, **extra):
        return subprocess.run([str(binary or self.bin / "superfind"), "upgrade", *args], env=self.env(url, **extra),
                              capture_output=True, text=True)

    def version(self, path=None):
        return subprocess.run([str(path or self.bin / "superfind"), "--version"], capture_output=True, text=True).stdout.strip()

    def tree(self):
        return sorted(str(p.relative_to(self.home)) for p in self.home.rglob("*"))

    def queued(self):
        shown = subprocess.run([str(self.bin / "superfind"), "telemetry", "--json"], env=self.env(SUPERFIND_TELEMETRY=""),
                               capture_output=True, text=True, check=True)
        return [(e["name"], e["props"]) for e in json.loads(shown.stdout)["events"]]

    def publish_new(self):
        self.releases.publish(NEW, binary=built(NEW))
        self.releases.latest = NEW

    def test_a_curl_install_upgrades_in_place_and_keeps_the_copy_it_replaced(self):
        self.releases.publish(OLD, binary=built(OLD))
        stubs = self.root / "stubs"
        stubs.mkdir()
        for name, body in (("curl", FAKE_CURL), ("uname", FAKE_UNAME)):
            (stubs / name).write_text(body)
            (stubs / name).chmod(0o755)
        env = {"HOME": str(self.home), "PATH": f"{stubs}:/usr/bin:/bin", "SHELL": "", "RELEASES_DIR": str(self.releases.dir),
               "LATEST_TAG": f"v{OLD}", "FAKE_SYSTEM": platform.system(), "FAKE_MACHINE": platform.machine()}
        script = self.root / "download" / "install.sh"
        script.parent.mkdir()
        shutil.copy(REPO / "install.sh", script)
        result = subprocess.run(["sh", str(script)], cwd=self.root, env=env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.version(), f"superfind {OLD}")
        old = (self.bin / "superfind").read_bytes()
        receipt = (self.state / "install.json").read_text()

        self.publish_new()
        result = self.upgrade(SUPERFIND_TELEMETRY="")
        self.assertEqual((result.returncode, result.stderr), (0, ""))
        self.assertEqual(result.stdout, f"installed superfind {NEW} in place of {OLD} at {self.bin}/superfind\n"
                         f"the release is signed with release key {KEY_ID} and matches its checksums.txt; "
                         f"superfind upgrade --rollback goes back to {OLD}\n")
        self.assertEqual(self.version(), f"superfind {NEW}")
        self.assertEqual((self.bin / "superfind").read_bytes(), built(NEW).read_bytes())
        self.assertEqual((self.state / "previous" / "superfind").read_bytes(), old)
        self.assertEqual((self.state / "install.json").read_text(), receipt)
        self.assertEqual(sorted(p.name for p in self.bin.iterdir()), ["superfind"])
        self.assertEqual(self.releases.requests[-4:], ["HEAD /latest", f"GET /download/v{NEW}/checksums.txt",
                                                       f"GET /download/v{NEW}/checksums.txt.minisig",
                                                       f"GET /download/v{NEW}/superfind_{NEW}_{PLATFORM}.tar.gz"])
        self.assertEqual(self.queued(), [("upgraded", {"method": "script", "outcome": "installed"})])

    def test_rollback_swaps_with_the_kept_copy_and_a_second_rollback_swaps_back(self):
        self.install(OLD)
        self.publish_new()
        self.assertEqual(self.upgrade().returncode, 0)
        result = self.upgrade("--rollback")
        self.assertEqual((result.returncode, result.stderr), (0, ""))
        self.assertEqual(result.stdout, f"replaced superfind {NEW} with {OLD} at {self.bin}/superfind; "
                         f"superfind upgrade --rollback goes back to {NEW}\n")
        self.assertEqual(self.version(), f"superfind {OLD}")
        self.assertEqual(self.version(self.state / "previous" / "superfind"), f"superfind {NEW}")
        self.assertEqual(sorted(p.name for p in (self.state / "skip").iterdir()), [NEW])
        result = self.upgrade("--rollback")
        self.assertEqual((result.returncode, result.stderr), (0, ""))
        self.assertEqual(result.stdout, f"replaced superfind {OLD} with {NEW} at {self.bin}/superfind; "
                         f"superfind upgrade --rollback goes back to {OLD}\n")
        self.assertEqual(self.version(), f"superfind {NEW}")
        self.assertEqual(self.version(self.state / "previous" / "superfind"), f"superfind {OLD}")
        self.assertEqual(sorted(p.name for p in self.bin.iterdir()), ["superfind"])
        self.assertEqual(sorted(p.name for p in (self.state / "previous").iterdir()), ["superfind"])

    def test_with_nothing_kept_rollback_says_so_offline_and_exits_0(self):
        self.install(OLD)
        result = self.upgrade("--rollback", url=f"http://127.0.0.1:{closed_port()}")
        self.assertEqual((result.returncode, result.stderr), (0, ""))
        self.assertEqual(result.stdout, "nothing to roll back to: superfind upgrade keeps the copy it replaces, and it hasn't replaced one here\n")

    def test_check_says_up_to_date_or_what_is_out_and_changes_nothing(self):
        self.install(OLD)
        self.releases.latest = OLD
        before = self.tree()
        result = self.upgrade("--check")
        self.assertEqual((result.returncode, result.stdout, result.stderr), (0, f"superfind {OLD} is up to date\n", ""))
        self.releases.latest = NEW
        result = self.upgrade("--check")
        self.assertEqual((result.returncode, result.stdout, result.stderr),
                         (0, f"superfind {NEW} is out; you have {OLD}. superfind upgrade installs it\n", ""))
        self.assertEqual(self.tree(), before)
        self.assertEqual(self.releases.requests, ["HEAD /latest", "HEAD /latest"])

    def test_version_pins_a_release_that_is_not_the_newest(self):
        self.install(OLD)
        self.releases.publish("0.12.1")
        self.publish_new()
        result = self.upgrade("--version=0.12.1")
        self.assertEqual((result.returncode, result.stderr), (0, ""))
        self.assertTrue(result.stdout.startswith(f"installed superfind 0.12.1 in place of {OLD} at {self.bin}/superfind\n"), result.stdout)
        self.assertEqual(self.version(), "superfind 0.12.1")
        self.assertEqual(self.version(self.state / "previous" / "superfind"), f"superfind {OLD}")
        self.assertFalse((self.state / "skip").exists())
        self.assertNotIn("HEAD /latest", self.releases.requests)

    def test_version_goes_back_to_an_older_release_and_marks_the_one_left(self):
        self.install(NEW)
        self.releases.publish("0.12.1")
        self.releases.latest = NEW
        result = self.upgrade("--version", "0.12.1")
        self.assertEqual((result.returncode, result.stderr), (0, ""))
        self.assertTrue(result.stdout.startswith(f"installed superfind 0.12.1 in place of {NEW} at {self.bin}/superfind\n"), result.stdout)
        self.assertEqual(self.version(), "superfind 0.12.1")
        self.assertEqual(self.version(self.state / "previous" / "superfind"), f"superfind {NEW}")
        self.assertEqual(sorted(p.name for p in (self.state / "skip").iterdir()), [NEW])

    def test_a_release_before_signing_started_is_refused_with_the_curl_line(self):
        self.install(OLD)
        result = self.upgrade("--version", "0.11.0")
        self.assertEqual((result.returncode, result.stdout), (1, ""))
        self.assertEqual(result.stderr, "superfind upgrade: 0.11.0 came out before releases were signed, so superfind upgrade won't install it. "
                         "To install it anyway:\n  curl -fsSL https://superfind.dev/install | SUPERFIND_VERSION=0.11.0 sh\n")
        self.assertEqual(self.releases.requests, [])
        self.assertEqual(self.version(), f"superfind {OLD}")

    def test_the_same_version_is_already_installed(self):
        self.install(OLD)
        self.releases.latest = OLD
        result = self.upgrade()
        self.assertEqual((result.returncode, result.stdout, result.stderr), (0, f"superfind {OLD} is already installed\n", ""))
        self.assertFalse((self.state / "previous").exists())

    def test_each_failed_check_is_named_and_leaves_the_installed_copy_byte_identical(self):
        self.install(OLD)
        installed = (self.bin / "superfind").read_bytes()
        name = f"superfind_{NEW}_{PLATFORM}.tar.gz"

        def tamper_archive(folder):
            (folder / name).write_bytes((folder / name).read_bytes() + b"\0")

        def tamper_signature(folder):
            lines = (folder / "checksums.txt.minisig").read_text().splitlines()
            lines[1] = lines[1][:40] + ("A" if lines[1][40] != "A" else "B") + lines[1][41:]
            (folder / "checksums.txt.minisig").write_text("\n".join(lines) + "\n")

        for reason, publish, tamper, want in (
            ("a tampered archive", {}, tamper_archive, f"superfind upgrade: {name}: sha256 is "),
            ("a bad signature", {}, tamper_signature, "superfind upgrade: checksums.txt.minisig does not match checksums.txt; nothing was installed\n"),
            ("a signature for another version", {"signed_as": OLD}, None,
             f'superfind upgrade: checksums.txt.minisig is signed for "superfind v{OLD} checksums.txt", not "superfind v{NEW} checksums.txt"; '
             "nothing was installed\n"),
            ("a missing signature", {"minisig": False}, None,
             f"superfind upgrade: superfind {NEW} has no checksums.txt.minisig, so its signature can't be checked; nothing was installed\n"),
            ("a superfind that lies about its version", {"prints": "0.12.5"}, None,
             f"superfind upgrade: the downloaded superfind says it is 0.12.5, not {NEW}; nothing was installed\n"),
        ):
            with self.subTest(reason):
                shutil.rmtree(self.releases.dir / f"v{NEW}", ignore_errors=True)
                folder = self.releases.publish(NEW, **publish)
                self.releases.latest = NEW
                if tamper:
                    tamper(folder)
                result = self.upgrade(SUPERFIND_TELEMETRY="")
                self.assertEqual((result.returncode, result.stdout), (1, ""))
                self.assertTrue(result.stderr.startswith(want), result.stderr)
                self.assertEqual((self.bin / "superfind").read_bytes(), installed)
                self.assertEqual(sorted(p.name for p in self.bin.iterdir()), ["superfind"])
                self.assertFalse((self.state / "previous").exists())
                self.assertEqual(self.queued(), [("upgraded", {"method": "script", "outcome": "failed"})])
                shutil.rmtree(self.state / "events")

    def test_a_homebrew_copy_is_left_alone_with_the_brew_command(self):
        cask = self.home / "Caskroom" / "superfind" / OLD
        cask.mkdir(parents=True)
        shutil.copy(built(OLD), cask / "superfind")
        (self.home / "bin").mkdir()
        os.symlink(cask / "superfind", self.home / "bin" / "superfind")
        offline = f"http://127.0.0.1:{closed_port()}"
        result = self.upgrade(binary=self.home / "bin" / "superfind", url=offline)
        self.assertEqual((result.returncode, result.stderr), (0, ""))
        self.assertEqual(result.stdout, "Homebrew installed this copy, so superfind left it alone. To upgrade:\n"
                         "  brew upgrade --cask adammiribyan/tap/superfind\n")
        result = self.upgrade("--rollback", binary=self.home / "bin" / "superfind", url=offline)
        self.assertEqual((result.returncode, result.stdout), (0, "Homebrew installed this copy, so superfind upgrade never replaced it, "
                                                                 "and there's nothing to roll back\n"))
        self.releases.latest = NEW
        result = self.upgrade("--check", binary=self.home / "bin" / "superfind")
        self.assertEqual((result.returncode, result.stdout), (0, f"superfind {NEW} is out; you have {OLD}. Homebrew installed this copy, "
                                                                 "so install it with: brew upgrade --cask adammiribyan/tap/superfind\n"))
        self.assertEqual((cask / "superfind").read_bytes(), built(OLD).read_bytes())

    def test_a_copy_the_receipt_does_not_name_is_left_alone_with_the_curl_command(self):
        self.install(OLD)
        elsewhere = self.home / "opt"
        elsewhere.mkdir()
        shutil.copy(built(OLD), elsewhere / "superfind")
        result = self.upgrade(binary=elsewhere / "superfind", url=f"http://127.0.0.1:{closed_port()}")
        self.assertEqual((result.returncode, result.stderr), (0, ""))
        self.assertEqual(result.stdout, f"superfind doesn't know what installed this copy at {elsewhere}/superfind, so superfind left it alone. "
                         "To upgrade in ~/.local/bin:\n  curl -fsSL https://superfind.dev/install | sh\n")
        result = self.upgrade("--version", NEW, binary=elsewhere / "superfind", url=f"http://127.0.0.1:{closed_port()}")
        self.assertEqual((result.returncode, result.stdout), (0, f"superfind doesn't know what installed this copy at {elsewhere}/superfind, "
                                                                 f"so superfind left it alone. To install {NEW} in ~/.local/bin:\n"
                                                                 f"  curl -fsSL https://superfind.dev/install | SUPERFIND_VERSION={NEW} sh\n"))
        self.assertEqual(self.version(elsewhere / "superfind"), f"superfind {OLD}")
        self.assertFalse((self.state / "previous").exists())

    def test_a_second_upgrade_at_once_is_refused(self):
        self.install(OLD)
        self.publish_new()
        with open(self.state / "upgrade.lock", "w") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)
            result = self.upgrade()
        self.assertEqual((result.returncode, result.stdout, result.stderr), (1, "", "superfind upgrade: another superfind upgrade is running\n"))
        self.assertEqual(self.releases.requests, [])
        self.assertEqual(self.version(), f"superfind {OLD}")

    def test_an_unwritable_bin_folder_is_named_before_any_download(self):
        self.install(OLD)
        self.publish_new()
        self.bin.chmod(0o500)
        self.addCleanup(self.bin.chmod, 0o755)
        result = self.upgrade()
        self.assertEqual((result.returncode, result.stdout), (1, ""))
        self.assertEqual(result.stderr, f"superfind upgrade: can't write to {self.bin} (permission denied), so superfind was left as it is. "
                         "To install the newest release in ~/.local/bin:\n  curl -fsSL https://superfind.dev/install | sh\n")
        self.assertEqual(self.releases.requests, ["HEAD /latest"])

    def test_an_unwritable_state_folder_is_named_before_any_request(self):
        self.install(OLD)
        self.publish_new()
        (self.state / "upgrade.lock").touch()
        (self.state / "upgrade.lock").chmod(0o400)
        result = self.upgrade()
        self.assertEqual((result.returncode, result.stdout), (1, ""))
        self.assertEqual(result.stderr, f"superfind upgrade: can't write to {self.state} (permission denied), so superfind was left as it is. "
                         "To install the newest release in ~/.local/bin:\n  curl -fsSL https://superfind.dev/install | sh\n")
        self.assertEqual(self.releases.requests, [])

    def test_offline_names_the_address_it_could_not_reach(self):
        self.install(OLD)
        port = closed_port()
        result = self.upgrade(url=f"http://127.0.0.1:{port}")
        self.assertEqual((result.returncode, result.stdout), (1, ""))
        self.assertTrue(result.stderr.startswith(f"superfind upgrade: couldn't reach http://127.0.0.1:{port}/latest: "), result.stderr)
        self.assertEqual(self.version(), f"superfind {OLD}")

    def test_stale_staged_files_of_dead_upgrades_are_swept_and_live_ones_kept(self):
        self.install(OLD)
        self.publish_new()
        (self.bin / ".superfind.upgrade.2147483646").write_bytes(b"left by a crash")
        mine = self.bin / f".superfind.upgrade.{os.getpid()}"
        mine.write_bytes(b"another upgrade, still running")
        self.assertEqual(self.upgrade().returncode, 0)
        self.assertEqual(sorted(p.name for p in self.bin.iterdir()), [mine.name, "superfind"])

    def test_progress_goes_to_stderr_only_at_a_terminal(self):
        self.install(OLD)
        self.publish_new()
        master, slave = pty.openpty()
        proc = subprocess.Popen([str(self.bin / "superfind"), "upgrade"], env=self.env(), stdout=subprocess.PIPE, stderr=slave, text=True)
        os.close(slave)
        progress = b""
        while True:
            try:
                chunk = os.read(master, 4096)
            except OSError:
                break
            if not chunk:
                break
            progress += chunk
        os.close(master)
        stdout, _ = proc.communicate()
        self.assertEqual(proc.returncode, 0)
        self.assertTrue(stdout.startswith(f"installed superfind {NEW} in place of {OLD}"), stdout)
        self.assertEqual(progress.decode().replace("\r\n", "\n"), "checking the newest release\n"
                         f"downloading superfind {NEW} for {PLATFORM.replace('_', ' ')}\nchecking that the new superfind runs\n")

    def test_check_rollback_and_version_are_separate_options(self):
        self.install(OLD)
        offline = f"http://127.0.0.1:{closed_port()}"
        for args in (("--check", "--rollback"), ("--check", "--version", NEW), ("--rollback", "--version", NEW),
                     ("--version",), ("--version", "latest"), ("--force",)):
            result = self.upgrade(*args, url=offline)
            self.assertEqual((result.returncode, result.stdout), (2, ""), args)
            self.assertTrue(result.stderr.startswith("superfind upgrade: "), args)
            self.assertTrue(result.stderr.endswith("See superfind help upgrade.\n"), args)
        self.assertEqual(self.version(), f"superfind {OLD}")

    def test_a_release_build_downloads_from_github_whatever_superfind_releases_url_says(self):
        self.install(OLD)
        proxy = f"http://127.0.0.1:{closed_port()}"
        result = self.upgrade("--check", binary=SUPERFIND, HTTPS_PROXY=proxy, HTTP_PROXY=proxy)
        self.assertEqual((result.returncode, result.stdout), (1, ""))
        self.assertIn("superfind upgrade: couldn't reach https://github.com/adammiribyan/superfind/releases/latest: ", result.stderr)
        self.assertEqual(self.releases.requests, [])


if __name__ == "__main__":
    unittest.main()

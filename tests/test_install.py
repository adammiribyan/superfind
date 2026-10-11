import hashlib
import os
import platform
import shutil
import subprocess
import tarfile
import tempfile
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
GO_CACHES = {name: subprocess.run(["go", "env", name], capture_output=True, text=True).stdout.strip()
             for name in ("GOCACHE", "GOMODCACHE", "GOTOOLCHAIN")} if shutil.which("go") else {}


@unittest.skipUnless(shutil.which("go"), "the installer builds superfind with Go")
class Installer(unittest.TestCase):
    def setUp(self):
        self.home = Path(tempfile.mkdtemp()).resolve()
        self.addCleanup(shutil.rmtree, self.home, ignore_errors=True)
        self.clone = self.home / "src" / "superfind"
        shutil.copytree(REPO, self.clone, ignore=shutil.ignore_patterns(".git", "bin", "dist", "__pycache__", ".env", "demo"))

    def install(self, shell):
        env = {"HOME": str(self.home), "PATH": os.environ["PATH"], "SHELL": f"/bin/{shell}", **GO_CACHES}
        return subprocess.run(["sh", str(self.clone / "install.sh")], env=env, capture_output=True, text=True)

    def startup_file(self, shell):
        if shell == "zsh":
            return self.home / ".zshrc"
        return self.home / (".bash_profile" if platform.system() == "Darwin" else ".bashrc")

    def test_installing_for_zsh_adds_one_setup_line_and_a_second_run_changes_nothing(self):
        self.assertEqual(self.install("zsh").returncode, 0)
        first = self.startup_file("zsh").read_text()
        self.assertEqual(first, '\neval "$("$HOME/.local/bin/superfind" init zsh)"\n')
        self.assertEqual(self.install("zsh").returncode, 0)
        self.assertEqual(self.startup_file("zsh").read_text(), first)

    def test_in_a_clone_it_installs_its_own_build_of_that_clone(self):
        self.assertEqual(self.install("zsh").returncode, 0)
        installed = (self.home / ".local" / "bin" / "superfind").read_bytes()
        self.assertEqual(installed, (self.clone / "bin" / "superfind").read_bytes())

    def test_installing_for_bash_sets_up_bash(self):
        self.assertEqual(self.install("bash").returncode, 0)
        self.assertEqual(self.startup_file("bash").read_text(), '\neval "$("$HOME/.local/bin/superfind" init bash)"\n')

    def test_installing_for_fish_creates_its_config_and_a_second_run_changes_nothing(self):
        self.assertEqual(self.install("fish").returncode, 0)
        config = self.home / ".config" / "fish" / "config.fish"
        self.assertEqual(config.read_text(), '\n"$HOME/.local/bin/superfind" init fish | source\n')
        self.assertEqual(self.install("fish").returncode, 0)
        self.assertEqual(config.read_text(), '\n"$HOME/.local/bin/superfind" init fish | source\n')

    def test_a_failed_copy_leaves_the_installed_superfind_working(self):
        self.assertEqual(self.install("zsh").returncode, 0)
        stub = self.home / "stub"
        stub.mkdir()
        (stub / "cp").write_text('#!/bin/sh\n: > "$2"\nexit 1\n')
        (stub / "cp").chmod(0o755)
        env = {"HOME": str(self.home), "PATH": f"{stub}:{os.environ['PATH']}", "SHELL": "/bin/zsh", **GO_CACHES}
        self.assertNotEqual(subprocess.run(["sh", str(self.clone / "install.sh")], env=env, capture_output=True).returncode, 0)
        installed = self.home / ".local" / "bin"
        self.assertEqual(sorted(p.name for p in installed.iterdir()), ["superfind"])
        result = subprocess.run([str(installed / "superfind"), "--version"], capture_output=True, text=True)
        self.assertEqual((result.returncode, result.stdout.startswith("superfind ")), (0, True))

    def test_superfind_keeps_working_after_the_clone_is_deleted(self):
        self.install("zsh")
        shutil.rmtree(self.clone)
        result = subprocess.run([str(self.home / ".local" / "bin" / "superfind"), "--version"], capture_output=True, text=True)
        self.assertEqual((result.returncode, result.stdout.startswith("superfind ")), (0, True))

    @unittest.skipIf(Path("/Applications/Ghostty.app").exists() or shutil.which("ghostty"),
                     "Ghostty is installed on this machine")
    def test_no_ghostty_config_is_created_without_ghostty(self):
        self.install("zsh")
        self.assertFalse((self.home / ".config" / "ghostty").exists())
        self.assertFalse((self.home / "Library" / "Application Support" / "com.mitchellh.ghostty").exists())


RELEASES = "https://github.com/adammiribyan/superfind/releases"

FAKE_CURL = """#!/bin/sh
out="" url=""
while [ $# -gt 0 ]; do
  case $1 in
    -o) out=$2; shift ;;
    -w | --proto | --retry) shift ;;
    -*) ;;
    *) url=$1 ;;
  esac
  shift
done
case $url in
  {releases}/latest) printf '{releases}/tag/%s' "$LATEST_TAG" ;;
  {releases}/download/*) cp "$RELEASES_DIR/${{url#{releases}/download/}}" "$out" 2>/dev/null || exit 22 ;;
  *) exit 6 ;;
esac
""".format(releases=RELEASES)

FAKE_UNAME = """#!/bin/sh
case ${1:-} in
  -m) echo "$FAKE_MACHINE" ;;
  *) echo "$FAKE_SYSTEM" ;;
esac
"""


class ReleaseInstaller(unittest.TestCase):
    def setUp(self):
        self.root = Path(tempfile.mkdtemp()).resolve()
        self.addCleanup(shutil.rmtree, self.root, ignore_errors=True)
        self.home = self.root / "home"
        self.home.mkdir()
        self.releases = self.root / "releases"
        self.script = self.root / "download" / "install.sh"
        self.script.parent.mkdir()
        shutil.copy(REPO / "install.sh", self.script)
        self.stubs = self.root / "stubs"
        self.stubs.mkdir()
        for name, body in (("curl", FAKE_CURL), ("uname", FAKE_UNAME)):
            (self.stubs / name).write_text(body)
            (self.stubs / name).chmod(0o755)
        self.publish("0.10.0")

    def publish(self, version, platform="linux_amd64", checksum=None, runs=True):
        folder = self.releases / f"v{version}"
        folder.mkdir(parents=True)
        binary = self.root / "superfind"
        binary.write_text(f"#!/bin/sh\necho 'superfind {version}'\n" if runs else "#!/bin/sh\nexit 1\n")
        binary.chmod(0o755)
        archive = folder / f"superfind_{version}_{platform}.tar.gz"
        with tarfile.open(archive, "w:gz") as tar:
            tar.add(binary, arcname="superfind")
            tar.add(REPO / "LICENSE", arcname="LICENSE")
        digest = checksum or hashlib.sha256(archive.read_bytes()).hexdigest()
        (folder / "checksums.txt").write_text(f"{digest}  {archive.name}\n")

    def install(self, *, stdin=False, cwd=None, machine="x86_64", **env):
        env = {"HOME": str(self.home), "PATH": f"{self.stubs}:/usr/bin:/bin", "SHELL": "/bin/zsh",
               "RELEASES_DIR": str(self.releases), "LATEST_TAG": "v0.10.0",
               "FAKE_SYSTEM": "Linux", "FAKE_MACHINE": machine, **env}
        if stdin:
            with open(self.script) as script:
                return subprocess.run(["sh"], stdin=script, cwd=cwd or self.root, env=env, capture_output=True, text=True)
        return subprocess.run(["sh", str(self.script)], cwd=cwd or self.root, env=env, capture_output=True, text=True)

    def installed_version(self):
        result = subprocess.run([str(self.home / ".local" / "bin" / "superfind"), "--version"], capture_output=True, text=True)
        return result.stdout.strip()

    def test_installs_the_latest_release_and_sets_up_zsh_without_go(self):
        result = self.install()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.installed_version(), "superfind 0.10.0")
        self.assertEqual((self.home / ".zshrc").read_text(), '\neval "$("$HOME/.local/bin/superfind" init zsh)"\n')
        self.assertEqual(sorted(p.name for p in (self.home / ".local" / "bin").iterdir()), ["superfind"])

    def test_superfind_version_installs_that_release_instead_of_the_latest(self):
        self.publish("0.9.1", platform="linux_arm64")
        result = self.install(machine="aarch64", SUPERFIND_VERSION="0.9.1")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.installed_version(), "superfind 0.9.1")

    def test_an_archive_that_does_not_match_its_checksum_installs_nothing(self):
        shutil.rmtree(self.releases / "v0.10.0")
        self.publish("0.10.0", checksum="0" * 64)
        result = self.install()
        self.assertEqual(result.returncode, 1)
        self.assertIn("does not match its checksum", result.stderr)
        self.assertEqual(sorted(p.name for p in self.home.iterdir()), [])

    def test_an_unsupported_machine_is_named_and_pointed_at_go_install(self):
        result = self.install(machine="riscv64")
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stderr.strip(), "no superfind release for Linux riscv64; with Go installed, "
                         "run: go install github.com/adammiribyan/superfind@latest")
        self.assertEqual(sorted(p.name for p in self.home.iterdir()), [])

    def put_installed(self, folder):
        folder.mkdir(parents=True, exist_ok=True)
        (folder / "superfind").write_text("#!/bin/sh\necho 'superfind 0.9.1'\n")
        (folder / "superfind").chmod(0o755)

    def test_an_existing_setup_line_is_kept_and_another_superfind_is_named(self):
        brew = self.root / "brew"
        self.put_installed(brew)
        rc = 'export EDITOR=vim\neval "$(superfind init zsh)"\n'
        (self.home / ".zshrc").write_text(rc)
        result = self.install(PATH=f"{self.stubs}:{brew}:/usr/bin:/bin")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.home / ".zshrc").read_text(), rc)
        self.assertIn(f"{self.home}/.zshrc already sets up superfind; left it as it is", result.stdout)
        self.assertIn(f"note: another superfind is installed at {brew}/superfind", result.stdout)
        self.assertEqual(self.installed_version(), "superfind 0.10.0")

    def test_a_release_that_does_not_run_leaves_the_installed_superfind_alone(self):
        self.put_installed(self.home / ".local" / "bin")
        shutil.rmtree(self.releases / "v0.10.0")
        self.publish("0.10.0", runs=False)
        result = self.install()
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stderr.strip(), "the new superfind does not run on this machine; nothing was installed")
        self.assertEqual(self.installed_version(), "superfind 0.9.1")
        self.assertEqual(sorted(p.name for p in (self.home / ".local" / "bin").iterdir()), ["superfind"])

    def test_another_shell_is_shown_the_setup_lines_and_gets_no_ghostty_keybind(self):
        (self.stubs / "ghostty").write_text("#!/bin/sh\n")
        (self.stubs / "ghostty").chmod(0o755)
        for shell in ("/usr/bin/nu", ""):
            result = self.install(SHELL=shell)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn('  eval "$("$HOME/.local/bin/superfind" init zsh)"     # in ~/.zshrc\n', result.stdout)
            self.assertIn('  eval "$("$HOME/.local/bin/superfind" init bash)"    # in ~/.bashrc, or ~/.bash_profile on macOS\n',
                          result.stdout)
            self.assertIn('  "$HOME/.local/bin/superfind" init fish | source      # in ~/.config/fish/config.fish\n', result.stdout)
            self.assertEqual(result.stdout.splitlines()[-1], f"done: run {self.home}/.local/bin/superfind to see what it does")
            self.assertEqual(sorted(p.name for p in self.home.iterdir()), [".local"])

    def test_piped_to_sh_inside_a_superfind_clone_it_still_installs_the_release(self):
        clone = self.root / "clone"
        clone.mkdir()
        shutil.copy(REPO / "go.mod", clone / "go.mod")
        result = self.install(stdin=True, cwd=clone)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.installed_version(), "superfind 0.10.0")
        self.assertEqual(sorted(p.name for p in clone.iterdir()), ["go.mod"])


if __name__ == "__main__":
    unittest.main()

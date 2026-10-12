import hashlib
import json
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

    def install(self, shell, **extra):
        env = {"HOME": str(self.home), "PATH": os.environ["PATH"], "SHELL": f"/bin/{shell}", **GO_CACHES, "SUPERFIND_TELEMETRY": "off", "SUPERFIND_CHECKIN_URL": "http://127.0.0.1:1/v1/checkin", **extra}
        return subprocess.run(["sh", str(self.clone / "install.sh")], env=env, capture_output=True, text=True)

    def omarchy(self, bindings, text="o.bind(\"SUPER + SHIFT + CTRL + A\", \"Agent\", \"omarchy-launch-tui claude\")\n"):
        (self.home / ".local" / "share" / "omarchy").mkdir(parents=True)
        path = self.home / ".config" / "hypr" / bindings
        path.parent.mkdir(parents=True)
        path.write_text(text)
        return path

    def test_on_omarchy_a_second_run_leaves_one_superfind_binding_in_bindings_lua(self):
        path = self.omarchy("bindings.lua")
        self.install("bash")
        result = self.install("bash")
        self.assertEqual(result.returncode, 0)
        self.assertEqual(path.read_text(), 'o.bind("SUPER + SHIFT + CTRL + A", "Agent", "omarchy-launch-tui claude")\n\n'
                         f'o.bind("SUPER + SHIFT + CTRL + F", "Superfind", "omarchy-launch-tui {self.home}/.local/bin/superfind pick --run")\n')
        self.assertNotIn("Super-Shift-Ctrl-F", result.stdout)

    def test_on_omarchy_with_only_bindings_conf_the_binding_goes_there(self):
        path = self.omarchy("bindings.conf", "bindd = SUPER SHIFT CTRL, A, Agent, exec, omarchy-launch-tui claude\n")
        result = self.install("bash")
        self.assertEqual(path.read_text(), "bindd = SUPER SHIFT CTRL, A, Agent, exec, omarchy-launch-tui claude\n\n"
                         f"bindd = SUPER SHIFT CTRL, F, Superfind, exec, omarchy-launch-tui {self.home}/.local/bin/superfind pick --run\n")
        self.assertIn(f"added Super-Shift-Ctrl-F to {path} (run hyprctl reload to use it now)\n", result.stdout)

    def test_on_omarchy_a_key_already_bound_is_left_alone_with_a_hint(self):
        taken = 'o.bind("SUPER + SHIFT + CTRL + F", "Files", "nautilus")\n'
        path = self.omarchy("bindings.lua", taken)
        result = self.install("bash")
        self.assertEqual(path.read_text(), taken)
        self.assertIn(f"to open superfind with Super-Shift-Ctrl-F, add this line to {path}: "
                      f'o.bind("SUPER + SHIFT + CTRL + F", "Superfind", "omarchy-launch-tui {self.home}/.local/bin/superfind pick --run")\n',
                      result.stdout)

    def test_on_omarchy_a_home_with_a_space_gets_a_hint_instead_of_a_broken_binding(self):
        self.home = self.home / "Adam M"
        path = self.omarchy("bindings.lua", "")
        result = self.install("bash")
        self.assertEqual(path.read_text(), "")
        self.assertIn(f"to open superfind with Super-Shift-Ctrl-F, add this line to {path}: "
                      f'o.bind("SUPER + SHIFT + CTRL + F", "Superfind", "omarchy-launch-tui \'{self.home}/.local/bin/superfind\' pick --run")\n',
                      result.stdout)

    def test_on_omarchy_a_home_with_a_quote_is_told_what_to_bind(self):
        self.home = self.home / "Adam's"
        path = self.omarchy("bindings.lua", "")
        result = self.install("bash")
        self.assertEqual(path.read_text(), "")
        self.assertIn(f"to open superfind with Super-Shift-Ctrl-F, bind that key in {path} to run "
                      f"{self.home}/.local/bin/superfind pick --run through omarchy-launch-tui\n", result.stdout)

    def test_omarchy_is_found_through_omarchy_path_alone(self):
        packaged = self.home / "usr-share-omarchy"
        packaged.mkdir()
        bindings = self.home / ".config" / "hypr" / "bindings.lua"
        bindings.parent.mkdir(parents=True)
        bindings.write_text("")
        result = self.install("bash", OMARCHY_PATH=str(packaged))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('o.bind("SUPER + SHIFT + CTRL + F", "Superfind",', bindings.read_text())

    def test_on_omarchy_ghostty_gets_no_super_keybind(self):
        self.omarchy("bindings.lua")
        ghostty = self.home / ".config" / "ghostty" / "config"
        ghostty.parent.mkdir(parents=True)
        ghostty.write_text("theme = dark\n")
        result = self.install("bash")
        self.assertEqual(ghostty.read_text(), "theme = dark\n")
        self.assertIn("Use the Omarchy desktop shortcut in Ghostty.\n", result.stdout)

    def test_on_omarchy_an_unknown_shell_still_gets_the_binding(self):
        path = self.omarchy("bindings.lua", "")
        result = self.install("nu")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('o.bind("SUPER + SHIFT + CTRL + F", "Superfind",', path.read_text())

    def test_without_omarchy_ghostty_gets_its_keybind_and_hyprland_nothing(self):
        ghostty = self.home / ".config" / "ghostty" / "config"
        ghostty.parent.mkdir(parents=True)
        ghostty.write_text("theme = dark\n")
        bindings = self.home / ".config" / "hypr" / "bindings.lua"
        bindings.parent.mkdir(parents=True)
        bindings.write_text("")
        self.install("bash")
        self.assertEqual(ghostty.read_text(), "theme = dark\n\nkeybind = super+shift+o=text:\\x18c\n")
        self.assertEqual(bindings.read_text(), "")

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

    def test_in_a_clone_it_writes_a_receipt_that_names_the_clone(self):
        self.assertEqual(self.install("zsh").returncode, 0)
        receipt = self.home / ".local" / "state" / "superfind" / "install.json"
        self.assertEqual(receipt.read_text(), f'{{"method":"checkout","path":"{self.home}/.local/bin/superfind","clone":"{self.clone}"}}\n')

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
        env = {"HOME": str(self.home), "PATH": f"{stub}:{os.environ['PATH']}", "SHELL": "/bin/zsh", **GO_CACHES, "SUPERFIND_TELEMETRY": "off", "SUPERFIND_CHECKIN_URL": "http://127.0.0.1:1/v1/checkin"}
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
               "FAKE_SYSTEM": "Linux", "FAKE_MACHINE": machine, "SUPERFIND_TELEMETRY": "off", "SUPERFIND_CHECKIN_URL": "http://127.0.0.1:1/v1/checkin", **env}
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

    def test_a_release_install_writes_a_receipt_for_superfind_upgrade(self):
        result = self.install(SHELL="")
        self.assertEqual(result.returncode, 0, result.stderr)
        state = self.home / ".local" / "state" / "superfind"
        self.assertEqual((state / "install.json").read_text(), f'{{"method":"script","path":"{self.home}/.local/bin/superfind"}}\n')
        self.assertEqual(oct(state.stat().st_mode & 0o777), "0o700")
        self.assertEqual(sorted(p.name for p in self.home.iterdir()), [".local"])
        self.assertNotIn("note:", result.stdout)

    def test_a_home_with_quotes_and_backslashes_gets_a_receipt_that_parses(self):
        self.home = self.root / 'ada "the" builder\\co'
        self.home.mkdir()
        result = self.install()
        self.assertEqual(result.returncode, 0, result.stderr)
        receipt = json.loads((self.home / ".local" / "state" / "superfind" / "install.json").read_text())
        self.assertEqual(receipt, {"method": "script", "path": f"{self.home}/.local/bin/superfind"})

    def test_a_home_with_a_control_character_gets_no_receipt_and_a_note(self):
        self.home = self.root / "ada\tb"
        self.home.mkdir()
        result = self.install()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.installed_version(), "superfind 0.10.0")
        self.assertFalse((self.home / ".local" / "state").exists())
        self.assertIn(f"note: {self.home}/.local/bin/superfind has a control character in its path, so no install receipt was written; "
                      "superfind upgrade will print the install command instead of upgrading in place\n", result.stdout)

    def test_an_unwritable_state_folder_still_installs_and_prints_one_note(self):
        state = self.home / ".local" / "state"
        state.mkdir(parents=True)
        state.chmod(0o500)
        self.addCleanup(state.chmod, 0o700)
        result = self.install()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.installed_version(), "superfind 0.10.0")
        self.assertEqual(result.stdout.count("note:"), 1)
        self.assertIn(f"note: couldn't write {state}/superfind/install.json, so superfind upgrade will print the install command "
                      "instead of upgrading in place\n", result.stdout)
        self.assertEqual(sorted(p.name for p in state.iterdir()), [])

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
        self.assertIn(f"Shell setup already exists in {self.home}/.zshrc", result.stdout)
        self.assertIn(f"Another superfind is installed at {brew}/superfind. Check which copy your shell uses with: command -v superfind", result.stdout)
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
            self.assertEqual(result.stdout.splitlines()[-1], f"Installed. Run {self.home}/.local/bin/superfind pick --run to choose and resume a session.")
            self.assertEqual(sorted(p.name for p in self.home.iterdir()), [".local"])

    def test_on_omarchy_the_release_install_binds_super_shift_ctrl_f(self):
        (self.home / ".local" / "share" / "omarchy").mkdir(parents=True)
        bindings = self.home / ".config" / "hypr" / "bindings.lua"
        bindings.parent.mkdir(parents=True)
        bindings.write_text("")
        result = self.install(stdin=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(bindings.read_text(), '\no.bind("SUPER + SHIFT + CTRL + F", "Superfind", '
                         f'"omarchy-launch-tui {self.home}/.local/bin/superfind pick --run")\n')

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

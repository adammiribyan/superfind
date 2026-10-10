import os
import platform
import shutil
import subprocess
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
        shutil.copytree(REPO, self.clone, ignore=shutil.ignore_patterns(".git", "bin", "dist", "__pycache__", ".env"))

    def install(self, shell, **extra):
        env = {"HOME": str(self.home), "PATH": os.environ["PATH"], "SHELL": f"/bin/{shell}", **GO_CACHES, **extra}
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

    def test_installing_for_bash_sets_up_bash(self):
        self.assertEqual(self.install("bash").returncode, 0)
        self.assertEqual(self.startup_file("bash").read_text(), '\neval "$("$HOME/.local/bin/superfind" init bash)"\n')

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

    def omarchy(self, bindings, text="o.bind(\"SUPER + SHIFT + CTRL + A\", \"Agent\", \"omarchy-launch-tui claude\")\n"):
        (self.home / ".local" / "share" / "omarchy").mkdir(parents=True)
        path = self.home / ".config" / "hypr" / bindings
        path.parent.mkdir(parents=True)
        path.write_text(text)
        return path

    def test_on_omarchy_4_a_second_run_leaves_one_superfind_binding(self):
        path = self.omarchy("bindings.lua")
        self.install("bash")
        result = self.install("bash")
        self.assertEqual(result.returncode, 0)
        self.assertEqual(path.read_text(), 'o.bind("SUPER + SHIFT + CTRL + A", "Agent", "omarchy-launch-tui claude")\n\n'
                         f'o.bind("SUPER + SHIFT + CTRL + F", "Superfind", "omarchy-launch-tui {self.home}/.local/bin/superfind pick --run")\n')
        self.assertNotIn("Super-Shift-Ctrl-F", result.stdout)

    def test_on_omarchy_3_the_binding_goes_in_bindings_conf(self):
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
        self.assertIn(f"to open superfind with Super-Shift-Ctrl-F, add this line to {path}: ", result.stdout)

    def test_on_omarchy_ghostty_gets_no_super_keybind(self):
        self.omarchy("bindings.lua")
        ghostty = self.home / ".config" / "ghostty" / "config"
        ghostty.parent.mkdir(parents=True)
        ghostty.write_text("theme = dark\n")
        result = self.install("bash")
        self.assertEqual(ghostty.read_text(), "theme = dark\n")
        self.assertIn("skipped the Ghostty keybind: on Omarchy, Hyprland takes Super keys before Ghostty sees them\n", result.stdout)

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


if __name__ == "__main__":
    unittest.main()

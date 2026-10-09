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

    def test_installing_for_bash_sets_up_bash(self):
        self.assertEqual(self.install("bash").returncode, 0)
        self.assertEqual(self.startup_file("bash").read_text(), '\neval "$("$HOME/.local/bin/superfind" init bash)"\n')

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


if __name__ == "__main__":
    unittest.main()

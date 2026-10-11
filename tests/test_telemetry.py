import calendar
import json
import os
import shutil
import subprocess
import tempfile
import time
import unittest
from http.server import BaseHTTPRequestHandler
from pathlib import Path

from test_picker import tmux
from test_superfind import SUPERFIND, claude, line, serve, tool, uid, user

NOTICE = "Daily update check and anonymous usage · superfind telemetry"
UPDATE = "99.0.0 is out · superfind upgrade"
PROMISE = ("It never sends a search, a path, a project name, or session text, and superfind.dev passes the check-in to "
           "PostHog without your IP address.")
HOW_TO_STOP = ("superfind telemetry off turns it off and deletes the install id; SUPERFIND_TELEMETRY=off or DO_NOT_TRACK=1 "
               "in your environment do the same.")


class Worker:
    def __init__(self, test):
        self.requests = []
        self.status = 200
        self.answer = {"v": 1, "latest": "99.0.0"}
        self.redirect = None
        worker = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_POST(self):
                body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
                worker.requests.append({"path": self.path, "host": self.headers.get("Host"),
                                        "agent": self.headers.get("User-Agent"), "type": self.headers.get("Content-Type"),
                                        "body": body})
                if worker.redirect and self.path.endswith("/v1/checkin"):
                    self.send_response(302)
                    self.send_header("Location", worker.redirect)
                    self.end_headers()
                    return
                answer = worker.answer if isinstance(worker.answer, bytes) else json.dumps(worker.answer).encode()
                self.send_response(worker.status)
                self.send_header("Content-Length", str(len(answer)))
                self.end_headers()
                self.wfile.write(answer)

        self.url = serve(test, Handler)

    def bodies(self):
        return [r["body"] for r in self.requests]


@unittest.skipUnless(shutil.which("tmux"), "the picker tests drive a real terminal through tmux")
class TelemetryBehavior(unittest.TestCase):
    def setUp(self):
        self.home = Path(tempfile.mkdtemp()).resolve()
        self.addCleanup(shutil.rmtree, self.home, ignore_errors=True)
        self.state = self.home / ".local" / "state" / "superfind"
        projects = self.home / ".claude" / "projects" / "-tmp"
        projects.mkdir(parents=True)
        (projects / f"{uid(1)}.jsonl").write_text("\n".join([
            user("migrate the ledger tables"), tool("Bash"), claude("Migrated."),
            line(type="ai-title", aiTitle="Ledger migration")]) + "\n")
        self.session_name = f"superfind-telemetry-{os.getpid()}-{id(self)}"
        self.addCleanup(tmux, "kill-session", "-t", self.session_name)
        self.out = self.home / "out"
        self.worker = Worker(self)
        self.env = {"HOME": str(self.home), "XDG_CACHE_HOME": str(self.home / ".cache"), "PATH": "/usr/bin:/bin",
                    "TERM": "xterm-256color", "NO_COLOR": "1", "TYPESAFE_API_KEY": "", "PWD": "/tmp",
                    "SUPERFIND_CHECKIN_URL": self.worker.url + "/v1/checkin"}
        self.state.mkdir(parents=True)
        (self.state / "install.json").write_text(json.dumps({"method": "script", "path": str(Path(SUPERFIND).resolve())}))
        subprocess.run([str(SUPERFIND), "index"], env=self.env, capture_output=True, check=True)
        self.version = subprocess.run([str(SUPERFIND), "--version"], capture_output=True, text=True).stdout.split()[1]

    def superfind(self, *args, **extra):
        return subprocess.run([str(SUPERFIND), *args], env={**self.env, **extra}, capture_output=True, text=True,
                              cwd="/tmp", stdin=subprocess.DEVNULL)

    def open_picker(self):
        Path(f"{self.out}.code").unlink(missing_ok=True)
        tmux("kill-session", "-t", self.session_name)
        env = " ".join(f"{k}='{v}'" for k, v in self.env.items())
        command = f"env -i {env} {SUPERFIND} pick > {self.out}.stdout 2> {self.out}.stderr; echo $? > {self.out}.code"
        tmux("new-session", "-d", "-s", self.session_name, "-c", "/", "-x", "120", "-y", "32", command)
        self.wait_for("Claude sessions")

    def screen(self):
        return tmux("capture-pane", "-p", "-t", self.session_name).stdout

    def wait_for(self, text, timeout=30):
        deadline = time.time() + timeout
        while text not in self.screen():
            if time.time() > deadline:
                self.fail(f"never saw {text!r} on screen:\n{self.screen()}")
            time.sleep(0.1)

    def settled_screen(self):
        previous, current = None, self.screen()
        while current != previous:
            time.sleep(0.5)
            previous, current = current, self.screen()
        return current

    def close_picker(self):
        tmux("send-keys", "-t", self.session_name, "Escape")
        deadline = time.time() + 30
        while not Path(f"{self.out}.code").exists():
            self.assertLess(time.time(), deadline, f"superfind never exited:\n{self.screen()}")
            time.sleep(0.1)
        self.assertEqual((Path(f"{self.out}.code").read_text(), Path(f"{self.out}.stdout").read_text(),
                          Path(f"{self.out}.stderr").read_text()), ("1\n", "", ""))

    def told(self, ago=120):
        marker = self.state / "shown" / "telemetry"
        marker.parent.mkdir(parents=True, exist_ok=True)
        marker.touch()
        os.utime(marker, (time.time() - ago, time.time() - ago))

    def check(self):
        return json.loads((self.state / "check.json").read_text())

    def events(self):
        folder = self.state / "events"
        return sorted(p.name for p in folder.iterdir()) if folder.exists() else []

    def queue(self, name, outcome="installed"):
        folder = self.state / "events"
        folder.mkdir(parents=True, exist_ok=True)
        (folder / name).write_text(json.dumps({"id": "0f8e2c1a-5b7d-4e3f-9a21-6c4d8b0e7f12", "name": "upgraded",
                                               "at": "2026-10-11T11:00:00Z",
                                               "props": {"method": "script", "outcome": outcome}}) + "\n")

    def state_tree(self):
        return sorted((str(p.relative_to(self.state)), p.stat().st_mtime_ns, p.read_bytes() if p.is_file() else None)
                      for p in self.state.rglob("*"))

    def test_the_first_picker_says_telemetry_is_on_and_sends_nothing_and_the_next_one_checks_in(self):
        self.open_picker()
        self.wait_for(NOTICE)
        self.settled_screen()
        self.assertEqual(self.worker.requests, [])
        self.assertTrue((self.state / "shown" / "telemetry").exists())
        self.assertFalse((self.state / "id").exists())
        self.close_picker()
        time.sleep(1.1)

        self.open_picker()
        self.wait_for(UPDATE)
        self.assertNotIn(NOTICE, self.screen())
        shown = self.superfind("telemetry", "--json")
        self.assertEqual((shown.returncode, shown.stderr), (0, ""))
        sent = self.worker.requests
        self.assertEqual(len(sent), 1, sent)
        self.assertEqual(sent[0]["body"], shown.stdout.encode())
        self.assertEqual((sent[0]["path"], sent[0]["agent"], sent[0]["type"]),
                         ("/v1/checkin", f"superfind/{self.version}", "application/json"))
        body = json.loads(sent[0]["body"])
        self.assertEqual(sorted(body), ["arch", "events", "id", "method", "os", "schema", "version"])
        self.assertEqual((body["schema"], body["version"], body["events"], body["method"]), (1, self.version, [], "script"))
        self.assertEqual(body["id"], (self.state / "id").read_text().strip())
        self.assertRegex(body["id"], r"^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")
        check = self.check()
        self.assertEqual((check["failures"], check["latest"]), (0, "99.0.0"))
        self.close_picker()

        self.open_picker()
        self.wait_for(UPDATE)
        self.settled_screen()
        self.assertEqual(len(self.worker.requests), 1)
        self.close_picker()

        status = self.superfind("telemetry")
        self.assertEqual(status.stdout.splitlines()[2],
                         f"Install id: {body['id']}, a random id made on this machine that names nothing but this install.")
        self.assertRegex(status.stdout.splitlines()[3], r"^Next check-in: the first time you open the picker after \w{3} \d{1,2} at \d\d:\d\d\.$")

    def test_scripted_commands_never_check_in_or_write_state_even_when_a_check_is_due(self):
        self.told()
        self.superfind("search", "ledger")
        before = self.state_tree()
        self.assertEqual([t[0] for t in before], ["install.json", "shown", "shown/telemetry", "shown/version"])
        for args in (("search", "ledger"), ("search", "--json", "ledger"), ("show", uid(1)), ("handoff", uid(1), "ledger"),
                     ("last",), ("index",)):
            result = self.superfind(*args)
            self.assertEqual((result.returncode, result.stderr), (0, ""), args)
            self.assertIn("Ledger migration" if args[0] != "index" else "1 session indexed", result.stdout, args)
        time.sleep(0.5)
        self.assertEqual(self.worker.requests, [])
        self.assertEqual(self.state_tree(), before)

    def test_telemetry_off_writes_the_setting_deletes_what_was_collected_and_the_picker_stays_quiet(self):
        self.told()
        self.superfind("checkin")
        self.queue("1760190000000000000-100.json")
        self.assertEqual(len(self.worker.requests), 1)
        self.assertEqual(sorted(p.name for p in self.state.iterdir()), ["check.json", "check.lock", "events", "id", "install.json", "shown"])
        (self.home / ".config" / "superfind").mkdir(parents=True)
        (self.home / ".config" / "superfind" / "env").write_text("TYPESAFE_API_KEY=abc\n")

        result = self.superfind("telemetry", "off")
        self.assertEqual((result.returncode, result.stderr), (0, ""))
        self.assertEqual(result.stdout, "Telemetry is off: SUPERFIND_TELEMETRY=off is now in ~/.config/superfind/env, and the "
                         "install id, queued events, and last check were deleted. superfind sends nothing and doesn't check "
                         "for updates; superfind upgrade --check still works when you run it.\n")
        self.assertEqual((self.home / ".config" / "superfind" / "env").read_text(), "TYPESAFE_API_KEY=abc\nSUPERFIND_TELEMETRY=off\n")
        self.assertEqual(sorted(p.name for p in self.state.iterdir()), ["check.lock", "install.json", "shown"])
        status = self.superfind("telemetry")
        self.assertEqual(status.stdout, 'Telemetry is off because SUPERFIND_TELEMETRY="off" is in ~/.config/superfind/env. '
                         "superfind sends nothing and doesn't check for updates; superfind upgrade --check still works when "
                         "you run it.\n")
        shown = self.superfind("telemetry", "--json")
        self.assertEqual((shown.returncode, shown.stdout), (0, ""))
        self.assertEqual(shown.stderr, 'superfind telemetry: off because SUPERFIND_TELEMETRY="off" is in ~/.config/superfind/env, '
                         "so no check-in is sent.\n")

        (self.state / "shown" / "telemetry").unlink()
        self.open_picker()
        self.settled_screen()
        self.assertNotIn("superfind telemetry", self.screen())
        self.close_picker()
        self.superfind("checkin")
        self.assertEqual(len(self.worker.requests), 1)
        self.assertFalse((self.state / "id").exists())

        result = self.superfind("telemetry", "on")
        self.assertEqual((result.returncode, result.stderr), (0, ""))
        self.assertTrue(result.stdout.startswith("Telemetry is on. "), result.stdout)
        self.assertEqual((self.home / ".config" / "superfind" / "env").read_text(), "TYPESAFE_API_KEY=abc\n")
        self.assertTrue((self.state / "shown" / "telemetry").exists())

    def test_do_not_track_ci_and_the_variable_each_turn_it_off_and_the_picker_shows_no_notice(self):
        for extra, why in (({"DO_NOT_TRACK": "1"}, "DO_NOT_TRACK is set"),
                           ({"CI": "true"}, "CI is set, so this looks like CI"),
                           ({"GITHUB_ACTIONS": "true"}, "GITHUB_ACTIONS is set, so this looks like CI"),
                           ({"SUPERFIND_TELEMETRY": "off"}, 'SUPERFIND_TELEMETRY="off" is in your environment'),
                           ({"SUPERFIND_TELEMETRY": "0"}, 'SUPERFIND_TELEMETRY="0" is in your environment'),
                           ({"SUPERFIND_TELEMETRY": "on", "DO_NOT_TRACK": "1"}, "DO_NOT_TRACK is set")):
            result = self.superfind("telemetry", **extra)
            self.assertEqual(result.stdout.split(". ")[0], f"Telemetry is off because {why}", extra)
            self.told()
            self.superfind("checkin", **extra)
        self.assertEqual(self.worker.requests, [])
        self.assertFalse((self.state / "id").exists())
        self.env["DO_NOT_TRACK"] = "1"
        shutil.rmtree(self.state / "shown")
        self.open_picker()
        self.settled_screen()
        self.assertNotIn("superfind telemetry", self.screen())
        self.assertFalse((self.state / "shown" / "telemetry").exists())
        self.close_picker()
        self.assertEqual(self.worker.requests, [])

    def test_a_check_in_that_fails_backs_off_and_keeps_the_event_and_a_rejected_one_drops_it(self):
        self.told()
        self.queue("1760190000000000000-100.json")
        self.worker.status = 500
        before = time.time()
        self.assertEqual(self.superfind("checkin").returncode, 0)
        check = self.check()
        self.assertEqual((check["failures"], check.get("latest")), (1, None))
        self.assertTrue(check["next"].endswith("Z"), check["next"])
        self.assertAlmostEqual(calendar.timegm(time.strptime(check["next"][:19], "%Y-%m-%dT%H:%M:%S")), before + 3600, delta=60)
        self.assertEqual(self.events(), ["1760190000000000000-100.json"])
        self.assertEqual(self.superfind("checkin").returncode, 0)
        self.assertEqual(len(self.worker.requests), 1)

        (self.state / "check.json").write_text(json.dumps({"next": "2026-01-01T00:00:00Z", "failures": 1}) + "\n")
        self.worker.status, self.worker.answer = 400, {"error": "upgrade"}
        self.superfind("checkin")
        self.assertEqual(self.events(), [])
        self.assertEqual(self.check()["failures"], 2)
        self.assertEqual(len(self.worker.requests), 2)

    def test_a_redirect_is_not_followed_and_counts_as_a_failure(self):
        self.told()
        self.worker.redirect = self.worker.url + "/elsewhere"
        self.superfind("checkin")
        self.assertEqual([r["path"] for r in self.worker.requests], ["/v1/checkin"])
        check = self.check()
        self.assertEqual((check["failures"], check.get("latest")), (1, None))
        self.worker.redirect = None
        self.worker.answer = b"<html>a captive portal</html>"
        (self.state / "check.json").write_text(json.dumps({"next": "2026-01-01T00:00:00Z", "failures": 0}) + "\n")
        self.superfind("checkin")
        self.assertEqual(self.check()["failures"], 1)
        self.open_picker()
        self.settled_screen()
        self.assertNotIn("is out", self.screen())
        self.close_picker()

    def test_the_check_in_goes_through_the_proxy_from_the_environment(self):
        self.told()
        self.env.update(SUPERFIND_CHECKIN_URL="http://superfind.test/v1/checkin", HTTP_PROXY=self.worker.url, https_proxy=self.worker.url)
        self.superfind("checkin")
        self.assertEqual([(r["path"], r["host"]) for r in self.worker.requests], [("http://superfind.test/v1/checkin", "superfind.test")])
        self.assertEqual(self.check()["latest"], "99.0.0")

    def test_an_upgrade_records_an_event_that_the_next_check_in_sends_and_then_forgets(self):
        cask = self.home / "Caskroom" / "superfind" / self.version / "superfind"
        cask.parent.mkdir(parents=True)
        shutil.copy(SUPERFIND, cask)
        result = subprocess.run([str(cask), "upgrade"], env={**self.env, "DO_NOT_TRACK": "1"}, capture_output=True, text=True)
        self.assertEqual((result.returncode, result.stdout.splitlines()[0]),
                         (0, "Homebrew installed this copy, so superfind left it alone. To upgrade:"))
        self.assertEqual(self.events(), [])
        result = subprocess.run([str(cask), "upgrade"], env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0)
        self.assertEqual(len(self.events()), 1)
        shown = json.loads(self.superfind("telemetry", "--json").stdout)
        self.assertEqual([(e["name"], e["hours_ago"], e["props"]) for e in shown["events"]],
                         [("upgraded", 0, {"method": "homebrew", "outcome": "unchanged"})])
        self.assertRegex(shown["events"][0]["id"], r"^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")
        self.told()
        self.superfind("checkin")
        sent = json.loads(self.worker.requests[0]["body"])
        self.assertEqual(sent["events"], shown["events"])
        self.assertEqual(self.events(), [])

    def test_the_telemetry_command_says_what_is_sent_and_how_to_stop_it(self):
        result = self.superfind("telemetry")
        self.assertEqual((result.returncode, result.stderr), (0, ""))
        lines = result.stdout.splitlines()
        self.assertEqual(lines[0], "Telemetry is on. Once a day, when you open the picker, superfind sends this to superfind.dev and "
                         "gets back the newest release's version:")
        self.assertEqual(json.loads(lines[1]), {"schema": 1, "id": "", "version": self.version, "os": os.uname().sysname.lower(),
                                                "arch": {"x86_64": "amd64", "arm64": "arm64", "aarch64": "arm64"}[os.uname().machine],
                                                "method": "script", "events": []})
        self.assertEqual(lines[2:], ["Install id: none yet; a random one is made at the first check-in.",
                                     "Next check-in: the next time you open the picker.", PROMISE, HOW_TO_STOP])
        self.assertTrue((self.state / "shown" / "telemetry").exists())
        self.assertEqual(self.worker.requests, [])
        for args in (("telemetry", "--jsn"), ("telemetry", "off", "on"), ("telemetry", "off", "--json"), ("telemetry", "stop")):
            result = self.superfind(*args)
            self.assertEqual((result.returncode, result.stdout), (2, ""), args)
            self.assertIn("See superfind help telemetry.", result.stderr)
        self.assertIn("superfind telemetry [off|on] [--json]", self.superfind("help", "telemetry").stdout)
        self.assertIn("superfind telemetry [off|on]", self.superfind("--help").stdout)

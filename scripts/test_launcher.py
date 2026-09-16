"""Run after make build. Every test uses a temporary, empty library."""
import concurrent.futures
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
import urllib.request

from launcher import Launcher, ROOT, data_directory


class LauncherTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="current-launcher-test-")
        self.addCleanup(self.temp.cleanup)
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        self.env = {**os.environ, "CURRENT_DATA_DIR": str(Path(self.temp.name) / "Library with spaces"),
                    "CURRENT_ADDR": f"127.0.0.1:{port}"}
        self.patch = patch.dict(os.environ, self.env)
        self.patch.start()
        self.addCleanup(self.patch.stop)
        self.launcher = Launcher()
        self.addCleanup(self.cleanup_server)

    def cleanup_server(self):
        if self.launcher.owned(self.launcher.pid()):
            self.launcher.stop()

    def cli(self, *args):
        return subprocess.run([str(ROOT / "current"), *args], env=self.env,
                              capture_output=True, text=True, timeout=25)

    def test_empty_library_start_restart_backup_and_stop(self):
        self.launcher.start()
        pid = self.launcher.pid()
        self.assertTrue(self.launcher.owned(pid))
        self.assertTrue(self.launcher.healthy())
        with urllib.request.urlopen(self.launcher.url + "/api/feeds") as response:
            self.assertEqual(json.load(response), [], "fresh installs must have no subscriptions")
        self.launcher.start()
        self.assertEqual(pid, self.launcher.pid(), "repeated launch must reuse the server")
        check = subprocess.run([str(ROOT / "bin/current-tui"), "--check"],
                               env={**self.env, "CURRENT_URL": self.launcher.url},
                               capture_output=True, text=True, timeout=5)
        self.assertEqual(check.returncode, 0, check.stderr)
        self.assertIn("0 unread, 0 saved", check.stdout)
        backup = self.cli("backup")
        self.assertEqual(backup.returncode, 0, backup.stderr)
        self.assertTrue(Path(backup.stdout.strip()).is_file())
        self.launcher.stop()
        self.assertFalse(self.launcher.healthy())
        self.launcher.start()
        self.assertNotEqual(pid, self.launcher.pid())

    def test_concurrent_cli_starts_share_one_process(self):
        with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
            results = list(pool.map(lambda _: self.cli("start"), range(3)))
        for result in results:
            self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(self.launcher.owned(self.launcher.pid()))
        self.assertEqual(self.cli("status").returncode, 0)

    def test_stale_pid_never_stops_an_unrelated_process(self):
        self.launcher.data.mkdir(parents=True)
        self.launcher.pid_file.write_text(str(os.getpid()))
        with self.assertRaisesRegex(RuntimeError, "does not match"):
            self.launcher.stop()
        self.launcher.clear_record()

    def test_old_start_identity_is_rejected(self):
        self.launcher.start()
        identity = json.loads(self.launcher.identity_file.read_text())
        self.launcher.identity_file.write_text(json.dumps({**identity, "process": "previous process"}))
        try:
            with self.assertRaisesRegex(RuntimeError, "does not match"):
                self.launcher.stop()
            self.assertTrue(self.launcher.healthy())
        finally:
            self.launcher.identity_file.write_text(json.dumps(identity))

    def test_platform_paths_and_browser_opener(self):
        self.assertEqual(data_directory(ROOT, "darwin", {}), Path.home() / "Library/Application Support/Current")
        self.assertEqual(data_directory(ROOT, "linux", {}), ROOT / "data")
        self.assertEqual(data_directory(ROOT, "darwin", {"CURRENT_DATA_DIR": "custom"}), ROOT / "custom")
        for platform, opener in [("darwin", "open"), ("linux", "xdg-open")]:
            with patch("sys.platform", platform), patch.object(self.launcher, "start"), patch("subprocess.call", return_value=0) as run:
                self.launcher.run_client("web", [])
                self.assertEqual(run.call_args.args[0], [opener, self.launcher.url])


if __name__ == "__main__":
    unittest.main()

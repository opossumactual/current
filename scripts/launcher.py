"""Current's Linux/macOS launcher. Uses only Python's standard library."""
import contextlib
import fcntl
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parent.parent


def data_directory(root, platform, environ):
    if environ.get("CURRENT_DATA_DIR"):
        path = Path(environ["CURRENT_DATA_DIR"]).expanduser()
        return (root / path).resolve()
    if platform == "darwin":
        return Path.home() / "Library" / "Application Support" / "Current"
    return root / "data"


class Launcher:
    def __init__(self, root=ROOT):
        self.root = root
        self.data = data_directory(root, sys.platform, os.environ)
        self.addr = os.environ.get("CURRENT_ADDR", "127.0.0.1:8490")
        self.url = "http://" + self.addr
        self.server = root / "bin" / "current-server"
        self.terminal = root / "bin" / "current-tui"
        self.pid_file = self.data / "server.pid"
        self.identity_file = self.data / "server.identity.json"
        self.command = [str(self.server), "--addr", self.addr, "--data", str(self.data / "library.db")]
        self.child = None

    @contextlib.contextmanager
    def locked(self):
        self.data.mkdir(parents=True, exist_ok=True, mode=0o700)
        # flock(2) is available on both systems; the external flock utility is not.
        with open(self.data / "server.lock", "a") as lock:
            deadline = time.monotonic() + 15
            while True:
                try:
                    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    break
                except BlockingIOError:
                    if time.monotonic() >= deadline:
                        raise RuntimeError("Another Current launcher is busy. Try again shortly.")
                    time.sleep(0.1)
            try:
                yield
            finally:
                fcntl.flock(lock, fcntl.LOCK_UN)

    def healthy(self):
        try:
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            with opener.open(self.url + "/api/library", timeout=1) as response:
                return response.status == 200
        except (OSError, urllib.error.URLError, ValueError):
            return False

    def pid(self):
        try:
            pid = int(self.pid_file.read_text().strip())
            return pid if pid > 1 else None
        except (OSError, ValueError):
            return None

    def identity(self, pid):
        if not pid:
            return None
        try:
            # -ww prevents truncation. Include the start time to detect PID reuse.
            result = subprocess.run(
                ["ps", "-ww", "-p", str(pid), "-o", "lstart=", "-o", "args="],
                capture_output=True, text=True, timeout=2, env={**os.environ, "LC_ALL": "C"},
            )
            if result.returncode != 0 or not result.stdout.strip():
                return None
            stamp_and_command = result.stdout.strip()
            if sys.platform.startswith("linux"):
                args = Path(f"/proc/{pid}/cmdline").read_bytes().split(b"\0")
                actual = [os.fsdecode(arg) for arg in args if arg]
                if actual != self.command:
                    return None
            elif not stamp_and_command.endswith(" " + " ".join(self.command)):
                return None
            return {"pid": pid, "process": stamp_and_command}
        except (OSError, subprocess.TimeoutExpired):
            return None

    def owned(self, pid):
        actual = self.identity(pid)
        if actual is None:
            return False
        try:
            return actual == json.loads(self.identity_file.read_text())
        except FileNotFoundError:
            # Adopt the original Linux launcher's PID only after checking all
            # executable arguments, including the library path.
            return sys.platform.startswith("linux")
        except (OSError, ValueError):
            return False

    def start(self):
        if not self.server.is_file():
            raise RuntimeError("Build Current first: ./scripts/setup.sh")
        with self.locked():
            pid = self.pid()
            if self.owned(pid):
                if self.healthy():
                    return
                raise RuntimeError("Current is running but not responding. Try ./current restart.")
            if self.healthy():
                raise RuntimeError(
                    f"A server already uses {self.url}, but belongs to another launch or library. "
                    "Use its original launcher, or choose a different CURRENT_ADDR."
                )
            with open(self.data / "server.log", "ab", buffering=0) as log:
                self.child = subprocess.Popen(
                    self.command, cwd=self.root, stdin=subprocess.DEVNULL,
                    stdout=log, stderr=log, start_new_session=True,
                )
            pid = self.child.pid
            self.pid_file.write_text(str(pid) + "\n")
            self.identity_file.unlink(missing_ok=True)
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                if self.child.poll() is not None:
                    break
                identity = self.identity(pid)
                if identity:
                    self.identity_file.write_text(json.dumps(identity))
                    self.identity_file.chmod(0o600)
                    if self.healthy():
                        return
                time.sleep(0.1)
            if self.child.poll() is None:
                self.child.terminate()
                try:
                    self.child.wait(timeout=8)
                except subprocess.TimeoutExpired:
                    raise RuntimeError(f"Current is still starting. See {self.data / 'server.log'}")
            self.clear_record()
            raise RuntimeError(f"Current could not start. See {self.data / 'server.log'}")

    def clear_record(self):
        self.identity_file.unlink(missing_ok=True)
        self.pid_file.unlink(missing_ok=True)

    def stop(self):
        with self.locked():
            pid = self.pid()
            if not pid:
                print("Current is stopped.")
                return
            if not self.owned(pid):
                try:
                    os.kill(pid, 0)
                except ProcessLookupError:
                    self.clear_record()
                    return
                raise RuntimeError("The recorded process does not match this Current library; leaving it alone.")
            os.kill(pid, signal.SIGTERM)
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                if self.child and self.child.pid == pid:
                    self.child.poll()
                if not self.owned(pid):
                    if self.child and self.child.pid == pid:
                        self.child.wait(timeout=2)
                    self.clear_record()
                    return
                time.sleep(0.1)
            raise RuntimeError("Current has not stopped yet. Check server.log before retrying.")

    def run_client(self, command, args):
        env = {**os.environ, "CURRENT_URL": self.url}
        if command == "web":
            opener = "open" if sys.platform == "darwin" else "xdg-open"
            self.start()
            return subprocess.call([opener, self.url], env=env)
        if not self.terminal.is_file():
            raise RuntimeError("Build the terminal reader first: ./scripts/setup.sh")
        if command == "terminal":
            kitty = shutil.which("kitty")
            if not kitty and sys.platform == "darwin":
                for path in [Path("/Applications/kitty.app"), Path.home() / "Applications/kitty.app"]:
                    executable = path / "Contents/MacOS/kitty"
                    if executable.is_file():
                        kitty = str(executable)
                        break
            if not kitty:
                raise RuntimeError("Kitty is not installed. Install it, or use ./current tui in this terminal.")
            self.start()
            env.pop("NO_COLOR", None)
            os.execvpe(kitty, [kitty, "--title", "Current · Terminal", "--override", "background_opacity=1", str(self.terminal), *args], env)
        self.start()
        os.execve(self.terminal, [str(self.terminal), *args], env)


def main(args):
    command, *rest = args or ["web"]
    launcher = Launcher()
    os.chdir(ROOT)
    if command in ("help", "-h", "--help"):
        print("Current — in development\n\nUsage: ./current [web|terminal|tui|serve|start|stop|restart|status|build|backup|paths]\n"
              "web        Open the graphical reader in your browser\n"
              "terminal   Open a new Kitty window\n"
              "tui        Run in the current terminal\n"
              "serve      Run the server in the foreground\n"
              "backup     Make a consistent SQLite backup\n"
              "paths      Show the library and log locations\n\n"
              "Set CURRENT_DATA_DIR or CURRENT_ADDR to override the defaults.")
    elif command == "build":
        os.execvp("make", ["make", "build", *rest])
    elif command == "paths":
        print(f"Library: {launcher.data / 'library.db'}\nLog: {launcher.data / 'server.log'}\nURL: {launcher.url}")
    elif command == "serve":
        launcher.data.mkdir(parents=True, exist_ok=True, mode=0o700)
        os.execve(launcher.server, [*launcher.command, *rest], os.environ)
    elif command == "backup":
        os.execv(sys.executable, [sys.executable, str(ROOT / "scripts/backup.py"), str(launcher.data / "library.db"), rest[0] if rest else str(launcher.data / "backups")])
    elif command == "status":
        if launcher.owned(launcher.pid()) and launcher.healthy():
            print(f"Current is running at {launcher.url}")
        else:
            print("Current is stopped or belongs to a different launch/library.")
            return 1
    elif command == "stop":
        launcher.stop()
    elif command in ("start", "restart"):
        if command == "restart":
            launcher.stop()
        launcher.start()
        print(f"Current is running at {launcher.url}")
    elif command in ("web", "terminal", "tui"):
        return launcher.run_client(command, rest)
    else:
        raise RuntimeError("Unknown command. Use ./current help.")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except (RuntimeError, OSError) as error:
        print(f"Current: {error}", file=sys.stderr)
        sys.exit(1)

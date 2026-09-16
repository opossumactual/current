"""Make a consistent online backup of Current's SQLite library."""
import datetime
from pathlib import Path
import sqlite3
import sys

source = Path(sys.argv[1]).expanduser().resolve(strict=True)
destination = Path(sys.argv[2]).expanduser().resolve()
destination.mkdir(parents=True, exist_ok=True)
stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%d-%H%M%S-%f")
backup = destination / f"current-{stamp}.db"
# SQLite's backup API includes committed WAL content without stopping the server.
with sqlite3.connect(source.as_uri() + "?mode=ro", uri=True) as src:
    with sqlite3.connect(backup) as dst:
        src.backup(dst)
backup.chmod(0o600)
print(backup)

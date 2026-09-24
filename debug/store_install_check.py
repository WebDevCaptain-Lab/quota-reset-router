"""Install quota-reset-router through CPA's plugin store API from a given registry URL.

Usage: store_install_check.py <cpa-archive> <registry-url> <expected-version>
"""

import json
import platform
import socket
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path

PLUGIN = "quota-reset-router"
KEY = "store-check-management-key"


def find_entry(node):
    if isinstance(node, dict):
        if node.get("id") == PLUGIN:
            return node
        node = list(node.values())
    if isinstance(node, list):
        for item in node:
            found = find_entry(item)
            if found:
                return found
    return None


def main():
    archive, registry, version = sys.argv[1:]
    goarch = {"x86_64": "amd64", "arm64": "arm64"}[platform.machine()]
    with tempfile.TemporaryDirectory(prefix="cpa-store-check-") as tmp:
        root = Path(tmp)
        with tarfile.open(archive) as tar:
            (root / "cli-proxy-api").write_bytes(tar.extractfile("cli-proxy-api").read())
        (root / "cli-proxy-api").chmod(0o755)
        (root / "auths").mkdir()
        with socket.socket() as s:
            s.bind(("127.0.0.1", 0))
            port = s.getsockname()[1]
        (root / "config.yaml").write_text(f"""host: 127.0.0.1
port: {port}
auth-dir: {root / "auths"}
remote-management:
  allow-remote: false
  secret-key: {KEY}
  disable-control-panel: true
plugins:
  enabled: true
  dir: {root / "plugins"}
  store-sources:
    - "{registry}"
""")
        log = (root / "cpa.log").open("w+")
        proc = subprocess.Popen(
            [str(root / "cli-proxy-api"), "--config", str(root / "config.yaml")],
            cwd=root,
            stdout=log,
            stderr=subprocess.STDOUT,
        )
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

        def api(path, method="GET", body=None):
            request = urllib.request.Request(
                f"http://127.0.0.1:{port}/v0/management{path}",
                data=None if body is None else json.dumps(body).encode(),
                headers={"Authorization": "Bearer " + KEY, "Content-Type": "application/json"},
                method=method,
            )
            with opener.open(request, timeout=120) as response:
                return json.loads(response.read())

        def wait(check, label, timeout=60):
            end = time.monotonic() + timeout
            while time.monotonic() < end:
                if proc.poll() is not None:
                    raise AssertionError(f"CPA exited during {label}")
                try:
                    result = check()
                    if result:
                        return result
                except (urllib.error.URLError, ConnectionError, KeyError):
                    pass
                time.sleep(0.5)
            raise AssertionError(f"timed out: {label}")

        try:
            listing = wait(lambda: api("/plugin-store"), "store listing")
            entry = find_entry(listing)
            assert entry, "plugin missing from store listing"
            print("listing:", json.dumps(entry, sort_keys=True))
            source = entry.get("source_id") or entry.get("source", {}).get("id") or entry.get("source")
            query = f"?source={source}" if isinstance(source, str) else ""
            result = api(f"/plugin-store/{PLUGIN}/install{query}", "POST", {})
            print("install:", json.dumps(result, sort_keys=True))
            status = wait(lambda: api(f"/plugins/{PLUGIN}/status"), "status route")
            assert status["version"] == version, status
            installed = sorted(p.relative_to(root).as_posix() for p in (root / "plugins").rglob("*") if p.is_file())
            print("files:", installed)
            assert f"plugins/darwin/{goarch}/{PLUGIN}-v{version}.dylib" in installed, installed
            print(f"PASS: store install of {PLUGIN} {version} on darwin/{goarch}; status mode {status['mode']}")
        except BaseException:
            log.flush()
            log.seek(0)
            print(log.read(), file=sys.stderr)
            raise
        finally:
            proc.terminate()
            proc.wait(timeout=15)


if __name__ == "__main__":
    main()

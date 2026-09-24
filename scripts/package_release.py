"""Build CLIProxyAPI plugin store release assets.

Usage:
  package_release.py zip <version> <target>
      Package dist/<target>/quota-reset-router.<ext> as
      dist/release/quota-reset-router_<version>_<target>.zip
  package_release.py checksums <version>
      Write dist/release/checksums.txt; requires the zips for every target

Zip entries use fixed timestamps and modes, so rebuilding on the same machine
produces identical archives.
"""

import hashlib
import re
import sys
import zipfile
from pathlib import Path

PLUGIN_ID = "quota-reset-router"
EXTENSIONS = {
    "linux_amd64": "so",
    "linux_arm64": "so",
    "darwin_arm64": "dylib",
    "windows_amd64": "dll",
}
RELEASE = Path("dist") / "release"
FIXED_TIME = (1980, 1, 1, 0, 0, 0)


def check_version(version):
    if not re.fullmatch(r"\d+(\.\d+)+", version):
        sys.exit(
            f"invalid version {version!r}; expected dotted numbers without a leading v"
        )


def add(archive, source, mode):
    entry = zipfile.ZipInfo(source.name, FIXED_TIME)
    entry.external_attr = (0o100000 | mode) << 16
    entry.compress_type = zipfile.ZIP_DEFLATED
    archive.writestr(entry, source.read_bytes())


def package(version, target):
    check_version(version)
    if target not in EXTENSIONS:
        sys.exit(
            f"unsupported target {target!r}; expected one of {', '.join(EXTENSIONS)}"
        )
    library = Path("dist") / target / f"{PLUGIN_ID}.{EXTENSIONS[target]}"
    RELEASE.mkdir(parents=True, exist_ok=True)
    path = RELEASE / f"{PLUGIN_ID}_{version}_{target}.zip"
    with zipfile.ZipFile(path, "w") as archive:
        add(archive, library, 0o755)
        add(archive, Path("LICENSE"), 0o644)
        add(archive, Path("THIRD_PARTY_NOTICES.md"), 0o644)
    print(path)


def checksums(version):
    check_version(version)
    archives = [
        RELEASE / f"{PLUGIN_ID}_{version}_{target}.zip" for target in sorted(EXTENSIONS)
    ]
    missing = [path.name for path in archives if not path.is_file()]
    if missing:
        # A partial set would break plugin store installs on the missing platforms.
        sys.exit(f"missing release archives: {', '.join(missing)}")
    lines = "".join(
        f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}\n"
        for path in archives
    )
    (RELEASE / "checksums.txt").write_text(lines)
    print(lines, end="")


def main():
    args = sys.argv[1:]
    if len(args) == 3 and args[0] == "zip":
        package(args[1], args[2])
    elif len(args) == 2 and args[0] == "checksums":
        checksums(args[1])
    else:
        sys.exit(__doc__)


if __name__ == "__main__":
    main()

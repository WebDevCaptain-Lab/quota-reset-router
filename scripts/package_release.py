"""Build CLIProxyAPI plugin store release assets from dist/quota-reset-router.so.

Writes dist/release/<id>_<version>_<goos>_<goarch>.zip and checksums.txt.
Entries use fixed timestamps and modes so the archive is reproducible.
"""

import hashlib
import re
import sys
import zipfile
from pathlib import Path

PLUGIN_ID = "quota-reset-router"
FIXED_TIME = (1980, 1, 1, 0, 0, 0)
REGULAR_FILE_0644 = 0o100644 << 16


def main():
    version, goos, goarch = sys.argv[1:4]
    if not re.fullmatch(r"\d+(\.\d+)+", version):
        sys.exit(
            f"invalid version {version!r}; expected dotted numbers without a leading v"
        )
    library = Path("dist") / f"{PLUGIN_ID}.so"
    release = Path("dist") / "release"
    release.mkdir(parents=True, exist_ok=True)
    archive = release / f"{PLUGIN_ID}_{version}_{goos}_{goarch}.zip"
    with zipfile.ZipFile(archive, "w") as out:
        for source in (library, Path("LICENSE"), Path("THIRD_PARTY_NOTICES.md")):
            entry = zipfile.ZipInfo(source.name, FIXED_TIME)
            entry.external_attr = REGULAR_FILE_0644
            entry.compress_type = zipfile.ZIP_DEFLATED
            out.writestr(entry, source.read_bytes())
    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
    (release / "checksums.txt").write_text(f"{digest}  {archive.name}\n")
    print(f"{digest}  {archive.name}")


if __name__ == "__main__":
    main()

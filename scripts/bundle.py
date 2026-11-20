"""Create deterministic Windows and combined release archives for 0.1.1."""
from hashlib import sha256
from pathlib import Path
from zipfile import ZIP_DEFLATED, ZipFile, ZipInfo

root = Path(__file__).resolve().parents[1]
packages = root / "packages"
windows = packages / "YourLM-Windows-x64"
windows_zip = packages / "YourLM-Windows-x64-0.1.1.zip"
apk = packages / "YourLM-Android-arm64.apk"
sums = packages / "SHA256SUMS"
bundle = root / "YourLM-0.1.1.zip"
required = [
    windows / "YourLM.exe",
    windows / "runtime" / "llama-server.exe",
    windows / "runtime" / "ggml-rpc-server.exe",
    windows / "SHA256SUMS.txt",
    windows / "BUILDINFO.txt",
    windows / "CZYTAJ-MNIE.txt",
    apk,
]
for path in required:
    if not path.is_file():
        raise SystemExit(f"missing release file: {path}")


def add(out: ZipFile, path: Path, name: str) -> None:
    info = ZipInfo(name, (2026, 11, 20, 0, 0, 0))
    info.compress_type = ZIP_DEFLATED
    info.external_attr = (0o644 << 16)
    out.writestr(info, path.read_bytes())


with ZipFile(windows_zip, "w") as out:
    for path in sorted(windows.rglob("*")):
        if path.is_file():
            add(out, path, (Path(windows.name) / path.relative_to(windows)).as_posix())

sums.write_text(
    "".join(
        f"{sha256(path.read_bytes()).hexdigest()}  {path.name}\n"
        for path in (windows_zip, apk)
    ),
    encoding="ascii",
)
with ZipFile(bundle, "w") as out:
    for path in sorted(windows.rglob("*")):
        if path.is_file():
            add(out, path, (Path(windows.name) / path.relative_to(windows)).as_posix())
    add(out, apk, f"packages/{apk.name}")
    add(out, sums, f"packages/{sums.name}")
    add(out, root / "README.md", "README.md")
    add(out, root / "SECURITY.md", "SECURITY.md")
    for path in sorted((root / "licenses").glob("*")):
        add(out, path, path.relative_to(root).as_posix())

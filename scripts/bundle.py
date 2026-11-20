"""Prepare the two checksums and a portable download containing both platforms."""
from hashlib import sha256
from pathlib import Path
from zipfile import ZIP_DEFLATED, ZipFile

root = Path(__file__).resolve().parents[1]
packages = root / 'packages'
files = [packages / 'YourLM-Windows-x64.exe', packages / 'YourLM-Android-arm64.apk']
sums = packages / 'SHA256SUMS'
sums.write_text(''.join(f'{sha256(f.read_bytes()).hexdigest()}  {f.name}\n' for f in files))
with ZipFile(root / 'YourLM-0.1.0.zip', 'w', ZIP_DEFLATED) as out:
    for path in [*files, sums, root / 'README.md', *sorted((root / 'licenses').glob('*'))]:
        out.write(path, path.relative_to(root))

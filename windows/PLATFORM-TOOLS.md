# Bundled Windows ADB payload

`platform-tools-windows.zip` contains only the three files required by the
in-app USB installer:

- `adb.exe`
- `AdbWinApi.dll`
- `AdbWinUsbApi.dll`

The payload was staged from the Android SDK preinstalled on GitHub's
`windows-latest` runner on 2026-11-20. Its SHA-256 is pinned in
`scripts/build-windows.sh`:

`63b73f6f1870305c5a3e0f43ca861427fe6856cc113392710c7e698ab477e3f9`

The build fails if it changes unexpectedly. To update ADB deliberately, stage
those same three files from an official Google Android SDK Platform Tools
installation, make a deterministic ZIP, review it, and update the pinned hash.
ADB is unpacked only under the current user's YourLM application-data folder.

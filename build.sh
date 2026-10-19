#!/bin/sh
# Builds dist/OmniWebServer/omni.exe and the Windows installer.
# Needs Go 1.22+ and NSIS 3 (makensis; on Debian/Ubuntu: apt install nsis).
# On Windows run it from Git Bash (bash build.sh) or double-click build.cmd.
set -eu
cd "$(dirname "$0")"
VERSION=$(sed -n 's/^const appVersion = "\(.*\)"/\1/p' app.go)

# makensis: PATH first, then the default NSIS install folders on Windows.
MAKENSIS=$(command -v makensis || true)
for p in "/c/Program Files (x86)/NSIS/makensis.exe" "/c/Program Files/NSIS/makensis.exe"; do
	[ -n "$MAKENSIS" ] || { [ -x "$p" ] && MAKENSIS=$p; } || true
done
[ -n "$MAKENSIS" ] || { echo "makensis not found: install NSIS 3 (https://nsis.sourceforge.io)" >&2; exit 1; }

# python3 on Windows is often the Microsoft Store stub, which exits with an error.
PYTHON=
for p in python3 python py; do
	if command -v "$p" >/dev/null 2>&1 && "$p" -c "" >/dev/null 2>&1; then PYTHON=$p; break; fi
done
[ -n "$PYTHON" ] || { echo "python not found (needed for the portable zip)" >&2; exit 1; }

rm -rf dist/OmniWebServer
mkdir -p dist/OmniWebServer
go test ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o dist/OmniWebServer/omni.exe .
# Same program without a console window, for shortcuts and autostart.
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -H windowsgui" -o dist/OmniWebServer/omniw.exe .
cp README.md LICENSE CREDITS.md dist/OmniWebServer/
(cd installer && "$MAKENSIS" -V2 -DVERSION="$VERSION" -DSRCDIR=../dist/OmniWebServer -DOUTFILE="../dist/OmniWebServer-$VERSION-Setup.exe" omni.nsi)
(cd dist && rm -f "OmniWebServer-$VERSION-portable.zip" && "$PYTHON" -c "
import zipfile,sys
z=zipfile.ZipFile('OmniWebServer-$VERSION-portable.zip','w',zipfile.ZIP_DEFLATED)
for f in ['OmniWebServer/omni.exe','OmniWebServer/omniw.exe','OmniWebServer/README.md','OmniWebServer/LICENSE','OmniWebServer/CREDITS.md']: z.write(f)
")
ls -la dist

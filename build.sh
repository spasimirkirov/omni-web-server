#!/bin/sh
# Builds dist/OmniWebServer/omni.exe and the Windows installer.
# Needs Go 1.22+ and NSIS 3 (makensis; on Debian/Ubuntu: apt install nsis).
set -eu
cd "$(dirname "$0")"
VERSION=$(sed -n 's/^const appVersion = "\(.*\)"/\1/p' app.go)
rm -rf dist/OmniWebServer
mkdir -p dist/OmniWebServer
go test ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o dist/OmniWebServer/omni.exe .
cp README.md dist/OmniWebServer/
(cd installer && makensis -V2 -DVERSION="$VERSION" -DSRCDIR=../dist/OmniWebServer -DOUTFILE="../dist/OmniWebServer-$VERSION-Setup.exe" omni.nsi)
(cd dist && rm -f "OmniWebServer-$VERSION-portable.zip" && python3 -c "
import zipfile,sys
z=zipfile.ZipFile('OmniWebServer-$VERSION-portable.zip','w',zipfile.ZIP_DEFLATED)
for f in ['OmniWebServer/omni.exe','OmniWebServer/README.md']: z.write(f)
")
ls -la dist

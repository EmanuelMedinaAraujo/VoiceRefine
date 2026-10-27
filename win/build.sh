#!/bin/bash
# Build VoiceRefine.exe for Windows from Linux. Copy win/dist/VoiceRefine.exe to the Windows machine
# and run it there: it installs itself, then downloads its speech engine and model. See README.md.
set -euo pipefail
cd "$(dirname "$0")/.."

# The version is the commit's time in UTC and the commit, so an older build never replaces a newer one.
version=$(TZ=UTC0 git log -1 --format=%cd --date=format-local:%Y%m%d.%H%M%S)-$(git rev-parse --short=7 HEAD)
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-H windowsgui -s -w -X main.version=$version" \
  -o win/dist/VoiceRefine.exe ./win
echo "VoiceRefine $version"
ls -lh win/dist/VoiceRefine.exe

#!/bin/bash
# Build VoiceRefine.exe for Windows from Linux. Copy win/dist/VoiceRefine.exe to the Windows machine
# and run it there: it installs itself, then downloads its speech engine and model. See README.md.
set -euo pipefail
cd "$(dirname "$0")/.."

GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-H windowsgui -s -w" \
  -o win/dist/VoiceRefine.exe ./win
ls -lh win/dist/VoiceRefine.exe

#!/bin/bash
# Install VoiceRefine for this user. No root needed. Run it again after a change; undo with ./uninstall.sh
# The dictation keys are not set here: the voxtype-setup block of ~/Projects/voxtype-bench routes
# them through VoiceRefine whenever ~/.local/bin/voicerefine exists.
set -euo pipefail
cd "$(dirname "$0")"

bin=~/.local/bin/voicerefine
voxtype_config=~/.config/voxtype/config.toml
plugin_dir=~/.config/omarchy/plugins/emu.voicerefine
shell_json=~/.config/omarchy/shell.json
hooks=~/.config/omarchy/hooks/post-update.d
units=~/.config/systemd/user

# 1. The program.
mkdir -p ~/.local/bin
go build -trimpath -o "$bin.new" ./cmd/voicerefine
mv "$bin.new" "$bin"

# 2. The text tap: Voxtype pipes each text through it before typing. A marked block at the end
#    of Voxtype's config, so the uninstall removes exactly this.
restart_voxtype=0
if ! grep -q '^# voicerefine begin' "$voxtype_config"; then
  if grep -q '^\[output\.post_process\]' "$voxtype_config"; then
    echo "Voxtype already has a post_process command in $voxtype_config; VoiceRefine needs that slot." >&2
    exit 1
  fi
  cp "$voxtype_config" "$voxtype_config.before-voicerefine"
  cat >>"$voxtype_config" <<EOF

# voicerefine begin: Voxtype hands each text to VoiceRefine, which saves it with the clip and gives it
# back unchanged. On a failure or after 300 ms Voxtype types its own text. See ~/Projects/VoiceRefine.
[output.post_process]
command = "$bin tap"
timeout_ms = 300
trim = false
# voicerefine end
EOF
  restart_voxtype=1
fi

# 3. The shell plugin, and its place in the bar next to the other widgets of this setup.
install -Dm644 shell/emu.voicerefine/* -t "$plugin_dir"
if [[ -f $shell_json ]] && ! jq -e '[.bar.layout[]?[]? | if type == "object" then .id else . end] | index("emu.voicerefine")' "$shell_json" >/dev/null; then
  cp "$shell_json" "$shell_json.bak.$(date +%s)"
  jq '.bar.layout.center += [{"id": "emu.voicerefine"}]' "$shell_json" >"$shell_json.new"
  mv "$shell_json.new" "$shell_json"
fi

# 4. The update check: after each Omarchy update, and once after each login.
install -Dm755 setup/check-voicerefine.hook -t "$hooks"
install -Dm644 setup/voicerefine-check.service setup/voicerefine-check.timer -t "$units"
systemctl --user daemon-reload
systemctl --user enable -q voicerefine-check.timer
"$bin" check --baseline

# Voxtype reads its config at start. Never restart it in the middle of a dictation or a meeting.
if ((restart_voxtype)) && systemctl --user is-active -q voxtype.service; then
  state=$(cat "$XDG_RUNTIME_DIR/voxtype/state" 2>/dev/null || true)
  meeting=$(cat "$XDG_RUNTIME_DIR/voxtype/meeting_state" 2>/dev/null || true)
  if [[ $state == idle && ( -z $meeting || $meeting == idle* ) ]]; then
    systemctl --user restart voxtype.service
  else
    echo "Voxtype is busy: restart it later with 'systemctl --user restart voxtype.service' to start saving."
  fi
fi

# The voxtype-setup block checks for the program when Hyprland loads its config.
hyprctl reload >/dev/null
hyprctl configerrors
"$bin" check

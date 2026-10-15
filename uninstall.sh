#!/bin/bash
# Undo ./install.sh. The saved dictations in ~/.local/share/voicerefine stay; delete them by hand.
set -uo pipefail

bin=~/.local/bin/voicerefine
voxtype_config=~/.config/voxtype/config.toml

systemctl --user disable -q --now voicerefine-check.timer 2>/dev/null
rm -f ~/.config/systemd/user/voicerefine-check.{service,timer}
systemctl --user daemon-reload
rm -f ~/.config/omarchy/hooks/post-update.d/check-voicerefine.hook

rm -rf ~/.config/omarchy/plugins/emu.voicerefine

if grep -q '^# voicerefine begin' "$voxtype_config"; then
  cp "$voxtype_config" "$voxtype_config.bak.$(date +%s)"
  # The block and the blank line install.sh put before it.
  sed -i -e '/^# voicerefine begin/,/^# voicerefine end/d' "$voxtype_config"
  sed -i -e '${/^$/d}' "$voxtype_config"
  if systemctl --user is-active -q voxtype.service; then
    state=$(cat "$XDG_RUNTIME_DIR/voxtype/state" 2>/dev/null)
    meeting=$(cat "$XDG_RUNTIME_DIR/voxtype/meeting_state" 2>/dev/null)
    if [[ $state == idle && ( -z $meeting || $meeting == idle* ) ]]; then
      systemctl --user restart voxtype.service
    else
      echo "Voxtype is busy: restart it later with 'systemctl --user restart voxtype.service'."
    fi
  fi
fi

# Without the program the voxtype-setup block binds the keys to voxtype-key again.
rm -f "$bin"
hyprctl reload >/dev/null
rm -rf ~/.local/state/voicerefine "$XDG_RUNTIME_DIR/voicerefine"
echo "VoiceRefine is removed. Your dictations stay in ~/.local/share/voicerefine."

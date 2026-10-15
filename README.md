# VoiceRefine

Collects my own dictation audio together with its transcript, with no extra effort while dictating, so the pairs can later fine-tune a speech-to-text model for personal use.

It sits beside [Voxtype](https://voxtype.io) on [Omarchy](https://omarchy.org): every dictation is recorded, the transcript and the model that produced it are logged, and corrections made afterwards are picked up as the better label. Fine-tuning itself is a later, manual step and not part of this repository yet.

Recordings and transcripts stay on the local machine and are never committed here.

## What gets saved

Dictation with F9 (English), F10 (German), SUPER+CTRL+X and SUPER+CTRL+ALT+X is saved. Nothing is saved for:

- SUPER+F9 and SUPER+F10, the private dictation keys,
- battery mode, also when it is forced with `voxtype-mode battery`,
- a running meeting (`voxtype-meeting`), because Voxtype then dictates in the meeting language,
- a key press while Voxtype is still turning the last dictation into text.

Each dictation gets one folder, for example `~/.local/share/voicerefine/2026/10/15/rec001/`:

| File | Content |
|---|---|
| `audio.wav` | 16 kHz mono, about 115 MB per hour of speech |
| `info.txt` | status (`untouched` or `corrected`), time, length, language, engine, model, power mode, trigger, whether Voxtype first had to wake up and where in the audio it started to listen, whether Voxtype cut the clip at its 60 s limit, whether the fix window was opened |
| `transcribed.txt` | the text the model wrote |
| `corrected.txt` | only when the text was fixed |

The numbers count up through the day. A take that gets no text (a cancel, a take too short for Voxtype, or a tap that missed its time limit) is deleted, and its number is used again.

## Fixing a dictation

F5, or a click on the bar widget, opens the fix window with the last dictation. Enter saves the fix as `corrected.txt`, Escape closes. The last dictation can be fixed until the next one starts. While the same window is still in front and the text is recent (20 s plus 0.1 s per character, at most 2 min), the fix replaces the typed text in the app; otherwise it goes to the clipboard. A dictation that is not fixed counts as correct.

The bar widget shows today's dictations and minutes and flashes a check mark for each save. It dims with a pause sign in battery mode, during a meeting and with dictation off, and turns to the warning colour when saving fails or the keys no longer lead here.

## How it works

```
key press ──► voicerefine key start ──► pw-record starts ──► voxtype-key start ──► Voxtype records
key release ─► voicerefine key stop ──► pw-record stops ───► voxtype-key stop ───► Voxtype transcribes
                                                                                         │
           folder: audio.wav, info.txt, transcribed.txt ◄── voicerefine tap ◄── post_process (text in, same text out)
                                                                                         │
                                                                                   Voxtype types it
```

- `voicerefine key` starts the recorder before it passes the key on to `voxtype-key`, so the clip holds everything Voxtype hears. When Voxtype first has to load its model, the clip keeps the whole sound and `info.txt` notes the wake-up.
- `voicerefine tap` is Voxtype's `post_process` command. It hands the text back unchanged, with a 300 ms limit after which Voxtype types its own text anyway.
- `voicerefine last` and `voicerefine fix` serve the fix window; `voicerefine status` serves the bar widget.
- `voicerefine check` runs after each Omarchy update (`~/.config/omarchy/hooks/post-update.d/check-voicerefine.hook`) and two minutes after each login (`voicerefine-check.timer`). It only reports, with a notification, when the keys or the tap no longer lead here, when the Voxtype setup scripts changed (accept them with `voicerefine check --baseline`), and when Voxtype upstream merged its own audio saving ([PR #328](https://github.com/peteonrails/voxtype/pull/328), [issue #209](https://github.com/peteonrails/voxtype/issues/209)).
- The shell plugin `emu.voicerefine` (in `shell/`) holds the bar widget and the fix window.

The dictation keys live in the voxtype-setup block of [voxtype-bench](https://github.com/EmanuelMedinaAraujo/voxtype-bench) (`setup/hypr-bindings.lua`). It runs `voicerefine key` when `~/.local/bin/voicerefine` exists and `voxtype-key` otherwise, and adds the private keys and F5 only with VoiceRefine installed.

## Install

Needs Go, `pw-record` (PipeWire), `wtype`, `wl-copy` and `jq`, all present on Omarchy with Voxtype set up by voxtype-bench.

```
./install.sh     # build, add the tap to Voxtype's config, the shell plugin, the hook and the login check
./uninstall.sh   # undo all of it; the saved dictations stay in ~/.local/share/voicerefine
```

Run voxtype-bench's `setup/install.sh` once so its key block is the one that knows about VoiceRefine. There is no backup of the clips: they exist only on this laptop.

## Development

```
go test ./...
```

`experiments/capture-lag/` holds the measurement that chose this recorder design: compared with what Voxtype itself captured, a recorder started from the key script begins at most 15 ms later and loses nothing at the end.

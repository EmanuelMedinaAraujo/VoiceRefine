# VoiceRefine for Windows

The same dictation and saving as on Linux, as one program: `VoiceRefine.exe`.

Voxtype runs only on Linux, so on Windows VoiceRefine does Voxtype's part too. It records while a dictation key is held, has [whisper.cpp](https://github.com/ggml-org/whisper.cpp) write the text (whisper.cpp is the engine behind Voxtype's Whisper), and types the text into the window in front. Meetings are left out.

## Install

```
./win/build.sh        # on Linux: builds win/dist/VoiceRefine.exe
```

Copy `VoiceRefine.exe` to the Windows machine and double-click it. You don't need administrator rights. It:

1. copies itself to `%LOCALAPPDATA%\Programs\VoiceRefine\`, starts with Windows from then on, and appears under Settings › Apps,
2. downloads the speech engine and the model once, checking each file against a pinned SHA-256:
   - with an NVIDIA driver: whisper.cpp with CUDA 12.4, 685 MB,
   - otherwise: whisper.cpp for the processor, 9 MB,
   - the model `large-v3-turbo-q5_0`, 574 MB. It is the model Voxtype uses for German, and here it does English too.
3. shows a notification when dictation is ready.

If Windows lacks the Visual C++ runtime that whisper.cpp needs, VoiceRefine runs Microsoft's installer for it. That installer asks once for permission. If the download breaks off, VoiceRefine continues it every minute.

To update, run a newer `VoiceRefine.exe` the same way. It replaces the running copy and keeps the engine, the model and the dictations.

Windows may warn about an unknown publisher when the file came from the internet. Choose *More info › Run anyway*.

## Keys

The same as on Linux:

| Key | |
|---|---|
| hold F9 / F10 | dictate English / German, saved |
| hold Win+F9 / Win+F10 | the same, private: nothing is saved |
| Ctrl+Win+X / Ctrl+Win+Alt+X | start and stop English / German, saved |
| F5 | fix the last dictation, right after it was typed |

These keys never reach the app in front. F5 is the exception: once the last dictation can no longer be fixed in place, it reaches the app again, so a browser still reloads with it, just as on Linux.

The text goes into the window that was in front when the key went up. If another window comes to the front while the text is being written, VoiceRefine doesn't type it there and puts it on the clipboard instead. Text that VoiceRefine puts on the clipboard stays out of the Windows clipboard history and the cloud clipboard.

A tray icon (a microphone) shows the state: grey while it sets up or loads, blue when ready, red while it listens, amber while it writes the text. Click it for the state; right-click it to fix the last dictation, open the saved dictations or the log, uninstall, or quit. Windows 11 may hide new tray icons under the ^ arrow; drag it out to keep it in view.

## What gets saved

The saved folders match the Linux ones, under `%LOCALAPPDATA%\VoiceRefine\recordings\`, for example `2026\10\15\rec001\` with `audio.wav`, `info.txt`, `transcribed.txt` and, once fixed, `corrected.txt`. In `info.txt` the engine is `whisper`, the model `large-v3-turbo-q5_0`, and `power_mode` stays empty. Here the recording is exactly the audio the model received, so `woke_up` is always `no` and `listen_at_s` always `0.000`.

A dictation is not saved when it is private, when it is shorter than 0.3 s, or when it has no words. A dictation stops by itself after 60 s, like Voxtype, and is then marked `cut_at_limit: yes`.

## Fixing a dictation

F5 opens the fix window with the last dictation, as on Linux, while the window it was typed into is still in front, no dictation started since, and the text is recent (20 s plus 0.1 s per character, at most 2 min). Enter saves the fix as `corrected.txt` and replaces the typed text, Shift+Enter starts a new line, and Escape or a click elsewhere closes the window. The tray menu opens the fix window at any time until the next dictation starts; a fix that can no longer replace the text goes to the clipboard.

## Uninstall

Settings › Apps › VoiceRefine › Uninstall, or *Uninstall VoiceRefine…* in the tray menu. This removes the program, the engine and the model. The dictations and the logs stay in `%LOCALAPPDATA%\VoiceRefine\`.

## When something is off

- No text, and the notification says the microphone failed: allow desktop apps to use the microphone under Settings › Privacy & security › Microphone. VoiceRefine records from the default recording device.
- Text appears in no app: Windows doesn't let normal programs type into windows that run as administrator.
- Everything else: `%LOCALAPPDATA%\VoiceRefine\voicerefine.log` and `engine.log`, which the tray menu opens.

## Development

The code is in this folder (package `main`). It shares `internal/clip` with the Linux program for the folders and files. `go test ./...` runs its tests on Linux as well. The Win32 parts are in the `*_windows.go` files.

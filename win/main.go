// VoiceRefine for Windows: dictation with the keys of the Linux setup, saved the same way.
//
// Voxtype runs only on Linux, so on Windows this one program does its part too: it records while a
// dictation key is held, has whisper.cpp (the engine behind Voxtype's Whisper) write the text, and
// types it. Each dictation not made private is saved as on Linux, in folders like
// %LOCALAPPDATA%\VoiceRefine\recordings\2026\10\15\rec001\ with audio.wav, info.txt,
// transcribed.txt and, once fixed, corrected.txt. Build it with build.sh; see README.md.
package main

func main() { run() }

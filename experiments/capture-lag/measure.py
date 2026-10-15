#!/usr/bin/env python3
# /// script
# dependencies = ["numpy"]
# ///
"""Measure how much audio a sidecar recorder loses compared with what Voxtype itself captured.

Runs a private Voxtype daemon (own runtime dir, own config) so the real one is never touched and
nothing is typed anywhere. The daemon's whisper engine is set to "remote" and pointed at a local
HTTP server, which receives the exact WAV Voxtype recorded: that is the ground truth.

Three sidecar designs record the same dictation and are compared with it:
  state  start pw-record when the state file says "recording", stop on "transcribing"
  key    start pw-record just before the start command is sent, stop it as the stop command is sent
  hook   start pw-record from Voxtype's pre_recording_command, stop on "transcribing"
  ring   a recorder that is always running; cut the clip by event time with a pre-roll

Source "virtual" plays seeded noise into a virtual microphone, so every clip can be located in the
source to the sample. Source "mic" uses the real default microphone and lets it suspend between
trials, which is the usual state when a dictation starts; clips are then located in each other.

Usage: uv run measure.py [--source virtual|mic] [--trials N] [--hook-sleep-ms MS] [--keep]
"""

import argparse
import json
import os
import random
import re
import shutil
import signal
import statistics
import subprocess
import tempfile
import threading
import time
import wave
from datetime import datetime
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

import numpy as np

RATE = 16000
VOXTYPE = "/usr/lib/voxtype/voxtype-onnx-avx512"
VIRTUAL_SINK = "vr_capture_lag_sink"
VIRTUAL_MIC = "vr_capture_lag_mic"
REAL_RUNTIME = os.environ["XDG_RUNTIME_DIR"]


def write_wav(path, samples):
    with wave.open(str(path), "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(RATE)
        w.writeframes(samples.astype("<i2").tobytes())


def read_wav(path):
    """Return mono float samples at RATE. pw-record leaves a zero length in the header when killed."""
    data = Path(path).read_bytes()
    pos = data.find(b"data")
    fmt = data.find(b"fmt ")
    channels = int.from_bytes(data[fmt + 10 : fmt + 12], "little")
    rate = int.from_bytes(data[fmt + 12 : fmt + 16], "little")
    bits = int.from_bytes(data[fmt + 22 : fmt + 24], "little")
    kind = int.from_bytes(data[fmt + 8 : fmt + 10], "little")
    body = data[pos + 8 :]
    if kind == 3:
        x = np.frombuffer(body[: len(body) // 4 * 4], "<f4").astype(np.float64)
    else:
        width = bits // 8
        x = np.frombuffer(body[: len(body) // width * width], f"<i{width}").astype(np.float64)
    x = x.reshape(-1, channels).mean(axis=1)
    assert rate == RATE, f"{path}: expected {RATE} Hz, got {rate}"
    return x


def locate(clip, ref):
    """Offset of clip inside ref (samples, may be negative) and how far the peak stands out."""
    n = 1 << int(np.ceil(np.log2(len(clip) + len(ref))))
    corr = np.fft.irfft(np.fft.rfft(ref, n) * np.conj(np.fft.rfft(clip, n)), n)
    peak = int(np.argmax(np.abs(corr)))
    strength = float(np.abs(corr[peak]) / (np.median(np.abs(corr)) + 1e-12))
    return (peak - n if peak > n // 2 else peak), strength


class Uploads(BaseHTTPRequestHandler):
    """Stands in for an OpenAI-compatible transcription API and keeps the audio it is sent."""

    received = []

    def do_POST(self):
        body = self.rfile.read(int(self.headers["Content-Length"]))
        # The WAV is one field of a multipart form; its RIFF header says where it ends.
        start = body.find(b"RIFF")
        size = int.from_bytes(body[start + 4 : start + 8], "little") + 8 if start >= 0 else 0
        Uploads.received.append((time.time(), body[start : start + size] if start >= 0 else b""))
        reply = json.dumps({"text": "ok"}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(reply)))
        self.end_headers()
        self.wfile.write(reply)

    def log_message(self, *_):
        pass


class Ring:
    """An always-on recorder: reads raw audio from parec and remembers when each chunk arrived."""

    def __init__(self, device):
        cmd = ["parec", "--rate", str(RATE), "--channels", "1", "--format", "s16le", "--latency-msec", "20", "--raw"]
        if device:
            cmd += ["--device", device]
        self.proc = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        self.chunks, self.times, self.counts, self.total = [], [], [], 0
        threading.Thread(target=self._read, daemon=True).start()

    def _read(self):
        fd = self.proc.stdout.fileno()
        while data := os.read(fd, 65536):
            data = data[: len(data) // 2 * 2]
            self.chunks.append(data)
            self.total += len(data) // 2
            self.times.append(time.time())
            self.counts.append(self.total)

    def index_at(self, when):
        """Samples that had arrived by `when`."""
        i = np.searchsorted(self.times, when, side="right")
        return self.counts[i - 1] if i else 0

    def cut(self, start, end):
        while self.times[-1] < end:
            time.sleep(0.01)
        audio = np.frombuffer(b"".join(self.chunks), "<i2").astype(np.float64)
        return audio[self.index_at(start) : self.index_at(end)]

    def stop(self):
        self.proc.terminate()


def wait_state(state_file, wanted, timeout=15):
    """Busy-poll the state file; an inotify watch would see the change at the same moment."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            if state_file.read_text().strip() == wanted:
                return time.time()
        except FileNotFoundError:
            pass
        time.sleep(0.0005)
    raise TimeoutError(f"state never became {wanted!r}")


def recorder(path, device):
    cmd = ["pw-record", "--rate", str(RATE), "--channels", "1", "--format", "s16"]
    if device:
        cmd += ["--target", device]
    return cmd + [str(path)]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--source", choices=["virtual", "mic"], default="virtual")
    ap.add_argument("--trials", type=int, default=30)
    ap.add_argument("--hook-sleep-ms", type=int, default=0, help="make pre_recording_command slow, to see if Voxtype waits for it")
    ap.add_argument("--post-process", default=None, help="run this as Voxtype's post_process command (text on stdin, text on stdout)")
    ap.add_argument("--post-process-timeout-ms", type=int, default=500)
    ap.add_argument("--idle", type=float, default=None, help="seconds between trials (default 0.8 virtual, 7 mic)")
    ap.add_argument("--keep", action="store_true", help="keep the work directory with all clips")
    args = ap.parse_args()

    virtual = args.source == "virtual"
    device = VIRTUAL_MIC if virtual else None
    idle = args.idle if args.idle is not None else (0.8 if virtual else 7.0)
    work = Path(tempfile.mkdtemp(prefix="vr-capture-lag-"))
    runtime = work / "rt"
    runtime.mkdir()
    state_file = work / "state"
    env = dict(os.environ, XDG_RUNTIME_DIR=str(runtime), PIPEWIRE_RUNTIME_DIR=REAL_RUNTIME,
               PULSE_SERVER=f"unix:{REAL_RUNTIME}/pulse/native")
    # Without a display the test daemon cannot type or touch the clipboard, whatever its output mode does.
    for name in ("VOXTYPE_ENGINE", "WAYLAND_DISPLAY", "DISPLAY", "HYPRLAND_INSTANCE_SIGNATURE"):
        env.pop(name, None)
    if virtual:
        env["PIPEWIRE_NODE"] = VIRTUAL_MIC  # Voxtype opens ALSA "default", which is PipeWire's ALSA plugin
    procs, modules = [], []
    rows = []

    try:
        server = HTTPServer(("127.0.0.1", 0), Uploads)
        threading.Thread(target=server.serve_forever, daemon=True).start()

        if virtual:
            # A silent sink to play into, and a source that hands its content to anything recording.
            for module in (["module-null-sink", f"sink_name={VIRTUAL_SINK}", "channel_map=mono"],
                           ["module-remap-source", f"master={VIRTUAL_SINK}.monitor", f"source_name={VIRTUAL_MIC}",
                            "channel_map=mono", "master_channel_map=mono"]):
                modules.append(subprocess.check_output(["pactl", "load-module", *module], text=True).strip())
            rng = np.random.default_rng(7)
            source = rng.standard_normal(int(RATE * (20 + args.trials * (idle + 5)))) * 4000
            write_wav(work / "source.wav", source)
            time.sleep(0.5)
            procs.append(subprocess.Popen(["pw-play", "--target", VIRTUAL_SINK, str(work / "source.wav")]))

        hook = work / "hook.sh"
        hook.write_text(f"""#!/bin/bash
echo "$1 $(date +%s.%N)" >>{work}/hooks.log
if [[ $1 == pre_recording ]]; then
  setsid {' '.join(recorder(f'{work}/hook_$(cat {work}/trial).wav', device))} </dev/null >/dev/null 2>&1 &
  echo $! >{work}/hook.pid
  sleep {args.hook_sleep_ms / 1000}
fi
""")
        hook.chmod(0o755)
        (work / "config.toml").write_text(f"""state_file = "{state_file}"
engine = "whisper"

[hotkey]
enabled = false

[audio]
device = "default"
sample_rate = {RATE}
max_duration_secs = 60
pause_media = false

[audio.feedback]
enabled = false

[whisper]
mode = "remote"
remote_endpoint = "http://127.0.0.1:{server.server_port}"
remote_model = "whisper-1"
model = "base.en"
language = "en"

[output]
mode = "file"
fallback_to_clipboard = false
pre_recording_command = "{hook} pre_recording"
pre_output_command = "{hook} pre_output"
post_output_command = "{hook} post_output"

[output.notification]
on_recording_start = false
on_recording_stop = false
on_transcription = false

[osd]
enabled = false
""" + (f"""
[output.post_process]
command = "{args.post_process}"
timeout_ms = {args.post_process_timeout_ms}
""" if args.post_process else ""))
        log = open(work / "daemon.log", "w")
        daemon = subprocess.Popen([VOXTYPE, "-c", str(work / "config.toml"), "-v", "daemon"], env=env, stdout=log, stderr=log)
        procs.append(daemon)
        wait_state(state_file, "idle", 30)

        follow = subprocess.Popen([VOXTYPE, "-c", str(work / "config.toml"), "status", "--follow", "--format", "json"],
                                  env=env, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
        procs.append(follow)
        follow_seen = []
        threading.Thread(target=lambda: [follow_seen.append((time.time(), line)) for line in follow.stdout], daemon=True).start()

        ring = Ring(device) if virtual else None  # on the real mic it would keep the device awake
        time.sleep(1.5)

        for i in range(args.trials):
            time.sleep(idle + random.uniform(0, 0.4))
            (work / "trial").write_text(str(i))
            uploads_before = len(Uploads.received)

            # A key wrapper starts its recorder, then passes the key on; it never waits for either.
            key_rec = subprocess.Popen(recorder(work / f"key_{i}.wav", device), stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            t_start = time.time()
            subprocess.Popen([VOXTYPE, "-c", str(work / "config.toml"), "record", "start", f"--file={work}/out.txt"], env=env)
            t_recording = wait_state(state_file, "recording")
            state_rec = subprocess.Popen(recorder(work / f"state_{i}.wav", device), stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

            time.sleep(random.uniform(1.5, 2.5))
            t_stop = time.time()
            daemon.send_signal(signal.SIGUSR2)
            key_rec.send_signal(signal.SIGINT)  # the wrapper only knows the key was released
            t_transcribing = wait_state(state_file, "transcribing")
            state_rec.send_signal(signal.SIGINT)
            os.kill(int((work / "hook.pid").read_text()), signal.SIGINT)
            state_rec.wait()
            key_rec.wait()
            wait_state(state_file, "idle")
            deadline = time.time() + 10
            while len(Uploads.received) == uploads_before and time.time() < deadline:
                time.sleep(0.01)
            if len(Uploads.received) == uploads_before:
                print(f"trial {i}: Voxtype sent no audio, skipped")
                continue
            (work / f"voxtype_{i}.wav").write_bytes(Uploads.received[-1][1])
            if ring:
                write_wav(work / f"ring_{i}.wav", ring.cut(t_recording - 0.5, t_transcribing + 0.2))

            follow_rec = next((t for t, line in follow_seen if t >= t_start and "recording" in line), None)
            follow_tr = next((t for t, line in follow_seen if t >= t_stop and "transcribing" in line), None)
            rows.append(dict(trial=i, signal_to_state_ms=(t_recording - t_start) * 1000,
                             stop_to_state_ms=(t_transcribing - t_stop) * 1000,
                             follow_rec_ms=(follow_rec - t_recording) * 1000 if follow_rec else None,
                             follow_tr_ms=(follow_tr - t_transcribing) * 1000 if follow_tr else None))
            time.sleep(0.3)  # let the hook's recorder flush its file

        if ring:
            ring.stop()
    finally:
        for p in procs:
            p.terminate()
        for module in reversed(modules):
            subprocess.run(["pactl", "unload-module", module])

    # Positions are samples in the source (virtual) or relative to Voxtype's own clip (mic).
    designs = ["state", "key", "hook"] + (["ring"] if virtual else [])
    for row in rows:
        i = row["trial"]
        vox = read_wav(work / f"voxtype_{i}.wav")
        row["voxtype_s"] = len(vox) / RATE
        if virtual:
            vox_start, row["voxtype_peak"] = locate(vox, source)
        else:
            vox_start = 0
        for name in designs:
            clip = read_wav(work / f"{name}_{i}.wav")
            start, peak = locate(clip, source) if virtual else locate(clip, vox)
            row[f"{name}_head_ms"] = (start - vox_start) / RATE * 1000
            row[f"{name}_tail_ms"] = ((start + len(clip)) - (vox_start + len(vox))) / RATE * 1000
            row[f"{name}_peak"] = peak

    # How long the text waits between leaving the model and being written out, from the daemon's own log.
    ansi = re.compile(r"\x1b\[[0-9;]*m")
    stamp = lambda line: datetime.fromisoformat(line.split()[0].replace("Z", "+00:00")).timestamp()
    ready, output_waits = None, []
    for line in map(lambda l: ansi.sub("", l), (work / "daemon.log").read_text().splitlines()):
        if " Transcribed: " in line:
            ready = stamp(line)
        elif "wrote transcription to" in line and ready:
            output_waits.append((stamp(line) - ready) * 1000)
            ready = None

    (work / "results.json").write_text(json.dumps(rows, indent=1))
    hooks = (work / "hooks.log").read_text() if (work / "hooks.log").exists() else ""

    def summary(key):
        values = sorted(r[key] for r in rows if r.get(key) is not None)
        if not values:
            return "n/a"
        return f"median {statistics.median(values):7.1f}   min {values[0]:7.1f}   max {values[-1]:7.1f}   (n={len(values)})"

    print(f"\nsource={args.source}  trials={len(rows)}  hook sleep={args.hook_sleep_ms} ms  idle={idle}s")
    print("Voxtype clip length (s):        ", summary("voxtype_s"))
    print("start signal -> state file (ms):", summary("signal_to_state_ms"))
    print("stop signal  -> state file (ms):", summary("stop_to_state_ms"))
    print("status --follow after state file, recording (ms):   ", summary("follow_rec_ms"))
    print("status --follow after state file, transcribing (ms):", summary("follow_tr_ms"))
    print("\nhead: clip starts this many ms AFTER Voxtype's audio starts (positive = audio lost)")
    print("tail: clip ends this many ms AFTER Voxtype's audio ends (negative = audio lost)")
    if virtual:
        print("  Voxtype's own clip, match strength:", summary("voxtype_peak"))
    for name in designs:
        print(f"  {name:5} head:", summary(f"{name}_head_ms"))
        print(f"  {name:5} tail:", summary(f"{name}_tail_ms"))
        print(f"  {name:5} match strength:", summary(f"{name}_peak"))
    if output_waits:
        output_waits.sort()
        print(f"\ntranscript ready -> written out (ms), post_process={args.post_process!r}: "
              f"median {statistics.median(output_waits):.1f}   min {output_waits[0]:.1f}   max {output_waits[-1]:.1f}   (n={len(output_waits)})")
    print(f"\nhook calls logged: {len(hooks.splitlines())}")
    if args.keep:
        print("kept:", work)
    else:
        shutil.rmtree(work)


if __name__ == "__main__":
    main()

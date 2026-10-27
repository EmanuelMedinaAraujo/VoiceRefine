package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EmanuelMedinaAraujo/VoiceRefine/internal/clip"
	"github.com/EmanuelMedinaAraujo/VoiceRefine/internal/voxtype"
)

// fakeRecorder writes one second of audio as soon as it starts.
type fakeRecorder struct {
	mu    sync.Mutex
	alive map[int]bool
	next  int
}

func (r *fakeRecorder) Start(path string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := os.WriteFile(path, make([]byte, clip.SampleRate*2), 0o600); err != nil {
		return 0, err
	}
	r.next++
	r.alive[r.next] = true
	return r.next, nil
}

func (r *fakeRecorder) Alive(pid int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.alive[pid]
}

func (r *fakeRecorder) Stop(pid int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.alive[pid] = false
}

func (r *fakeRecorder) Wait(int, time.Duration) bool { return true }

// rig is a fake Voxtype: its voxtype-key moves the state file the way the daemon does.
type rig struct {
	t         *testing.T
	a         *App
	rec       *fakeRecorder
	asleep    bool // the next start first wakes the daemon, like after an idle unload
	mu        sync.Mutex
	commands  []string
	typed     []string
	window    string // the focused window's Hyprland address
	clipboard string
	binds     string // hyprctl binds -j
	notified  []string
}

func newRig(t *testing.T) *rig {
	root := t.TempDir()
	r := &rig{t: t, rec: &fakeRecorder{alive: map[int]bool{}}, window: "0xabc"}
	vox := voxtype.Paths{Runtime: filepath.Join(root, "run"), Config: filepath.Join(root, "config"), Bin: filepath.Join(root, "bin")}
	os.MkdirAll(filepath.Join(vox.Runtime, "voxtype"), 0o700)
	os.MkdirAll(vox.Config, 0o700)
	os.WriteFile(vox.ConfigFile(), []byte("[cohere]\nmodel = \"cohere-transcribe-q4\"\n"), 0o600)
	r.a = &App{
		Vox: vox, Store: clip.Store{Root: filepath.Join(root, "data")},
		Run: filepath.Join(vox.Runtime, "voicerefine"), State: filepath.Join(root, "state"), Home: root,
		Now: time.Now, Recorder: r.rec, Command: r.command,
	}
	r.setState("idle")
	r.write("voxtype-power-mode", "power")
	t.Setenv("VOXTYPE_ENGINE", "cohere")
	r.binds = `[{"key": "F9", "modmask": 0, "release": false, "description": "Start dictation (push-to-talk, saved by VoiceRefine)"}]`
	os.WriteFile(vox.ConfigFile(), []byte("[cohere]\nmodel = \"cohere-transcribe-q4\"\n\n[output.post_process]\ncommand = \"/home/x/.local/bin/voicerefine tap\"\n"), 0o600)
	return r
}

func (r *rig) write(name, data string) {
	os.WriteFile(filepath.Join(r.a.Vox.Runtime, name), []byte(data+"\n"), 0o600)
}

func (r *rig) setState(s string) { os.WriteFile(r.a.Vox.StateFile(), []byte(s), 0o600) }

func (r *rig) command(stdin, name string, args ...string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = append(r.commands, filepath.Base(name)+" "+strings.Join(args, " "))
	switch filepath.Base(name) {
	case "voxtype-key":
		switch args[0] {
		case "start":
			if r.asleep {
				time.Sleep(50 * time.Millisecond) // loading the model
				r.asleep = false
			}
			r.setState("recording")
		case "stop":
			r.setState("transcribing")
		case "cancel":
			r.setState("idle")
		}
	case "hyprctl":
		if args[0] == "binds" {
			return r.binds, nil
		}
		return `{"address": "` + r.window + `"}`, nil
	case "notify-send":
		r.notified = append(r.notified, strings.Join(args, " "))
	case "wtype":
		r.typed = append(r.typed, strings.Join(args, " ")+stdin)
	case "wl-copy":
		r.clipboard = stdin
	}
	return "", nil
}

func (r *rig) key(args ...string) {
	if err := r.a.key(args); err != nil {
		r.t.Fatal(err)
	}
}

// release runs the key release, which waits for the text, while Voxtype transcribes and taps text.
func (r *rig) release(text string, args ...string) {
	done := make(chan struct{})
	go func() { r.key(args...); close(done) }()
	time.Sleep(50 * time.Millisecond)
	if text != "" {
		r.tap(text)
	}
	r.setState("idle")
	<-done
}

func (r *rig) tap(text string) {
	var out bytes.Buffer
	r.a.In, r.a.Out = strings.NewReader(text), &out
	if err := r.a.tap(); err != nil {
		r.t.Fatal(err)
	}
	if out.String() != text {
		r.t.Fatalf("tap changed the text: %q -> %q", text, out.String())
	}
}

func (r *rig) saved() []clip.Saved { return r.a.Store.Day(time.Now()) }

func (r *rig) folders() int {
	entries, _ := os.ReadDir(r.a.Store.DayDir(time.Now()))
	return len(entries)
}

func TestPushToTalkSavesAudioTextAndInfo(t *testing.T) {
	r := newRig(t)
	r.key("start", "en")
	if r.a.pending() == nil {
		t.Fatal("no folder waits for the text")
	}
	r.release("Hello world.", "stop", "en")

	saved := r.saved()
	if len(saved) != 1 {
		t.Fatalf("%d clips saved, want 1", len(saved))
	}
	dir, info := saved[0].Dir, saved[0].Info
	if text, _ := clip.ReadText(dir, clip.TranscribedFile); text != "Hello world." {
		t.Fatalf("transcribed.txt = %q", text)
	}
	if info.Status != clip.Untouched || info.Engine != "cohere" || info.Model != "cohere-transcribe-q4" ||
		info.Language != "en" || info.PowerMode != "power" || info.Trigger != "push-to-talk" || info.WokeUp || info.CutAtLimit {
		t.Fatalf("info = %+v", info)
	}
	if info.Length != time.Second {
		t.Fatalf("length %v, want the full recording", info.Length)
	}
	if _, err := os.Stat(filepath.Join(dir, clip.AudioFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, clip.RawFile)); err == nil {
		t.Fatal("audio.raw left behind")
	}
	if l := r.a.last(); l == nil || l.Dir != dir || l.InApp != "Hello world." || l.Window != "0xabc" {
		t.Fatalf("last = %+v", l)
	}
	if got := strings.Join(r.commands, "|"); !strings.Contains(got, "voxtype-key start en") || !strings.Contains(got, "voxtype-key stop en") {
		t.Fatalf("keys not passed on: %s", got)
	}
}

func TestATakeWithoutTextIsDeleted(t *testing.T) {
	r := newRig(t)
	r.key("start", "en")
	r.release("", "stop", "en")
	if n := r.folders(); n != 0 {
		t.Fatalf("%d folders left, want the folder deleted", n)
	}
	if r.a.pending() != nil {
		t.Fatal("pending.json left behind")
	}
}

func TestCancelDeletesTheFolder(t *testing.T) {
	r := newRig(t)
	r.key("start", "de")
	r.key("cancel", "de")
	if n := r.folders(); n != 0 {
		t.Fatalf("%d folders left after a cancel", n)
	}
}

func TestPrivateBatteryAndMeetingSaveNothing(t *testing.T) {
	cases := map[string]func(*rig){
		"private": func(r *rig) {},
		"battery": func(r *rig) { r.write("voxtype-power-mode", "battery") },
		"meeting": func(r *rig) {
			r.write("voxtype-meeting.toml", "")
			os.WriteFile(filepath.Join(r.a.Vox.Runtime, "voxtype", "meeting_state"), []byte("recording"), 0o600)
		},
	}
	for name, prepare := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			prepare(r)
			if name == "private" {
				r.key("--private", "start", "en")
			} else {
				r.key("start", "en")
			}
			r.release("Secret.", "stop", "en")
			if n := r.folders(); n != 0 {
				t.Fatalf("%d folders saved", n)
			}
			if !strings.Contains(strings.Join(r.commands, "|"), "voxtype-key start en") {
				t.Fatal("the key did not reach voxtype-key")
			}
		})
	}
}

func TestWakingUpIsMarked(t *testing.T) {
	r := newRig(t)
	os.Remove(r.a.Vox.StateFile()) // unloaded after an idle hour
	r.asleep = true
	r.key("start", "en")
	r.release("Good morning.", "stop", "en")
	saved := r.saved()
	if len(saved) != 1 || !saved[0].Info.WokeUp || saved[0].Info.ListenAt < 40*time.Millisecond {
		t.Fatalf("saved = %+v, want a woken clip that notes when Voxtype listened", saved)
	}
}

func TestBatteryAfterWakingUpSavesNothing(t *testing.T) {
	r := newRig(t)
	os.Remove(r.a.Vox.StateFile())
	r.write("voxtype-power-mode", "battery") // the daemon starts in battery mode
	r.key("start", "en")
	if r.a.pending() != nil || r.folders() != 0 {
		t.Fatal("a battery dictation kept its folder")
	}
}

func TestAClipVoxtypeEndedAtItsLimitIsCutThere(t *testing.T) {
	r := newRig(t)
	r.key("start", "en")
	p := r.a.pending()
	// Voxtype stops on its own half a second in; the key is still held.
	r.setState("transcribing")
	os.Chtimes(r.a.Vox.StateFile(), time.Now(), p.Started.Add(500*time.Millisecond))
	r.tap("Long text.")
	if r.rec.Alive(p.PID) {
		t.Fatal("the recorder still runs")
	}
	r.setState("idle")
	r.key("stop", "en") // the late release only passes through

	saved := r.saved()
	if len(saved) != 1 || !saved[0].Info.CutAtLimit || saved[0].Info.Length != 500*time.Millisecond {
		t.Fatalf("saved = %+v, want a clip cut at 0.5 s", saved)
	}
}

func TestToggleStartsAndStops(t *testing.T) {
	r := newRig(t)
	r.key("toggle", "en")
	r.release("Toggled.", "toggle", "en")
	saved := r.saved()
	if len(saved) != 1 || saved[0].Info.Trigger != "toggle" {
		t.Fatalf("saved = %+v", saved)
	}
}

func TestTheTapPassesTextThroughWithoutADictation(t *testing.T) {
	r := newRig(t)
	r.tap("Typed by a meeting or a private key.")
	if r.folders() != 0 {
		t.Fatal("tap saved text with no folder waiting")
	}
}

func (r *rig) fix(dir, text string) {
	r.a.In = strings.NewReader(text)
	if err := r.a.fix([]string{dir}); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) lastOut(args ...string) string {
	var out bytes.Buffer
	r.a.Out = &out
	if err := r.a.lastCmd(args); err != nil {
		r.t.Fatal(err)
	}
	return out.String()
}

func dictate(r *rig, text string) string {
	r.key("start", "en")
	r.release(text, "stop", "en")
	return r.a.last().Dir
}

func TestFixReplacesTheTextInTheSameWindow(t *testing.T) {
	r := newRig(t)
	dir := dictate(r, "Helo wörld")
	if out := r.lastOut("--opened"); !strings.Contains(out, `"fixable":true`) || !strings.Contains(out, "Helo wörld") {
		t.Fatalf("last = %s", out)
	}
	r.fix(dir, "Hello world\n")

	if text, _ := clip.ReadText(dir, clip.CorrectedFile); text != "Hello world" {
		t.Fatalf("corrected.txt = %q", text)
	}
	info, _ := clip.ReadInfo(dir)
	if info.Status != clip.Corrected || !info.FixOpened {
		t.Fatalf("info = %+v", info)
	}
	if len(r.typed) != 2 || strings.Count(r.typed[0], "BackSpace") != 10 || r.typed[1] != "-d 1 -Hello world" {
		t.Fatalf("typed = %q, want 10 backspaces then the fix", r.typed)
	}
	if l := r.a.last(); l.InApp != "Hello world" {
		t.Fatalf("the app now holds %q", l.InApp)
	}
	// Fixing again replaces the fixed text, and fixing back to the model's text undoes the fix.
	r.fix(dir, "Helo wörld")
	if _, ok := clip.ReadText(dir, clip.CorrectedFile); ok {
		t.Fatal("corrected.txt kept after fixing back to the transcribed text")
	}
	if info, _ := clip.ReadInfo(dir); info.Status != clip.Untouched {
		t.Fatalf("status %s", info.Status)
	}
}

func TestFixInAnotherWindowGoesToTheClipboard(t *testing.T) {
	r := newRig(t)
	dir := dictate(r, "Helo")
	r.window = "0xother"
	r.fix(dir, "Hello")
	if len(r.typed) != 0 || r.clipboard != "Hello" {
		t.Fatalf("typed %q, clipboard %q", r.typed, r.clipboard)
	}
	if text, _ := clip.ReadText(dir, clip.CorrectedFile); text != "Hello" {
		t.Fatal("the fix was not saved")
	}
}

func TestFixAfterTheTimeLimitGoesToTheClipboard(t *testing.T) {
	r := newRig(t)
	dir := dictate(r, "Helo")
	r.a.Now = func() time.Time { return time.Now().Add(replaceLimit("Helo") + time.Second) }
	r.fix(dir, "Hello")
	if len(r.typed) != 0 || r.clipboard != "Hello" {
		t.Fatalf("typed %q, clipboard %q", r.typed, r.clipboard)
	}
}

func TestOnlyTheLastDictationCanBeFixed(t *testing.T) {
	r := newRig(t)
	dictate(r, "First.")
	r.key("--private", "start", "en") // even a private dictation ends the chance to fix
	r.release("Private.", "stop", "en")
	if out := r.lastOut(); !strings.Contains(out, `"fixable":false`) {
		t.Fatalf("last = %s", out)
	}
}

func TestF5OpensTheFixOnlyWhileItCanReplaceTheText(t *testing.T) {
	const summon, pass = `omarchy-shell shell summon emu.voicerefine {}`, `hyprctl dispatch hl.dsp.send_shortcut({ mods = "", key = "F5" })`
	for _, tc := range []struct {
		name  string
		setup func(r *rig)
		want  string
	}{
		{"just typed", func(r *rig) { dictate(r, "Helo") }, summon},
		{"nothing saved", func(r *rig) {}, pass},
		{"another window", func(r *rig) { dictate(r, "Helo"); r.window = "0xother" }, pass},
		{"too late", func(r *rig) {
			dictate(r, "Helo")
			r.a.Now = func() time.Time { return time.Now().Add(replaceLimit("Helo") + time.Second) }
		}, pass},
		{"newer dictation", func(r *rig) { dictate(r, "Helo"); r.key("start", "en") }, pass},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			tc.setup(r)
			r.commands = nil
			if err := r.a.fixKey(nil); err != nil {
				t.Fatal(err)
			}
			if got := r.commands[len(r.commands)-1]; got != tc.want {
				t.Fatalf("F5 ran %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAnEmptyFixChangesNothing(t *testing.T) {
	r := newRig(t)
	dir := dictate(r, "Text.")
	r.fix(dir, "  \n")
	if _, ok := clip.ReadText(dir, clip.CorrectedFile); ok || len(r.typed) != 0 {
		t.Fatal("an emptied box was saved as a fix")
	}
}

func TestWiringProblemsFindKeysOrATapThatLeadElsewhere(t *testing.T) {
	r := newRig(t)
	if p := r.a.wiringProblems(); len(p) != 0 {
		t.Fatalf("problems %q", p)
	}
	r.binds = `[{"key": "F9", "modmask": 0, "release": false, "description": "Start dictation (push-to-talk)"}]`
	if p := strings.Join(r.a.wiringProblems(), "\n"); !strings.Contains(p, "keys") {
		t.Fatalf("problems %q", p)
	}
	r.binds = "not running"
	os.WriteFile(r.a.Vox.ConfigFile(), []byte("[cohere]\n"), 0o600)
	if p := strings.Join(r.a.wiringProblems(), "\n"); !strings.Contains(p, "text") || strings.Contains(p, "keys") {
		t.Fatalf("problems %q", p)
	}
}

func TestCheckReportsChangedScriptsAndUpstreamNews(t *testing.T) {
	r := newRig(t)
	os.MkdirAll(r.a.Vox.Bin, 0o700)
	os.WriteFile(filepath.Join(r.a.Vox.Bin, "voxtype-key"), []byte("#!/bin/bash\n"), 0o700)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/repos/peteonrails/voxtype/pulls/328":
			w.Write([]byte(`{"merged": true}`))
		case "/repos/peteonrails/voxtype/issues/209":
			w.Write([]byte(`{"state": "open"}`))
		}
	}))
	defer api.Close()
	githubAPI = api.URL + "/"

	check := func(args ...string) string {
		var out bytes.Buffer
		r.a.Out = &out
		if err := r.a.check(args); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	check("--baseline")
	out := check("--notify")
	if !strings.Contains(out, "PR 328") || strings.Contains(out, "issue 209") || strings.Contains(out, "scripts changed") {
		t.Fatalf("check = %s", out)
	}
	if len(r.notified) != 1 {
		t.Fatalf("notified %d times", len(r.notified))
	}

	os.WriteFile(filepath.Join(r.a.Vox.Bin, "voxtype-key"), []byte("#!/bin/bash\necho changed\n"), 0o700)
	if out := check(); !strings.Contains(out, "scripts changed (voxtype-key)") {
		t.Fatalf("check = %s", out)
	}
}

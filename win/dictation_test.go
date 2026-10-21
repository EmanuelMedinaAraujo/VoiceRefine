package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EmanuelMedinaAraujo/VoiceRefine/internal/clip"
)

// fakeMic records one second of audio per take, unless told otherwise.
type fakeMic struct{ samples int }

type fakeTake struct{ pcm []byte }

func (m *fakeMic) Start() (Take, error) { return fakeTake{make([]byte, m.samples*2)}, nil }
func (t fakeTake) Stop() []byte         { return t.pcm }

type fakeEngine struct {
	text      string
	err       error
	why       string
	languages []string
	during    func() // runs while the text is written
}

func (e *fakeEngine) Unavailable() string { return e.why }
func (e *fakeEngine) Model() string       { return modelName }
func (e *fakeEngine) Transcribe(wav []byte, language string) (string, error) {
	e.languages = append(e.languages, language)
	if e.during != nil {
		e.during()
	}
	if len(wav) < 44 || string(wav[:4]) != "RIFF" {
		return "", errors.New("not a WAV")
	}
	return e.text, e.err
}

type fakeDesktop struct {
	mu        sync.Mutex
	window    uintptr
	app       string // what the window in front holds
	clipboard string
	notified  []string
}

func (f *fakeDesktop) Foreground() uintptr { f.mu.Lock(); defer f.mu.Unlock(); return f.window }
func (f *fakeDesktop) Type(text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.app += text
	return nil
}
func (f *fakeDesktop) Erase(n int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := []rune(f.app)
	f.app = string(r[:max(0, len(r)-n)])
	return nil
}
func (f *fakeDesktop) Copy(text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clipboard = text
	return nil
}
func (f *fakeDesktop) Notify(title, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notified = append(f.notified, title)
}
func (f *fakeDesktop) typed() string { f.mu.Lock(); defer f.mu.Unlock(); return f.app }

type rig struct {
	d      *Dictation
	mic    *fakeMic
	engine *fakeEngine
	desk   *fakeDesktop
	states chan string
}

func newRig(t *testing.T) *rig {
	r := &rig{
		mic:    &fakeMic{samples: clip.SampleRate},
		engine: &fakeEngine{text: " Hello there.\n How are you?\n"},
		desk:   &fakeDesktop{window: 0xabc},
		states: make(chan string, 100),
	}
	r.d = &Dictation{
		Store: clip.Store{Root: t.TempDir()}, Mic: r.mic, Engine: r.engine, Desktop: r.desk,
		Now: time.Now, OnState: func(s string) { r.states <- s },
	}
	return r
}

// dictate holds the key, releases it, and waits until the text is in.
func (r *rig) dictate(t *testing.T, language string, private bool) {
	t.Helper()
	id := r.d.Start(language, "push-to-talk", private)
	if id == 0 {
		t.Fatal("the take did not start")
	}
	r.d.Stop(id)
	r.waitIdle(t)
}

func (r *rig) waitIdle(t *testing.T) {
	t.Helper()
	for {
		select {
		case s := <-r.states:
			if s == Idle {
				return
			}
		case <-time.After(2 * time.Second):
			t.Fatal("the dictation never went idle")
		}
	}
}

func saved(t *testing.T, d *Dictation) []clip.Saved {
	t.Helper()
	return d.Store.Day(time.Now())
}

func TestDictationTypesAndSaves(t *testing.T) {
	r := newRig(t)
	r.dictate(t, "de", false)
	if got := r.desk.typed(); got != "Hello there. How are you?" {
		t.Fatalf("typed %q", got)
	}
	if r.engine.languages[0] != "de" {
		t.Fatalf("asked the engine for %v", r.engine.languages)
	}
	s := saved(t, r.d)
	if len(s) != 1 || filepath.Base(s[0].Dir) != "rec001" {
		t.Fatalf("saved %+v", s)
	}
	i := s[0].Info
	if i.Status != clip.Untouched || i.Language != "de" || i.Engine != "whisper" || i.Model != modelName ||
		i.Trigger != "push-to-talk" || i.Length != time.Second || i.CutAtLimit {
		t.Fatalf("info %+v", i)
	}
	if text, _ := clip.ReadText(s[0].Dir, clip.TranscribedFile); text != "Hello there. How are you?" {
		t.Fatalf("transcribed.txt %q", text)
	}
	wav, _ := os.ReadFile(filepath.Join(s[0].Dir, clip.AudioFile))
	if len(wav) != 44+clip.SampleRate*2 {
		t.Fatalf("audio.wav has %d bytes", len(wav))
	}
}

func TestPrivateDictationSavesNothing(t *testing.T) {
	r := newRig(t)
	r.dictate(t, "en", true)
	if r.desk.typed() == "" {
		t.Fatal("nothing typed")
	}
	if s := saved(t, r.d); len(s) != 0 {
		t.Fatalf("saved %+v", s)
	}
	if _, _, reason := r.d.FixTarget(); reason == "" {
		t.Fatal("a private dictation can be fixed")
	}
}

func TestNothingSavedWithoutWords(t *testing.T) {
	for name, setup := range map[string]func(*rig){
		"blank":     func(r *rig) { r.engine.text = " [BLANK_AUDIO]\n" },
		"music":     func(r *rig) { r.engine.text = " (Musik)\n" },
		"too short": func(r *rig) { r.mic.samples = clip.SampleRate / 10 },
		"failed":    func(r *rig) { r.engine.err = errors.New("down") },
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			setup(r)
			r.dictate(t, "en", false)
			if r.desk.typed() != "" || len(saved(t, r.d)) != 0 {
				t.Fatalf("typed %q, saved %d", r.desk.typed(), len(saved(t, r.d)))
			}
		})
	}
}

func TestStartIsIgnoredWhileBusyOrNotReady(t *testing.T) {
	r := newRig(t)
	id := r.d.Start("en", "push-to-talk", false)
	if again := r.d.Start("de", "push-to-talk", false); again != 0 {
		t.Fatal("a second take started while recording")
	}
	r.d.Stop(0) // the release of the ignored key
	if r.d.state != Recording {
		t.Fatal("the release of an ignored key stopped the take")
	}
	r.d.Stop(id)
	r.waitIdle(t)

	r.engine.why = "Downloading."
	if r.d.Start("en", "push-to-talk", false) != 0 {
		t.Fatal("started without an engine")
	}
	if n := len(r.desk.notified); n == 0 || r.desk.notified[n-1] != "Dictation is not ready" {
		t.Fatalf("notified %v", r.desk.notified)
	}
}

func TestToggle(t *testing.T) {
	r := newRig(t)
	r.d.Toggle("en")
	r.d.Toggle("en")
	r.waitIdle(t)
	s := saved(t, r.d)
	if len(s) != 1 || s[0].Info.Trigger != "toggle" {
		t.Fatalf("saved %+v", s)
	}
}

func TestFixReplacesTheTypedText(t *testing.T) {
	r := newRig(t)
	r.engine.text = "Hallo Welt"
	r.desk.app = "Before: "
	r.dictate(t, "de", false)
	dir, text, reason := r.d.FixTarget()
	if reason != "" || text != "Hallo Welt" {
		t.Fatalf("fix target %q %q", text, reason)
	}
	if err := r.d.Fix(dir, "Hallo, Welt!\r\n"); err != nil {
		t.Fatal(err)
	}
	if got := r.desk.typed(); got != "Before: Hallo, Welt!" {
		t.Fatalf("app holds %q", got)
	}
	if text, _ := clip.ReadText(dir, clip.CorrectedFile); text != "Hallo, Welt!" {
		t.Fatalf("corrected.txt %q", text)
	}
	info, _ := clip.ReadInfo(dir)
	if info.Status != clip.Corrected || !info.FixOpened {
		t.Fatalf("info %+v", info)
	}

	// Fixing back to the transcript: untouched again, and the app follows.
	r.d.Fix(dir, "Hallo Welt")
	if _, ok := clip.ReadText(dir, clip.CorrectedFile); ok {
		t.Fatal("corrected.txt stays")
	}
	if info, _ := clip.ReadInfo(dir); info.Status != clip.Untouched {
		t.Fatalf("status %s", info.Status)
	}
	if got := r.desk.typed(); got != "Before: Hallo Welt" {
		t.Fatalf("app holds %q", got)
	}
}

func TestFixGoesToTheClipboardInAnotherWindow(t *testing.T) {
	r := newRig(t)
	r.dictate(t, "en", false)
	dir, _, _ := r.d.FixTarget()
	r.desk.window = 0xdef
	start := time.Now()
	r.d.Fix(dir, "Fixed.")
	if time.Since(start) < focusWait {
		t.Fatal("did not wait for the focus to come back")
	}
	if r.desk.clipboard != "Fixed." || strings.Contains(r.desk.typed(), "Fixed.") {
		t.Fatalf("clipboard %q, app %q", r.desk.clipboard, r.desk.typed())
	}
}

func TestOnlyTheLastDictationCanBeFixed(t *testing.T) {
	r := newRig(t)
	r.dictate(t, "en", false)
	dir, _, _ := r.d.FixTarget()
	r.dictate(t, "en", true) // a private one counts too
	if _, _, reason := r.d.FixTarget(); !strings.Contains(reason, "newer dictation") {
		t.Fatalf("reason %q", reason)
	}
	if err := r.d.Fix(dir, "x"); err != nil {
		t.Fatalf("fixing the still-last saved one: %v", err)
	}
	if err := r.d.Fix(filepath.Join(dir, "..", "rec999"), "x"); err == nil {
		t.Fatal("fixed a dictation that is not the last")
	}
}

func TestCleanText(t *testing.T) {
	for in, want := range map[string]string{
		" Hello.\n":                 "Hello.",
		" One.\n Two.\n":            "One. Two.",
		" [BLANK_AUDIO]\n":          "",
		" (upbeat music)\n Hi.\n":   "Hi.",
		" [Tastaturgeräusche]\n":    "",
		" Grüße (und mehr) hier.\n": "Grüße (und mehr) hier.",
		" Tab\there\x1b[2J.\n":      "Tabhere[2J.",
	} {
		if got := cleanText(in); got != want {
			t.Errorf("cleanText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTextIsNotTypedIntoAWindowThatCameToTheFront(t *testing.T) {
	for _, private := range []bool{false, true} {
		r := newRig(t)
		r.engine.during = func() { r.desk.mu.Lock(); r.desk.window = 0xdef; r.desk.mu.Unlock() }
		r.dictate(t, "en", private)
		if r.desk.typed() != "" || r.desk.clipboard != "Hello there. How are you?" {
			t.Fatalf("private %v: typed %q, clipboard %q", private, r.desk.typed(), r.desk.clipboard)
		}
		if got := len(saved(t, r.d)); got != map[bool]int{false: 1, true: 0}[private] {
			t.Fatalf("private %v: saved %d", private, got)
		}
	}
	// A fix of text that was never typed goes to the clipboard as well.
	r := newRig(t)
	r.engine.during = func() { r.desk.mu.Lock(); r.desk.window = 0xdef; r.desk.mu.Unlock() }
	r.dictate(t, "en", false)
	dir, _, _ := r.d.FixTarget()
	r.d.Fix(dir, "Fixed.")
	if r.desk.typed() != "" || r.desk.clipboard != "Fixed." {
		t.Fatalf("typed %q, clipboard %q", r.desk.typed(), r.desk.clipboard)
	}
}

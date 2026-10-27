package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/EmanuelMedinaAraujo/VoiceRefine/internal/clip"
)

// Mic records the default microphone as 16-bit mono samples at clip.SampleRate.
type Mic interface {
	Start() (Take, error)
}

// Take is one running recording.
type Take interface {
	Stop() []byte // ends the recording and returns its raw samples
}

// Engine turns speech into text.
type Engine interface {
	// Unavailable says why nothing can be dictated yet, for example while the model downloads.
	Unavailable() string
	Transcribe(wav []byte, language string) (string, error)
	Model() string
}

// Desktop is what dictation does to the rest of Windows.
type Desktop interface {
	Foreground() uintptr    // the window that gets the typed text
	Type(text string) error // once no modifier key is held any more
	Erase(n int) error      // n backspaces
	Copy(text string) error
	Notify(title, text string)
}

const (
	// maxTake is Voxtype's max_duration_secs: a toggle left running stops by itself there.
	maxTake = 60 * time.Second
	// minTake is less than any word: a shorter take was a slip of the key.
	minTake = 300 * time.Millisecond

	// The text in the app is only replaced shortly after it was typed, as on Linux (cmd/voicerefine/fix.go).
	replaceBase    = 20 * time.Second
	replacePerChar = 100 * time.Millisecond
	replaceMax     = 2 * time.Minute
	focusWait      = 700 * time.Millisecond // the fix window closing hands the focus back
)

func replaceLimit(text string) time.Duration {
	return min(replaceBase+time.Duration(utf8.RuneCountInString(text))*replacePerChar, replaceMax)
}

// The states the tray icon shows.
const (
	Idle         = "idle"
	Recording    = "recording"
	Transcribing = "transcribing"
)

// Last is the newest saved dictation, the one the fix window works on.
type Last struct {
	Dir     string
	InApp   string  // the text the app holds now: as typed, or as last replaced by a fix
	Window  uintptr // the window it was typed into
	TypedAt time.Time
}

// Dictation records while a key is held, has the engine write the text, types it, and saves the
// pair unless the dictation is private. It does at most one dictation at a time: like Voxtype, it
// ignores a start while it still records or turns the last take into text.
type Dictation struct {
	Store   clip.Store
	Mic     Mic
	Engine  Engine
	Desktop Desktop
	Now     func() time.Time
	OnState func(state string)

	mu        sync.Mutex
	state     string
	take      Take
	takeID    int
	started   time.Time
	language  string
	trigger   string
	private   bool
	lastPress time.Time
	last      *Last
}

func (d *Dictation) setState(s string) {
	d.state = s
	if d.OnState != nil {
		d.OnState(s)
	}
}

// Start begins a take and returns its id, or 0 when nothing started.
func (d *Dictation) Start(language, trigger string, private bool) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	// Every start counts, the private ones too: the last dictation can only be fixed until the next starts.
	d.lastPress = d.Now()
	if d.state != "" && d.state != Idle {
		return 0
	}
	if why := d.Engine.Unavailable(); why != "" {
		d.Desktop.Notify("Dictation is not ready", why)
		return 0
	}
	take, err := d.Mic.Start()
	if err != nil {
		d.Desktop.Notify("The microphone did not start", err.Error())
		return 0
	}
	d.takeID++
	d.take, d.started, d.language, d.trigger, d.private = take, d.Now(), language, trigger, private
	d.setState(Recording)
	id := d.takeID
	time.AfterFunc(maxTake, func() { d.stop(id, true) })
	return id
}

// Stop ends the take a key started; the text follows a moment later.
func (d *Dictation) Stop(id int) { d.stop(id, false) }

// Toggle starts a take, or stops the one running.
func (d *Dictation) Toggle(language string) {
	d.mu.Lock()
	state, id := d.state, d.takeID
	d.mu.Unlock()
	if state == Recording {
		d.Stop(id)
		return
	}
	d.Start(language, "toggle", false)
}

func (d *Dictation) stop(id int, cut bool) {
	d.mu.Lock()
	if id == 0 || id != d.takeID || d.state != Recording {
		d.mu.Unlock()
		return
	}
	pcm := d.take.Stop()
	d.take = nil
	d.setState(Transcribing)
	t := take{pcm: pcm, started: d.started, language: d.language, trigger: d.trigger, private: d.private, cut: cut,
		window: d.Desktop.Foreground()}
	d.mu.Unlock()
	go d.finish(t)
}

type take struct {
	pcm      []byte
	started  time.Time
	language string
	trigger  string
	private  bool
	cut      bool    // stopped by itself at maxTake
	window   uintptr // the window in front when the take ended, which the text is for
}

func (d *Dictation) finish(t take) {
	defer func() {
		d.mu.Lock()
		d.setState(Idle)
		d.mu.Unlock()
	}()
	length := clip.RawDuration(int64(len(t.pcm)))
	if length < minTake {
		return
	}
	wav := clip.EncodeWAV(t.pcm)
	text, err := d.Engine.Transcribe(wav, t.language)
	if err != nil {
		d.Desktop.Notify("Dictation failed", err.Error())
		return
	}
	text = cleanText(text)
	if text == "" {
		return
	}
	// Another window came to the front while the text was written: it is not typed there, where
	// it may not belong, but handed over through the clipboard.
	typeIt := d.Desktop.Foreground() == t.window
	if !t.private {
		window := t.window
		if !typeIt {
			window = 0 // a fix cannot replace text that was never typed
		}
		if err := d.save(t, wav, length, text, window); err != nil {
			d.Desktop.Notify("The dictation was not saved", err.Error())
		}
	}
	if !typeIt {
		if err := d.Desktop.Copy(text); err != nil {
			d.Desktop.Notify("The text could not be copied", err.Error())
			return
		}
		d.Desktop.Notify("The window changed", "The text was not typed into the new window. Paste it with Ctrl+V.")
		return
	}
	if err := d.Desktop.Type(text); err != nil {
		d.Desktop.Notify("The text could not be typed", err.Error())
	}
}

func (d *Dictation) save(t take, wav []byte, length time.Duration, text string, window uintptr) error {
	dir, err := d.Store.New(t.started)
	if err != nil {
		return err
	}
	if err := clip.WriteFileAtomic(filepath.Join(dir, clip.AudioFile), wav); err != nil {
		os.RemoveAll(dir)
		return err
	}
	if err := clip.WriteFileAtomic(filepath.Join(dir, clip.TranscribedFile), []byte(text)); err != nil {
		os.RemoveAll(dir)
		return err
	}
	info := clip.Info{
		Status:     clip.Untouched,
		Time:       t.started,
		Length:     length,
		Language:   t.language,
		Engine:     "whisper",
		Model:      d.Engine.Model(),
		Trigger:    t.trigger,
		CutAtLimit: t.cut,
	}
	if err := clip.WriteInfo(dir, info); err != nil {
		os.RemoveAll(dir)
		return err
	}
	d.mu.Lock()
	d.last = &Last{Dir: dir, InApp: text, Window: window, TypedAt: d.Now()}
	d.mu.Unlock()
	return nil
}

// nonSpeech is what Whisper writes for a take without words: [BLANK_AUDIO], (music), [Musik].
var nonSpeech = regexp.MustCompile(`^[\[(][^\])]*[\])]$`)

// cleanText joins Whisper's segments, one per line and each with a leading space, into one line.
func cleanText(text string) string {
	var words []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" && !nonSpeech.MatchString(line) {
			words = append(words, line)
		}
	}
	// No control characters: typed, they could act as keys.
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.Join(words, " "))
}

// FixTarget is the last dictation and its text, if it can still be fixed: until the next
// dictation starts. It notes in info.txt that the fix window opened.
func (d *Dictation) FixTarget() (dir, text, reason string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	l := d.last
	if l == nil {
		return "", "", "No dictation saved yet."
	}
	if _, err := clip.ReadInfo(l.Dir); err != nil {
		return "", "", "The last dictation was deleted."
	}
	if d.lastPress.After(l.TypedAt) {
		return "", "", "A newer dictation started. Only the last one can be fixed."
	}
	text, ok := clip.ReadText(l.Dir, clip.CorrectedFile)
	if !ok {
		text, _ = clip.ReadText(l.Dir, clip.TranscribedFile)
	}
	clip.UpdateInfo(l.Dir, func(i *clip.Info) { i.FixOpened = true })
	return l.Dir, text, ""
}

// Fix saves text as the dictation's correct text and puts it in the app: in place of the typed
// text while that is safe, otherwise on the clipboard.
func (d *Dictation) Fix(dir, text string) error {
	text = strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if strings.TrimSpace(text) == "" {
		return nil // an emptied box is closing without a fix
	}
	d.mu.Lock()
	l := d.last
	if l == nil || l.Dir != dir {
		d.mu.Unlock()
		return fmt.Errorf("%s is not the last dictation", dir)
	}
	transcribed, _ := clip.ReadText(dir, clip.TranscribedFile)
	status := clip.Corrected
	var err error
	if text == transcribed {
		os.Remove(filepath.Join(dir, clip.CorrectedFile))
		status = clip.Untouched
	} else {
		err = clip.WriteFileAtomic(filepath.Join(dir, clip.CorrectedFile), []byte(text))
	}
	if err == nil {
		err = clip.UpdateInfo(dir, func(i *clip.Info) { i.Status = status })
	}
	old, replace := *l, text != l.InApp
	lastPress := d.lastPress
	d.mu.Unlock()
	if err != nil {
		d.Desktop.Notify("The fix was not saved", err.Error())
		return err
	}
	if !replace {
		return nil
	}

	if d.canReplace(old, lastPress) {
		err := d.Desktop.Erase(utf8.RuneCountInString(old.InApp))
		if err == nil {
			err = d.Desktop.Type(text)
		}
		if err == nil {
			d.mu.Lock()
			if d.last != nil && d.last.Dir == dir {
				d.last.InApp = text
			}
			d.mu.Unlock()
			return nil
		}
	}
	if err := d.Desktop.Copy(text); err != nil {
		return err
	}
	d.Desktop.Notify("Fixed text copied", "The text in the app was left as it was. Paste the fix with Ctrl+V.")
	return nil
}

// Accept marks the last dictation as read and right while F5 would open the fix window:
// untouched becomes verified, a fix stays corrected.
func (d *Dictation) Accept() {
	d.mu.Lock()
	l, lastPress := d.last, d.lastPress
	d.mu.Unlock()
	if l == nil || !d.inTime(*l, lastPress) || d.Desktop.Foreground() != l.Window {
		return
	}
	d.mu.Lock()
	if d.last != l || d.lastPress.After(l.TypedAt) {
		d.mu.Unlock()
		return // a dictation started in between
	}
	err := clip.UpdateInfo(l.Dir, func(i *clip.Info) {
		if i.Status == clip.Untouched {
			i.Status = clip.Verified
		}
	})
	d.mu.Unlock()
	if err != nil {
		d.Desktop.Notify("The dictation was not accepted", err.Error())
		return
	}
	d.Desktop.Notify("Accepted", "The last dictation counts as right.")
}

// FixKey is true while F5 opens the fix window: while the fix can replace the typed text, with
// the same window in front, no dictation since, and the text recent. The keyboard hook asks it.
func (d *Dictation) FixKey() bool {
	d.mu.Lock()
	l, lastPress := d.last, d.lastPress
	d.mu.Unlock()
	return l != nil && d.inTime(*l, lastPress) && d.Desktop.Foreground() == l.Window
}

// inTime is true while no dictation started since and the text was typed only a short time ago.
func (d *Dictation) inTime(l Last, lastPress time.Time) bool {
	return l.Window != 0 && !lastPress.After(l.TypedAt) && d.Now().Sub(l.TypedAt) <= replaceLimit(l.InApp)
}

// canReplace is true while the same window is in front, no dictation started since, and the
// text was typed only a short time ago.
func (d *Dictation) canReplace(l Last, lastPress time.Time) bool {
	if !d.inTime(l, lastPress) {
		return false
	}
	for end := d.Now().Add(focusWait); ; time.Sleep(25 * time.Millisecond) {
		if d.Desktop.Foreground() == l.Window {
			return true
		}
		if d.Now().After(end) {
			return false
		}
	}
}

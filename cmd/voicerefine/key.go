package main

import (
	"flag"
	"os"
	"path/filepath"
	"time"

	"github.com/EmanuelMedinaAraujo/VoiceRefine/internal/clip"
	"github.com/EmanuelMedinaAraujo/VoiceRefine/internal/voxtype"
)

// staleAfter is far beyond Voxtype's 60 s limit plus its transcription: a folder that has waited
// this long for its text lost its key release or its text.
const staleAfter = 5 * time.Minute

// key is what the dictation keys run instead of voxtype-key: it starts the recorder first, so the
// clip holds all that Voxtype hears, then passes the key on to voxtype-key unchanged.
//
//	voicerefine key [--private] <start|stop|toggle|cancel> [language]
func (a *App) key(args []string) error {
	fs := flag.NewFlagSet("key", flag.ContinueOnError)
	private := fs.Bool("private", false, "dictate without saving")
	if err := fs.Parse(args); err != nil || fs.NArg() < 1 || fs.NArg() > 2 {
		return errUsage
	}
	action, language := fs.Arg(0), "en"
	if fs.NArg() == 2 {
		language = fs.Arg(1)
	}

	switch action {
	case "start":
		return a.keyStart(action, language, *private)
	case "stop", "cancel":
		return a.keyStop(action, language)
	case "toggle":
		if state, _ := a.Vox.State(); state == "recording" {
			return a.keyStop(action, language)
		}
		return a.keyStart(action, language, *private)
	}
	return errUsage
}

func (a *App) voxtypeKey(action, language string) error {
	_, err := a.Command("", a.Vox.KeyScript(), action, language)
	return err
}

func (a *App) keyStart(action, language string, private bool) error {
	// Every start counts, the private ones too: the last dictation can only be fixed until the next starts.
	os.MkdirAll(a.Run, 0o700)
	os.WriteFile(a.path("last-press"), []byte(a.Now().Format(time.RFC3339Nano)+"\n"), 0o600)
	a.changed()

	state, _ := a.Vox.State()
	// Voxtype ignores a start while it records or transcribes. During a meeting it dictates in the
	// meeting language whichever key is pressed, so the saved language would be wrong.
	if private || voxtype.Busy(state) || a.Vox.MeetingRunning() {
		return a.voxtypeKey(action, language)
	}
	awake := state != "" && a.Vox.Language() == language
	if awake && a.Vox.PowerMode() == "battery" {
		return a.voxtypeKey(action, language)
	}

	var mine *Pending
	err := a.locked(func() error {
		if p := a.pending(); p != nil {
			if p.Stopped.IsZero() && a.Recorder.Alive(p.PID) && a.Now().Sub(p.Started) < staleAfter {
				return nil // a recorder still runs for a dictation Voxtype has not started yet
			}
			// Voxtype is idle, so this folder's dictation ended without text.
			a.discardLocked(p)
		}
		started := a.Now()
		dir, err := a.Store.New(started)
		if err != nil {
			return err
		}
		pid, err := a.Recorder.Start(filepath.Join(dir, clip.RawFile))
		if err != nil {
			os.RemoveAll(dir)
			return err
		}
		trigger := "push-to-talk"
		if action == "toggle" {
			trigger = "toggle"
		}
		mine = &Pending{Dir: dir, PID: pid, Started: started, Language: language, Trigger: trigger, WokeUp: !awake}
		return a.savePending(mine)
	})
	if err != nil {
		a.fail(err)
	}

	keyErr := a.voxtypeKey(action, language)
	if mine == nil {
		return keyErr
	}

	// voxtype-key returns once Voxtype was told to record. After an idle unload or a language
	// switch it first loads the model, and Voxtype only listens from then on.
	listening := a.waitForState("recording", time.Second)
	_, since := a.Vox.State()
	a.locked(func() error {
		p := a.pending()
		if p == nil || p.Dir != mine.Dir {
			return nil // the key was released and the take already handled
		}
		// The daemon writes its mode at each start, also the one this key press caused.
		if !listening || a.Vox.PowerMode() == "battery" || a.Vox.MeetingRunning() {
			return a.discardLocked(p)
		}
		if since.After(p.Started) {
			p.ListenAt = since.Sub(p.Started)
		}
		return a.savePending(p)
	})
	return keyErr
}

func (a *App) waitForState(want string, limit time.Duration) bool {
	for end := a.Now().Add(limit); ; time.Sleep(10 * time.Millisecond) {
		if state, _ := a.Vox.State(); state == want {
			return true
		}
		if a.Now().After(end) {
			return false
		}
	}
}

func (a *App) keyStop(action, language string) error {
	var mine *Pending
	a.locked(func() error {
		p := a.pending()
		if p == nil || !p.Stopped.IsZero() {
			return nil
		}
		a.Recorder.Stop(p.PID)
		p.Stopped = a.Now()
		mine = p
		return a.savePending(p)
	})

	keyErr := a.voxtypeKey(action, language)
	if mine == nil {
		return keyErr
	}
	if action == "cancel" {
		a.discard(mine.Dir)
		return keyErr
	}
	a.awaitText(mine.Dir)
	return keyErr
}

// awaitText deletes the folder when Voxtype goes idle without handing its text to the tap: the
// take was too short, held no speech, or the tap missed its time limit.
func (a *App) awaitText(dir string) {
	time.Sleep(300 * time.Millisecond) // let Voxtype move on from recording
	idleSeen := 0
	for end := a.Now().Add(staleAfter); a.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		if p := a.pending(); p == nil || p.Dir != dir {
			return // the tap saved it
		}
		if state, _ := a.Vox.State(); voxtype.Busy(state) {
			idleSeen = 0
			continue
		}
		if idleSeen++; idleSeen >= 2 {
			break
		}
	}
	a.discard(dir)
}

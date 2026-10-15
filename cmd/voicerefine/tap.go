package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/EmanuelMedinaAraujo/VoiceRefine/internal/clip"
	"github.com/EmanuelMedinaAraujo/VoiceRefine/internal/voxtype"
)

// tap is Voxtype's post_process command. Voxtype pipes each text through it before typing, and
// types the original text when the tap fails or misses its time limit, so the tap always hands
// the text back unchanged and only copies it aside.
func (a *App) tap() error {
	data, err := io.ReadAll(a.In)
	text := string(data)
	defer a.Out.Write(data)
	if err != nil || text == "" {
		return err
	}
	if err := a.locked(func() error { return a.save(text) }); err != nil {
		a.fail(err)
	}
	return nil
}

// save puts the text with the waiting clip. Without a waiting folder the dictation was private,
// in battery mode or during a meeting, and nothing is saved.
func (a *App) save(text string) error {
	p := a.pending()
	if p == nil {
		return nil
	}
	if err := clip.WriteFileAtomic(filepath.Join(p.Dir, clip.TranscribedFile), []byte(text)); err != nil {
		return err
	}

	// Voxtype ended this recording itself at its length limit while the key was still held:
	// the recorder still runs, and the state file changed to transcribing where Voxtype stopped.
	cut := false
	var limit time.Duration
	if a.Recorder.Alive(p.PID) {
		a.Recorder.Stop(p.PID)
		if state, since := a.Vox.State(); state == "transcribing" && since.After(p.Started) {
			limit, cut = since.Sub(p.Started), true
		}
	}
	a.Recorder.Wait(p.PID, 150*time.Millisecond)

	raw := filepath.Join(p.Dir, clip.RawFile)
	length, err := clip.RawToWAV(raw, filepath.Join(p.Dir, clip.AudioFile), limit)
	if err != nil {
		return err
	}
	os.Remove(raw)

	engine := os.Getenv("VOXTYPE_ENGINE") // voxtype-daemon picks the engine through it
	info := clip.Info{
		Status:     clip.Untouched,
		Time:       p.Started,
		Length:     length,
		Language:   p.Language,
		Engine:     engine,
		Model:      voxtype.Model(a.Vox.ActiveConfig(p.Language), engine),
		PowerMode:  a.Vox.PowerMode(),
		Trigger:    p.Trigger,
		WokeUp:     p.WokeUp,
		ListenAt:   p.ListenAt,
		CutAtLimit: cut,
	}
	if err := clip.WriteInfo(p.Dir, info); err != nil {
		return err
	}
	os.Remove(a.path("pending.json"))
	a.saveLast(&Last{Dir: p.Dir, InApp: text, Window: a.activeWindow(), TypedAt: a.Now()})
	return nil
}

// activeWindow is the Hyprland address of the focused window, which Voxtype types into next.
func (a *App) activeWindow() string {
	out, err := a.Command("", "hyprctl", "activewindow", "-j")
	if err != nil {
		return ""
	}
	var w struct {
		Address string `json:"address"`
	}
	json.Unmarshal([]byte(out), &w)
	return w.Address
}

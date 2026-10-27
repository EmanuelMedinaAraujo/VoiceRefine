package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/EmanuelMedinaAraujo/VoiceRefine/internal/clip"
)

// The text in the app is only replaced shortly after it was typed: past that, the cursor has
// likely moved, and a long text takes longer to read through.
const (
	replaceBase    = 20 * time.Second
	replacePerChar = 100 * time.Millisecond
	replaceMax     = 2 * time.Minute
	focusWait      = 700 * time.Millisecond // the fix window closing hands the focus back
)

func replaceLimit(text string) time.Duration {
	return min(replaceBase+time.Duration(utf8.RuneCountInString(text))*replacePerChar, replaceMax)
}

func (a *App) lastPress() time.Time {
	data, _ := os.ReadFile(a.path("last-press"))
	t, _ := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(data)))
	return t
}

// fixable is the last dictation, if it can still be fixed: until the next dictation starts.
func (a *App) fixable() (*Last, string) {
	l := a.last()
	if l == nil {
		return nil, "No dictation saved yet."
	}
	if _, err := clip.ReadInfo(l.Dir); err != nil {
		return nil, "The last dictation was deleted."
	}
	if a.lastPress().After(l.TypedAt) {
		return nil, "A newer dictation started. Only the last one can be fixed."
	}
	return l, ""
}

// last prints what the fix window shows. With --opened it also notes in info.txt that the
// window was opened for this dictation.
func (a *App) lastCmd(args []string) error {
	fs := flag.NewFlagSet("last", flag.ContinueOnError)
	opened := fs.Bool("opened", false, "note that the fix window opened")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return errUsage
	}
	out := struct {
		Fixable bool   `json:"fixable"`
		Dir     string `json:"dir,omitempty"`
		Text    string `json:"text,omitempty"`
		Reason  string `json:"reason,omitempty"`
	}{}
	err := a.locked(func() error {
		l, reason := a.fixable()
		if l == nil {
			out.Reason = reason
			return nil
		}
		text, ok := clip.ReadText(l.Dir, clip.CorrectedFile)
		if !ok {
			text, _ = clip.ReadText(l.Dir, clip.TranscribedFile)
		}
		out.Fixable, out.Dir, out.Text = true, l.Dir, text
		if *opened {
			return clip.UpdateInfo(l.Dir, func(i *clip.Info) { i.FixOpened = true })
		}
		return nil
	})
	if err != nil {
		return err
	}
	return json.NewEncoder(a.Out).Encode(out)
}

// fixKey is what F5 runs. It opens the fix window only while the fix can replace the typed text:
// the same window in front, no dictation since, and the text recent. Otherwise F5 goes on to the
// app, so a browser still reloads with it.
func (a *App) fixKey(args []string) error {
	if len(args) != 0 {
		return errUsage
	}
	var open bool
	a.locked(func() error {
		l, _ := a.fixable()
		open = l != nil && a.inTime(l) && a.activeWindow() == l.Window
		return nil
	})
	if open {
		_, err := a.Command("", "omarchy-shell", "shell", "summon", "emu.voicerefine", "{}")
		return err
	}
	_, err := a.Command("", "hyprctl", "dispatch", `hl.dsp.send_shortcut({ mods = "", key = "F5" })`)
	return err
}

// fix saves the text read from stdin as the dictation's correct text, and puts it in the app.
//
//	voicerefine fix <dir> < text
func (a *App) fix(args []string) error {
	if len(args) != 1 {
		return errUsage
	}
	dir := filepath.Clean(args[0])
	data, err := io.ReadAll(a.In)
	if err != nil {
		return err
	}
	text := strings.TrimRight(string(data), "\n")
	if strings.TrimSpace(text) == "" {
		return nil // an emptied box is closing without a fix
	}

	var replace *Last
	err = a.locked(func() error {
		l := a.last()
		if l == nil || l.Dir != dir {
			return fmt.Errorf("%s is not the last dictation", dir)
		}
		transcribed, _ := clip.ReadText(dir, clip.TranscribedFile)
		status := clip.Corrected
		if text == transcribed {
			os.Remove(filepath.Join(dir, clip.CorrectedFile))
			status = clip.Untouched
		} else if err := clip.WriteFileAtomic(filepath.Join(dir, clip.CorrectedFile), []byte(text)); err != nil {
			return err
		}
		if err := clip.UpdateInfo(dir, func(i *clip.Info) { i.Status = status }); err != nil {
			return err
		}
		if text != l.InApp {
			replace = l
		}
		return nil
	})
	if err != nil {
		a.fail(err)
		return err
	}
	if replace == nil {
		return nil
	}

	if a.canReplace(replace) {
		if err := a.replaceInApp(replace.InApp, text); err == nil {
			return a.locked(func() error {
				if l := a.last(); l != nil && l.Dir == dir {
					l.InApp = text
					return a.saveLast(l)
				}
				return nil
			})
		}
	}
	return a.toClipboard(text)
}

// inTime is true while no dictation started since and the text was typed only a short time ago.
func (a *App) inTime(l *Last) bool {
	return l.Window != "" && !a.lastPress().After(l.TypedAt) && a.Now().Sub(l.TypedAt) <= replaceLimit(l.InApp)
}

// canReplace is true while the same window is in front, no dictation started since, and the
// text was typed only a short time ago.
func (a *App) canReplace(l *Last) bool {
	if !a.inTime(l) {
		return false
	}
	for end := a.Now().Add(focusWait); ; time.Sleep(25 * time.Millisecond) {
		if a.activeWindow() == l.Window {
			return true
		}
		if a.Now().After(end) {
			return false
		}
	}
}

// replaceInApp deletes the typed text with backspaces and types the fixed text, both with wtype,
// which Voxtype types with too.
func (a *App) replaceInApp(old, text string) error {
	keys := []string{"-d", "1"}
	for range utf8.RuneCountInString(old) {
		keys = append(keys, "-k", "BackSpace")
	}
	if _, err := a.Command("", "wtype", keys...); err != nil {
		return err
	}
	_, err := a.Command(text, "wtype", "-d", "1", "-")
	return err
}

func (a *App) toClipboard(text string) error {
	if _, err := a.Command(text, "wl-copy"); err != nil {
		return err
	}
	a.Command("", "notify-send", "-t", "3000", "Fixed text copied", "The text in the app was left as it was. Paste the fix with Ctrl+V.")
	return nil
}

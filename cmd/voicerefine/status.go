package main

import (
	"encoding/json"
	"os"
	"strings"
	"time"
)

// Status is what the bar widget shows.
type Status struct {
	Clips     int     `json:"clips"`   // saved today
	Minutes   float64 `json:"minutes"` // of audio saved today
	LastSaved string  `json:"last_saved,omitempty"`
	Recording bool    `json:"recording"`
	Paused    string  `json:"paused,omitempty"`  // battery, meeting or off: nothing is saved, as intended
	Warning   string  `json:"warning,omitempty"` // saving is broken
}

func (a *App) status() Status {
	var s Status
	var newest time.Time
	for _, c := range a.Store.Day(a.Now()) {
		s.Clips++
		s.Minutes += c.Info.Length.Minutes()
		if st, err := os.Stat(c.Dir); err == nil && st.ModTime().After(newest) {
			newest = st.ModTime()
		}
	}
	if !newest.IsZero() {
		s.LastSaved = newest.Format(time.RFC3339Nano)
	}
	if p := a.pending(); p != nil && p.Stopped.IsZero() && a.Recorder.Alive(p.PID) {
		s.Recording = true
	}
	s.Paused = a.paused()
	if data, err := os.ReadFile(a.path("error")); err == nil {
		s.Warning = "Saving failed: " + strings.TrimSpace(string(data))
	} else if problems := a.wiringProblems(); len(problems) > 0 {
		s.Warning = strings.Join(problems, "\n")
	}
	return s
}

func (a *App) paused() string {
	if a.Vox.MeetingRunning() {
		return "meeting"
	}
	if _, err := a.Command("", "systemctl", "--user", "is-active", "-q", "voxtype-power-watch.service"); err != nil {
		return "off" // voxtype-mode off
	}
	mode := ""
	if state, _ := a.Vox.State(); state != "" {
		mode = a.Vox.PowerMode()
	} else {
		// Unloaded after an idle hour: the mode the next dictation will load.
		out, _ := a.Command("", a.Vox.Bin+"/voxtype-daemon", "--which")
		mode = strings.TrimSpace(out)
	}
	if mode == "battery" {
		return "battery"
	}
	return ""
}

// wiringProblems finds what stops saving without any error: the keys or the tap no longer lead
// to VoiceRefine.
func (a *App) wiringProblems() []string {
	var out []string
	if ok, known := a.keysLeadHere(); known && !ok {
		out = append(out, "The dictation keys do not run VoiceRefine. Re-run ~/Projects/voxtype-bench/setup/install.sh, or check the voxtype-setup block in ~/.config/hypr/bindings.lua.")
	}
	if config, _ := os.ReadFile(a.Vox.ConfigFile()); !strings.Contains(string(config), tapCommand) {
		out = append(out, "Voxtype no longer hands its text to VoiceRefine. Re-run VoiceRefine's install.sh.")
	}
	return out
}

// tapCommand is how install.sh adds the tap to Voxtype's config; the check looks for it.
const tapCommand = "voicerefine tap"

// keysLeadHere reads Hyprland's key list. Lua bindings do not show their command there, so the
// voxtype-setup block names VoiceRefine in the descriptions of the keys it routes here.
// known is false when Hyprland cannot be asked, for example before the session runs.
func (a *App) keysLeadHere() (ok, known bool) {
	out, err := a.Command("", "hyprctl", "binds", "-j")
	if err != nil {
		return false, false
	}
	var binds []struct {
		Key         string `json:"key"`
		Modmask     int    `json:"modmask"`
		Release     bool   `json:"release"`
		Description string `json:"description"`
	}
	if json.Unmarshal([]byte(out), &binds) != nil {
		return false, false
	}
	for _, b := range binds {
		if b.Key == "F9" && b.Modmask == 0 && !b.Release {
			return strings.Contains(b.Description, "VoiceRefine"), true
		}
	}
	return false, true
}

func (a *App) statusCmd() error {
	return json.NewEncoder(a.Out).Encode(a.status())
}

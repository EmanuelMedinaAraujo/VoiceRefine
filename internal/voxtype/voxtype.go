// Package voxtype reads what VoiceRefine needs to know about the running Voxtype setup: the files
// Voxtype and the voxtype-bench scripts (voxtype-key, voxtype-daemon, voxtype-meeting) keep in
// $XDG_RUNTIME_DIR, and the model each engine is configured with.
package voxtype

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Paths locates the Voxtype setup. Tests point it at a temporary folder.
type Paths struct {
	Runtime string // $XDG_RUNTIME_DIR
	Config  string // ~/.config/voxtype
	Bin     string // where voxtype-key and voxtype-daemon live, ~/.local/bin
}

func Default() Paths {
	home, _ := os.UserHomeDir()
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		config = filepath.Join(home, ".config")
	}
	return Paths{
		Runtime: os.Getenv("XDG_RUNTIME_DIR"),
		Config:  filepath.Join(config, "voxtype"),
		Bin:     filepath.Join(home, ".local", "bin"),
	}
}

func (p Paths) StateFile() string  { return filepath.Join(p.Runtime, "voxtype", "state") }
func (p Paths) ConfigFile() string { return filepath.Join(p.Config, "config.toml") }
func (p Paths) KeyScript() string  { return filepath.Join(p.Bin, "voxtype-key") }

func read(path string) string {
	data, _ := os.ReadFile(path)
	return strings.TrimSpace(string(data))
}

// State is what the daemon's state file says: idle, recording or transcribing. It is "" when the
// daemon is down (unloaded after an idle hour, or restarting into the other language), because
// voxtype-key and the daemon remove the file then. Since is when the state last changed.
func (p Paths) State() (state string, since time.Time) {
	st, err := os.Stat(p.StateFile())
	if err != nil {
		return "", time.Time{}
	}
	return read(p.StateFile()), st.ModTime()
}

// Busy is true while Voxtype records or transcribes. A start key is then ignored by Voxtype.
func Busy(state string) bool { return state == "recording" || state == "transcribing" }

// MeetingRunning mirrors voxtype-key: during a meeting the daemon dictates in the meeting language
// whichever key is pressed.
func (p Paths) MeetingRunning() bool {
	if _, err := os.Stat(filepath.Join(p.Runtime, "voxtype-meeting.toml")); err != nil {
		return false
	}
	s := read(filepath.Join(p.Runtime, "voxtype", "meeting_state"))
	return s != "" && !strings.HasPrefix(s, "idle")
}

// Language is the language the daemon holds, as the keys last asked for it.
func (p Paths) Language() string {
	if l := read(filepath.Join(p.Runtime, "voxtype-language")); l != "" {
		return l
	}
	return "en"
}

// PowerMode is the mode voxtype-daemon started the daemon in: power or battery. It is written at
// each daemon start, including the one a key press triggers after an idle unload.
func (p Paths) PowerMode() string { return read(filepath.Join(p.Runtime, "voxtype-power-mode")) }

// ActiveConfig is the config the daemon runs on for a language: voxtype-daemon writes a copy with
// other models for languages other than English.
func (p Paths) ActiveConfig(language string) string {
	if language != "en" {
		if copy := filepath.Join(p.Runtime, "voxtype-language.toml"); exists(copy) {
			return copy
		}
	}
	return p.ConfigFile()
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Model reads `model = "..."` from an engine's section ([cohere], [whisper], [parakeet]).
func Model(configPath, engine string) string {
	f, err := os.Open(configPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	section := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			section = strings.Trim(line, "[] ")
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok && section == engine && strings.TrimSpace(k) == "model" {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

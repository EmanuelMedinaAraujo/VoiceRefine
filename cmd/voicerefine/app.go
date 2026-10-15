package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/EmanuelMedinaAraujo/VoiceRefine/internal/clip"
	"github.com/EmanuelMedinaAraujo/VoiceRefine/internal/voxtype"
)

// App holds where everything lives and the few outside effects, so tests can replace them.
type App struct {
	Vox   voxtype.Paths
	Store clip.Store
	Run   string // $XDG_RUNTIME_DIR/voicerefine: what the parts share about the current dictation
	State string // ~/.local/state/voicerefine: the update check's baseline
	Home  string

	Now      func() time.Time
	Recorder Recorder
	// Command runs a program and waits for it, for voxtype-key, hyprctl, wtype and friends.
	Command func(stdin string, name string, args ...string) (string, error)
	In      io.Reader
	Out     io.Writer
}

func newApp() *App {
	home, _ := os.UserHomeDir()
	data := os.Getenv("VOICEREFINE_DATA")
	if data == "" {
		data = filepath.Join(home, ".local", "share", "voicerefine")
	}
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		state = filepath.Join(home, ".local", "state")
	}
	vox := voxtype.Default()
	return &App{
		Vox:      vox,
		Store:    clip.Store{Root: data},
		Run:      filepath.Join(vox.Runtime, "voicerefine"),
		State:    filepath.Join(state, "voicerefine"),
		Home:     home,
		Now:      time.Now,
		Recorder: pwRecord{},
		Command:  runCommand,
		In:       os.Stdin,
		Out:      os.Stdout,
	}
}

func runCommand(stdin string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	return string(out), err
}

// Pending is the dictation whose folder waits for its text. There is at most one: a key press
// only makes a folder while Voxtype is idle or asleep, and Voxtype ignores a start while it still
// turns the last dictation into text.
type Pending struct {
	Dir      string        `json:"dir"`
	PID      int           `json:"pid"`
	Started  time.Time     `json:"started"` // the recorder start, which is where the audio begins
	Stopped  time.Time     `json:"stopped,omitzero"`
	Language string        `json:"language"`
	Trigger  string        `json:"trigger"`
	WokeUp   bool          `json:"woke_up"`
	ListenAt time.Duration `json:"listen_at"`
}

// Last is the newest saved dictation, the one the fix window works on.
type Last struct {
	Dir     string    `json:"dir"`
	InApp   string    `json:"in_app"` // the text the app holds now: as typed, or as last replaced by a fix
	Window  string    `json:"window"` // Hyprland address of the window it was typed into
	TypedAt time.Time `json:"typed_at"`
}

func (a *App) path(name string) string { return filepath.Join(a.Run, name) }

// locked runs f while holding the lock that keeps the key, the tap and the fix in step.
func (a *App) locked(f func() error) error {
	if err := os.MkdirAll(a.Run, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(a.path("lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	return f()
}

func readJSON[T any](path string) *T {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	v := new(T)
	if json.Unmarshal(data, v) != nil {
		return nil
	}
	return v
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return clip.WriteFileAtomic(path, append(data, '\n'))
}

func (a *App) pending() *Pending            { return readJSON[Pending](a.path("pending.json")) }
func (a *App) savePending(p *Pending) error { return writeJSON(a.path("pending.json"), p) }
func (a *App) last() *Last                  { return readJSON[Last](a.path("last.json")) }
func (a *App) saveLast(l *Last) error       { return writeJSON(a.path("last.json"), l) }

// discardLocked deletes the waiting folder: the dictation never got its text.
func (a *App) discardLocked(p *Pending) error {
	if a.Recorder.Alive(p.PID) {
		a.Recorder.Stop(p.PID)
		a.Recorder.Wait(p.PID, time.Second)
	}
	os.Remove(a.path("pending.json"))
	return os.RemoveAll(p.Dir)
}

// discard deletes dir if it is still the folder waiting for its text.
func (a *App) discard(dir string) error {
	return a.locked(func() error {
		if p := a.pending(); p != nil && p.Dir == dir {
			return a.discardLocked(p)
		}
		return nil
	})
}

// changed marks that a dictation was saved or fixed.
func (a *App) changed() {
	os.MkdirAll(a.Run, 0o700)
	os.WriteFile(a.path("changed"), []byte(a.Now().Format(time.RFC3339Nano)+"\n"), 0o600)
}

// fail records a saving fault for 'voicerefine status'. A later save clears it.
func (a *App) fail(err error) {
	fmt.Fprintln(os.Stderr, "voicerefine:", err)
	os.MkdirAll(a.Run, 0o700)
	os.WriteFile(a.path("error"), []byte(err.Error()+"\n"), 0o600)
	a.changed()
}

// Recorder records the default microphone to a raw file in the background.
type Recorder interface {
	Start(rawPath string) (pid int, err error)
	Alive(pid int) bool
	Stop(pid int)
	Wait(pid int, limit time.Duration) bool
}

type pwRecord struct{}

func (pwRecord) Start(rawPath string) (int, error) {
	cmd := exec.Command("pw-record", "--rate", fmt.Sprint(clip.SampleRate), "--channels", "1", "--format", "s16",
		"--raw", "-P", "{ node.name = voicerefine node.description = VoiceRefine }", rawPath)
	// Its own session: the recorder outlives this key press and waits for the release.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	return pid, cmd.Process.Release()
}

// Alive also rules out a finished recorder no one has reaped yet and a reused process id.
func (pwRecord) Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	s := string(stat)
	open, end := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || end < open || end+2 >= len(s) {
		return false
	}
	return s[open+1:end] == "pw-record" && s[end+2] != 'Z' && s[end+2] != 'X'
}

// Stop asks pw-record to finish; it writes out what it still holds and exits.
func (pwRecord) Stop(pid int) { syscall.Kill(pid, syscall.SIGINT) }

func (r pwRecord) Wait(pid int, limit time.Duration) bool {
	for end := time.Now().Add(limit); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if !r.Alive(pid) {
			return true
		}
	}
	return !r.Alive(pid)
}

var errUsage = errors.New("usage")

package clip

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// A clip is untouched until it is accepted as right (verified) or fixed (corrected). Only
// verified and corrected clips have a text that was checked.
const (
	Untouched = "untouched"
	Verified  = "verified"
	Corrected = "corrected"
)

// Info is info.txt: what the later export needs to pick and label a clip. It is plain
// "key: value" lines so it can be read by eye and by any tool.
type Info struct {
	Status     string        // Untouched, Verified or Corrected
	Time       time.Time     // when the key was pressed, which is where the audio starts
	Length     time.Duration // length of audio.wav
	Language   string        // the language the key asked for, as Voxtype names it (en, de)
	Engine     string        // the Voxtype engine that wrote the text (cohere, whisper, parakeet)
	Model      string        // that engine's model
	PowerMode  string        // power or battery, as voxtype-daemon chose it
	Trigger    string        // push-to-talk or toggle
	WokeUp     bool          // Voxtype first had to load its model, so the audio starts before it listened
	ListenAt   time.Duration // where in audio.wav Voxtype started to listen
	CutAtLimit bool          // Voxtype stopped by itself at its length limit, and the clip was cut there
	FixOpened  bool          // the fix window was opened for this dictation
}

func seconds(d time.Duration) string { return strconv.FormatFloat(d.Seconds(), 'f', 3, 64) }

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func (i Info) Encode() []byte {
	var b bytes.Buffer
	line := func(k, v string) { fmt.Fprintf(&b, "%s: %s\n", k, v) }
	line("status", i.Status)
	line("time", i.Time.Format(time.RFC3339Nano))
	line("length_s", seconds(i.Length))
	line("language", i.Language)
	line("engine", i.Engine)
	line("model", i.Model)
	line("power_mode", i.PowerMode)
	line("trigger", i.Trigger)
	line("woke_up", yesNo(i.WokeUp))
	line("listen_at_s", seconds(i.ListenAt))
	line("cut_at_limit", yesNo(i.CutAtLimit))
	line("fix_opened", yesNo(i.FixOpened))
	return b.Bytes()
}

func ParseInfo(data []byte) (Info, error) {
	var i Info
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		dur := func() time.Duration {
			f, _ := strconv.ParseFloat(v, 64)
			return time.Duration(f * float64(time.Second))
		}
		switch strings.TrimSpace(k) {
		case "status":
			i.Status = v
		case "time":
			t, err := time.Parse(time.RFC3339Nano, v)
			if err != nil {
				return i, fmt.Errorf("info.txt: bad time %q", v)
			}
			i.Time = t
		case "length_s":
			i.Length = dur()
		case "language":
			i.Language = v
		case "engine":
			i.Engine = v
		case "model":
			i.Model = v
		case "power_mode":
			i.PowerMode = v
		case "trigger":
			i.Trigger = v
		case "woke_up":
			i.WokeUp = v == "yes"
		case "listen_at_s":
			i.ListenAt = dur()
		case "cut_at_limit":
			i.CutAtLimit = v == "yes"
		case "fix_opened":
			i.FixOpened = v == "yes"
		}
	}
	if i.Status == "" {
		return i, fmt.Errorf("info.txt: no status")
	}
	return i, sc.Err()
}

func ReadInfo(dir string) (Info, error) {
	data, err := os.ReadFile(filepath.Join(dir, InfoFile))
	if err != nil {
		return Info{}, err
	}
	return ParseInfo(data)
}

func WriteInfo(dir string, i Info) error {
	return WriteFileAtomic(filepath.Join(dir, InfoFile), i.Encode())
}

// UpdateInfo reads info.txt, lets change edit it, and writes it back.
func UpdateInfo(dir string, change func(*Info)) error {
	i, err := ReadInfo(dir)
	if err != nil {
		return err
	}
	change(&i)
	return WriteInfo(dir, i)
}

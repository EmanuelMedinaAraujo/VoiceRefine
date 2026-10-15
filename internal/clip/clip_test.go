package clip

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var day = time.Date(2026, 10, 15, 9, 30, 0, 0, time.Local)

func TestNewCountsUpAndReusesTheDeletedNewest(t *testing.T) {
	s := Store{t.TempDir()}
	var dirs []string
	for range 3 {
		dir, err := s.New(day)
		if err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, dir)
	}
	if got := filepath.Base(dirs[2]); got != "rec003" {
		t.Fatalf("third folder is %s, want rec003", got)
	}
	if want := filepath.Join(s.Root, "2026", "10", "15", "rec001"); dirs[0] != want {
		t.Fatalf("first folder is %s, want %s", dirs[0], want)
	}
	os.Remove(dirs[2])
	again, _ := s.New(day)
	if again != dirs[2] {
		t.Fatalf("after deleting rec003 the next folder is %s, want it reused", again)
	}
}

func TestNewKeepsCountingPastAThousand(t *testing.T) {
	s := Store{t.TempDir()}
	os.MkdirAll(filepath.Join(s.DayDir(day), "rec999"), 0o700)
	dir, _ := s.New(day)
	if got := filepath.Base(dir); got != "rec1000" {
		t.Fatalf("got %s, want rec1000", got)
	}
}

func TestInfoRoundTrip(t *testing.T) {
	in := Info{
		Status: Corrected, Time: day, Length: 4250 * time.Millisecond, Language: "de",
		Engine: "whisper", Model: "large-v3-turbo", PowerMode: "power", Trigger: "push-to-talk",
		WokeUp: true, ListenAt: 2100 * time.Millisecond, CutAtLimit: true, FixOpened: true,
	}
	out, err := ParseInfo(in.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if !out.Time.Equal(in.Time) {
		t.Fatalf("time %v, want %v", out.Time, in.Time)
	}
	out.Time = in.Time
	if out != in {
		t.Fatalf("round trip changed info:\n got %+v\nwant %+v", out, in)
	}
}

func TestDaySkipsFoldersWithoutInfo(t *testing.T) {
	s := Store{t.TempDir()}
	a, _ := s.New(day)
	s.New(day) // still waiting for its text
	WriteInfo(a, Info{Status: Untouched, Time: day, Length: time.Second})
	got := s.Day(day)
	if len(got) != 1 || got[0].Dir != a {
		t.Fatalf("Day = %+v, want only %s", got, a)
	}
}

func TestRawToWAVWritesHeaderAndCuts(t *testing.T) {
	dir := t.TempDir()
	raw := filepath.Join(dir, RawFile)
	wav := filepath.Join(dir, AudioFile)
	samples := make([]byte, SampleRate*bytesPerSample*2+1) // 2 s and one stray byte
	for i := range samples {
		samples[i] = byte(i)
	}
	os.WriteFile(raw, samples, 0o600)

	length, err := RawToWAV(raw, wav, 1500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if length != 1500*time.Millisecond {
		t.Fatalf("length %v, want 1.5s", length)
	}
	data, _ := os.ReadFile(wav)
	if !bytes.Equal(data[:4], []byte("RIFF")) || !bytes.Equal(data[8:12], []byte("WAVE")) {
		t.Fatal("no RIFF/WAVE header")
	}
	if rate := binary.LittleEndian.Uint32(data[24:28]); rate != SampleRate {
		t.Fatalf("rate %d", rate)
	}
	size := binary.LittleEndian.Uint32(data[40:44])
	if int(size) != len(data)-44 || size != SampleRate*bytesPerSample*3/2 {
		t.Fatalf("data size %d, file body %d", size, len(data)-44)
	}
	if !bytes.Equal(data[44:], samples[:size]) {
		t.Fatal("samples changed")
	}

	length, _ = RawToWAV(raw, wav, 0)
	if length != 2*time.Second {
		t.Fatalf("uncut length %v, want 2s without the stray byte", length)
	}
}

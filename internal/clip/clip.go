// Package clip stores dictations on disk: one folder per dictation, for example
// 2026/10/15/rec001/, holding audio.wav, info.txt, transcribed.txt and, once fixed, corrected.txt.
package clip

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"
)

const (
	RawFile         = "audio.raw" // what the recorder writes while it runs
	AudioFile       = "audio.wav"
	InfoFile        = "info.txt"
	TranscribedFile = "transcribed.txt"
	CorrectedFile   = "corrected.txt"
)

var recName = regexp.MustCompile(`^rec(\d{3,})$`)

// Store is the folder tree that holds all dictations.
type Store struct {
	Root string
}

// DayDir is the folder that holds the dictations of the day t falls on.
func (s Store) DayDir(t time.Time) string {
	return filepath.Join(s.Root, t.Format("2006"), t.Format("01"), t.Format("02"))
}

// New makes the folder for a new dictation. Numbers count up through the day; a folder that was
// deleted because it never got its text was the newest one, so its number is used again.
func (s Store) New(t time.Time) (string, error) {
	day := s.DayDir(t)
	if err := os.MkdirAll(day, 0o700); err != nil {
		return "", err
	}
	next := 1
	for _, n := range s.numbers(day) {
		next = max(next, n+1)
	}
	for ; ; next++ {
		dir := filepath.Join(day, fmt.Sprintf("rec%03d", next))
		err := os.Mkdir(dir, 0o700)
		if err == nil {
			return dir, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
}

func (s Store) numbers(day string) []int {
	entries, _ := os.ReadDir(day)
	var out []int
	for _, e := range entries {
		if m := recName.FindStringSubmatch(e.Name()); e.IsDir() && m != nil {
			n, _ := strconv.Atoi(m[1])
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out
}

// Saved is one finished dictation of a day.
type Saved struct {
	Dir  string
	Info Info
}

// Day lists the finished dictations of the day t falls on, oldest first. Folders still waiting
// for their text have no info.txt yet and are left out.
func (s Store) Day(t time.Time) []Saved {
	day := s.DayDir(t)
	var out []Saved
	for _, n := range s.numbers(day) {
		dir := filepath.Join(day, fmt.Sprintf("rec%03d", n))
		info, err := ReadInfo(dir)
		if err == nil {
			out = append(out, Saved{dir, info})
		}
	}
	return out
}

// WriteFileAtomic replaces path in one step, so a reader never sees half a file.
func WriteFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ReadText reads transcribed.txt or corrected.txt; a missing file gives "" and false.
func ReadText(dir, name string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "", false
	}
	return string(data), true
}

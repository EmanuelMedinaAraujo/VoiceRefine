package voxtype

import (
	"os"
	"path/filepath"
	"testing"
)

func setup(t *testing.T) Paths {
	root := t.TempDir()
	p := Paths{Runtime: filepath.Join(root, "run"), Config: filepath.Join(root, "config"), Bin: filepath.Join(root, "bin")}
	for _, d := range []string{filepath.Join(p.Runtime, "voxtype"), p.Config, p.Bin} {
		os.MkdirAll(d, 0o700)
	}
	return p
}

func write(t *testing.T, path, data string) {
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStateIsEmptyWhileTheDaemonIsDown(t *testing.T) {
	p := setup(t)
	if s, _ := p.State(); s != "" {
		t.Fatalf("state %q without a state file", s)
	}
	write(t, p.StateFile(), "recording\n")
	if s, since := p.State(); s != "recording" || since.IsZero() {
		t.Fatalf("state %q since %v", s, since)
	}
}

func TestMeetingRunning(t *testing.T) {
	p := setup(t)
	write(t, filepath.Join(p.Runtime, "voxtype", "meeting_state"), "recording")
	if p.MeetingRunning() {
		t.Fatal("meeting without the meeting config")
	}
	write(t, filepath.Join(p.Runtime, "voxtype-meeting.toml"), "")
	if !p.MeetingRunning() {
		t.Fatal("meeting not seen")
	}
	write(t, filepath.Join(p.Runtime, "voxtype", "meeting_state"), "idle")
	if p.MeetingRunning() {
		t.Fatal("idle meeting state counts as a meeting")
	}
}

func TestModelOfTheActiveConfig(t *testing.T) {
	p := setup(t)
	write(t, p.ConfigFile(), `
[whisper]
model = "base.en"

[cohere]
model = "cohere-transcribe-q4"
language = "en"
`)
	if m := Model(p.ActiveConfig("en"), "cohere"); m != "cohere-transcribe-q4" {
		t.Fatalf("cohere model %q", m)
	}
	if m := Model(p.ActiveConfig("de"), "whisper"); m != "base.en" {
		t.Fatalf("German without its config copy should read the main config, got %q", m)
	}
	write(t, filepath.Join(p.Runtime, "voxtype-language.toml"), "[whisper]\nmodel = \"large-v3-turbo\"\n")
	if m := Model(p.ActiveConfig("de"), "whisper"); m != "large-v3-turbo" {
		t.Fatalf("German whisper model %q", m)
	}
	if p.Language() != "en" {
		t.Fatal("language should default to en")
	}
}

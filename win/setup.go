package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Voxtype runs only on Linux. Its Whisper engine is whisper.cpp, which ships for Windows: VoiceRefine
// runs its server with the model Voxtype uses for German dictation, for English too.
const (
	modelName = "large-v3-turbo-q5_0"
	modelFile = "ggml-" + modelName + ".bin"
)

// A pinned file: the download is only used when its SHA-256 matches.
type download struct {
	Name   string
	URL    string
	Size   int64
	SHA256 string
}

var (
	engineCPU = download{
		Name:   "the speech engine",
		URL:    "https://github.com/ggml-org/whisper.cpp/releases/download/b5454/whisper-bin-x64.zip",
		Size:   8928640,
		SHA256: "6ba69e3482d7826214f90a6a9c84ca07782aec1e1d0c6a7c30c994fd5d816ccb",
	}
	// For NVIDIA cards: the same engine with CUDA 12.4, which brings its own CUDA libraries.
	engineCUDA = download{
		Name:   "the speech engine for the NVIDIA card",
		URL:    "https://github.com/ggml-org/whisper.cpp/releases/download/b5454/whisper-bin-win-cuda-12.4.0-x64.zip",
		Size:   684913404,
		SHA256: "afef0b881c500958921c3f5523b50e59ee2ec9b6f5cbd25b324c51ed308a957a",
	}
	// Microsoft's Visual C++ runtime installer, only fetched when Windows lacks the runtime. It runs
	// elevated, so it is pinned like the rest; the hash is also the one in Microsoft's own URL.
	vcRedist = download{
		Name:   "the Visual C++ runtime",
		URL:    "https://download.visualstudio.microsoft.com/download/pr/990c720a-46ce-4dc7-87b9-c2eb615d34b9/CC0FF0EB1DC3F5188AE6300FAEF32BF5BEEBA4BDD6E8E445A9184072096B713B/VC_redist.x64.exe",
		Size:   25635768,
		SHA256: "cc0ff0eb1dc3f5188ae6300faef32bf5beeba4bdd6e8e445a9184072096b713b",
	}
	model = download{
		Name:   "the speech model",
		URL:    "https://huggingface.co/ggerganov/whisper.cpp/resolve/5359861c739e955e79d9a303bcbc70fb988958b1/" + modelFile,
		Size:   574041195,
		SHA256: "394221709cd5ad1f40c46e6031ca61bce88931e6e088c188294c6d5a55ffa7e2",
	}
)

// engineFiles are the parts of a whisper.cpp release the server needs: no other tools, no SDL,
// no llama or parakeet libraries, no CUDA compiler.
var engineFiles = regexp.MustCompile(`^(whisper-server\.exe|whisper\.dll|ggml(-[a-z0-9]+)*\.dll|cudart64_12\.dll|cublas64_12\.dll|cublasLt64_12\.dll)$`)

// Paths is where the program, its engine and the dictations live.
type Paths struct {
	Program string // %LOCALAPPDATA%\Programs\VoiceRefine: the exe, engine\ and models\
	Data    string // %LOCALAPPDATA%\VoiceRefine: the dictations and the logs
}

func (p Paths) Exe() string        { return filepath.Join(p.Program, "VoiceRefine.exe") }
func (p Paths) Engine() string     { return filepath.Join(p.Program, "engine") }
func (p Paths) Server() string     { return filepath.Join(p.Engine(), "whisper-server.exe") }
func (p Paths) Model() string      { return filepath.Join(p.Program, "models", modelFile) }
func (p Paths) Recordings() string { return filepath.Join(p.Data, "recordings") }
func (p Paths) Log() string        { return filepath.Join(p.Data, "voicerefine.log") }
func (p Paths) EngineLog() string  { return filepath.Join(p.Data, "engine.log") }

// engineSource names the release an installed engine came from.
func (p Paths) engineSource() string { return filepath.Join(p.Engine(), "source.txt") }

// Setup brings the engine and the model into place, and reports how far it got.
type Setup struct {
	Paths  Paths
	Engine download
	Client *http.Client

	mu        sync.Mutex
	done      bool
	err       error
	got, size int64
}

// Needed lists the downloads still missing.
func (s *Setup) Needed() []download {
	var out []download
	if src, _ := os.ReadFile(s.Paths.engineSource()); strings.TrimSpace(string(src)) != s.Engine.SHA256 {
		out = append(out, s.Engine)
	}
	if st, err := os.Stat(s.Paths.Model()); err != nil || st.Size() != model.Size {
		out = append(out, model)
	}
	return out
}

// Run downloads what is missing. A broken-off download goes on where it stopped next time.
func (s *Setup) Run() error {
	needed := s.Needed()
	var size int64
	for _, d := range needed {
		size += d.Size
	}
	s.mu.Lock()
	s.size, s.got, s.err = size, 0, nil
	s.mu.Unlock()

	var err error
	for _, d := range needed {
		if d == model {
			err = s.fetch(d, s.Paths.Model())
		} else {
			err = s.installEngine(d)
		}
		if err != nil {
			break
		}
	}
	s.mu.Lock()
	s.done, s.err = err == nil, err
	s.mu.Unlock()
	return err
}

// Unavailable is "" once the engine and the model are in place.
func (s *Setup) Unavailable() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.done:
		return ""
	case s.err != nil:
		return fmt.Sprintf("Setting up failed: %v. VoiceRefine tries again in a minute.", s.err)
	case s.size == 0:
		return "VoiceRefine is getting ready."
	}
	return fmt.Sprintf("Downloading the speech engine and model: %d %% of %.1f GB.", s.got*100/s.size, float64(s.size)/1e9)
}

func (s *Setup) progress(n int64) {
	s.mu.Lock()
	s.got += n
	s.mu.Unlock()
}

func (s *Setup) installEngine(d download) error {
	zipPath := filepath.Join(s.Paths.Program, "engine.zip")
	if err := s.fetch(d, zipPath); err != nil {
		return err
	}
	staged := s.Paths.Engine() + ".new"
	os.RemoveAll(staged)
	if err := extractEngine(zipPath, staged); err != nil {
		os.RemoveAll(staged)
		return err
	}
	if err := os.WriteFile(filepath.Join(staged, "source.txt"), []byte(d.SHA256+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.RemoveAll(s.Paths.Engine()); err != nil {
		return err
	}
	if err := os.Rename(staged, s.Paths.Engine()); err != nil {
		return err
	}
	return os.Remove(zipPath)
}

func extractEngine(zipPath, dir string) error {
	z, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer z.Close()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	found := false
	for _, f := range z.File {
		name := filepath.Base(f.Name)
		if f.FileInfo().IsDir() || !engineFiles.MatchString(name) {
			continue
		}
		found = found || name == "whisper-server.exe"
		if err := extractFile(f, filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	if !found {
		return errors.New("the engine download holds no whisper-server.exe")
	}
	return nil
}

func extractFile(f *zip.File, path string) error {
	in, err := f.Open()
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// fetch downloads d to path through path.part, which survives a broken connection.
func (s *Setup) fetch(d download, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	part := path + ".part"
	have := int64(0)
	if st, err := os.Stat(part); err == nil && st.Size() <= d.Size {
		have = st.Size()
	}
	if have < d.Size {
		req, err := http.NewRequest(http.MethodGet, d.URL, nil)
		if err != nil {
			return err
		}
		if have > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
		}
		resp, err := s.Client.Do(req)
		if err != nil {
			return fmt.Errorf("downloading %s: %w", d.Name, err)
		}
		defer resp.Body.Close()
		flags := os.O_CREATE | os.O_WRONLY
		switch resp.StatusCode {
		case http.StatusPartialContent:
			flags |= os.O_APPEND
		case http.StatusOK:
			flags |= os.O_TRUNC
			have = 0
		default:
			return fmt.Errorf("downloading %s: %s", d.Name, resp.Status)
		}
		s.progress(have)
		out, err := os.OpenFile(part, flags, 0o644)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, io.LimitReader(&counter{resp.Body, s.progress}, d.Size-have))
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return fmt.Errorf("downloading %s: %w", d.Name, err)
		}
	} else {
		s.progress(have)
	}
	if err := verify(part, d); err != nil {
		os.Remove(part)
		return err
	}
	return os.Rename(part, path)
}

func verify(path string, d download) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	if n != d.Size || hex.EncodeToString(h.Sum(nil)) != d.SHA256 {
		return fmt.Errorf("%s arrived damaged and is downloaded again", d.Name)
	}
	return nil
}

type counter struct {
	r   io.Reader
	add func(int64)
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.add(int64(n))
	return n, err
}

// RunUntilDone retries a failed setup every minute: the connection may come back.
func (s *Setup) RunUntilDone(failed func(error)) {
	for {
		err := s.Run()
		if err == nil {
			return
		}
		failed(err)
		time.Sleep(time.Minute)
	}
}

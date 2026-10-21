package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func pinned(name, url string, data []byte) download {
	sum := sha256.Sum256(data)
	return download{Name: name, URL: url, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}
}

func engineZip(t *testing.T) []byte {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, name := range []string{"Release/whisper-server.exe", "Release/whisper.dll", "Release/ggml.dll",
		"Release/ggml-base.dll", "Release/ggml-cpu-haswell.dll", "Release/ggml-cuda.dll", "Release/cudart64_12.dll",
		"Release/cublasLt64_12.dll", "Release/SDL2.dll", "Release/llama.dll", "Release/parakeet.dll",
		"Release/nvrtc64_120_0.dll", "Release/whisper-cli.exe", "Release/test-vad.exe"} {
		w, _ := z.Create(name)
		w.Write([]byte(name))
	}
	z.Close()
	return b.Bytes()
}

// serve hands out files and honours Range, like GitHub and Hugging Face.
func serve(t *testing.T, files map[string][]byte, requests *atomic.Int32) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		data, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSetupDownloadsVerifiesAndKeepsOnlyTheEngine(t *testing.T) {
	modelData := bytes.Repeat([]byte("model"), 1000)
	zipData := engineZip(t)
	var requests atomic.Int32
	srv := serve(t, map[string][]byte{"/engine.zip": zipData, "/model.bin": modelData}, &requests)

	saved := model
	model = pinned("the speech model", srv.URL+"/model.bin", modelData)
	t.Cleanup(func() { model = saved })
	s := &Setup{Paths: Paths{Program: t.TempDir()}, Engine: pinned("the engine", srv.URL+"/engine.zip", zipData), Client: srv.Client()}

	// A download broken off halfway goes on from there.
	os.MkdirAll(filepath.Dir(s.Paths.Model()), 0o755)
	os.WriteFile(s.Paths.Model()+".part", modelData[:2000], 0o644)

	if why := s.Unavailable(); why == "" {
		t.Fatal("available before setup")
	}
	if err := s.Run(); err != nil {
		t.Fatal(err)
	}
	if why := s.Unavailable(); why != "" {
		t.Fatalf("unavailable after setup: %s", why)
	}
	if got, _ := os.ReadFile(s.Paths.Model()); !bytes.Equal(got, modelData) {
		t.Fatal("the model is not the download")
	}
	entries, _ := os.ReadDir(s.Paths.Engine())
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	want := "cublasLt64_12.dll cudart64_12.dll ggml-base.dll ggml-cpu-haswell.dll ggml-cuda.dll ggml.dll source.txt whisper-server.exe whisper.dll"
	if got := strings.Join(names, " "); got != want {
		t.Fatalf("engine holds %s\nwant %s", got, want)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(s.Paths.Program, "*.zip*")); len(leftovers) != 0 {
		t.Fatalf("left behind %v", leftovers)
	}

	// Everything in place: nothing is downloaded again.
	before := requests.Load()
	if needed := s.Needed(); len(needed) != 0 {
		t.Fatalf("still needed: %v", needed)
	}
	s.Run()
	if requests.Load() != before {
		t.Fatal("downloaded again")
	}
	// Another engine (the CUDA one after a new graphics card) is fetched in its place.
	s.Engine.SHA256 = strings.Repeat("0", 64)
	if needed := s.Needed(); len(needed) != 1 || needed[0].URL != s.Engine.URL {
		t.Fatalf("needed %v", needed)
	}
}

func TestSetupRejectsADamagedDownload(t *testing.T) {
	var requests atomic.Int32
	srv := serve(t, map[string][]byte{"/model.bin": []byte("tampered!")}, &requests)
	s := &Setup{Paths: Paths{Program: t.TempDir()}, Client: srv.Client()}
	d := pinned("the speech model", srv.URL+"/model.bin", []byte("original!"))
	err := s.fetch(d, filepath.Join(s.Paths.Program, "model.bin"))
	if err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Fatalf("err %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Paths.Program, "model.bin.part")); !os.IsNotExist(err) {
		t.Fatal("the damaged download stays")
	}
}

func TestPinnedDownloadsAreComplete(t *testing.T) {
	for _, d := range []download{engineCPU, engineCUDA, model, vcRedist} {
		if d.Size <= 0 || len(d.SHA256) != 64 || !strings.HasPrefix(d.URL, "https://") {
			t.Errorf("%s is not pinned: %+v", d.Name, d)
		}
	}
}

func TestTranscribeTalksToWhisperServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, ok := strings.CutPrefix(r.URL.Path, "/secret")
		if !ok {
			http.NotFound(w, r)
			return
		}
		switch path {
		case "/health":
			fmt.Fprint(w, `{"status":"ok"}`)
		case "/inference":
			_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			form, err := multipart.NewReader(r.Body, params["boundary"]).ReadForm(1 << 20)
			if err != nil {
				http.Error(w, `{"error":"bad form"}`, 400)
				return
			}
			f, _ := form.File["file"][0].Open()
			wav, _ := io.ReadAll(f)
			fmt.Fprintf(w, `{"text":" %s %d bytes in %s\n"}`, form.Value["response_format"][0], len(wav), form.Value["language"][0])
		}
	}))
	defer srv.Close()

	started := 0
	w := &Whisper{Setup: &Setup{}, Start: func() (string, error) { started++; return srv.URL + "/secret", nil }}
	text, err := w.Transcribe([]byte("RIFF....WAVE"), "de")
	if err != nil || text != " json 12 bytes in de\n" {
		t.Fatalf("text %q, err %v", text, err)
	}
	w.Transcribe([]byte("RIFF"), "en")
	if started != 1 {
		t.Fatalf("the server started %d times", started)
	}
	w.Ended()
	w.Transcribe([]byte("RIFF"), "en")
	if started != 2 {
		t.Fatal("the server was not started again after it ended")
	}
}

func TestEngineLogKeepsNothingAboutDictations(t *testing.T) {
	for _, line := range []string{
		"Received request: audio.wav",
		"Successfully loaded audio.wav",
		"system_info: n_threads = 8 / 16 | AVX = 1",
		"run_inference: processing 'audio.wav' (80000 samples, 5.0 sec), 8 threads, 1 processors, lang = de, task = transcribe, timestamps = 0 ...",
	} {
		if !perDictation(line) {
			t.Errorf("kept %q", line)
		}
	}
	for _, line := range []string{"whisper_backend_init_gpu: using CUDA0 backend", "whisper_model_load: model size    =  573.40 MB"} {
		if perDictation(line) {
			t.Errorf("dropped %q", line)
		}
	}
}

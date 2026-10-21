package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Whisper talks to whisper-server, which keeps the model loaded between dictations.
type Whisper struct {
	Setup *Setup
	// Start runs the server and returns its address once it listens; it is called again when
	// the server ended.
	Start func() (url string, err error)
	// OnLoaded hears whether the model loaded.
	OnLoaded func(error)

	mu    sync.Mutex
	url   string
	err   error
	ready chan struct{} // closed once url or err is set
}

// loadWait covers loading the model, which on the CPU of a slow laptop takes a while.
const loadWait = 2 * time.Minute

func (w *Whisper) Model() string { return modelName }

func (w *Whisper) Unavailable() string { return w.Setup.Unavailable() }

// Load starts the server in the background. Transcribe waits for it.
func (w *Whisper) Load() {
	w.mu.Lock()
	if w.ready != nil {
		w.mu.Unlock()
		return
	}
	ready := make(chan struct{})
	w.ready, w.url, w.err = ready, "", nil
	w.mu.Unlock()
	go func() {
		url, err := w.Start()
		if err == nil {
			err = waitHealthy(url, loadWait)
		}
		w.mu.Lock()
		w.url, w.err = url, err
		w.mu.Unlock()
		close(ready)
		if w.OnLoaded != nil {
			w.OnLoaded(err)
		}
	}()
}

// Ended is called when the server process exits; the next dictation starts it again.
func (w *Whisper) Ended() {
	w.mu.Lock()
	w.ready = nil
	w.mu.Unlock()
}

func (w *Whisper) server() (string, error) {
	w.Load()
	w.mu.Lock()
	ready := w.ready
	w.mu.Unlock()
	<-ready
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		w.ready = nil // try again with the next dictation
	}
	return w.url, w.err
}

func waitHealthy(url string, limit time.Duration) error {
	client := http.Client{Timeout: 2 * time.Second}
	for end := time.Now().Add(limit); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
		resp, err := client.Get(url + "/health")
		if err != nil {
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return nil
		}
	}
	return errors.New("the speech engine did not load its model in time; see engine.log")
}

func (w *Whisper) Transcribe(wav []byte, language string) (string, error) {
	url, err := w.server()
	if err != nil {
		return "", err
	}
	return transcribe(url, wav, language)
}

func transcribe(url string, wav []byte, language string) (string, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile("file", "audio.wav")
	part.Write(wav)
	for k, v := range map[string]string{
		"language":        language,
		"response_format": "json",
		"temperature":     "0.0",
		"no_timestamps":   "true",
		"suppress_nst":    "true", // no [music] and the like
	} {
		form.WriteField(k, v)
	}
	form.Close()

	client := http.Client{Timeout: 2 * time.Minute} // a full 60 s take on a slow processor
	resp, err := client.Post(url+"/inference", form.FormDataContentType(), &body)
	if err != nil {
		return "", fmt.Errorf("the speech engine did not answer: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var out struct {
		Text  string `json:"text"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(data, &out); err != nil || resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the speech engine failed: %s %s", resp.Status, strings.TrimSpace(out.Error))
	}
	return out.Text, nil
}

// perDictation tells whisper-server's log lines about one request from those about the engine.
func perDictation(line string) bool {
	for _, mark := range []string{"Received request", "Successfully loaded", "processing '", "system_info:", "run_inference"} {
		if strings.Contains(line, mark) {
			return true
		}
	}
	return false
}

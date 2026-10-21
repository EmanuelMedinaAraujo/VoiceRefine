package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

const (
	createNoWindow                    = 0x08000000
	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x2000
	processSetQuota                   = 0x0100
	processTerminate                  = 0x0001
)

// server runs whisper-server.exe in the background. It belongs to a job that Windows ends together
// with VoiceRefine, so the model never stays loaded without it.
type server struct {
	paths   Paths
	onEnded func()
	onGPU   func(bool)

	mu  sync.Mutex
	cmd *exec.Cmd
	job uintptr
}

func (s *server) start() (string, error) {
	port, err := freePort()
	if err != nil {
		return "", err
	}
	// whisper-server answers any web page (it allows every origin) and can be told to load another
	// model. Under a new secret path each start, only VoiceRefine knows where to send its requests.
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	path := "/" + hex.EncodeToString(secret)
	// whisper.cpp uses 4 threads by default; on the CPU the physical cores help.
	threads := min(8, max(4, runtime.NumCPU()/2))
	cmd := exec.Command(s.paths.Server(), "-m", s.paths.Model(), "--host", "127.0.0.1", "--port", fmt.Sprint(port),
		"--request-path", path, "-t", fmt.Sprint(threads), "-l", "en", "-nt")
	cmd.Dir = s.paths.Engine()
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	logFile, err := os.OpenFile(s.paths.EngineLog(), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	out, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	cmd.Stdout = nil // only notes each request
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return "", fmt.Errorf("the speech engine did not start: %w", err)
	}
	s.mu.Lock()
	s.cmd = cmd
	s.mu.Unlock()
	s.bind(cmd.Process.Pid)
	log.Printf("engine: whisper-server started (pid %d, port %d, %d threads)", cmd.Process.Pid, port, threads)

	go func() {
		s.watchLog(out, logFile) // to the end of the output, before Wait closes it
		err := cmd.Wait()
		logFile.Close()
		log.Printf("engine: whisper-server ended: %v", err)
		s.mu.Lock()
		if s.cmd == cmd {
			s.cmd = nil
		}
		s.mu.Unlock()
		s.onEnded()
	}()
	return fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil
}

// watchLog copies the engine's messages to engine.log and notes whether it found the GPU. The
// lines about each dictation (its length and language) stay out, private ones included.
func (s *server) watchLog(r io.Reader, w io.Writer) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !perDictation(line) {
			fmt.Fprintln(w, line)
		}
		if strings.Contains(line, "whisper_backend_init_gpu: using") {
			s.onGPU(true)
		}
	}
}

// bind puts the server into a job that ends with VoiceRefine, even when it crashes.
func (s *server) bind(pid int) {
	if s.job == 0 {
		job, _, _ := pCreateJobObjectW.Call(0, 0)
		if job == 0 {
			return
		}
		var limits jobExtendedLimits
		limits.Basic.LimitFlags = jobObjectLimitKillOnJobClose
		pSetInformationJobObject.Call(job, jobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits))
		s.job = job
	}
	proc, _, _ := pOpenProcess.Call(processSetQuota|processTerminate, 0, uintptr(pid))
	if proc == 0 {
		return
	}
	defer syscall.CloseHandle(syscall.Handle(proc))
	pAssignProcessToJobObject.Call(s.job, proc)
}

func (s *server) stop() {
	s.mu.Lock()
	cmd := s.cmd
	s.cmd = nil
	s.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
	}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

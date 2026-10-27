package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/EmanuelMedinaAraujo/VoiceRefine/internal/clip"
)

const keysHelp = "Hold F9 (English) or F10 (German) while you speak. Win+F9 and Win+F10 save nothing. " +
	"Ctrl+Win+X and Ctrl+Win+Alt+X start and stop. F5 fixes the last dictation right after it was typed, Shift+F5 accepts it as right."

func run() {
	runtime.LockOSThread() // windows, the tray and the keyboard hook belong to this thread
	// DLLs come from System32 only, never from the folder the exe was started in or from.
	if pSetDefaultDllDirectories.Find() == nil {
		pSetDefaultDllDirectories.Call(0x800) // LOAD_LIBRARY_SEARCH_SYSTEM32
	}
	if pSetProcessDpiAwarenessCtx.Find() == nil {
		pSetProcessDpiAwarenessCtx.Call(^uintptr(3)) // per monitor v2: a sharp fix window
	}
	p := defaultPaths()
	if !p.usable() {
		messageBox(0, "VoiceRefine", "Windows gave no folder for local app data (%LOCALAPPDATA%).", mbIconError)
		return
	}
	self, _ := os.Executable()

	// Apps runs the installed copy with "uninstall"; any copy, the download too, takes --uninstall.
	if len(os.Args) > 1 && (os.Args[1] == "uninstall" || os.Args[1] == "--uninstall") {
		uninstall(p, true)
		return
	}
	if !samePath(self, p.Exe()) {
		if installed := installedVersion(); olderThan(installed) {
			messageBox(0, "VoiceRefine", "A newer VoiceRefine is installed ("+installed+"), so this older copy ("+version+
				") does not replace it.\n\nTo go back to this one, uninstall first: run it with --uninstall, or use Settings › Apps. Then run it again.",
				mbIconInformation)
			return
		}
		if err := install(p, self); err != nil {
			messageBox(0, "VoiceRefine", "Installing failed: "+err.Error(), mbIconError)
		}
		return
	}
	if _, _, err := pCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(wstr(`Local\VoiceRefine`)))); err == syscall.Errno(errorAlreadyExists) {
		pPostMessageW.Call(runningWindow(), wmAppAgain, 0, 0)
		return
	}
	openLog(p)
	log.Printf("VoiceRefine %s starts", version)

	u := newUI()
	registerClass(fixClass, colorWindow+1)
	a := &app{ui: u, paths: p}
	a.start(len(os.Args) > 1 && os.Args[1] == "--welcome")
	u.loop()
	log.Printf("VoiceRefine ends")
}

type app struct {
	ui     *ui
	paths  Paths
	setup  *Setup
	whisp  *Whisper
	server *server
	dict   *Dictation
	fix    *fixWindow

	announce atomic.Bool // say when dictation is ready, after a download

	mu     sync.Mutex
	loaded bool // the model is loaded
	gpu    bool
	state  string
}

func (a *app) start(welcome bool) {
	client := &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}}
	engine := engineCPU
	if nvidiaCard() {
		engine = engineCUDA
	}
	a.setup = &Setup{Paths: a.paths, Engine: engine, Client: client}
	a.server = &server{
		paths:   a.paths,
		onEnded: func() { a.whisp.Ended(); a.setLoaded(false) },
		onGPU:   func(on bool) { a.mu.Lock(); a.gpu = on; a.mu.Unlock() },
	}
	a.whisp = &Whisper{Setup: a.setup, Start: a.server.start}
	a.whisp.OnLoaded = func(err error) {
		if err != nil {
			log.Printf("engine: %v", err)
			desktop{a.ui}.Notify("The speech engine did not start", err.Error())
		} else if a.announce.Swap(false) {
			desktop{a.ui}.Notify("Dictation is ready", keysHelp)
		}
		a.setLoaded(err == nil)
	}
	a.dict = &Dictation{
		Store:   clip.Store{Root: a.paths.Recordings()},
		Mic:     waveIn{},
		Engine:  a.whisp,
		Desktop: desktop{a.ui},
		Now:     time.Now,
		OnState: func(s string) { a.ui.do(func() { a.mu.Lock(); a.state = s; a.mu.Unlock(); a.showState() }) },
	}
	a.fix = &fixWindow{ui: a.ui, d: a.dict}
	a.ui.menu = a.menu
	a.ui.click = func() { a.ui.balloon("VoiceRefine", a.status()+"\n"+keysHelp) }

	a.ui.addTray("setup", "VoiceRefine")
	a.showState()
	a.ui.keys.fixKey = a.dict.FixKey
	if err := a.ui.hookKeys(); err != nil {
		messageBox(0, "VoiceRefine", "The dictation keys could not be set up: "+err.Error(), mbIconError)
	}
	go dispatch(a.ui.events, a.dict, func() { a.ui.do(a.fix.open) })

	needed := a.setup.Needed()
	if welcome {
		what := "It starts with Windows from now on."
		if len(needed) > 0 {
			var size int64
			for _, d := range needed {
				size += d.Size
			}
			what += fmt.Sprintf(" It now downloads the speech engine and model (%.1f GB); a message says when dictation is ready.", float64(size)/1e9)
		}
		a.ui.balloon("VoiceRefine is installed", what)
	}
	go a.prepare(len(needed) > 0)
}

// prepare downloads what is missing, then loads the model so the first dictation is quick.
func (a *app) prepare(downloading bool) {
	stop := make(chan struct{})
	go func() { // the tray tip shows the download's progress
		for {
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Second):
				a.ui.do(a.showState)
			}
		}
	}()
	asked := false
	failed := false
	for {
		err := a.ensureRuntime(&asked)
		if err == nil {
			err = a.setup.Run()
		}
		if err == nil {
			break
		}
		log.Printf("setup: %v", err)
		if !failed {
			desktop{a.ui}.Notify("VoiceRefine could not set up", err.Error()+"\nIt tries again every minute.")
			failed = true
		}
		time.Sleep(time.Minute)
	}
	close(stop)
	log.Printf("setup: engine and model in place")
	a.announce.Store(downloading)
	a.whisp.Load()
}

func (a *app) ensureRuntime(asked *bool) error {
	if !vcRuntimeMissing() {
		return nil
	}
	if *asked {
		return fmt.Errorf("the Microsoft Visual C++ runtime is missing; install \"vc_redist.x64.exe\" from Microsoft and restart VoiceRefine")
	}
	*asked = true
	return installVCRuntime(a.setup)
}

func (a *app) setLoaded(on bool) {
	a.mu.Lock()
	a.loaded = on
	a.mu.Unlock()
	a.ui.do(a.showState)
}

func (a *app) status() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if why := a.setup.Unavailable(); why != "" {
		return why
	}
	switch {
	case a.state == Recording:
		return "Listening."
	case a.state == Transcribing:
		return "Writing the text."
	case !a.loaded:
		return "Loading the speech model."
	case a.gpu:
		return "Ready, on the NVIDIA card."
	}
	return "Ready, on the processor."
}

func (a *app) showState() {
	a.mu.Lock()
	state := a.state
	if state == "" || state == Idle {
		state = Idle
		if !a.loaded {
			state = "setup"
		}
	}
	a.mu.Unlock()
	a.ui.setTray(state, "VoiceRefine: "+a.status())
}

func (a *app) menu(cmd int) {
	switch cmd {
	case menuFix:
		a.fix.open()
	case menuRecordings:
		os.MkdirAll(a.paths.Recordings(), 0o755)
		open(a.paths.Recordings())
	case menuLog:
		open(a.paths.Log())
	case menuUninstall:
		if messageBox(a.ui.hwnd, "Uninstall VoiceRefine",
			"Remove VoiceRefine, its speech engine and its model from this computer?\n\nYour saved dictations stay in "+a.paths.Recordings()+".",
			mbYesNo|mbIconQuestion) != idYes {
			return
		}
		a.quit()
		removeProgram(a.paths)
	case menuQuit:
		a.quit()
	}
}

func (a *app) quit() {
	a.server.stop()
	a.ui.quit()
}

func openLog(p Paths) {
	os.MkdirAll(p.Data, 0o755)
	if st, err := os.Stat(p.Log()); err == nil && st.Size() > 1<<20 {
		os.Rename(p.Log(), p.Log()+".old")
	}
	f, err := os.OpenFile(p.Log(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		log.SetOutput(f)
	}
}

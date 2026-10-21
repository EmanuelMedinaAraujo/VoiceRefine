package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	runKey       = `Software\Microsoft\Windows\CurrentVersion\Run`
	uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\VoiceRefine`
)

func defaultPaths() Paths {
	local := os.Getenv("LOCALAPPDATA")
	return Paths{
		Program: filepath.Join(local, "Programs", "VoiceRefine"),
		Data:    filepath.Join(local, "VoiceRefine"),
	}
}

// usable is false when Windows gave no profile folder: relative paths would land anywhere.
func (p Paths) usable() bool { return filepath.IsAbs(p.Program) && filepath.IsAbs(p.Data) }

func system32() string { return filepath.Join(os.Getenv("SystemRoot"), "System32") }

func samePath(a, b string) bool {
	a, _ = filepath.Abs(a)
	b, _ = filepath.Abs(b)
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func runningWindow() uintptr {
	h, _, _ := pFindWindowW.Call(uintptr(unsafe.Pointer(wstr(className))), 0)
	return h
}

// stopRunning asks a running VoiceRefine to quit and waits until it is gone.
func stopRunning() {
	h := runningWindow()
	if h == 0 {
		return
	}
	pPostMessageW.Call(h, wmAppQuit, 0, 0)
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end) && runningWindow() != 0; {
		time.Sleep(100 * time.Millisecond)
	}
}

// install copies this exe into place, has Windows start it at login and list it under Apps, and
// starts it. Running a newer exe the same way updates VoiceRefine.
func install(p Paths, self string) error {
	stopRunning()
	if err := os.MkdirAll(p.Program, 0o755); err != nil {
		return err
	}
	data, err := os.ReadFile(self)
	if err != nil {
		return err
	}
	staged := p.Exe() + ".new"
	if err := os.WriteFile(staged, data, 0o755); err != nil {
		return err
	}
	// The quitting copy may hold its exe for a moment longer.
	for end := time.Now().Add(10 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		if err = os.Rename(staged, p.Exe()); err == nil || time.Now().After(end) {
			break
		}
	}
	if err != nil {
		os.Remove(staged)
		return fmt.Errorf("could not replace %s: %w", p.Exe(), err)
	}

	exe := `"` + p.Exe() + `"`
	if err := regSet(runKey, "VoiceRefine", exe); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"DisplayName":     "VoiceRefine",
		"DisplayIcon":     p.Exe(),
		"Publisher":       "Emanuel Medina",
		"InstallLocation": p.Program,
		"UninstallString": exe + " uninstall",
	} {
		if err := regSet(uninstallKey, name, value); err != nil {
			return err
		}
	}
	shortcut(p) // a convenience: VoiceRefine works without it
	cmd := exec.Command(p.Exe(), "--welcome")
	cmd.Dir = p.Program // not the folder the download sits in
	return cmd.Start()
}

// startMenuEntry starts VoiceRefine again after a Quit.
func startMenuEntry() string {
	return filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "VoiceRefine.lnk")
}

// shortcut writes the Start menu entry with Windows' own shortcut maker.
func shortcut(p Paths) error {
	if !filepath.IsAbs(startMenuEntry()) {
		return errors.New("no Start menu folder")
	}
	// The paths go in as variables, so no character in them is ever read as code.
	script := "$s = (New-Object -ComObject WScript.Shell).CreateShortcut($env:VR_LNK); " +
		"$s.TargetPath = $env:VR_EXE; $s.Description = 'Dictation that saves what you say'; $s.Save()"
	cmd := exec.Command(filepath.Join(system32(), "WindowsPowerShell", "v1.0", "powershell.exe"),
		"-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(os.Environ(), "VR_LNK="+startMenuEntry(), "VR_EXE="+p.Exe())
	cmd.Dir = p.Program
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("the Start menu entry: %v %s", err, out)
	}
	return nil
}

// uninstall removes the program, its engine and model, and its entries. The dictations stay.
func uninstall(p Paths, ask bool) {
	if ask && messageBox(0, "Uninstall VoiceRefine",
		"Remove VoiceRefine, its speech engine and its model from this computer?\n\nYour saved dictations stay in "+p.Recordings()+".",
		mbYesNo|mbIconQuestion) != idYes {
		return
	}
	stopRunning()
	removeProgram(p)
}

func removeProgram(p Paths) {
	regDelete(runKey, "VoiceRefine")
	regDeleteKey(uninstallKey)
	os.Remove(startMenuEntry())
	// A running exe cannot delete itself: a hidden cmd waits for it to end, then deletes the folder.
	// cmd.exe reads its command line itself and knows no backslash escapes, so it gets it as is.
	cmd := exec.Command(filepath.Join(system32(), "cmd.exe"))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow,
		CmdLine: `cmd.exe /c ping -n 5 127.0.0.1 >nul & rmdir /s /q "` + p.Program + `"`}
	cmd.Dir = system32()
	cmd.Start()
}

func regSet(key, name, value string) error {
	var h uintptr
	if r, _, _ := pRegCreateKeyExW.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(wstr(key))), 0, 0, 0,
		keySetValue, 0, uintptr(unsafe.Pointer(&h)), 0); r != 0 {
		return fmt.Errorf("registry %s: error %d", key, r)
	}
	defer pRegCloseKey.Call(h)
	v, _ := syscall.UTF16FromString(value)
	if r, _, _ := pRegSetValueExW.Call(h, uintptr(unsafe.Pointer(wstr(name))), 0, regSZ,
		uintptr(unsafe.Pointer(&v[0])), uintptr(len(v)*2)); r != 0 {
		return fmt.Errorf("registry %s\\%s: error %d", key, name, r)
	}
	return nil
}

func regDelete(key, name string) {
	var h uintptr
	if r, _, _ := pRegCreateKeyExW.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(wstr(key))), 0, 0, 0,
		keySetValue, 0, uintptr(unsafe.Pointer(&h)), 0); r != 0 {
		return
	}
	defer pRegCloseKey.Call(h)
	pRegDeleteValueW.Call(h, uintptr(unsafe.Pointer(wstr(name))))
}

func regDeleteKey(key string) {
	advapi32.NewProc("RegDeleteKeyW").Call(hkeyCurrentUser, uintptr(unsafe.Pointer(wstr(key))))
}

// nvidiaCard is true when an NVIDIA driver is installed: it brings nvcuda.dll.
func nvidiaCard() bool {
	_, err := os.Stat(filepath.Join(system32(), "nvcuda.dll"))
	return err == nil
}

// whisper.cpp is built with Visual C++ and needs its runtime, which most Windows installs have.
var vcRuntime = []string{"msvcp140.dll", "vcruntime140.dll", "vcruntime140_1.dll", "vcomp140.dll"}

func vcRuntimeMissing() bool {
	for _, dll := range vcRuntime {
		if _, err := os.Stat(filepath.Join(system32(), dll)); err != nil {
			return true
		}
	}
	return false
}

// installVCRuntime runs Microsoft's installer for it, which asks once for permission. The file is
// held open against writes from its check until the installer has run, so nothing can swap it.
func installVCRuntime(s *Setup) error {
	path := filepath.Join(s.Paths.Program, "vc_redist.x64.exe")
	if err := s.fetch(vcRedist, path); err != nil {
		return err
	}
	defer os.Remove(path)
	const genericRead, fileShareRead, openExisting = 0x80000000, 0x1, 3
	h, _, err := pCreateFileW.Call(uintptr(unsafe.Pointer(wstr(path))), genericRead, fileShareRead, 0, openExisting, 0, 0)
	if syscall.Handle(h) == syscall.InvalidHandle {
		return fmt.Errorf("opening the Visual C++ runtime installer: %w", err)
	}
	held := os.NewFile(h, path)
	defer held.Close()
	sum := sha256.New()
	if n, err := io.Copy(sum, held); err != nil || n != vcRedist.Size || hex.EncodeToString(sum.Sum(nil)) != vcRedist.SHA256 {
		return errors.New("the Visual C++ runtime installer changed after its download")
	}

	const seeMaskNoCloseProcess = 0x40
	info := shellExecuteInfo{
		Mask: seeMaskNoCloseProcess, Verb: wstr("runas"), File: wstr(path),
		Parameters: wstr("/install /passive /norestart"), Show: swShowNormal,
	}
	info.Size = uint32(unsafe.Sizeof(info))
	if r, _, err := pShellExecuteExW.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		return fmt.Errorf("the Visual C++ runtime was not installed: %w", err)
	}
	defer syscall.CloseHandle(syscall.Handle(info.Process))
	pWaitForSingleObject.Call(info.Process, syscall.INFINITE)
	var code uint32
	pGetExitCodeProcess.Call(info.Process, uintptr(unsafe.Pointer(&code)))
	switch code {
	case 0, 1638, 3010: // installed, a newer one is there, installed but wants a restart
		return nil
	}
	return errors.New("the Visual C++ runtime installer failed with code " + fmt.Sprint(code))
}

package main

import (
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

const className = "VoiceRefine" // the hidden window a second copy finds the running one by

// The window procedures. Windows calls them on the UI thread; handlers maps each window to its code.
var (
	handlers       = map[uintptr]func(msg uint32, w, l uintptr) (uintptr, bool){}
	wndProcCb      = syscall.NewCallback(wndProc)
	hookProcCb     = syscall.NewCallback(hookProc)
	instance, _, _ = pGetModuleHandleW.Call(0)
	activeUI       *ui
)

// Callbacks take only full-width arguments; msg is a UINT.
func wndProc(hwnd, msg, w, l uintptr) uintptr {
	if h := handlers[hwnd]; h != nil {
		if r, done := h(uint32(msg), w, l); done {
			return r
		}
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, w, l)
	return r
}

func registerClass(name string, background uintptr) {
	cursor, _, _ := pLoadCursorW.Call(0, 32512) // the arrow
	wc := wndClassEx{
		WndProc: wndProcCb, Instance: instance, Cursor: cursor, Background: background,
		ClassName: wstr(name),
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
}

// ui owns the UI thread: the hidden window, the tray icon, the fix window and the keyboard hook.
type ui struct {
	hwnd uintptr
	keys hotkeys
	// events carries the dictation keys away from the hook, which has to return at once.
	events chan keyEvent
	menu   func(cmd int)
	click  func()

	mu    sync.Mutex
	queue []func()

	nid            notifyIconData
	icons          map[string]uintptr
	taskbarCreated uint32
}

func newUI() *ui {
	u := &ui{events: make(chan keyEvent, 64), icons: map[string]uintptr{}}
	registerClass(className, 0)
	u.hwnd, _, _ = pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(wstr(className))), uintptr(unsafe.Pointer(wstr(className))),
		wsOverlapped, 0, 0, 0, 0, 0, 0, instance, 0)
	handlers[u.hwnd] = u.handle
	r, _, _ := pRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(wstr("TaskbarCreated"))))
	u.taskbarCreated = uint32(r)
	activeUI = u
	return u
}

// do runs f on the UI thread.
func (u *ui) do(f func()) {
	u.mu.Lock()
	u.queue = append(u.queue, f)
	u.mu.Unlock()
	pPostMessageW.Call(u.hwnd, wmAppRun, 0, 0)
}

func (u *ui) handle(msg uint32, w, l uintptr) (uintptr, bool) {
	switch msg {
	case wmAppRun:
		u.mu.Lock()
		queue := u.queue
		u.queue = nil
		u.mu.Unlock()
		for _, f := range queue {
			f()
		}
		return 0, true
	case wmAppTray:
		switch uint32(l) {
		case wmRButtonUp:
			u.showMenu()
		case wmLButtonUp:
			u.click()
		}
		return 0, true
	case wmAppQuit, wmClose:
		u.menu(menuQuit)
		return 0, true
	case wmAppAgain:
		u.click()
		return 0, true
	case u.taskbarCreated: // Explorer restarted and lost the icon
		pShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&u.nid)))
		return 0, true
	}
	return 0, false
}

func (u *ui) loop() {
	var m msg
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func (u *ui) quit() {
	pShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&u.nid)))
	pPostQuitMessage.Call(0)
}

// The tray icon.

func (u *ui) addTray(state, tip string) {
	u.nid = notifyIconData{Wnd: u.hwnd, ID: 1, Flags: nifMessage | nifIcon | nifTip, CallbackMessage: wmAppTray}
	u.nid.Size = uint32(unsafe.Sizeof(u.nid))
	u.nid.Icon = u.icon(state)
	setUTF16(u.nid.Tip[:], tip)
	pShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&u.nid)))
}

func (u *ui) setTray(state, tip string) {
	u.nid.Flags = nifIcon | nifTip
	u.nid.Icon = u.icon(state)
	u.nid.Tip = [128]uint16{}
	setUTF16(u.nid.Tip[:], tip)
	pShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&u.nid)))
}

func (u *ui) balloon(title, text string) {
	n := u.nid
	n.Flags = nifInfo
	n.InfoFlags = niifInfo | niifNoSound
	setUTF16(n.InfoTitle[:], title)
	setUTF16(n.Info[:], text)
	pShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&n)))
}

func (u *ui) icon(state string) uintptr {
	if h := u.icons[state]; h != 0 {
		return h
	}
	size, _, _ := pGetSystemMetrics.Call(smCxSmIcon)
	h := createIcon(int(size), stateColors[state])
	u.icons[state] = h
	return h
}

func createIcon(size int, rgb [3]byte) uintptr {
	bmi := bitmapInfoHeader{Width: int32(size), Height: -int32(size), Planes: 1, BitCount: 32}
	bmi.Size = uint32(unsafe.Sizeof(bmi))
	var bits uintptr
	color, _, _ := pCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&bmi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if color == 0 {
		return 0
	}
	defer pDeleteObject.Call(color)
	copy(unsafe.Slice((*byte)(unsafe.Pointer(bits)), size*size*4), iconPixels(size, rgb))
	mask, _, _ := pCreateBitmap.Call(uintptr(size), uintptr(size), 1, 1, 0)
	defer pDeleteObject.Call(mask)
	info := iconInfo{Icon: 1, Mask: mask, Color: color}
	h, _, _ := pCreateIconIndirect.Call(uintptr(unsafe.Pointer(&info)))
	return h
}

// The tray menu.
const (
	menuFix = iota + 1
	menuRecordings
	menuLog
	menuUninstall
	menuQuit
)

func (u *ui) showMenu() {
	m, _, _ := pCreatePopupMenu.Call()
	defer pDestroyMenu.Call(m)
	for _, item := range []struct {
		id   int
		text string
	}{
		{menuFix, "Fix the last dictation\tF5"},
		{menuRecordings, "Open the saved dictations"},
		{menuLog, "Open the log"},
		{0, ""},
		{menuUninstall, "Uninstall VoiceRefine…"},
		{menuQuit, "Quit VoiceRefine"},
	} {
		if item.id == 0 {
			pAppendMenuW.Call(m, mfSeparator, 0, 0)
			continue
		}
		pAppendMenuW.Call(m, mfString, uintptr(item.id), uintptr(unsafe.Pointer(wstr(item.text))))
	}
	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// The menu only closes on a click elsewhere while VoiceRefine is in front.
	pSetForegroundWindow.Call(u.hwnd)
	cmd, _, _ := pTrackPopupMenu.Call(m, tpmReturnCmd|tpmRightButton, uintptr(pt.X), uintptr(pt.Y), 0, u.hwnd, 0)
	pPostMessageW.Call(u.hwnd, 0, 0, 0)
	if cmd != 0 {
		u.menu(int(cmd))
	}
}

// The keyboard hook sees every key before the app in front does. It has a thread of its own:
// Windows drops a hook without a word when its thread is slow to answer even once.

func (u *ui) hookKeys() error {
	result := make(chan error)
	go func() {
		runtime.LockOSThread()
		h, _, err := pSetWindowsHookExW.Call(whKeyboardLL, hookProcCb, instance, 0)
		if h == 0 {
			result <- err
			return
		}
		result <- nil
		var m msg
		for {
			if r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0); int32(r) <= 0 {
				return
			}
		}
	}()
	return <-result
}

func hookProc(code, w, l uintptr) uintptr {
	if u := activeUI; int32(code) >= 0 && u != nil {
		kb := (*kbdLLHookStruct)(unsafe.Pointer(l))
		// Keys VoiceRefine types itself, and every other program's, pass.
		if kb.Flags&llkhfInjected == 0 {
			down := w == wmKeyDown || w == wmSysKeyDown
			m := mods{
				ctrl: keyDown(vkControl), alt: keyDown(vkMenu), shift: keyDown(vkShift),
				win: keyDown(vkLWin) || keyDown(vkRWin),
			}
			swallow, ev, mask := u.keys.handle(kb.VkCode, down, m)
			if ev != nil {
				select {
				case u.events <- *ev:
				default:
				}
			}
			if mask {
				u.do(maskStartMenu)
			}
			if swallow {
				return 1
			}
		}
	}
	r, _, _ := pCallNextHookEx.Call(0, code, w, l)
	return r
}

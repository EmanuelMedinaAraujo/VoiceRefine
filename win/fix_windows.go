package main

import (
	"strings"
	"syscall"
	"unsafe"
)

const (
	fixClass       = "VoiceRefineFix"
	wmAppSubmit    = wmApp + 10
	wmAppDismiss   = wmApp + 11 // closes without taking the focus back: the click elsewhere has it
	colorWindow    = 5
	waInactive     = 0
	monitorNearest = 2
)

var editProcCb = syscall.NewCallback(editProc)

// fixWindow is the window F5 opens: one text box with the last dictation. Enter saves the fix,
// Shift+Enter adds a line break, Escape or a click elsewhere closes it.
type fixWindow struct {
	ui *ui
	d  *Dictation

	hwnd, edit, hint uintptr
	fonts            []uintptr
	editProc         uintptr // the text box's own window procedure
	dir, original    string
	prev             uintptr // the window that had the focus, and gets it back
}

var openFix *fixWindow // the one fix window, for the text box's procedure

func (f *fixWindow) open() {
	if f.hwnd != 0 {
		forceForeground(f.hwnd)
		return
	}
	dir, text, reason := f.d.FixTarget()
	if reason != "" {
		f.ui.balloon("Nothing to fix", reason)
		return
	}
	f.dir, f.original, f.prev = dir, text, foregroundWindow()

	dpi := systemDPI()
	scale := func(v int) int { return v * dpi / 96 }
	w, h := scale(640), scale(210)
	var mi monitorInfo
	mi.Size = uint32(unsafe.Sizeof(mi))
	mon, _, _ := pMonitorFromWindow.Call(f.prev, monitorNearest)
	pGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi)))
	x := int(mi.Work.Left) + (int(mi.Work.Right-mi.Work.Left)-w)/2
	y := int(mi.Work.Top) + (int(mi.Work.Bottom-mi.Work.Top)-h)/3

	f.hwnd, _, _ = pCreateWindowExW.Call(wsExTopmost|wsExToolWindow, uintptr(unsafe.Pointer(wstr(fixClass))),
		uintptr(unsafe.Pointer(wstr("Fix the last dictation"))), wsPopup|wsCaption|wsSysMenu,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h), 0, 0, instance, 0)
	if f.hwnd == 0 {
		return
	}
	handlers[f.hwnd] = f.handle
	f.edit, _, _ = pCreateWindowExW.Call(wsExClientEdge, uintptr(unsafe.Pointer(wstr("EDIT"))),
		uintptr(unsafe.Pointer(wstr(strings.ReplaceAll(text, "\n", "\r\n")))),
		wsChild|wsVisible|wsVScroll|esMultiline|esAutoVScroll|esWantReturn, 0, 0, 0, 0, f.hwnd, 0, instance, 0)
	f.hint, _, _ = pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(wstr("STATIC"))),
		uintptr(unsafe.Pointer(wstr("Enter saves the fix · Shift+Enter starts a new line · Escape closes"))),
		wsChild|wsVisible, 0, 0, 0, 0, f.hwnd, 0, instance, 0)
	f.fonts = []uintptr{font(scale(19)), font(scale(14))}
	pSendMessageW.Call(f.edit, wmSetFont, f.fonts[0], 1)
	pSendMessageW.Call(f.hint, wmSetFont, f.fonts[1], 1)
	f.editProc, _, _ = pSetWindowLongPtrW.Call(f.edit, gwlpWndProc, editProcCb)
	openFix = f
	f.layout()

	pShowWindow.Call(f.hwnd, swShow)
	forceForeground(f.hwnd)
	pSetFocus.Call(f.edit)
	end := uintptr(len(syscall.StringToUTF16(text)) + strings.Count(text, "\n"))
	pSendMessageW.Call(f.edit, emSetSel, end, end)
}

func font(height int) uintptr {
	h, _, _ := pCreateFontW.Call(uintptr(-int32(height)), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 5, 0,
		uintptr(unsafe.Pointer(wstr("Segoe UI"))))
	return h
}

func (f *fixWindow) layout() {
	var r rect
	pGetClientRect.Call(f.hwnd, uintptr(unsafe.Pointer(&r)))
	dpi := int32(systemDPI())
	pad, hint := 12*dpi/96, 22*dpi/96
	pMoveWindow.Call(f.edit, uintptr(pad), uintptr(pad), uintptr(r.Right-2*pad), uintptr(r.Bottom-3*pad-hint), 1)
	pMoveWindow.Call(f.hint, uintptr(pad), uintptr(r.Bottom-pad-hint), uintptr(r.Right-2*pad), uintptr(hint), 1)
}

func (f *fixWindow) handle(msg uint32, w, l uintptr) (uintptr, bool) {
	switch msg {
	case wmSize:
		f.layout()
		return 0, true
	case wmActivate:
		if loword(w) == waInactive {
			pPostMessageW.Call(f.hwnd, wmAppDismiss, 0, 0)
		}
	case wmAppDismiss:
		f.close()
		return 0, true
	case wmClose:
		f.close()
		pSetForegroundWindow.Call(f.prev)
		return 0, true
	case wmAppSubmit:
		f.submit()
		return 0, true
	}
	return 0, false
}

func (f *fixWindow) close() {
	if f.hwnd == 0 {
		return
	}
	hwnd := f.hwnd
	f.hwnd = 0
	delete(handlers, hwnd)
	pDestroyWindow.Call(hwnd)
	for _, h := range f.fonts {
		pDeleteObject.Call(h)
	}
	f.fonts = nil
	openFix = nil
}

func (f *fixWindow) submit() {
	n, _, _ := pGetWindowTextLengthW.Call(f.edit)
	buf := make([]uint16, n+1)
	pGetWindowTextW.Call(f.edit, uintptr(unsafe.Pointer(&buf[0])), n+1)
	text := strings.ReplaceAll(syscall.UTF16ToString(buf), "\r\n", "\n")
	dir, original := f.dir, f.original
	f.close()
	// Closing first hands the focus back to the app, which the fix may type into.
	pSetForegroundWindow.Call(f.prev)
	if text == original {
		return
	}
	go f.d.Fix(dir, text)
}

func editProc(hwnd, m, w, l uintptr) uintptr {
	msg := uint32(m)
	f := openFix
	if f == nil {
		return 0
	}
	shift := keyDown(vkShift)
	switch {
	case msg == wmKeyDown && w == vkReturn && !shift:
		pPostMessageW.Call(f.hwnd, wmAppSubmit, 0, 0)
		return 0
	case msg == wmKeyDown && w == vkEscape:
		pPostMessageW.Call(f.hwnd, wmClose, 0, 0)
		return 0
	case msg == wmKeyDown && w == 'A' && keyDown(vkControl):
		pSendMessageW.Call(hwnd, emSetSel, 0, ^uintptr(0))
		return 0
	case msg == wmChar && (w == '\r' && !shift || w == vkEscape || w == 1): // 1 is Ctrl+A
		return 0
	}
	r, _, _ := pCallWindowProcW.Call(f.editProc, hwnd, uintptr(msg), w, l)
	return r
}

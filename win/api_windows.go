package main

import (
	"os/exec"
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")
	winmm    = syscall.NewLazyDLL("winmm.dll")

	pRegisterClassExW          = user32.NewProc("RegisterClassExW")
	pCreateWindowExW           = user32.NewProc("CreateWindowExW")
	pDestroyWindow             = user32.NewProc("DestroyWindow")
	pDefWindowProcW            = user32.NewProc("DefWindowProcW")
	pCallWindowProcW           = user32.NewProc("CallWindowProcW")
	pSetWindowLongPtrW         = user32.NewProc("SetWindowLongPtrW")
	pGetMessageW               = user32.NewProc("GetMessageW")
	pTranslateMessage          = user32.NewProc("TranslateMessage")
	pDispatchMessageW          = user32.NewProc("DispatchMessageW")
	pPostMessageW              = user32.NewProc("PostMessageW")
	pSendMessageW              = user32.NewProc("SendMessageW")
	pPostQuitMessage           = user32.NewProc("PostQuitMessage")
	pFindWindowW               = user32.NewProc("FindWindowW")
	pShowWindow                = user32.NewProc("ShowWindow")
	pMoveWindow                = user32.NewProc("MoveWindow")
	pGetClientRect             = user32.NewProc("GetClientRect")
	pSetFocus                  = user32.NewProc("SetFocus")
	pGetForegroundWindow       = user32.NewProc("GetForegroundWindow")
	pSetForegroundWindow       = user32.NewProc("SetForegroundWindow")
	pBringWindowToTop          = user32.NewProc("BringWindowToTop")
	pGetWindowThreadProcessId  = user32.NewProc("GetWindowThreadProcessId")
	pAttachThreadInput         = user32.NewProc("AttachThreadInput")
	pGetWindowTextLengthW      = user32.NewProc("GetWindowTextLengthW")
	pGetWindowTextW            = user32.NewProc("GetWindowTextW")
	pSetWindowTextW            = user32.NewProc("SetWindowTextW")
	pMonitorFromWindow         = user32.NewProc("MonitorFromWindow")
	pGetMonitorInfoW           = user32.NewProc("GetMonitorInfoW")
	pGetDpiForSystem           = user32.NewProc("GetDpiForSystem")
	pSetProcessDpiAwarenessCtx = user32.NewProc("SetProcessDpiAwarenessContext")
	pGetSystemMetrics          = user32.NewProc("GetSystemMetrics")
	pSetWindowsHookExW         = user32.NewProc("SetWindowsHookExW")
	pCallNextHookEx            = user32.NewProc("CallNextHookEx")
	pGetAsyncKeyState          = user32.NewProc("GetAsyncKeyState")
	pSendInput                 = user32.NewProc("SendInput")
	pCreatePopupMenu           = user32.NewProc("CreatePopupMenu")
	pAppendMenuW               = user32.NewProc("AppendMenuW")
	pTrackPopupMenu            = user32.NewProc("TrackPopupMenu")
	pDestroyMenu               = user32.NewProc("DestroyMenu")
	pGetCursorPos              = user32.NewProc("GetCursorPos")
	pCreateIconIndirect        = user32.NewProc("CreateIconIndirect")
	pDestroyIcon               = user32.NewProc("DestroyIcon")
	pLoadCursorW               = user32.NewProc("LoadCursorW")
	pMessageBoxW               = user32.NewProc("MessageBoxW")
	pOpenClipboard             = user32.NewProc("OpenClipboard")
	pCloseClipboard            = user32.NewProc("CloseClipboard")
	pEmptyClipboard            = user32.NewProc("EmptyClipboard")
	pSetClipboardData          = user32.NewProc("SetClipboardData")
	pRegisterClipboardFormatW  = user32.NewProc("RegisterClipboardFormatW")
	pRegisterWindowMessageW    = user32.NewProc("RegisterWindowMessageW")
	pGetModuleHandleW          = kernel32.NewProc("GetModuleHandleW")
	pCreateMutexW              = kernel32.NewProc("CreateMutexW")
	pCreateEventW              = kernel32.NewProc("CreateEventW")
	pWaitForSingleObject       = kernel32.NewProc("WaitForSingleObject")
	pGetCurrentThreadId        = kernel32.NewProc("GetCurrentThreadId")
	pGlobalAlloc               = kernel32.NewProc("GlobalAlloc")
	pGlobalLock                = kernel32.NewProc("GlobalLock")
	pGlobalUnlock              = kernel32.NewProc("GlobalUnlock")
	pGlobalFree                = kernel32.NewProc("GlobalFree")
	pVirtualAlloc              = kernel32.NewProc("VirtualAlloc")
	pVirtualFree               = kernel32.NewProc("VirtualFree")
	pCreateJobObjectW          = kernel32.NewProc("CreateJobObjectW")
	pSetInformationJobObject   = kernel32.NewProc("SetInformationJobObject")
	pAssignProcessToJobObject  = kernel32.NewProc("AssignProcessToJobObject")
	pOpenProcess               = kernel32.NewProc("OpenProcess")
	pGetExitCodeProcess        = kernel32.NewProc("GetExitCodeProcess")
	pCreateFileW               = kernel32.NewProc("CreateFileW")
	pSetDefaultDllDirectories  = kernel32.NewProc("SetDefaultDllDirectories")
	pCreateFontW               = gdi32.NewProc("CreateFontW")
	pCreateBitmap              = gdi32.NewProc("CreateBitmap")
	pCreateDIBSection          = gdi32.NewProc("CreateDIBSection")
	pDeleteObject              = gdi32.NewProc("DeleteObject")
	pShellNotifyIconW          = shell32.NewProc("Shell_NotifyIconW")
	pShellExecuteExW           = shell32.NewProc("ShellExecuteExW")
	pRegCreateKeyExW           = advapi32.NewProc("RegCreateKeyExW")
	pRegSetValueExW            = advapi32.NewProc("RegSetValueExW")
	pRegDeleteValueW           = advapi32.NewProc("RegDeleteValueW")
	pRegCloseKey               = advapi32.NewProc("RegCloseKey")
	pWaveInOpen                = winmm.NewProc("waveInOpen")
	pWaveInClose               = winmm.NewProc("waveInClose")
	pWaveInPrepareHeader       = winmm.NewProc("waveInPrepareHeader")
	pWaveInUnprepareHeader     = winmm.NewProc("waveInUnprepareHeader")
	pWaveInAddBuffer           = winmm.NewProc("waveInAddBuffer")
	pWaveInStart               = winmm.NewProc("waveInStart")
	pWaveInReset               = winmm.NewProc("waveInReset")
)

const (
	wmDestroy          = 0x0002
	wmSize             = 0x0005
	wmActivate         = 0x0006
	wmClose            = 0x0010
	wmKeyDown          = 0x0100
	wmKeyUp            = 0x0101
	wmChar             = 0x0102
	wmSysKeyDown       = 0x0104
	wmSysKeyUp         = 0x0105
	wmCommand          = 0x0111
	wmLButtonUp        = 0x0202
	wmRButtonUp        = 0x0205
	wmSetFont          = 0x0030
	wmApp              = 0x8000
	wmAppRun           = wmApp + 1 // runs the queued functions on the window thread
	wmAppTray          = wmApp + 2 // tray icon clicks
	wmAppQuit          = wmApp + 3 // a newer copy asks this one to make room
	wmAppAgain         = wmApp + 4 // VoiceRefine was started again while running
	wsOverlapped       = 0x00000000
	wsPopup            = 0x80000000
	wsVisible          = 0x10000000
	wsChild            = 0x40000000
	wsCaption          = 0x00C00000
	wsSysMenu          = 0x00080000
	wsVScroll          = 0x00200000
	wsExTopmost        = 0x00000008
	wsExToolWindow     = 0x00000080
	wsExClientEdge     = 0x00000200
	esMultiline        = 0x0004
	esAutoVScroll      = 0x0040
	esWantReturn       = 0x1000
	emSetSel           = 0x00B1
	swShow             = 5
	swShowNormal       = 1
	whKeyboardLL       = 13
	llkhfInjected      = 0x10
	inputKeyboard      = 1
	keyEventKeyUp      = 0x0002
	keyEventUnicode    = 0x0004
	vkBack             = 0x08
	vkReturn           = 0x0D
	vkEscape           = 0x1B
	vkShift            = 0x10
	vkControl          = 0x11
	vkMenu             = 0x12
	vkLWin             = 0x5B
	vkRWin             = 0x5C
	vkMask             = 0xE8 // unassigned: pressed to keep the Start menu shut
	nimAdd             = 0
	nimModify          = 1
	nimDelete          = 2
	nifMessage         = 0x1
	nifIcon            = 0x2
	nifTip             = 0x4
	nifInfo            = 0x10
	niifInfo           = 0x1
	niifNoSound        = 0x10
	mfString           = 0x0
	mfSeparator        = 0x800
	tpmReturnCmd       = 0x0100
	tpmRightButton     = 0x0002
	cfUnicodeText      = 13
	gmemMoveable       = 0x2
	gmemZeroInit       = 0x40
	mbYesNo            = 0x4
	mbIconQuestion     = 0x20
	mbIconError        = 0x10
	idYes              = 6
	hkeyCurrentUser    = 0x80000001
	keySetValue        = 0x0002
	regSZ              = 1
	smCxSmIcon         = 49
	gwlpWndProc        = ^uintptr(3) // -4
	errorAlreadyExists = 183
)

func wstr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func setUTF16(dst []uint16, s string) {
	u, _ := syscall.UTF16FromString(s)
	if len(u) > len(dst) {
		u = u[:len(dst)]
		u[len(u)-1] = 0
	}
	copy(dst, u)
}

func loword(x uintptr) uint32 { return uint32(x & 0xffff) }

func messageBox(hwnd uintptr, title, text string, flags uintptr) int {
	r, _, _ := pMessageBoxW.Call(hwnd, uintptr(unsafe.Pointer(wstr(text))), uintptr(unsafe.Pointer(wstr(title))), flags)
	return int(r)
}

// open shows a folder in Explorer, or a file in the app Windows opens it with.
func open(path string) { exec.Command("explorer.exe", path).Start() }

// systemDPI is 96 at 100 % scaling; Windows 10 before 1607 has no GetDpiForSystem.
func systemDPI() int {
	if pGetDpiForSystem.Find() != nil {
		return 96
	}
	dpi, _, _ := pGetDpiForSystem.Call()
	return int(dpi)
}

func foregroundWindow() uintptr {
	h, _, _ := pGetForegroundWindow.Call()
	return h
}

// forceForeground brings hwnd to the front. Windows only lets the process that got the last input
// do that, so it borrows the input queue of the window in front for a moment.
func forceForeground(hwnd uintptr) {
	fg := foregroundWindow()
	fgThread, _, _ := pGetWindowThreadProcessId.Call(fg, 0)
	ours, _, _ := pGetCurrentThreadId.Call()
	if fg != 0 && fgThread != ours {
		pAttachThreadInput.Call(ours, fgThread, 1)
		defer pAttachThreadInput.Call(ours, fgThread, 0)
	}
	pBringWindowToTop.Call(hwnd)
	pSetForegroundWindow.Call(hwnd)
	pSetFocus.Call(hwnd)
}

func keyDown(vk uintptr) bool {
	r, _, _ := pGetAsyncKeyState.Call(vk)
	return r&0x8000 != 0
}

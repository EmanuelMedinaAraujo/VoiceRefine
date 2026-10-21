package main

import (
	"testing"
	"unsafe"
)

// The sizes Windows expects on 64-bit, from the Windows SDK headers.
func TestWin32StructSizes(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("the layouts are for 64-bit Windows")
	}
	for name, got := range map[string]uintptr{
		"MSG":                                  unsafe.Sizeof(msg{}),
		"WNDCLASSEXW":                          unsafe.Sizeof(wndClassEx{}),
		"KBDLLHOOKSTRUCT":                      unsafe.Sizeof(kbdLLHookStruct{}),
		"INPUT":                                unsafe.Sizeof(input{}),
		"NOTIFYICONDATAW":                      unsafe.Sizeof(notifyIconData{}),
		"WAVEHDR":                              unsafe.Sizeof(waveHdr{}),
		"BITMAPINFOHEADER":                     unsafe.Sizeof(bitmapInfoHeader{}),
		"ICONINFO":                             unsafe.Sizeof(iconInfo{}),
		"MONITORINFO":                          unsafe.Sizeof(monitorInfo{}),
		"SHELLEXECUTEINFOW":                    unsafe.Sizeof(shellExecuteInfo{}),
		"JOBOBJECT_EXTENDED_LIMIT_INFORMATION": unsafe.Sizeof(jobExtendedLimits{}),
	} {
		want := map[string]uintptr{
			"MSG": 48, "WNDCLASSEXW": 80, "KBDLLHOOKSTRUCT": 24, "INPUT": 40, "NOTIFYICONDATAW": 976,
			"WAVEHDR": 48, "BITMAPINFOHEADER": 40, "ICONINFO": 32, "MONITORINFO": 40,
			"SHELLEXECUTEINFOW": 112, "JOBOBJECT_EXTENDED_LIMIT_INFORMATION": 144,
		}[name]
		if got != want {
			t.Errorf("%s is %d bytes, want %d", name, got, want)
		}
	}
	// WAVEFORMATEX is packed to 18 bytes; Windows reads no more than that.
	if off := unsafe.Offsetof(waveFormatEx{}.CbSize); off != 16 {
		t.Errorf("WAVEFORMATEX.cbSize at %d, want 16", off)
	}
	if off := unsafe.Offsetof(input{}.Ki); off != 8 {
		t.Errorf("INPUT.ki at %d, want 8", off)
	}
	if off := unsafe.Offsetof(notifyIconData{}.Info); off != 304 {
		t.Errorf("NOTIFYICONDATAW.szInfo at %d, want 304", off)
	}
}

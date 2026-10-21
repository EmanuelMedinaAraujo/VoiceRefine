package main

import (
	"errors"
	"time"
	"unicode/utf16"
	"unsafe"
)

// desktop types into the window in front with SendInput, the way Voxtype types with wtype.
type desktop struct {
	ui *ui
}

func (desktop) Foreground() uintptr { return foregroundWindow() }

func (d desktop) Notify(title, text string) { d.ui.do(func() { d.ui.balloon(title, text) }) }

// waitForModifiers waits until the dictation keys' Ctrl, Alt, Shift and Windows are up: typed
// while one is held, the letters would become shortcuts.
func waitForModifiers() {
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if !keyDown(vkShift) && !keyDown(vkControl) && !keyDown(vkMenu) && !keyDown(vkLWin) && !keyDown(vkRWin) {
			return
		}
	}
}

func key(vk uint16, up bool) input {
	in := input{Type: inputKeyboard, Ki: keybdInput{Vk: vk}}
	if up {
		in.Ki.Flags = keyEventKeyUp
	}
	return in
}

func unicodeKey(unit uint16, up bool) input {
	in := input{Type: inputKeyboard, Ki: keybdInput{Scan: unit, Flags: keyEventUnicode}}
	if up {
		in.Ki.Flags |= keyEventKeyUp
	}
	return in
}

func sendInputs(ins []input) error {
	if len(ins) == 0 {
		return nil
	}
	n, _, err := pSendInput.Call(uintptr(len(ins)), uintptr(unsafe.Pointer(&ins[0])), unsafe.Sizeof(ins[0]))
	if int(n) != len(ins) {
		return errors.New("Windows blocked the typing: " + err.Error())
	}
	return nil
}

// Type sends the text as characters, so it does not depend on the keyboard layout. A line break
// is an Enter.
func (desktop) Type(text string) error {
	waitForModifiers()
	var batch []input
	for _, r := range text {
		if r == '\n' {
			batch = append(batch, key(vkReturn, false), key(vkReturn, true))
		} else {
			for _, unit := range utf16.Encode([]rune{r}) {
				batch = append(batch, unicodeKey(unit, false), unicodeKey(unit, true))
			}
		}
		// Small batches with a pause, like Voxtype's type_delay_ms, so no app drops letters.
		if len(batch) >= 32 {
			if err := sendInputs(batch); err != nil {
				return err
			}
			batch = batch[:0]
			time.Sleep(time.Millisecond)
		}
	}
	return sendInputs(batch)
}

func (desktop) Erase(n int) error {
	waitForModifiers()
	for ; n > 0; n -= 16 {
		var batch []input
		for range min(n, 16) {
			batch = append(batch, key(vkBack, false), key(vkBack, true))
		}
		if err := sendInputs(batch); err != nil {
			return err
		}
		time.Sleep(time.Millisecond)
	}
	return nil
}

func (d desktop) Copy(text string) error {
	done := make(chan error, 1)
	d.ui.do(func() { done <- setClipboard(d.ui.hwnd, text) })
	return <-done
}

func setClipboard(owner uintptr, text string) error {
	units := utf16.Encode([]rune(text + "\x00"))
	size := uintptr(len(units) * 2)
	mem, _, _ := pGlobalAlloc.Call(gmemMoveable, size)
	if mem == 0 {
		return errors.New("no memory for the clipboard")
	}
	p, _, _ := pGlobalLock.Call(mem)
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(p)), len(units)), units)
	pGlobalUnlock.Call(mem)

	var opened uintptr
	for range 10 { // another program may hold the clipboard for a moment
		if opened, _, _ = pOpenClipboard.Call(owner); opened != 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if opened == 0 {
		pGlobalFree.Call(mem)
		return errors.New("the clipboard is busy")
	}
	defer pCloseClipboard.Call()
	pEmptyClipboard.Call()
	if r, _, _ := pSetClipboardData.Call(cfUnicodeText, mem); r == 0 {
		pGlobalFree.Call(mem)
		return errors.New("the clipboard did not take the text")
	}
	// Dictated text stays out of the clipboard history and off the cloud clipboard.
	for _, format := range []string{"CanIncludeInClipboardHistory", "CanUploadToCloudClipboard"} {
		id, _, _ := pRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(wstr(format))))
		no, _, _ := pGlobalAlloc.Call(gmemMoveable|gmemZeroInit, 4) // a DWORD 0
		if id == 0 || no == 0 {
			continue
		}
		if r, _, _ := pSetClipboardData.Call(id, no); r == 0 {
			pGlobalFree.Call(no)
		}
	}
	return nil
}

// maskStartMenu presses a key that does nothing, so releasing the Windows key after a swallowed
// Win+F9 does not open the Start menu.
func maskStartMenu() {
	sendInputs([]input{key(vkMask, false), key(vkMask, true)})
}

package main

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"github.com/EmanuelMedinaAraujo/VoiceRefine/internal/clip"
)

const (
	waveMapper     = 0xFFFFFFFF // the default recording device
	callbackEvent  = 0x00050000
	whdrDone       = 0x1
	memCommit      = 0x1000
	memReserve     = 0x2000
	memRelease     = 0x8000
	pageReadWrite  = 0x04
	bufferCount    = 8
	bufferDuration = 50 // ms per buffer: the delay before the last samples arrive
)

// waveIn records the default microphone with the waveIn API, which converts to 16 kHz mono itself.
type waveIn struct{}

type waveTake struct {
	handle   uintptr
	event    uintptr
	mem      uintptr // the headers and the buffers, outside Go's heap: Windows writes into them
	headers  []waveHdr
	mu       sync.Mutex
	pcm      []byte
	stopping bool
	done     chan struct{}
}

func (waveIn) Start() (Take, error) {
	t := &waveTake{done: make(chan struct{})}
	t.event, _, _ = pCreateEventW.Call(0, 0, 0, 0)
	format := waveFormatEx{
		FormatTag: 1, Channels: 1, SamplesPerSec: clip.SampleRate,
		AvgBytesPerSec: clip.SampleRate * 2, BlockAlign: 2, BitsPerSample: 16,
	}
	if r, _, _ := pWaveInOpen.Call(uintptr(unsafe.Pointer(&t.handle)), waveMapper, uintptr(unsafe.Pointer(&format)),
		t.event, 0, callbackEvent); r != 0 {
		syscall.CloseHandle(syscall.Handle(t.event))
		return nil, fmt.Errorf("no microphone to record from (waveIn error %d)", r)
	}

	bufBytes := clip.SampleRate * 2 * bufferDuration / 1000
	hdrSize := int(unsafe.Sizeof(waveHdr{}))
	total := bufferCount * (hdrSize + bufBytes)
	t.mem, _, _ = pVirtualAlloc.Call(0, uintptr(total), memCommit|memReserve, pageReadWrite)
	if t.mem == 0 {
		t.close()
		return nil, fmt.Errorf("no memory to record into")
	}
	t.headers = unsafe.Slice((*waveHdr)(unsafe.Pointer(t.mem)), bufferCount)
	data := t.mem + uintptr(bufferCount*hdrSize)
	for i := range t.headers {
		h := &t.headers[i]
		*h = waveHdr{Data: data + uintptr(i*bufBytes), BufferLength: uint32(bufBytes)}
		pWaveInPrepareHeader.Call(t.handle, uintptr(unsafe.Pointer(h)), uintptr(hdrSize))
		pWaveInAddBuffer.Call(t.handle, uintptr(unsafe.Pointer(h)), uintptr(hdrSize))
	}
	if r, _, _ := pWaveInStart.Call(t.handle); r != 0 {
		t.close()
		return nil, fmt.Errorf("the microphone did not start (waveIn error %d)", r)
	}
	go t.collect()
	return t, nil
}

// collect moves each filled buffer into pcm and hands it back to Windows. Buffers fill in the
// order they were queued, so they are read in that order too.
func (t *waveTake) collect() {
	defer close(t.done)
	hdrSize := unsafe.Sizeof(waveHdr{})
	next := 0
	for {
		pWaitForSingleObject.Call(t.event, 20)
		t.mu.Lock()
		for h := &t.headers[next]; h.Flags&whdrDone != 0; h = &t.headers[next] {
			t.pcm = append(t.pcm, unsafe.Slice((*byte)(unsafe.Pointer(h.Data)), h.BytesRecorded)...)
			h.Flags &^= whdrDone
			h.BytesRecorded = 0
			next = (next + 1) % len(t.headers)
			if t.stopping {
				continue
			}
			pWaveInAddBuffer.Call(t.handle, uintptr(unsafe.Pointer(h)), hdrSize)
		}
		stopping := t.stopping
		t.mu.Unlock()
		if stopping {
			return
		}
	}
}

// Stop ends the recording. waveInReset hands back the buffers half filled, so nothing said
// before the key went up is lost. Under the lock, so no buffer is queued again after it.
func (t *waveTake) Stop() []byte {
	t.mu.Lock()
	t.stopping = true
	pWaveInReset.Call(t.handle)
	t.mu.Unlock()
	<-t.done
	t.close()
	return t.pcm
}

func (t *waveTake) close() {
	hdrSize := unsafe.Sizeof(waveHdr{})
	if t.handle != 0 {
		for i := range t.headers {
			pWaveInUnprepareHeader.Call(t.handle, uintptr(unsafe.Pointer(&t.headers[i])), hdrSize)
		}
		pWaveInClose.Call(t.handle)
		t.handle = 0
	}
	if t.mem != 0 {
		pVirtualFree.Call(t.mem, 0, memRelease)
		t.mem = 0
	}
	if t.event != 0 {
		syscall.CloseHandle(syscall.Handle(t.event))
		t.event = 0
	}
}

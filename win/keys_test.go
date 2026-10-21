package main

import "testing"

func TestHotkeys(t *testing.T) {
	var h hotkeys
	none, win, ctrlWin, ctrlWinAlt := mods{}, mods{win: true}, mods{ctrl: true, win: true}, mods{ctrl: true, win: true, alt: true}

	step := func(vk uint32, down bool, m mods, wantSwallow, wantMask bool, want *keyEvent) {
		t.Helper()
		swallow, ev, mask := h.handle(vk, down, m)
		if swallow != wantSwallow || mask != wantMask {
			t.Fatalf("key %#x down=%v: swallow %v mask %v", vk, down, swallow, mask)
		}
		if (ev == nil) != (want == nil) || ev != nil && *ev != *want {
			t.Fatalf("key %#x down=%v: event %+v, want %+v", vk, down, ev, want)
		}
	}

	// F9 push-to-talk: the press starts, repeats are swallowed, the release stops.
	step(vkF9, true, none, true, false, &keyEvent{kind: keyStart, vk: vkF9, language: "en"})
	step(vkF9, true, none, true, false, nil)
	step(vkF9, false, none, true, false, &keyEvent{kind: keyStop, vk: vkF9})

	// Win+F10 is private German, and keeps the Start menu shut.
	step(vkF10, true, win, true, true, &keyEvent{kind: keyStart, vk: vkF10, language: "de", private: true})
	step(vkF10, false, none, true, false, &keyEvent{kind: keyStop, vk: vkF10})

	step(vkX, true, ctrlWin, true, true, &keyEvent{kind: keyToggle, language: "en"})
	step(vkX, false, ctrlWin, true, false, nil)
	step(vkX, true, ctrlWinAlt, true, true, &keyEvent{kind: keyToggle, language: "de"})
	step(vkX, false, none, true, false, nil)

	step(vkF5, true, none, true, false, &keyEvent{kind: keyFix})
	step(vkF5, false, none, true, false, nil)

	// Everything else reaches the app: Ctrl+F5, Shift+F9, a plain X, and their releases.
	step(vkF5, true, mods{ctrl: true}, false, false, nil)
	step(vkF5, false, none, false, false, nil)
	step(vkF9, true, mods{shift: true}, false, false, nil)
	step(vkF9, false, none, false, false, nil)
	step(vkX, true, none, false, false, nil)
	step(vkX, false, none, false, false, nil)
}

func TestDispatchStopsOnlyTheTakeItsKeyStarted(t *testing.T) {
	r := newRig(t)
	events := make(chan keyEvent, 10)
	fixes := 0
	events <- keyEvent{kind: keyToggle, language: "en"}
	events <- keyEvent{kind: keyStart, vk: vkF9, language: "en"} // ignored: the toggle records
	events <- keyEvent{kind: keyStop, vk: vkF9}
	events <- keyEvent{kind: keyFix}
	close(events)
	dispatch(events, r.d, func() { fixes++ })
	if r.d.state != Recording || fixes != 1 {
		t.Fatalf("state %s, fixes %d", r.d.state, fixes)
	}
}

func TestIconPixels(t *testing.T) {
	for _, size := range []int{16, 20, 24, 32, 48} {
		px := iconPixels(size, stateColors[Recording])
		if len(px) != size*size*4 {
			t.Fatalf("size %d: %d bytes", size, len(px))
		}
		if px[3] != 0 {
			t.Fatalf("size %d: the corner is not clear", size)
		}
		c := (size/2*size + size/2) * 4
		if px[c+3] != 255 {
			t.Fatalf("size %d: the centre is not solid", size)
		}
	}
}

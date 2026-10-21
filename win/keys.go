package main

// The keys are the ones voxtype-bench binds on Linux:
//
//	F9 / F10               push-to-talk, English / German, saved
//	Win+F9 / Win+F10       the same, private: nothing is saved
//	Ctrl+Win+X             toggle English, saved
//	Ctrl+Win+Alt+X         toggle German, saved
//	F5                     fix the last dictation
const (
	vkF5  = 0x74
	vkF9  = 0x78
	vkF10 = 0x79
	vkX   = 0x58
)

type mods struct{ ctrl, alt, shift, win bool }

type keyKind int

const (
	keyStart keyKind = iota + 1
	keyStop
	keyToggle
	keyFix
)

type keyEvent struct {
	kind     keyKind
	vk       uint32 // the push-to-talk key, which ends its take when it goes up
	language string
	private  bool
}

// hotkeys turns key presses into dictation events. Every press of a dictation key is swallowed
// together with its repeats and its release, so the app in front never sees it.
type hotkeys struct {
	held map[uint32]bool
}

// handle returns whether to swallow the key and what it asks for. mask is true when a swallowed
// press came with the Windows key held: without a key in between, releasing it opens the Start menu.
func (h *hotkeys) handle(vk uint32, down bool, m mods) (swallow bool, ev *keyEvent, mask bool) {
	if h.held == nil {
		h.held = map[uint32]bool{}
	}
	if !down {
		if !h.held[vk] {
			return false, nil, false
		}
		delete(h.held, vk)
		if vk == vkF9 || vk == vkF10 {
			return true, &keyEvent{kind: keyStop, vk: vk}, false
		}
		return true, nil, false
	}
	if h.held[vk] {
		return true, nil, false // auto-repeat
	}
	switch {
	case (vk == vkF9 || vk == vkF10) && !m.ctrl && !m.alt && !m.shift:
		ev = &keyEvent{kind: keyStart, vk: vk, language: "en", private: m.win}
		if vk == vkF10 {
			ev.language = "de"
		}
	case vk == vkX && m.ctrl && m.win && !m.shift:
		ev = &keyEvent{kind: keyToggle, language: "en"}
		if m.alt {
			ev.language = "de"
		}
	case vk == vkF5 && m == mods{}:
		ev = &keyEvent{kind: keyFix}
	default:
		return false, nil, false
	}
	h.held[vk] = true
	return true, ev, m.win
}

// dispatch runs the dictation for the key events, in order.
func dispatch(events <-chan keyEvent, d *Dictation, fix func()) {
	takes := map[uint32]int{} // push-to-talk key -> its take
	for ev := range events {
		switch ev.kind {
		case keyStart:
			takes[ev.vk] = d.Start(ev.language, "push-to-talk", ev.private)
		case keyStop:
			d.Stop(takes[ev.vk])
			delete(takes, ev.vk)
		case keyToggle:
			d.Toggle(ev.language)
		case keyFix:
			fix()
		}
	}
}

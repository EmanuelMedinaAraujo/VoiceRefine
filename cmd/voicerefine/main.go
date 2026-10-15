// voicerefine saves each dictation's audio with the text Voxtype made of it, and the text as fixed
// afterwards, so the pairs can later fine-tune a speech model.
package main

import (
	"errors"
	"fmt"
	"os"
)

const usage = `usage: voicerefine <command>

  key [--private] <start|stop|toggle|cancel> [language]
                  what the dictation keys run: records, then passes the key on to voxtype-key
  tap             Voxtype's post_process command: copies each text aside, unchanged
  last [--opened] what the fix window shows: the last dictation, while it can still be fixed
  fix <dir>       saves the text on stdin as the fix, and puts it in the app when that is safe
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	a := newApp()
	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "key":
		err = a.key(args)
	case "tap":
		err = a.tap()
	case "last":
		err = a.lastCmd(args)
	case "fix":
		err = a.fix(args)
	default:
		err = errUsage
	}
	if errors.Is(err, errUsage) {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "voicerefine:", err)
		os.Exit(1)
	}
}

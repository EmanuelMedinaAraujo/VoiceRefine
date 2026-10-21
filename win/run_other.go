//go:build !windows

package main

import (
	"fmt"
	"os"
)

func run() {
	fmt.Fprintln(os.Stderr, "VoiceRefine for Windows only runs on Windows. Build it with win/build.sh.")
	os.Exit(1)
}

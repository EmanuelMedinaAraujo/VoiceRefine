package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The Voxtype setup scripts VoiceRefine depends on: their behaviour decides when Voxtype records.
var voxtypeScripts = []string{"voxtype-key", "voxtype-daemon", "voxtype-mode", "voxtype-meeting", "voxtype-power-watch"}

// Upstream work that would let VoiceRefine use Voxtype's own saving instead of its recorder.
const (
	upstreamRepo  = "peteonrails/voxtype"
	upstreamPR    = 328 // saves each dictation's audio
	upstreamIssue = 209 // the feature request for it
)

// check runs after each Omarchy update and at each login. It only reports, never changes anything.
//
//	voicerefine check [--notify] [--baseline]
func (a *App) check(args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	notify := fs.Bool("notify", false, "send a notification when something needs a look")
	baseline := fs.Bool("baseline", false, "accept the current Voxtype scripts as the ones to compare with")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return errUsage
	}
	if *baseline {
		return a.writeBaseline()
	}

	findings := a.wiringProblems()
	if _, err := a.Command("", "voxtype", "record", "--help"); err != nil {
		findings = append(findings, "Voxtype no longer has the 'voxtype record' command that voxtype-key passes the keys to.")
	}
	if config, _ := os.ReadFile(a.Vox.ConfigFile()); strings.Contains(string(config), `state_file = "disabled"`) {
		findings = append(findings, "Voxtype's state file is disabled; VoiceRefine needs it to know when Voxtype records.")
	}
	findings = append(findings, a.changedScripts()...)
	findings = append(findings, a.upstreamNews()...)

	if len(findings) == 0 {
		fmt.Fprintln(a.Out, "VoiceRefine: nothing to review.")
		return nil
	}
	fmt.Fprintf(a.Out, "VoiceRefine: %d thing(s) to review:\n", len(findings))
	for _, f := range findings {
		fmt.Fprintln(a.Out, "  -", f)
	}
	if *notify {
		a.Command("", "notify-send", "-u", "critical", "VoiceRefine needs a look", strings.Join(findings, "\n"))
	}
	return nil
}

func (a *App) baselinePath() string { return filepath.Join(a.State, "voxtype-scripts.sha256") }

func (a *App) scriptHashes() map[string]string {
	out := map[string]string{}
	for _, name := range voxtypeScripts {
		if data, err := os.ReadFile(filepath.Join(a.Vox.Bin, name)); err == nil {
			sum := sha256.Sum256(data)
			out[name] = hex.EncodeToString(sum[:])
		}
	}
	return out
}

func (a *App) writeBaseline() error {
	if err := os.MkdirAll(a.State, 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(a.scriptHashes(), "", "  ")
	return os.WriteFile(a.baselinePath(), append(data, '\n'), 0o600)
}

func (a *App) changedScripts() []string {
	base := readJSON[map[string]string](a.baselinePath())
	if base == nil {
		return []string{"No baseline of the Voxtype scripts yet. Re-run VoiceRefine's install.sh."}
	}
	now := a.scriptHashes()
	var changed []string
	for _, name := range voxtypeScripts {
		if (*base)[name] != now[name] {
			changed = append(changed, name)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("The Voxtype setup scripts changed (%s). Check that VoiceRefine still records exactly what Voxtype hears, then run 'voicerefine check --baseline'.",
		strings.Join(changed, ", "))}
}

func (a *App) upstreamNews() []string {
	var out []string
	var pr struct {
		Merged bool `json:"merged"`
	}
	if a.github(fmt.Sprintf("repos/%s/pulls/%d", upstreamRepo, upstreamPR), &pr) && pr.Merged {
		out = append(out, fmt.Sprintf("Voxtype merged its own saving of dictation audio (PR %d). VoiceRefine could use it instead of its own recorder.", upstreamPR))
	}
	var issue struct {
		State string `json:"state"`
	}
	if a.github(fmt.Sprintf("repos/%s/issues/%d", upstreamRepo, upstreamIssue), &issue) && issue.State == "closed" {
		out = append(out, fmt.Sprintf("Voxtype closed the request for saving dictation audio (issue %d). Check whether it was built.", upstreamIssue))
	}
	return out
}

// githubAPI is replaced in tests.
var githubAPI = "https://api.github.com/"

func (a *App) github(path string, v any) bool {
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(githubAPI + path)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(v) == nil
}

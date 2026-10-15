import QtQuick
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

// Today's saved dictations. Each save flashes the widget; battery mode, a meeting or dictation
// turned off show a calm pause sign; a broken save or keys that skip VoiceRefine turn it to the
// warning colour. A click opens the fix window, like F5.
BarWidget {
  id: root
  moduleName: "emu.voicerefine"

  readonly property string home: Quickshell.env("HOME")
  readonly property string runDir: Quickshell.env("XDG_RUNTIME_DIR") + "/voicerefine"
  readonly property string program: home + "/.local/bin/voicerefine"

  property int clips: 0
  property real minutes: 0
  property string lastSaved: ""
  property string paused: ""
  property string warning: ""
  property bool flashing: false
  property bool watching: false

  readonly property var pauseReasons: ({
    "battery": "Paused in battery mode",
    "meeting": "Paused during the meeting",
    "off": "Paused: dictation is off"
  })

  function refresh() {
    if (!statusProc.running) statusProc.running = true
  }

  function apply(text) {
    var s
    try { s = JSON.parse(text) } catch (e) { return }
    if (root.lastSaved !== "" && s.last_saved && s.last_saved !== root.lastSaved) {
      root.flashing = true
      flashTimer.restart()
    }
    root.clips = s.clips || 0
    root.minutes = s.minutes || 0
    root.lastSaved = s.last_saved || ""
    root.paused = s.paused || ""
    root.warning = s.warning || ""
  }

  function openFix() {
    Quickshell.execDetached([Quickshell.env("OMARCHY_PATH") + "/bin/omarchy-shell", "shell", "summon", "emu.voicerefine", "{}"])
  }

  function label() {
    // Font Awesome: warning, pause, a check mark while a save flashes, and the microphone.
    var icon = root.warning ? "\uf071" : (root.paused ? "\uf04c" : (root.flashing ? "\uf00c" : "\uf130"))
    if (root.vertical) return icon
    return icon + " " + root.clips + (root.minutes >= 1 ? " · " + Math.round(root.minutes) + "m" : "")
  }

  function tooltip() {
    var lines = ["VoiceRefine: " + root.clips + " dictation" + (root.clips === 1 ? "" : "s")
      + " saved today, " + root.minutes.toFixed(1) + " min"]
    if (root.warning) lines.push(root.warning)
    else if (root.paused) lines.push(root.pauseReasons[root.paused] || "Paused")
    lines.push("Click or F5 to fix the last dictation")
    return lines.join("\n")
  }

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  Process {
    id: statusProc
    command: [root.program, "status"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: root.apply(text)
    }
  }

  // FileView cannot watch a file that does not exist yet, and the runtime folder starts empty
  // after each boot: make the file first, then watch it. Each press, save and fix rewrites it.
  Process {
    id: prepare
    running: true
    command: ["sh", "-c", "mkdir -p \"$1\" && touch \"$1/changed\"", "sh", root.runDir]
    onExited: { root.watching = true; root.refresh() }
  }

  FileView {
    path: root.watching ? root.runDir + "/changed" : ""
    watchChanges: true
    printErrors: false
    onFileChanged: root.refresh()
  }

  // Power source, meetings and the keys change without a dictation.
  Timer {
    interval: 20000
    running: true
    repeat: true
    onTriggered: root.refresh()
  }

  Timer {
    id: flashTimer
    interval: 900
    onTriggered: root.flashing = false
  }

  WidgetButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    text: root.label()
    fontSize: Style.font.caption
    dimmed: root.paused !== "" && !root.warning
    active: root.flashing || root.warning !== ""
    activeColor: root.warning ? (root.bar ? root.bar.urgent : Color.urgent) : Color.accent
    tooltipText: root.tooltip()
    onPressed: function(mouseButton) {
      if (mouseButton === Qt.RightButton) root.refresh()
      else root.openFix()
    }
  }
}

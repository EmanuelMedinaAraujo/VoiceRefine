import Quickshell
import Quickshell.Io
import Quickshell.Wayland
import QtQuick
import qs.Commons
import qs.Ui

// The fix window: one text box with the last dictation. Enter saves the fix, Escape closes,
// Shift+Enter adds a line break. Opened by F5 or a click on the bar widget, through
// `omarchy-shell shell summon emu.voicerefine`.
Item {
  id: root

  property var shell: null
  property var manifest: null

  readonly property string program: Quickshell.env("HOME") + "/.local/bin/voicerefine"

  property bool opened: false
  property string dir: ""
  property string original: ""

  property color background: Color.menu.background
  property color foreground: Color.menu.text
  property color border: Color.menu.border
  property var borderSpec: Border.surfaceSpec("menu", "border", border, Math.max(1, Style.space(2)))
  property color scrim: Color.menu.scrim
  property int contentMargin: Style.spacing.panelPadding
  property int cardWidth: Math.min(Style.space(560), panel.width - Style.gapsOut * 2)
  property int cardHeight: Math.min(contentMargin * 2 + hint.height + Style.space(10)
    + Math.max(edit.contentHeight, Style.font.heading * 1.4), panel.height * 0.6)

  function open(payloadJson) {
    if (!lastProc.running) lastProc.running = true
  }

  function close() {
    root.opened = false
  }

  function dismiss() {
    root.opened = false
    if (root.shell && typeof root.shell.hide === "function")
      root.shell.hide((root.manifest && root.manifest.id) || "emu.voicerefine")
  }

  function show(text) {
    var last
    try { last = JSON.parse(text) } catch (e) { last = { fixable: false, reason: "VoiceRefine did not answer." } }
    if (!last.fixable) {
      root.dismiss()
      Quickshell.execDetached(["notify-send", "-t", "2500", "Nothing to fix", last.reason || ""])
      return
    }
    root.dir = last.dir
    root.original = last.text
    edit.text = last.text
    edit.cursorPosition = edit.length
    root.opened = true
    Qt.callLater(function() { edit.forceActiveFocus() })
  }

  function submit() {
    var text = edit.text
    root.dismiss()
    if (text === root.original) return
    // Closing first hands the focus back to the app, which the fix may type into.
    Quickshell.execDetached(["sh", "-c", "printf '%s' \"$1\" | \"$2\" fix \"$3\"", "sh", text, root.program, root.dir])
  }

  Process {
    id: lastProc
    command: [root.program, "last", "--opened"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: root.show(text)
    }
  }

  PanelWindow {
    id: panel
    visible: root.opened
    anchors { top: true; bottom: true; left: true; right: true }
    color: "transparent"
    WlrLayershell.namespace: "voicerefine-fix"
    WlrLayershell.layer: WlrLayer.Overlay
    WlrLayershell.keyboardFocus: WlrKeyboardFocus.Exclusive
    exclusionMode: ExclusionMode.Ignore

    Rectangle {
      anchors.fill: parent
      color: root.scrim
    }

    MouseArea {
      anchors.fill: parent
      onClicked: root.dismiss()
    }

    BorderSurface {
      id: card
      width: root.cardWidth
      height: root.cardHeight
      radius: Style.cornerRadius
      anchors.centerIn: parent
      color: root.background
      borderSpec: root.borderSpec
      padding: root.contentMargin

      MouseArea { anchors.fill: parent; onClicked: edit.forceActiveFocus() }

      Item {
        anchors.fill: parent
        anchors.topMargin: card.contentTopInset
        anchors.rightMargin: card.contentRightInset
        anchors.bottomMargin: card.contentBottomInset
        anchors.leftMargin: card.contentLeftInset

        Flickable {
          id: flick
          anchors.left: parent.left
          anchors.right: parent.right
          anchors.top: parent.top
          anchors.bottom: hint.top
          anchors.bottomMargin: Style.space(10)
          contentWidth: width
          contentHeight: edit.contentHeight
          clip: true
          boundsBehavior: Flickable.StopAtBounds

          function follow(r) {
            if (contentY >= r.y) contentY = r.y
            else if (contentY + height <= r.y + r.height) contentY = r.y + r.height - height
          }

          TextEdit {
            id: edit
            width: flick.width
            wrapMode: TextEdit.Wrap
            textFormat: TextEdit.PlainText
            color: root.foreground
            selectionColor: Util.alpha(root.foreground, 0.25)
            selectedTextColor: root.foreground
            font.family: Style.font.menuFamily
            font.pixelSize: Style.font.heading
            onCursorRectangleChanged: flick.follow(cursorRectangle)

            Keys.priority: Keys.BeforeItem
            Keys.onPressed: function(event) {
              if (event.key === Qt.Key_Escape) {
                root.dismiss()
                event.accepted = true
              } else if ((event.key === Qt.Key_Return || event.key === Qt.Key_Enter)
                         && !(event.modifiers & Qt.ShiftModifier)) {
                root.submit()
                event.accepted = true
              }
            }
          }
        }

        Text {
          id: hint
          anchors.left: parent.left
          anchors.right: parent.right
          anchors.bottom: parent.bottom
          textFormat: Text.PlainText
          text: "Enter saves the fix · Escape closes"
          color: root.foreground
          opacity: 0.58
          font.family: Style.font.menuFamily
          font.pixelSize: Style.font.caption
          elide: Text.ElideRight
        }
      }
    }
  }
}

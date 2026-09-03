import QtQuick
import qs.Ui

BarWidget {
  id: root
  moduleName: "io.github.kitsunesemcalda.feader-rss"

  function injectPanel() {
    if (!panelLoader.item) return
    panelLoader.item.bar = root.bar
    panelLoader.item.settings = root.settings
    panelLoader.item.anchorItem = button
    panelLoader.item.hostWidget = root
  }

  readonly property bool opened: panelLoader.item ? panelLoader.item.opened : false

  function open() {
    if (panelLoader.item) panelLoader.item.openFromHotkey()
  }

  function close() {
    if (panelLoader.item) panelLoader.item.close()
  }

  function toggle() {
    if (panelLoader.item) panelLoader.item.toggle()
  }

  function closeForPopoutSwitch() {
    if (panelLoader.item) panelLoader.item.closeForPopoutSwitch()
  }

  readonly property bool popoutSwitchClosing: panelLoader.item
    ? panelLoader.item.popoutSwitchClosing : false

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  onBarChanged: injectPanel()
  onSettingsChanged: injectPanel()

  Loader {
    id: panelLoader
    active: true
    source: Qt.resolvedUrl("Panel.qml")
    visible: false
    onLoaded: {
      root.injectPanel()
      Qt.callLater(root.injectPanel)
    }
  }

  BarIconButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    text: panelLoader.item ? panelLoader.item.label : ""
    active: panelLoader.item ? panelLoader.item.unreadCount > 0 : false
    tooltipText: panelLoader.item ? panelLoader.item.unreadSummary
      + "\nClick open · middle refresh · right first unread" : "Feader RSS"
    onPressed: function(buttonCode) {
      if (!panelLoader.item) return
      if (buttonCode === Qt.MiddleButton) panelLoader.item.refresh()
      else if (buttonCode === Qt.RightButton) panelLoader.item.openAllUnread()
      else panelLoader.item.toggle()
    }
  }
}

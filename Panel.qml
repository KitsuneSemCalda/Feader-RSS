import QtQuick
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

Panel {
  id: root
  moduleName: "io.github.kitsunesemcalda.feader-rss"
  ipcTarget: "io.github.kitsunesemcalda.feader-rss"
  manageIpc: false

  property var anchorItem: null
  property var hostWidget: null
  readonly property var barIdentity: hostWidget || root
  property bool openedFromHotkey: false

  property string home: Quickshell.env("HOME") || ""
  readonly property string configPath: home + "/.config/omarchy/rss-reader.json"
  readonly property string stateDir: (Quickshell.env("XDG_STATE_HOME") || home + "/.local/state")
    + "/omarchy/rss-reader"
  readonly property string statePath: stateDir + "/items.json"
  readonly property string fetchScript: Qt.resolvedUrl("rss-fetch.py").toString().replace(/^file:\/\//, "")

  property var config: ({ feeds: [
    { name: "LWN", url: "https://lwn.net/headlines/rss" },
    { name: "Omarchy", url: "https://omarchy.org/feed.xml" }
  ], maxItems: 200 })
  property var articles: []
  property bool loading: false
  property string status: ""
  property int selectedIndex: 0
  readonly property var selectedArticle: articles.length ? articles[selectedIndex] : null
  readonly property string label: articles.length ? "󰓶 " + articles.length : "󰓶"
  readonly property int refreshSeconds: 900

  function loadJson(raw, fallback) {
    try { return JSON.parse(raw) } catch (error) { return fallback }
  }

  function loadConfig(raw) {
    var value = loadJson(raw, null)
    if (value && Array.isArray(value.feeds)) root.config = value
  }

  function loadState(raw) {
    var value = loadJson(raw, null)
    if (value && Array.isArray(value.items)) root.articles = value.items
  }

  function saveState() {
    stateFile.setText(JSON.stringify({ version: 1, updatedAt: new Date().toISOString(), items: articles }, null, 2) + "\n")
  }

  function open() {
    root.refresh()
    root.controller.show()
    Qt.callLater(function() { if (root.opened) setCenterHoverRevealSuppressed(true) })
  }

  function openFromHotkey() { openedFromHotkey = true; open() }
  function close() { setCenterHoverRevealSuppressed(false); root.controller.hide() }
  function closeForPopoutSwitch() { close() }
  function toggle() { if (root.opened) close(); else open() }

  function setCenterHoverRevealSuppressed(value) {
    if (root.bar && "centerHoverRevealSuppressed" in root.bar)
      root.bar.centerHoverRevealSuppressed = value
  }

  function refresh() {
    if (fetchProcess.running || !config.feeds.length) return
    loading = true
    status = "Atualizando…"
    var command = ["python3", fetchScript, "--limit", "200"]
    for (var i = 0; i < config.feeds.length; i++) {
      var feed = config.feeds[i]
      if (feed && feed.url) command.push(String(feed.name || feed.url), String(feed.url))
    }
    fetchProcess.command = command
    fetchProcess.running = true
  }

  function mergeFetched(raw) {
    var result = loadJson(raw, null)
    if (!result || !Array.isArray(result.items)) {
      loading = false; status = "Não foi possível atualizar os feeds"; return
    }
    var byId = {}
    for (var i = 0; i < articles.length; i++) byId[String(articles[i].id)] = articles[i]
    for (var j = 0; j < result.items.length; j++) {
      var incoming = result.items[j]
      var old = byId[String(incoming.id)] || {}
      byId[String(incoming.id)] = Object.assign({}, old, incoming)
    }
    var merged = Object.keys(byId).map(function(key) { return byId[key] })
    merged.sort(function(a, b) { return String(b.published).localeCompare(String(a.published)) })
    root.articles = merged.slice(0, Number(config.maxItems || 200))
    selectedIndex = Math.min(selectedIndex, Math.max(0, articles.length - 1))
    saveState()
    loading = false
    status = new Date().toLocaleTimeString(Qt.locale(), Locale.ShortFormat)
  }

  function markRead(article) {
    if (!article || article.read) return
    var next = articles.slice()
    var index = next.indexOf(article)
    if (index >= 0) { next[index] = Object.assign({}, article, { read: true }); articles = next; saveState() }
  }

  function openArticle(article) {
    if (!article) return
    markRead(article)
    Qt.openUrlExternally(String(article.url))
  }

  function openAllUnread() {
    for (var i = 0; i < articles.length; i++) {
      if (!articles[i].read) { openArticle(articles[i]); return }
    }
  }

  Component.onCompleted: initDir.running = true

  Process {
    id: initDir
    command: ["mkdir", "-p", root.stateDir]
    onExited: stateFile.reload()
  }

  FileView {
    id: configFile
    path: root.configPath
    watchChanges: true
    printErrors: false
    onLoaded: root.loadConfig(text())
    onFileChanged: reload()
  }

  FileView {
    id: stateFile
    path: root.statePath
    watchChanges: true
    atomicWrites: true
    printErrors: false
    onLoaded: root.loadState(text())
    onLoadFailed: root.articles = []
    onFileChanged: reload()
  }

  Process {
    id: fetchProcess
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: root.mergeFetched(text)
    }
    stderr: StdioCollector {
      waitForEnd: true
      onStreamFinished: if (text.trim() !== "") console.warn("io.github.kitsunesemcalda.feader-rss", text.trim())
    }
  }

  Timer {
    interval: root.refreshSeconds * 1000
    running: true
    repeat: true
    triggeredOnStart: true
    onTriggered: root.refresh()
  }

  IpcHandler {
    target: root.ipcTarget
    function open(): void { root.open() }
    function close(): void { root.close() }
    function toggle(): void { root.toggle() }
    function refresh(): string { root.refresh(); return "ok" }
  }

  KeyboardPanel {
    id: panel
    anchorItem: root.anchorItem
    owner: root.barIdentity
    bar: root.bar
    open: root.opened
    contentWidth: panel.fittedContentWidth(Style.space(520))
    contentHeight: panel.fittedContentHeight(column.implicitHeight, Style.space(620))

    Column {
      id: column
      width: parent.width
      spacing: Style.space(10)
      padding: Style.space(16)

      Row {
        width: parent.width
        spacing: Style.space(10)
        Text { text: "FEADER RSS"; color: Color.foreground; font.family: Style.font.family; font.pixelSize: Style.font.pixelSize.large; font.bold: true }
        Item { width: 1; height: 1; Layout.fillWidth: true }
        Text { text: root.loading ? "Atualizando…" : root.status; color: Color.muted; font.family: Style.font.family; font.pixelSize: Style.font.pixelSize.small }
      }

      Text {
        visible: root.articles.length === 0
        text: root.loading ? "Buscando artigos…" : "Nenhum artigo salvo. Edite o arquivo de feeds e atualize."
        color: Color.muted; wrapMode: Text.WordWrap; width: parent.width
      }

      Repeater {
        model: root.articles
        delegate: Rectangle {
          required property var modelData
          width: column.width - Style.space(32)
          height: title.implicitHeight + summary.implicitHeight + Style.space(18)
          color: modelData.read ? Color.background : Color.primary
          radius: Style.radius.small

          Column {
            anchors.fill: parent; anchors.margins: Style.space(9); spacing: Style.space(3)
            Text { id: title; width: parent.width; text: modelData.title; color: Color.foreground; font.family: Style.font.family; font.bold: !modelData.read; elide: Text.ElideRight; maximumLineCount: 2; wrapMode: Text.WordWrap }
            Text { text: String(modelData.feed || "") + " · " + String(modelData.published || ""); color: Color.muted; font.family: Style.font.family; font.pixelSize: Style.font.pixelSize.small }
            Text { id: summary; width: parent.width; text: modelData.summary || ""; color: Color.foreground; opacity: 0.8; elide: Text.ElideRight; maximumLineCount: 2; wrapMode: Text.WordWrap }
          }
          MouseArea { anchors.fill: parent; onClicked: root.openArticle(modelData) }
        }
      }
    }
  }
}

import QtQuick
import Quickshell
import Quickshell.Io
import QtQuick.Controls as Controls
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
  readonly property color foreground: Color.popups.text
  readonly property color dim: Color.muted
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family

  property string home: Quickshell.env("HOME") || ""
  property string omarchyPath: Quickshell.env("OMARCHY_PATH") || ""
  readonly property string configPath: home + "/.config/omarchy/rss-reader.json"
  readonly property string stateDir: (Quickshell.env("XDG_STATE_HOME") || home + "/.local/state")
    + "/omarchy/rss-reader"
  readonly property string statePath: stateDir + "/items.json"
  readonly property string fetchScript: Qt.resolvedUrl("rss-fetch.py").toString().replace(/^file:\/\//, "")

  property var config: ({ feeds: [], maxItems: 200, refreshMinutes: 5 })
  property var articles: []
  property bool loading: false
  property string status: ""
  property var feedErrors: []
  property string lastUpdated: ""
  property int selectedIndex: 0
  property var selectedArticle: null
  property bool detailOpen: false
  property bool settingsOpen: false
  property bool formControlFocused: false
  property bool stateReady: false
  property bool articleLoading: false
  property string articleContent: ""
  property string articleError: ""
  property string searchQuery: ""
  property string readFilter: "all"
  property string selectedFeed: ""
  property bool preferencesReady: false
  property int refreshMinutesDraft: 5
  property string pendingConfirm: ""
  readonly property int unreadCount: articles.filter(function(article) { return !article.read }).length
  readonly property int unreadFeedCount: {
    var feeds = {}
    for (var i = 0; i < articles.length; i++) {
      if (!articles[i].read) feeds[String(articles[i].feed || "")] = true
    }
    return Object.keys(feeds).filter(function(feed) { return feed !== "" }).length
  }
  readonly property string label: unreadCount ? " " + unreadCount : ""
  readonly property string unreadSummary: unreadFeedCount + " feeds unread · " + unreadCount + " articles"
  readonly property int refreshSeconds: Math.max(60, Math.min(300, Number(config.refreshMinutes || 5) * 60))
  readonly property var visibleArticles: articles.filter(function(article) { return articleMatches(article) })

  function loadJson(raw, fallback) {
    try { return JSON.parse(raw) } catch (error) { return fallback }
  }

  function inferFeedName(url) {
    var match = /^https?:\/\/([^\/?#]+)/i.exec(String(url || "").trim())
    if (!match) return ""
    var host = match[1].split("@").pop().split(":")[0].toLowerCase()
    return host.replace(/^www\./, "")
  }

  function loadConfig(raw) {
    var value = loadJson(raw, null)
    if (value && Array.isArray(value.feeds)) {
      root.config = value
      refreshMinutesDraft = Math.max(1, Math.min(5, Number(value.refreshMinutes || 5)))
      feedModel.clear()
      for (var i = 0; i < value.feeds.length; i++) {
        var feed = value.feeds[i]
        if (feed && feed.url) feedModel.append({
          name: String(feed.name || root.inferFeedName(feed.url)), url: String(feed.url)
        })
      }
    }
  }

  function openSettings() {
    resetFeedModel()
    settingsOpen = true
    detailOpen = false
    selectedArticle = null
    formControlFocused = false
    refreshMinutesDraft = Math.max(1, Math.min(5, Number(config.refreshMinutes || 5)))
  }

  function closeSettings() {
    settingsOpen = false
    formControlFocused = false
    resetFeedModel()
  }

  function ensureFeedModel() {
    if (feedModel.count > 0 || !config || !Array.isArray(config.feeds)) return
    resetFeedModel()
  }

  function resetFeedModel() {
    feedModel.clear()
    if (!config || !Array.isArray(config.feeds)) return
    for (var i = 0; i < config.feeds.length; i++) {
      var feed = config.feeds[i]
      if (feed && feed.url) feedModel.append({
        name: String(feed.name || root.inferFeedName(feed.url)), url: String(feed.url)
      })
    }
  }

  function addFeed() {
    if (feedModel.count >= 8) {
      status = "You can configure up to 8 feeds."
      return
    }
    feedModel.append({ name: "", url: "" })
  }

  function removeFeed(index) {
    if (index >= 0 && index < feedModel.count) feedModel.remove(index)
  }

  function saveConfig() {
    var feeds = []
    var seenUrls = {}
    var seenNames = {}
    for (var i = 0; i < feedModel.count; i++) {
      var feed = feedModel.get(i)
      var url = String(feed.url || "").trim()
      if (!/^https?:\/\/[^\s]+$/i.test(url)) {
        status = "Enter a valid http(s) URL for every feed."
        return
      }
      var normalizedUrl = url.toLowerCase().replace(/\/$/, "")
      if (seenUrls[normalizedUrl]) {
        status = "Each feed URL must be unique."
        return
      }
      seenUrls[normalizedUrl] = true
      var name = String(feed.name || "").trim() || root.inferFeedName(url) || url
      if (seenNames[name.toLowerCase()]) {
        status = "Each feed name must be unique."
        return
      }
      seenNames[name.toLowerCase()] = true
      feeds.push({ name: name, url: url })
    }
    root.config = {
      feeds: feeds,
      maxItems: Number(root.config.maxItems || 200),
      refreshMinutes: refreshMinutesDraft
    }
    configFile.setText(JSON.stringify(root.config, null, 2) + "\n")
    settingsOpen = false
    status = "Feeds saved"
    refresh()
  }

  function loadState(raw) {
    var value = loadJson(raw, null)
    if (value && value.preferences) {
      searchQuery = String(value.preferences.searchQuery || "")
      readFilter = ["all", "unread", "read"].indexOf(String(value.preferences.readFilter)) >= 0
        ? String(value.preferences.readFilter) : "all"
      selectedFeed = String(value.preferences.selectedFeed || "")
      selectedIndex = Math.max(0, Number(value.preferences.selectedIndex || 0))
    }
    preferencesReady = true
    if (!config || !Array.isArray(config.feeds) || config.feeds.length === 0) {
      root.articles = []
      return
    }
    if (value && Array.isArray(value.items)) {
      var configuredFeeds = {}
      for (var i = 0; i < config.feeds.length; i++) {
        var feed = config.feeds[i]
        if (feed && feed.name) configuredFeeds[String(feed.name)] = true
      }
      if (selectedFeed !== "" && !configuredFeeds[selectedFeed]) selectedFeed = ""
      root.articles = value.items.filter(function(article) {
        return article && configuredFeeds[String(article.feed || "")] === true
      })
    }
  }

  function saveState() {
    stateFile.setText(JSON.stringify({
      version: 1,
      updatedAt: new Date().toISOString(),
      preferences: { searchQuery: searchQuery, readFilter: readFilter, selectedFeed: selectedFeed, selectedIndex: selectedIndex },
      items: articles
    }, null, 2) + "\n")
  }

  function articleMatches(article) {
    if (!article) return false
    if (readFilter === "unread" && article.read) return false
    if (readFilter === "read" && !article.read) return false
    if (selectedFeed !== "" && String(article.feed || "") !== selectedFeed) return false
    var query = searchQuery.trim().toLowerCase()
    if (query === "") return true
    return (String(article.title || "") + " " + String(article.summary || "") + " "
      + String(article.feed || "")).toLowerCase().indexOf(query) >= 0
  }

  function setReadFilter(value) {
    readFilter = value
    selectedIndex = 0
    if (preferencesReady) saveState()
  }

  function setSelectedFeed(value) {
    selectedFeed = value
    selectedIndex = 0
    if (preferencesReady) saveState()
  }

  function setSearchQuery(value) {
    searchQuery = value
    selectedIndex = 0
    if (preferencesReady) saveState()
  }

  function setRefreshMinutes(value) {
    refreshMinutesDraft = Math.max(1, Math.min(5, Number(value)))
  }

  function open() {
    root.refresh()
    root.controller.show()
    Qt.callLater(function() { if (root.opened) setCenterHoverRevealSuppressed(true) })
  }

  function openFromHotkey() { openedFromHotkey = true; open() }
  function close() {
    detailOpen = false
    if (settingsOpen) { settingsOpen = false; resetFeedModel() }
    setCenterHoverRevealSuppressed(false)
    root.controller.hide()
  }
  function closeForPopoutSwitch() { close() }
  function toggle() { if (root.opened) close(); else open() }

  function setCenterHoverRevealSuppressed(value) {
    if (root.bar && "centerHoverRevealSuppressed" in root.bar)
      root.bar.centerHoverRevealSuppressed = value
  }

  function switchPanel(direction) {
    if (root.bar && typeof root.bar.switchPanelFrom === "function")
      return root.bar.switchPanelFrom(root.barIdentity, direction)
    return false
  }

  function refresh() {
    if (fetchProcess.running || !config.feeds.length) return
    loading = true
    status = "Refreshing…"
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
      loading = false; status = "Could not refresh feeds"; return
    }
    feedErrors = Array.isArray(result.errors) ? result.errors : []
    var byId = {}
    for (var i = 0; i < articles.length; i++) byId[String(articles[i].id)] = articles[i]
    var newItems = []
    for (var j = 0; j < result.items.length; j++) {
      var incoming = result.items[j]
      var old = byId[String(incoming.id)] || {}
      if (!byId[String(incoming.id)]) newItems.push(incoming)
      byId[String(incoming.id)] = Object.assign({}, old, incoming, {
        read: old.read === true ? true : Boolean(incoming.read)
      })
    }
    var merged = Object.keys(byId).map(function(key) { return byId[key] })
    merged.sort(function(a, b) { return String(b.published).localeCompare(String(a.published)) })
    root.articles = merged.slice(0, Number(config.maxItems || 200))
    selectedIndex = Math.min(selectedIndex, Math.max(0, visibleArticles.length - 1))
    saveState()
    root.notifyNewPosts(newItems)
    loading = false
    lastUpdated = new Date().toLocaleTimeString(Qt.locale(), Locale.ShortFormat)
    status = feedErrors.length
      ? feedErrors.length + " feed(s) failed · " + lastUpdated
      : lastUpdated
  }

  function notifyNewPosts(items) {
    if (!stateReady || articles.length === 0 || !items || items.length === 0 || !omarchyPath) return
    var count = items.length
    var headline = count === 1 ? "New RSS post" : count + " new RSS posts"
    var description = String(items[0].title || "New article")
    if (count > 1) description += " and " + (count - 1) + " more"
    Quickshell.execDetached([
      omarchyPath + "/bin/omarchy-notification-send",
      "--app-name", "Feader RSS",
      "-g", "",
      "-u", "low",
      headline,
      description
    ])
  }

  function markRead(article) {
    if (!article || article.read) return article
    var next = articles.slice()
    var articleId = String(article.id || "")
    var articleUrl = String(article.url || "")
    var index = -1
    for (var i = 0; i < next.length; i++) {
      var candidate = next[i]
      if ((articleId !== "" && String(candidate.id || "") === articleId)
          || (articleId === "" && articleUrl !== "" && String(candidate.url || "") === articleUrl)) {
        index = i
        break
      }
    }
    if (index < 0) return article
    var updated = Object.assign({}, next[index], { read: true })
    next[index] = updated
    articles = next
    saveState()
    return updated
  }

  function markAllRead() {
    var next = articles.map(function(article) { return Object.assign({}, article, { read: true }) })
    articles = next
    saveState()
  }

  function markAllUnread() {
    var next = articles.map(function(article) { return Object.assign({}, article, { read: false }) })
    articles = next
    saveState()
  }

  function requestConfirm(action) { pendingConfirm = action }

  function cancelConfirm() { pendingConfirm = "" }

  function confirmPending() {
    if (pendingConfirm === "markAllRead") markAllRead()
    else if (pendingConfirm === "markAllUnread") markAllUnread()
    pendingConfirm = ""
  }

  function openArticle(article) {
    if (!article) return
    var openedArticle = markRead(article)
    var url = String(openedArticle.url || "")
    if (!/^https?:\/\//i.test(url)) {
      status = "Refused to open unsafe article link."
      return
    }
    Qt.openUrlExternally(url)
  }

  function loadArticle(article) {
    if (!article || !article.url || articleProcess.running) return
    articleLoading = true
    articleError = ""
    articleContent = ""
    articleProcess.command = ["python3", fetchScript, "--article", String(article.url)]
    articleProcess.running = true
  }

  function showArticle(article) {
    if (!article) return
    var openedArticle = markRead(article)
    selectedArticle = openedArticle
    detailOpen = true
    loadArticle(openedArticle)
  }

  function mergeArticle(raw) {
    var result = loadJson(raw, null)
    articleLoading = false
    if (!result || result.error || !result.content) {
      articleError = result && result.error ? String(result.error) : "Could not load article"
      articleContent = selectedArticle ? String(selectedArticle.summary || "") : ""
      return
    }
    articleContent = String(result.content)
  }

  function hideArticle() {
    detailOpen = false
    selectedArticle = null
    articleLoading = false
    articleContent = ""
    articleError = ""
  }

  function moveCursor(delta) {
    if (!visibleArticles.length) return
    selectedIndex = Math.max(0, Math.min(visibleArticles.length - 1, selectedIndex + delta))
    if (preferencesReady) saveState()
    ensureSelectedVisible()
  }

  function ensureSelectedVisible() {
    var item = articleRepeater.itemAt(selectedIndex)
    if (!item || !scrollArea) return
    var top = item.y
    var bottom = top + item.height
    if (top < scrollArea.contentY) scrollArea.contentY = top
    else if (bottom > scrollArea.contentY + scrollArea.height)
      scrollArea.contentY = bottom - scrollArea.height
  }

  function openAllUnread() {
    for (var i = 0; i < articles.length; i++) {
      if (!articles[i].read) { openArticle(articles[i]); return }
    }
  }

  Component.onCompleted: { ensureFeedModel(); initDir.running = true }

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
    onLoaded: { root.loadConfig(text()); stateFile.reload(); root.refresh() }
    onFileChanged: reload()
  }

  ListModel { id: feedModel }

  FileView {
    id: stateFile
    path: root.statePath
    watchChanges: true
    atomicWrites: true
    printErrors: false
    onLoaded: { root.loadState(text()); root.stateReady = true }
    onLoadFailed: { root.articles = []; root.stateReady = true }
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

  Process {
    id: articleProcess
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: root.mergeArticle(text)
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
    focusTarget: keyCatcher
    contentWidth: panel.fittedContentWidth(Style.space(520))
    contentHeight: panel.fittedContentHeight(column.y + column.implicitHeight + Style.space(16))

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      z: 0
      blocked: root.formControlFocused || root.pendingConfirm !== ""
      onCloseRequested: {
        if (root.pendingConfirm !== "") root.cancelConfirm()
        else root.close()
      }
      onTabRequested: function(direction) { root.switchPanel(direction) }
      onMoveRequested: function(dx, dy) {
        if (root.settingsOpen) return
        if (root.detailOpen && dx < 0) root.hideArticle()
        else if (!root.detailOpen && dy !== 0) root.moveCursor(dy)
      }
      onActivateRequested: {
        if (root.settingsOpen) return
        if (root.detailOpen) root.openArticle(root.selectedArticle)
        else if (root.visibleArticles.length) root.showArticle(root.visibleArticles[root.selectedIndex])
      }
      onTextKey: function(text) {
        if (text === "r" || text === "R") root.refresh()
        else if (text === "s" || text === "S") root.openSettings()
      }
    }

    Flickable {
      id: scrollArea
      anchors.fill: parent
      z: 1
      contentWidth: width
      contentHeight: column.y + column.implicitHeight + Style.space(16)
      clip: true
      boundsBehavior: Flickable.StopAtBounds
      interactive: contentHeight > height
      Controls.ScrollBar.vertical: Controls.ScrollBar { policy: Controls.ScrollBar.AsNeeded }

    Column {
      id: column
      x: Style.space(16)
      y: Style.space(16)
      width: Math.max(0, scrollArea.width - Style.space(32))
      spacing: Style.space(10)

      Flow {
        width: parent.width
        spacing: Style.space(10)
        Text {
          width: Math.max(0, parent.width - Math.min(statusLabel.implicitWidth, parent.width) - parent.spacing)
          text: root.settingsOpen ? "FEEDS" : (root.detailOpen ? "ARTICLE" : "FEADER RSS")
          color: root.foreground; font.family: root.fontFamily
          font.pixelSize: Style.font.heading; font.bold: true
        }
        Text {
          id: statusLabel
          width: Math.min(implicitWidth, parent.width)
          text: root.loading ? "Refreshing…" : root.status
          color: root.dim; font.family: root.fontFamily
          font.pixelSize: Style.font.bodySmall; wrapMode: Text.WordWrap
        }
      }

      Flow {
        visible: !root.detailOpen && !root.settingsOpen
        spacing: Style.space(8)
        width: parent.width
        Button {
          id: refreshButton
          text: "Refresh"
          foreground: root.foreground
          focusable: true
          onActiveFocusChanged: root.formControlFocused = activeFocus
          onClicked: root.refresh()
        }
        Button {
          id: configureButton
          text: "Configure feeds"
          foreground: root.foreground
          focusable: true
          onActiveFocusChanged: root.formControlFocused = activeFocus
          onClicked: root.openSettings()
        }
        Button {
          id: closeButton
          text: "Close"
          foreground: root.foreground
          focusable: true
          onActiveFocusChanged: root.formControlFocused = activeFocus
          onClicked: root.close()
        }
      }

      Flow {
        visible: !root.detailOpen && !root.settingsOpen
        spacing: Style.space(8)
        width: parent.width
        Button {
          text: "Mark all read"
          foreground: root.foreground
          focusable: true
          onActiveFocusChanged: root.formControlFocused = activeFocus
          onClicked: root.requestConfirm("markAllRead")
        }
        Button {
          text: "Mark all unread"
          foreground: root.foreground
          focusable: true
          onActiveFocusChanged: root.formControlFocused = activeFocus
          onClicked: root.requestConfirm("markAllUnread")
        }
      }

      Text {
        visible: !root.detailOpen && !root.settingsOpen
        text: "Shortcuts: R refresh · S settings · ↑↓ navigate · Enter open"
        color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.caption
        wrapMode: Text.WordWrap; width: parent.width
      }

      Column {
        visible: !root.detailOpen && !root.settingsOpen && root.feedErrors.length > 0
        width: parent.width
        spacing: Style.space(2)
        Repeater {
          model: root.feedErrors
          delegate: Text {
            required property var modelData
            width: parent.width
            text: "⚠ " + String(modelData.name || modelData.feed || modelData.url || "Feed")
              + ": " + String(modelData.error || modelData.message || "failed to refresh")
            color: Color.urgent; font.family: root.fontFamily; font.pixelSize: Style.font.bodySmall
            wrapMode: Text.WordWrap
            textFormat: Text.PlainText
          }
        }
      }

      Text {
        visible: !root.detailOpen && !root.settingsOpen && root.articles.length > 0
        text: root.unreadCount + " unread · " + root.unreadFeedCount + " feeds"
        color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.bodySmall
      }

      Text {
        visible: !root.detailOpen && !root.settingsOpen && root.articles.length > 0
        text: root.unreadCount === 0 ? "All articles read" : "Unread articles"
        color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.caption
      }

      TextField {
        id: searchField
        visible: !root.detailOpen && !root.settingsOpen && root.articles.length > 0
        width: parent.width
        foreground: root.foreground
        placeholderText: "Search articles"
        onTextChanged: root.setSearchQuery(text)
        onActiveFocusChanged: root.formControlFocused = activeFocus
        Component.onCompleted: text = root.searchQuery
      }

      Flow {
        visible: !root.detailOpen && !root.settingsOpen && root.articles.length > 0
        spacing: Style.space(6)
        width: parent.width
        Button {
          text: "All"
          selected: root.readFilter === "all"
          foreground: root.foreground
          focusable: true
          onActiveFocusChanged: root.formControlFocused = activeFocus
          onClicked: root.setReadFilter("all")
        }
        Button {
          text: "Unread"
          selected: root.readFilter === "unread"
          foreground: root.foreground
          focusable: true
          onActiveFocusChanged: root.formControlFocused = activeFocus
          onClicked: root.setReadFilter("unread")
        }
        Button {
          text: "Read"
          selected: root.readFilter === "read"
          foreground: root.foreground
          focusable: true
          onActiveFocusChanged: root.formControlFocused = activeFocus
          onClicked: root.setReadFilter("read")
        }
      }

      Dropdown {
        id: feedFilterDropdown
        visible: !root.detailOpen && !root.settingsOpen && root.articles.length > 0
        width: parent.width
        showLabel: false
        foreground: root.foreground
        value: root.selectedFeed
        options: [{ value: "", label: "All feeds" }].concat(
          root.config.feeds.map(function(feed) {
            var v = String(feed.name || feed.url)
            return { value: v, label: v }
          }))
        onHovered: function(isHovered) {}
        onChanged: function(value) { root.setSelectedFeed(value) }
        onActiveFocusChanged: root.formControlFocused = activeFocus
      }

      Text {
        visible: !root.detailOpen && !root.settingsOpen && root.visibleArticles.length === 0
        text: root.loading ? "Fetching articles…" : (root.articles.length ? "No articles match the current filters." : "No saved articles. Configure a feed and refresh.")
        color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.body
        wrapMode: Text.WordWrap; width: parent.width
      }

      Flow {
        visible: root.detailOpen && !root.settingsOpen
        spacing: Style.space(8)
        width: parent.width
        Button {
          text: "Back"
          foreground: root.foreground
          focusable: true
          onActiveFocusChanged: root.formControlFocused = activeFocus
          onClicked: root.hideArticle()
        }
        Button {
          text: "Open in browser"
          foreground: root.foreground
          focusable: true
          onActiveFocusChanged: root.formControlFocused = activeFocus
          onClicked: root.openArticle(root.selectedArticle)
        }
        Button {
          text: "Close"
          foreground: root.foreground
          focusable: true
          onActiveFocusChanged: root.formControlFocused = activeFocus
          onClicked: root.close()
        }
      }

      Column {
        visible: root.detailOpen && !root.settingsOpen && root.selectedArticle !== null
        width: parent.width
        spacing: Style.space(8)
        Text {
          id: articleTitle
          width: parent.width
          text: root.selectedArticle ? root.selectedArticle.title : ""
          color: Color.accent; font.family: root.fontFamily
          font.pixelSize: Style.font.heading; font.bold: true
          font.underline: titleHover.hovered
          wrapMode: Text.WordWrap
          textFormat: Text.PlainText
          MouseArea {
            id: titleHover
            anchors.fill: parent
            hoverEnabled: true
            cursorShape: Qt.PointingHandCursor
            onClicked: root.openArticle(root.selectedArticle)
          }
        }
        Text {
          width: parent.width
          text: root.selectedArticle ? String(root.selectedArticle.feed || "") + " · " + String(root.selectedArticle.published || "") : ""
          color: root.dim; font.family: root.fontFamily
          font.pixelSize: Style.font.bodySmall; wrapMode: Text.WordWrap
          textFormat: Text.PlainText
        }
        Text {
          width: parent.width
          text: root.selectedArticle ? root.selectedArticle.summary : ""
          color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.body
          wrapMode: Text.WordWrap
          textFormat: Text.PlainText
        }
        Text {
          width: parent.width
          visible: root.articleLoading
          text: "Loading article…"
          color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.body
          wrapMode: Text.WordWrap
        }
        Text {
          width: parent.width
          visible: !root.articleLoading && root.articleError !== ""
          text: "⚠ " + root.articleError
          color: Color.urgent; font.family: root.fontFamily; font.pixelSize: Style.font.body
          wrapMode: Text.WordWrap
          textFormat: Text.PlainText
        }
        Text {
          width: parent.width
          visible: !root.articleLoading && root.articleError === "" && root.articleContent !== ""
          text: root.articleContent
          color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.body
          wrapMode: Text.WordWrap
          textFormat: Text.PlainText
        }
      }

      Column {
        visible: !root.detailOpen && !root.settingsOpen
        width: parent.width
        spacing: Style.space(6)
        Repeater {
          id: articleRepeater
          model: root.visibleArticles
          delegate: BorderSurface {
            required property var modelData
            required property int index
            width: column.width
            height: title.implicitHeight + meta.implicitHeight + summary.implicitHeight + Style.space(18)
            color: index === root.selectedIndex
              ? Style.selectedFillFor(root.foreground, Color.accent)
              : Style.normalFillFor(root.foreground, Color.accent)
            radius: Style.cornerRadius
            borderSpec: index === root.selectedIndex
              ? Border.controlSpec("selected", root.foreground, Color.accent)
              : Border.none()

            Column {
              anchors.fill: parent; anchors.margins: Style.space(9); spacing: Style.space(3)
              Text { id: title; width: parent.width; text: modelData.title; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.body; font.bold: !modelData.read; maximumLineCount: 3; elide: Text.ElideRight; wrapMode: Text.WordWrap; textFormat: Text.PlainText }
              Text { id: meta; width: parent.width; text: String(modelData.feed || "") + " · " + String(modelData.published || ""); color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.bodySmall; maximumLineCount: 2; elide: Text.ElideRight; wrapMode: Text.WordWrap; textFormat: Text.PlainText }
              Text { id: summary; width: parent.width; text: modelData.summary || ""; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.bodySmall; opacity: 0.8; maximumLineCount: 3; elide: Text.ElideRight; wrapMode: Text.WordWrap; textFormat: Text.PlainText }
            }
            MouseArea { anchors.fill: parent; onClicked: { root.selectedIndex = index; root.showArticle(modelData) } }
          }
        }
      }

      Column {
        id: settingsColumn
        visible: root.settingsOpen
        width: parent.width
        spacing: Style.space(10)

        Text {
          width: parent.width
          text: "Manage your RSS sources and refresh interval. Changes are stored locally."
          color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.body
          wrapMode: Text.WordWrap
        }

        PanelSeparator { foreground: root.foreground }

        PanelSectionHeader {
          text: "UPDATE INTERVAL"
          foreground: root.foreground
          fontFamily: root.fontFamily
        }

        Text {
          width: parent.width
          text: "Feeds refresh automatically between 1 and 5 minutes."
          color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.bodySmall
          wrapMode: Text.WordWrap
        }

        Flow {
          width: parent.width
          spacing: Style.space(6)
          Text {
            width: Math.min(implicitWidth, parent.width)
            text: "Refresh every"
            color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.body
          }
          Button {
            text: "1 min"
            selected: root.refreshMinutesDraft === 1
            foreground: root.foreground
            focusable: true
            onActiveFocusChanged: root.formControlFocused = activeFocus
            onClicked: root.setRefreshMinutes(1)
          }
          Button {
            text: "5 min"
            selected: root.refreshMinutesDraft === 5
            foreground: root.foreground
            focusable: true
            onActiveFocusChanged: root.formControlFocused = activeFocus
            onClicked: root.setRefreshMinutes(5)
          }
        }

        PanelSeparator { foreground: root.foreground }

        Flow {
          width: parent.width
          spacing: Style.space(8)
          PanelSectionHeader {
            text: "RSS FEEDS"
            foreground: root.foreground
            fontFamily: root.fontFamily
          }
          Text {
            text: feedModel.count + "/8"
            color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.caption
          }
        }

        Text {
          width: parent.width
          text: feedModel.count === 0
            ? "No feeds configured yet. Add one to start reading."
            : "Give each feed a name, or leave it blank to use the site name."
          color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.bodySmall
          wrapMode: Text.WordWrap
        }

        Flow {
          width: parent.width
          spacing: Style.space(8)
          Button {
            text: "Add feed"
            foreground: root.foreground
            focusable: true
            onActiveFocusChanged: root.formControlFocused = activeFocus
            onClicked: root.addFeed()
          }
        }

        Column {
          id: feedList
          width: parent.width
          spacing: Style.space(8)

          Repeater {
            model: feedModel
            delegate: BorderSurface {
              required property int index
              required property string name
              required property string url
              width: feedList.width
              height: feedCard.implicitHeight + Style.space(18)
              color: Style.normalFillFor(root.foreground, Color.accent)
              radius: Style.cornerRadius
              borderSpec: Border.none()

              Column {
                id: feedCard
                anchors.left: parent.left
                anchors.right: parent.right
                anchors.top: parent.top
                anchors.margins: Style.space(9)
                spacing: Style.space(7)

                Flow {
                  width: parent.width
                  spacing: Style.space(8)
                  Text {
                    width: Math.max(0, parent.width - removeButton.implicitWidth - parent.spacing)
                    text: "FEED " + (index + 1)
                    color: root.dim; font.family: root.fontFamily
                    font.pixelSize: Style.font.caption; font.bold: true
                  }
                  Button {
                    id: removeButton
                    text: "Remove"
                    foreground: root.foreground
                    focusable: true
                    onActiveFocusChanged: root.formControlFocused = activeFocus
                    onClicked: root.removeFeed(index)
                  }
                }

                Flow {
                  id: feedFields
                  readonly property real minFieldWidth: Style.space(180)
                  readonly property bool twoColumn: feedFields.width >= (minFieldWidth * 2 + feedFields.spacing)
                  width: parent.width
                  spacing: Style.space(6)
                  TextField {
                    id: feedName
                    width: feedFields.twoColumn
                      ? Math.max(0, (feedFields.width - feedFields.spacing) / 2)
                      : feedFields.width
                    text: name
                    foreground: root.foreground
                    placeholderText: "Feed name (optional)"
                    onTextChanged: if (activeFocus) feedModel.setProperty(index, "name", text)
                    onEditingFinished: if (text.trim() === "") feedModel.setProperty(index, "name", root.inferFeedName(feedUrl.text))
                    onActiveFocusChanged: root.formControlFocused = activeFocus
                    Component.onCompleted: {
                      if (root.settingsOpen && index === 0) Qt.callLater(forceActiveFocus)
                    }
                  }
                  TextField {
                    id: feedUrl
                    width: feedFields.twoColumn
                      ? Math.max(0, (feedFields.width - feedFields.spacing) / 2)
                      : feedFields.width
                    text: url
                    foreground: root.foreground
                    placeholderText: "https://example.org/feed.xml"
                    inputMethodHints: Qt.ImhUrlCharactersOnly
                    onTextChanged: if (activeFocus) feedModel.setProperty(index, "url", text)
                    onActiveFocusChanged: root.formControlFocused = activeFocus
                  }
                }
              }
            }
          }
        }

        PanelSeparator { foreground: root.foreground }

        PanelSectionHeader {
          text: "ACTIONS"
          foreground: root.foreground
          fontFamily: root.fontFamily
        }

        Flow {
          id: actionsFlow
          readonly property real minButtonWidth: Style.space(140)
          readonly property bool twoColumn: actionsFlow.width >= (minButtonWidth * 2 + spacing)
          width: parent.width
          spacing: Style.space(8)
          Button {
            text: "Save feeds"
            width: actionsFlow.twoColumn ? (actionsFlow.width - actionsFlow.spacing) / 2 : actionsFlow.width
            height: Style.space(44)
            foreground: root.foreground
            focusable: true
            onActiveFocusChanged: root.formControlFocused = activeFocus
            onClicked: root.saveConfig()
          }
          Button {
            text: "Cancel"
            width: actionsFlow.twoColumn ? (actionsFlow.width - actionsFlow.spacing) / 2 : actionsFlow.width
            height: Style.space(44)
            foreground: root.foreground
            focusable: true
            onActiveFocusChanged: root.formControlFocused = activeFocus
            onClicked: root.closeSettings()
          }
        }

      }
    }
    }

    ConfirmDialog {
      id: confirmDialog
      anchors.fill: parent
      z: 2
      opened: root.pendingConfirm !== ""
      message: root.pendingConfirm === "markAllRead" ? "Mark all articles as read?"
        : (root.pendingConfirm === "markAllUnread" ? "Mark all articles as unread?" : "")
      confirmText: "Confirm"
      cancelText: "Cancel"
      foreground: root.foreground
      background: Color.popups.background
      fontFamily: root.fontFamily
      focus: opened
      Keys.onPressed: function(event) { if (handleKey(event)) event.accepted = true }
      onCanceled: root.cancelConfirm()
      onConfirmed: root.confirmPending()
    }
  }
}

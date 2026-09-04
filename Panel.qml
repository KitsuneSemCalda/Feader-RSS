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
  readonly property string configHome: Quickshell.env("XDG_CONFIG_HOME") || home + "/.config"
  readonly property string configPath: configHome + "/omarchy/rss-reader.json"
  readonly property string stateDir: (Quickshell.env("XDG_STATE_HOME") || home + "/.local/state")
    + "/omarchy/rss-reader"
  readonly property string statePath: stateDir + "/preferences.json"
  readonly property string dbPath: stateDir + "/items.db"
  readonly property string fetchBinary: Qt.resolvedUrl("feader-rss-fetch").toString().replace(/^file:\/\//, "")

  property var config: ({
    feeds: [], maxItems: 200, retentionItems: 1000, refreshMinutes: 5,
    scrollStep: 54, panelGap: Style.gapsOut
  })
  property var articles: []
  property bool loading: false
  property string status: ""
  property var feedErrors: []
  property string lastUpdated: ""
  property string prefetchNote: ""
  property int unreadNotificationId: 0
  property int lastNotifiedUnreadCount: -1
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
  property string selectedFolder: ""
  property bool preferencesReady: false
  property int refreshMinutesDraft: 5
  property int retentionItemsDraft: 1000
  property real scrollStepDraft: 54
  property string pendingConfirm: ""
  property int globalUnreadCount: -1
  property int globalUnreadFeedCount: -1
  property bool searchResultsActive: false
  property string searchProcessQuery: ""
  property string tagDraft: ""
  property int searchDebounceMs: 180
  readonly property var refreshOptions: [
    { value: 1, label: "1 min" },
    { value: 2, label: "2 min" },
    { value: 3, label: "3 min" },
    { value: 4, label: "4 min" },
    { value: 5, label: "5 min" },
    { value: 15, label: "15 min" },
    { value: 30, label: "30 min" },
    { value: 60, label: "1 hour" },
    { value: 300, label: "5 hours" }
  ]
  readonly property int localUnreadCount: articles.filter(function(article) { return !article.read }).length
  readonly property int localUnreadFeedCount: {
    var feeds = {}
    for (var i = 0; i < articles.length; i++) {
      if (!articles[i].read) feeds[String(articles[i].feed || "")] = true
    }
    return Object.keys(feeds).filter(function(feed) { return feed !== "" }).length
  }
  readonly property int unreadCount: globalUnreadCount >= 0 ? globalUnreadCount : localUnreadCount
  readonly property int unreadFeedCount: globalUnreadFeedCount >= 0 ? globalUnreadFeedCount : localUnreadFeedCount
  // A single glyph, always — BarIconButton renders `text` as one un-clipped
  // optical glyph sized for its fixed icon slot. Appending the live unread
  // count here (e.g. " 15") used to bleed digits past that slot and
  // over whichever bar widget sits next to this one. The count still shows
  // in the tooltip (unreadSummary) and inside the panel itself.
  readonly property string label: ""
  readonly property string unreadSummary: unreadFeedCount + " feeds unread · " + unreadCount + " articles"
  readonly property int refreshSeconds: {
    return root.normalizeRefreshMinutes(config && config.refreshMinutes) * 60
  }
  readonly property int resolvedRetentionItems: {
    var value = Math.floor(Number(config && config.retentionItems))
    return isFinite(value) && value >= 0 ? Math.min(100000, value) : 1000
  }
  readonly property int resolvedMaxItems: {
    var value = Math.floor(Number(config && config.maxItems))
    return isFinite(value) && value > 0 ? Math.min(100000, value) : 200
  }
  readonly property real resolvedScrollStep: {
    var value = Number(config.scrollStep)
    return (isFinite(value) && value > 0) ? value : 54
  }
  readonly property real resolvedPanelGap: {
    var value = Number(config.panelGap)
    return (isFinite(value) && value >= 0) ? value : Style.gapsOut
  }
  // Single-character keys, always lower-cased before matching. Override any
  // subset via config.shortcuts (e.g. {"markAllRead": "x"}) — unknown action
  // names are ignored so a typo in the config never breaks the rest.
  readonly property var defaultShortcuts: ({
    refresh: "r",
    settings: "s",
    search: "/",
    markAllRead: "a",
    openArticle: "o",
    filterAll: "1",
    filterUnread: "2",
    filterRead: "3",
    filterStarred: "4"
  })
  readonly property var resolvedShortcuts: {
    var merged = {}
    for (var action in root.defaultShortcuts) merged[action] = root.defaultShortcuts[action]
    var overrides = config && config.shortcuts
    if (overrides && typeof overrides === "object") {
      for (var name in overrides) {
        if (!(name in merged)) continue
        var key = String(overrides[name] || "").trim().toLowerCase()
        if (key !== "") merged[name] = key
      }
    }
    return merged
  }
  readonly property var visibleArticles: articles.filter(function(article) { return articleMatches(article) })

  function shortcutsHint() {
    var s = root.resolvedShortcuts
    return "Shortcuts: " + s.refresh.toUpperCase() + " refresh · " + s.settings.toUpperCase() + " settings · "
      + s.search.toUpperCase() + " search · " + s.markAllRead.toUpperCase() + " mark all read · "
      + s.openArticle.toUpperCase() + " open · " + s.filterAll + "/" + s.filterUnread + "/" + s.filterRead
      + "/" + s.filterStarred + " filter · ↑↓ navigate · Enter open"
  }

  function loadJson(raw, fallback) {
    try { return JSON.parse(raw) } catch (error) { return fallback }
  }

  function normalizeRefreshMinutes(value) {
    var numeric = Number(value)
    if (!isFinite(numeric)) return 5
    for (var i = 0; i < root.refreshOptions.length; i++) {
      if (root.refreshOptions[i].value === numeric) return numeric
    }
    return 5
  }

  function refreshLabel(value) {
    var normalized = root.normalizeRefreshMinutes(value)
    for (var i = 0; i < root.refreshOptions.length; i++) {
      if (root.refreshOptions[i].value === normalized) return root.refreshOptions[i].label
    }
    return "5 min"
  }

  function inferFeedName(url) {
    var match = /^https?:\/\/([^\/?#]+)/i.exec(String(url || "").trim())
    if (!match) return ""
    var host = match[1].split("@").pop().split(":")[0].toLowerCase()
    return host.replace(/^www\./, "")
  }

  function feedDisplayName(feed) {
    if (!feed) return ""
    var explicit = String(feed.name || "").trim()
    var url = String(feed.url || "").trim()
    return explicit || root.inferFeedName(url) || url
  }

  function feedFolderForName(feedName) {
    if (!config || !Array.isArray(config.feeds)) return ""
    var target = String(feedName || "")
    for (var i = 0; i < config.feeds.length; i++) {
      var feed = config.feeds[i]
      if (feed && root.feedDisplayName(feed) === target) return String(feed.folder || "").trim()
    }
    return ""
  }

  function configuredFeedNamesArray() {
    var names = []
    if (!config || !Array.isArray(config.feeds)) return names
    for (var i = 0; i < config.feeds.length; i++) {
      var name = root.feedDisplayName(config.feeds[i])
      if (name !== "" && names.indexOf(name) < 0) names.push(name)
    }
    return names
  }

  function configuredFolders() {
    var folders = []
    if (!config || !Array.isArray(config.feeds)) return folders
    for (var i = 0; i < config.feeds.length; i++) {
      var folder = String(config.feeds[i].folder || "").trim()
      if (folder !== "" && folders.indexOf(folder) < 0) folders.push(folder)
    }
    return folders
  }

  function formatPublished(value) {
    var raw = String(value || "").trim()
    if (raw === "") return "Date unknown"
    var date = new Date(raw)
    if (isNaN(date.getTime())) return raw
    return date.toLocaleDateString(Qt.locale(), Locale.ShortFormat)
  }

  function articleMeta(article) {
    if (!article) return ""
    var parts = []
    var feedName = String(article.feed || "").trim()
    var author = String(article.author || "").trim()
    if (feedName !== "") parts.push(feedName)
    if (author !== "") parts.push("by " + author)
    if (String(article.published || "").trim() !== "") parts.push(root.formatPublished(article.published))
    var categories = Array.isArray(article.categories) ? article.categories.slice(0, 3) : []
    categories = categories.map(function(category) { return String(category || "").trim() })
      .filter(function(category) { return category !== "" })
    if (categories.length > 0) parts.push(categories.join(", "))
    var tags = Array.isArray(article.tags) ? article.tags.slice(0, 3) : []
    tags = tags.map(function(tag) { return String(tag || "").trim() })
      .filter(function(tag) { return tag !== "" })
    if (article.starred) parts.push("saved")
    if (tags.length > 0) parts.push("#" + tags.join(" #"))
    if (article.cached) parts.push("ready")
    return parts.join(" · ")
  }

  function articleSummary(article) {
    var summary = article ? String(article.summary || "").trim() : ""
    return summary !== "" ? summary : "This feed did not provide a summary. Open the article to read it."
  }

  // Bounds and validates feeds coming from disk — a hand-edited config file
  // or an OPML import can carry more than 8 entries, non-http(s) URLs, or
  // duplicates that the settings form would otherwise have rejected.
  function sanitizeFeeds(rawFeeds) {
    var feeds = []
    if (!Array.isArray(rawFeeds)) return feeds
    var seenUrls = {}
    var seenNames = {}
    for (var i = 0; i < rawFeeds.length && feeds.length < 8; i++) {
      var feed = rawFeeds[i]
      if (!feed) continue
      var url = String(feed.url || "").trim()
      if (!/^https?:\/\/[^\s]+$/i.test(url)) continue
      var normalizedUrl = url.toLowerCase().replace(/\/$/, "")
      if (seenUrls[normalizedUrl]) continue
      var name = String(feed.name || "").trim() || root.inferFeedName(url) || url
      if (seenNames[name.toLowerCase()]) continue
      seenUrls[normalizedUrl] = true
      seenNames[name.toLowerCase()] = true
      feeds.push({ name: name, url: url, folder: String(feed.folder || "").trim() })
    }
    return feeds
  }

  function loadConfig(raw) {
    var value = loadJson(raw, null)
    if (value && Array.isArray(value.feeds)) {
      var feeds = root.sanitizeFeeds(value.feeds)
      root.config = Object.assign({}, value, { feeds: feeds })
      refreshMinutesDraft = root.normalizeRefreshMinutes(value.refreshMinutes || 5)
      retentionItemsDraft = root.resolvedRetentionItems
      feedModel.clear()
      for (var i = 0; i < feeds.length; i++) {
        feedModel.append(feeds[i])
      }
    }
  }

  function openSettings() {
    resetFeedModel()
    settingsOpen = true
    detailOpen = false
    selectedArticle = null
    formControlFocused = false
    refreshMinutesDraft = root.normalizeRefreshMinutes(config.refreshMinutes || 5)
    retentionItemsDraft = root.resolvedRetentionItems
    scrollStepDraft = root.resolvedScrollStep
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
        name: root.feedDisplayName(feed), url: String(feed.url), folder: String(feed.folder || "")
      })
    }
  }

  function addFeed() {
    if (feedModel.count >= 8) {
      status = "You can configure up to 8 feeds."
      return
    }
    feedModel.append({ name: "", url: "", folder: "" })
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
      feeds.push({ name: name, url: url, folder: String(feed.folder || "").trim() })
    }
    root.config = Object.assign({}, root.config, {
      feeds: feeds,
      maxItems: root.resolvedMaxItems,
      retentionItems: retentionItemsDraft,
      refreshMinutes: root.normalizeRefreshMinutes(refreshMinutesDraft),
      scrollStep: scrollStepDraft
    })
    configFile.setText(JSON.stringify(root.config, null, 2) + "\n")
    settingsOpen = false
    // The write is asynchronous; configFile's onSaved/onSaveFailed below
    // correct this once Quickshell reports what actually happened, so a
    // failed write (e.g. a broken symlink or a missing directory) is never
    // reported to the user as a successful save.
    status = "Saving feeds…"
    refresh()
  }

  function loadState(raw) {
    var value = loadJson(raw, null)
    if (value && value.preferences) {
      searchQuery = String(value.preferences.searchQuery || "")
      readFilter = ["all", "unread", "read", "starred"].indexOf(String(value.preferences.readFilter)) >= 0
        ? String(value.preferences.readFilter) : "all"
      selectedFeed = String(value.preferences.selectedFeed || "")
      selectedFolder = String(value.preferences.selectedFolder || "")
      selectedIndex = Math.max(0, Number(value.preferences.selectedIndex || 0))
    }
    preferencesReady = true
    if (!config || !Array.isArray(config.feeds) || config.feeds.length === 0) {
      root.articles = []
      return
    }
    if (searchQuery.trim() !== "") root.requestSearch()
    else root.loadInitialArticles()
  }

  function saveState() {
    stateFile.setText(JSON.stringify({
      version: 2,
      updatedAt: new Date().toISOString(),
      preferences: { searchQuery: searchQuery, readFilter: readFilter, selectedFeed: selectedFeed, selectedFolder: selectedFolder, selectedIndex: selectedIndex }
    }, null, 2) + "\n")
  }

  function articleMatches(article) {
    if (!article) return false
    if (readFilter === "unread" && article.read) return false
    if (readFilter === "read" && !article.read) return false
    if (readFilter === "starred" && !article.starred) return false
    if (selectedFeed !== "" && String(article.feed || "") !== selectedFeed) return false
    if (selectedFolder !== "" && root.feedFolderForName(article.feed) !== selectedFolder) return false
    var query = searchQuery.trim().toLowerCase()
    if (query === "") return true
    if (searchResultsActive) return true
    return (String(article.title || "") + " " + String(article.summary || "") + " "
      + String(article.feed || "") + " " + String(article.author || "") + " "
      + (Array.isArray(article.categories) ? article.categories.join(" ") : ""))
      .toLowerCase().indexOf(query) >= 0
  }

  function setReadFilter(value) {
    readFilter = value
    selectedIndex = 0
    if (preferencesReady) saveState()
  }

  function setSelectedFeed(value) {
    selectedFeed = value
    selectedFolder = ""
    selectedIndex = 0
    if (preferencesReady) saveState()
  }

  function setSelectedFolder(value) {
    selectedFolder = value
    selectedFeed = ""
    selectedIndex = 0
    if (preferencesReady) saveState()
  }

  function setSearchQuery(value) {
    searchQuery = String(value || "")
    selectedIndex = 0
    searchResultsActive = false
    if (preferencesReady) {
      saveState()
      searchDebounce.restart()
    }
  }

  function clearSearch() {
    searchField.text = ""
    searchField.forceActiveFocus()
  }

  function setRefreshMinutes(value) {
    refreshMinutesDraft = root.normalizeRefreshMinutes(value)
  }

  function setRetentionItems(value) {
    var numeric = Math.floor(Number(value))
    retentionItemsDraft = isFinite(numeric) && numeric >= 0 ? Math.min(100000, numeric) : 1000
  }

  function setScrollStep(value) {
    scrollStepDraft = Math.max(10, Math.min(200, Number(value)))
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
    if (fetchProcess.running || !config || !Array.isArray(config.feeds) || !config.feeds.length) return
    loading = true
    status = "Refreshing…"
    var command = [fetchBinary, "fetch", "--db", dbPath,
      "--limit", String(root.resolvedMaxItems),
      "--retention", String(root.resolvedRetentionItems)]
    for (var i = 0; i < config.feeds.length; i++) {
      var feed = config.feeds[i]
      if (feed && feed.url) command.push(root.feedDisplayName(feed), String(feed.url))
    }
    fetchProcess.command = command
    fetchProcess.running = true
  }

  function loadInitialArticles() {
    if (listProcess.running) return
    var command = [fetchBinary, "list", "--db", dbPath, "--limit", String(root.resolvedMaxItems)]
    var names = root.configuredFeedNamesArray()
    for (var i = 0; i < names.length; i++) command.push("--feed", names[i])
    listProcess.command = command
    listProcess.running = true
  }

  function applySnapshotStats(result) {
    if (!result || !isFinite(Number(result.unreadCount)) || !isFinite(Number(result.unreadFeedCount))) {
      globalUnreadCount = -1
      globalUnreadFeedCount = -1
      return
    }
    globalUnreadCount = Math.max(0, Number(result.unreadCount))
    globalUnreadFeedCount = Math.max(0, Number(result.unreadFeedCount))
  }

  function requestSearch() {
    searchDebounce.stop()
    if (!preferencesReady) return
    if (searchQuery.trim() === "") {
      searchResultsActive = false
      root.loadInitialArticles()
      return
    }
    if (searchProcess.running) return
    searchProcessQuery = searchQuery
    var command = [fetchBinary, "search", "--db", dbPath,
      "--query", searchProcessQuery, "--limit", String(root.resolvedMaxItems)]
    var names = root.configuredFeedNamesArray()
    for (var i = 0; i < names.length; i++) command.push("--feed", names[i])
    searchProcess.command = command
    searchProcess.running = true
  }

  function configuredFeedNames() {
    var names = {}
    if (config && Array.isArray(config.feeds)) {
      for (var i = 0; i < config.feeds.length; i++) {
        var feed = config.feeds[i]
        var name = root.feedDisplayName(feed)
        if (name !== "") names[name] = true
      }
    }
    return names
  }

  function filterToConfiguredFeeds(items) {
    var names = configuredFeedNames()
    return items.filter(function(article) {
      return article && names[String(article.feed || "")] === true
    })
  }

  function mergeFetched(raw) {
    var result = loadJson(raw, null)
    if (!result || !Array.isArray(result.items)) {
      loading = false; status = "Could not refresh feeds"; return
    }
    feedErrors = Array.isArray(result.errors) ? result.errors : []
    root.applySnapshotStats(result)
    searchResultsActive = false
    root.articles = filterToConfiguredFeeds(result.items)
    selectedIndex = Math.min(selectedIndex, Math.max(0, visibleArticles.length - 1))
    root.notifyNewPosts(Array.isArray(result.newItems) ? result.newItems : [])
    loading = false
    lastUpdated = new Date().toLocaleTimeString(Qt.locale(), Locale.ShortFormat)
    status = feedErrors.length
      ? feedErrors.length + " feed(s) failed · " + lastUpdated
      : lastUpdated
    root.prefetchArticles()
    root.updateUnreadNotification()
    if (searchQuery.trim() !== "") root.requestSearch()
  }

  function prefetchArticles() {
    // Warm the article cache in the background right after a refresh, so
    // opening an unread article is usually instant instead of waiting on a
    // live fetch. Runs as its own process and never blocks the UI; loadArticle
    // still fetches live if an article wasn't prefetched in time.
    if (prefetchProcess.running) return
    prefetchProcess.command = [fetchBinary, "prefetch", "--db", dbPath, "--limit", "20", "--concurrency", "3"]
    prefetchProcess.running = true
  }

  function reportPrefetched(raw) {
    var result = loadJson(raw, null)
    if (!result || !(result.prefetched > 0)) return
    root.prefetchNote = result.prefetched === 1
      ? "1 article ready to read offline"
      : result.prefetched + " articles ready to read offline"
  }

  function applyInitialArticles(raw) {
    var result = loadJson(raw, null)
    var items = (result && Array.isArray(result.items)) ? result.items : []
    root.applySnapshotStats(result)
    if (searchQuery.trim() !== "") {
      root.requestSearch()
      return
    }
    searchResultsActive = false
    root.articles = filterToConfiguredFeeds(items)
    root.prefetchArticles()
    root.updateUnreadNotification()
  }

  function applySearchArticles(raw) {
    var result = loadJson(raw, null)
    if (searchProcessQuery !== searchQuery) {
      searchDebounce.restart()
      return
    }
    root.applySnapshotStats(result)
    var items = (result && Array.isArray(result.items)) ? result.items : []
    searchResultsActive = true
    root.articles = filterToConfiguredFeeds(items)
    selectedIndex = Math.min(selectedIndex, Math.max(0, visibleArticles.length - 1))
    root.updateUnreadNotification()
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

  function updateUnreadNotification() {
    // Conveys the unread count through Omarchy's own notification system
    // instead of the bar icon: BarIconButton renders its glyph unclipped,
    // so appending a live count there bled past the icon slot and over the
    // neighboring bar widget (see BarWidget.qml). This notification is
    // replaced in place (via --replace-id) rather than stacking a new toast
    // every refresh, and never auto-expires (-t 0) so it behaves like a
    // persistent counter until the user dismisses or reads everything.
    if (!omarchyPath || !stateReady) return
    if (unreadCount === lastNotifiedUnreadCount) return
    lastNotifiedUnreadCount = unreadCount

    if (unreadCount === 0) {
      unreadNotifyProcess.command = [
        omarchyPath + "/bin/omarchy-notification-send",
        "--app-name", "Feader RSS",
        "-g", "",
        "-u", "low",
        "-t", "4000",
        "-r", String(unreadNotificationId),
        "Feader RSS",
        "All caught up"
      ]
    } else {
      var headline = unreadCount === 1 ? "1 unread article" : unreadCount + " unread articles"
      var description = unreadFeedCount === 1 ? "1 feed" : unreadFeedCount + " feeds"
      var command = [
        omarchyPath + "/bin/omarchy-notification-send",
        "--app-name", "Feader RSS",
        "-g", "",
        "-u", "low",
        "-t", "0",
        "-p"
      ]
      if (unreadNotificationId > 0) command.push("-r", String(unreadNotificationId))
      command.push(headline, description)
      unreadNotifyProcess.command = command
    }
    if (!unreadNotifyProcess.running) unreadNotifyProcess.running = true
  }

  function applyUnreadNotificationId(raw) {
    var id = parseInt(String(raw).trim(), 10)
    if (isFinite(id) && id > 0) root.unreadNotificationId = id
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
    if (globalUnreadCount >= 0) globalUnreadCount = Math.max(0, globalUnreadCount - 1)
    persistMarkRead(updated.id)
    root.updateUnreadNotification()
    return updated
  }

  function persistMarkRead(id) {
    Quickshell.execDetached([fetchBinary, "mark-read", "--db", dbPath, String(id)])
  }

  function markAllRead() {
    var next = articles.map(function(article) { return Object.assign({}, article, { read: true }) })
    articles = next
    var command = [fetchBinary, "mark-all", "--db", dbPath]
    var names = root.configuredFeedNamesArray()
    for (var i = 0; i < names.length; i++) command.push("--feed", names[i])
    Quickshell.execDetached(command)
    globalUnreadCount = 0
    globalUnreadFeedCount = 0
    root.updateUnreadNotification()
  }

  function markAllUnread() {
    var next = articles.map(function(article) { return Object.assign({}, article, { read: false }) })
    articles = next
    var command = [fetchBinary, "mark-all", "--db", dbPath, "--unread"]
    var names = root.configuredFeedNamesArray()
    for (var i = 0; i < names.length; i++) command.push("--feed", names[i])
    Quickshell.execDetached(command)
    // The database may contain more articles than the current page. Let the
    // next list/refresh response provide the exact global count instead of
    // pretending that the visible subset is the whole store.
    globalUnreadCount = -1
    globalUnreadFeedCount = -1
    root.updateUnreadNotification()
    root.loadInitialArticles()
  }

  function toggleStar(article) {
    if (!article || !article.id) return
    var articleId = String(article.id)
    var starred = !article.starred
    var next = articles.slice()
    for (var i = 0; i < next.length; i++) {
      if (String(next[i].id || "") === articleId) {
        next[i] = Object.assign({}, next[i], { starred: starred })
        break
      }
    }
    articles = next
    if (selectedArticle && String(selectedArticle.id || "") === articleId)
      selectedArticle = Object.assign({}, selectedArticle, { starred: starred })
    // Go's flag package only accepts a boolean flag's value joined with "=";
    // a separate array element (e.g. "--value", "true") leaves "true" as an
    // extra positional argument and the command silently fails.
    Quickshell.execDetached([fetchBinary, "star", "--db", dbPath, "--value=" + (starred ? "true" : "false"), articleId])
  }

  function parseTags(value) {
    var tags = []
    var seen = {}
    var values = String(value || "").split(",")
    for (var i = 0; i < values.length; i++) {
      var tag = values[i].trim()
      var key = tag.toLowerCase()
      if (tag !== "" && !seen[key]) {
        seen[key] = true
        tags.push(tag)
      }
    }
    return tags
  }

  function saveArticleTags() {
    if (!selectedArticle || !selectedArticle.id) return
    var tags = root.parseTags(tagDraft)
    var articleId = String(selectedArticle.id)
    var next = articles.slice()
    for (var i = 0; i < next.length; i++) {
      if (String(next[i].id || "") === articleId) {
        next[i] = Object.assign({}, next[i], { tags: tags })
        break
      }
    }
    articles = next
    selectedArticle = Object.assign({}, selectedArticle, { tags: tags })
    tagDraft = tags.join(", ")
    Quickshell.execDetached([fetchBinary, "set-tags", "--db", dbPath, "--tags", tags.join(","), articleId])
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
    articleProcess.command = [fetchBinary, "article", "--db", dbPath, String(article.url)]
    articleProcess.running = true
  }

  function showArticle(article) {
    if (!article) return
    var openedArticle = markRead(article)
    selectedArticle = openedArticle
    tagDraft = Array.isArray(openedArticle.tags) ? openedArticle.tags.join(", ") : ""
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
    // Also ensures the config directory exists: on a fresh XDG_CONFIG_HOME
    // (or one where "omarchy" hasn't been created yet by anything else),
    // writing rss-reader.json would otherwise fail with no indication why.
    command: ["mkdir", "-p", root.stateDir, root.configHome + "/omarchy"]
    onExited: { stateFile.reload(); configFile.reload() }
  }

  FileView {
    id: configFile
    path: root.configPath
    watchChanges: true
    // Write to a temp file and rename it into place, matching stateFile
    // below, so a crash or power loss mid-write leaves the last known-good
    // rss-reader.json intact instead of a truncated file that would parse
    // as empty and silently drop every configured feed on the next load.
    atomicWrites: true
    printErrors: false
    onLoaded: { root.loadConfig(text()); stateFile.reload(); root.refresh() }
    // A missing file is the normal first-run state (handled the same way
    // onLoaded would with no feeds); any other error — e.g. a dangling
    // symlink or a permissions problem — previously failed completely
    // silently, leaving the panel stuck on an empty configuration with no
    // indication why.
    onLoadFailed: (error) => {
      if (error !== FileViewError.FileNotFound) {
        root.status = "Could not load feed configuration: " + FileViewError.toString(error)
      }
      stateFile.reload()
      root.refresh()
    }
    onSaved: root.status = "Feeds saved"
    onSaveFailed: (error) => {
      root.status = "Could not save feeds: " + FileViewError.toString(error)
    }
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
    onLoadFailed: {
      root.preferencesReady = true
      root.stateReady = true
      if (root.searchQuery.trim() !== "") root.requestSearch()
      else root.loadInitialArticles()
    }
    // Best-effort: UI preferences are not worth interrupting the reader over,
    // but a silent failure here previously left no trace at all.
    onSaveFailed: (error) => console.warn("io.github.kitsunesemcalda.feader-rss",
      "could not save UI preferences: " + FileViewError.toString(error))
    onFileChanged: reload()
  }

  Process {
    id: fetchProcess
    // Quickshell.Io's Process.running does not reset itself to false when
    // the child exits; refresh() gates on it to avoid overlapping fetches,
    // so leaving it stuck true here would silently block every future
    // refresh (including the periodic Timer below) for the rest of the
    // session.
    onExited: running = false
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
    id: listProcess
    onExited: running = false
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: root.applyInitialArticles(text)
    }
    stderr: StdioCollector {
      waitForEnd: true
      onStreamFinished: if (text.trim() !== "") console.warn("io.github.kitsunesemcalda.feader-rss", text.trim())
    }
  }

  Process {
    id: searchProcess
    onExited: {
      running = false
      if (root.searchQuery !== root.searchProcessQuery) searchDebounce.restart()
    }
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: root.applySearchArticles(text)
    }
    stderr: StdioCollector {
      waitForEnd: true
      onStreamFinished: if (text.trim() !== "") console.warn("io.github.kitsunesemcalda.feader-rss", text.trim())
    }
  }

  Timer {
    id: searchDebounce
    interval: root.searchDebounceMs
    repeat: false
    onTriggered: root.requestSearch()
  }

  Process {
    id: prefetchProcess
    onExited: running = false
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: root.reportPrefetched(text)
    }
    stderr: StdioCollector {
      waitForEnd: true
      onStreamFinished: if (text.trim() !== "") console.warn("io.github.kitsunesemcalda.feader-rss", text.trim())
    }
  }

  Process {
    id: unreadNotifyProcess
    onExited: running = false
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: root.applyUnreadNotificationId(text)
    }
    stderr: StdioCollector {
      waitForEnd: true
      onStreamFinished: if (text.trim() !== "") console.warn("io.github.kitsunesemcalda.feader-rss", text.trim())
    }
  }

  Process {
    id: articleProcess
    onExited: running = false
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
    contentWidth: panel.fittedContentWidth(Style.space(620))
    contentHeight: panel.fittedContentHeight(column.y + column.implicitHeight + Style.space(16))
    // Distance between the bar edge and this panel. Configurable via
    // config.panelGap (falls back to the shell's default gap) for anyone
    // who needs extra clearance so it doesn't crowd a neighboring panel.
    gap: root.resolvedPanelGap

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
        var key = String(text || "").toLowerCase()
        var shortcuts = root.resolvedShortcuts
        if (key === shortcuts.refresh) root.refresh()
        else if (key === shortcuts.settings) root.openSettings()
        else if (key === shortcuts.openArticle) {
          if (root.detailOpen) root.openArticle(root.selectedArticle)
          else if (root.visibleArticles.length) root.openArticle(root.visibleArticles[root.selectedIndex])
        } else if (!root.settingsOpen && !root.detailOpen) {
          if (key === shortcuts.search) searchField.forceActiveFocus()
          else if (key === shortcuts.markAllRead) root.requestConfirm("markAllRead")
          else if (key === shortcuts.filterAll) root.setReadFilter("all")
          else if (key === shortcuts.filterUnread) root.setReadFilter("unread")
          else if (key === shortcuts.filterRead) root.setReadFilter("read")
          else if (key === shortcuts.filterStarred) root.setReadFilter("starred")
        }
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
      flickDeceleration: 6000
      maximumFlickVelocity: 2000
      Controls.ScrollBar.vertical: Controls.ScrollBar { policy: Controls.ScrollBar.AsNeeded }

      WheelHandler {
        // Flickable's own wheel handling moves the content by a large,
        // device-dependent amount per notch, which reads as a jarring jump
        // on this panel's short list. Take over wheel input entirely and
        // step by a configurable amount instead (config.scrollStep, falls
        // back to 54 when unset or invalid — see root.resolvedScrollStep).
        target: null
        acceptedDevices: PointerDevice.Mouse | PointerDevice.TouchPad
        onWheel: function(event) {
          var step = Style.space(root.resolvedScrollStep)
          var maxY = Math.max(0, scrollArea.contentHeight - scrollArea.height)
          var deltaY = event.angleDelta.y !== 0 ? event.angleDelta.y : event.pixelDelta.y
          if (deltaY === 0) return
          var next = scrollArea.contentY - (deltaY / 120) * step
          scrollArea.contentY = Math.max(0, Math.min(next, maxY))
        }
      }

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

      Text {
        visible: !root.detailOpen && !root.settingsOpen && root.prefetchNote !== ""
        text: root.prefetchNote
        color: root.dim; font.family: root.fontFamily
        font.pixelSize: Style.font.caption; wrapMode: Text.WordWrap
        width: parent.width
      }

      BorderSurface {
        id: inboxSummary
        visible: !root.detailOpen && !root.settingsOpen && root.config.feeds.length > 0
        width: parent.width
        height: inboxSummaryContent.y + inboxSummaryContent.implicitHeight + Style.space(10)
        // Neutral regardless of unread state — the unread count below is the
        // one place this summary uses Color.accent, so it reads as the
        // single signal worth noticing rather than a colored block.
        color: Style.normalFillFor(root.foreground, root.foreground)
        borderSpec: Border.controlSpec("normal", root.foreground, root.foreground)
        radius: Style.cornerRadius

        Column {
          id: inboxSummaryContent
          x: Style.space(10)
          y: Style.space(10)
          width: parent.width - Style.space(20)
          spacing: Style.space(4)

          Row {
            width: parent.width
            spacing: Style.space(10)

            Text {
              text: String(root.unreadCount)
              color: root.unreadCount > 0 ? Color.accent : root.foreground
              font.family: root.fontFamily
              font.pixelSize: Style.font.display
              font.bold: true
              verticalAlignment: Text.AlignVCenter
            }

            Column {
              width: Math.max(0, parent.width - Style.space(64))
              spacing: Style.space(1)
              Text {
                text: "UNREAD"
                color: root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.caption
                font.bold: true
              }
              Text {
                text: root.unreadFeedCount === 1 ? "1 feed needs attention" : root.unreadFeedCount + " feeds need attention"
                color: root.dim
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
                elide: Text.ElideRight
                width: parent.width
              }
            }
          }

          Text {
            text: root.unreadCount + " unread · " + root.unreadFeedCount + " feeds"
            color: root.dim
            font.family: root.fontFamily
            font.pixelSize: Style.font.bodySmall
          }
          Text {
            text: searchProcess.running ? "Searching saved articles…"
              : (root.unreadCount === 0 ? "All caught up" : "Open an article to mark it read")
            color: root.dim
            font.family: root.fontFamily
            font.pixelSize: Style.font.caption
            wrapMode: Text.WordWrap
            width: parent.width
          }
        }
      }

      Flow {
        visible: !root.detailOpen && !root.settingsOpen
        spacing: Style.space(8)
        width: parent.width
        Button {
          id: refreshButton
          text: "Refresh"
          iconText: "↻"
          iconSpinning: root.loading
          tooltipText: root.loading ? "Refreshing feeds…" : "Refresh feeds now"
          foreground: root.foreground
          focusable: true
          onActiveFocusChanged: root.formControlFocused = activeFocus
          onClicked: root.refresh()
        }
        Button {
          id: configureButton
          text: "Configure feeds"
          iconText: "⚙"
          tooltipText: "Manage feeds and reader settings"
          foreground: root.foreground
          focusable: true
          onActiveFocusChanged: root.formControlFocused = activeFocus
          onClicked: root.openSettings()
        }
        Button {
          id: closeButton
          text: "Close"
          iconText: "×"
          tooltipText: "Close Feader RSS"
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
          iconText: "✓"
          tooltipText: "Mark every configured article as read"
          foreground: root.foreground
          focusable: true
          onActiveFocusChanged: root.formControlFocused = activeFocus
          onClicked: root.requestConfirm("markAllRead")
        }
        Button {
          text: "Mark all unread"
          iconText: "↺"
          tooltipText: "Mark every configured article as unread"
          foreground: root.foreground
          focusable: true
          onActiveFocusChanged: root.formControlFocused = activeFocus
          onClicked: root.requestConfirm("markAllUnread")
        }
      }

      Text {
        visible: !root.detailOpen && !root.settingsOpen
        text: root.shortcutsHint()
        color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.caption
        wrapMode: Text.WordWrap; width: parent.width
      }

      Flow {
        visible: !root.detailOpen && !root.settingsOpen && root.articles.length > 0
        width: parent.width
        spacing: Style.space(8)
        Text {
          text: root.visibleArticles.length + (root.visibleArticles.length === 1 ? " article" : " articles")
          color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.bodySmall
        }
        Text {
          visible: root.searchResultsActive
          text: "matching your search"
          color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.bodySmall
        }
      }

      BorderSurface {
        id: feedErrorSurface
        visible: !root.detailOpen && !root.settingsOpen && root.feedErrors.length > 0
        width: parent.width
        height: feedErrorContent.y + feedErrorContent.implicitHeight + Style.space(10)
        color: Qt.rgba(Color.urgent.r, Color.urgent.g, Color.urgent.b, 0.08)
        borderSpec: Border.controlSpec("normal", root.foreground, Color.urgent)
        radius: Style.cornerRadius

        Column {
          id: feedErrorContent
          x: Style.space(10)
          y: Style.space(10)
          width: parent.width - Style.space(20)
          spacing: Style.space(4)

          Flow {
            width: parent.width
            spacing: Style.space(8)
            Text {
              width: Math.max(0, parent.width - retryButton.implicitWidth - parent.spacing)
              text: "Some feeds need attention"
              color: Color.urgent
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
              font.bold: true
              wrapMode: Text.WordWrap
            }
            Button {
              id: retryButton
              text: "Retry"
              iconText: "↻"
              tooltipText: "Retry failed feeds"
              foreground: root.foreground
              focusable: true
              onActiveFocusChanged: root.formControlFocused = activeFocus
              onClicked: root.refresh()
            }
          }

          Repeater {
            model: root.feedErrors
            delegate: Text {
              required property var modelData
              width: parent.width
              // Feed failures come from server-controlled response fields.
              // Keep this explicit on the same delegate as the remote binding:
              // markup such as <img> must only ever be displayed as text.
              textFormat: Text.PlainText
              text: String(modelData.name || modelData.feed || modelData.url || "Feed")
                + ": " + String(modelData.error || modelData.message || "failed to refresh")
              color: Color.urgent
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
              wrapMode: Text.WordWrap
            }
          }
        }
      }

      Flow {
        id: searchRow
        visible: !root.detailOpen && !root.settingsOpen && root.config.feeds.length > 0
        width: parent.width
        spacing: Style.space(6)
        TextField {
          id: searchField
          width: root.searchQuery !== ""
            ? Math.max(0, searchRow.width - clearSearchButton.implicitWidth - searchRow.spacing)
            : searchRow.width
          foreground: root.foreground
          placeholderText: "Search title, summary, full text, tags or author"
          onTextChanged: root.setSearchQuery(text)
          onActiveFocusChanged: root.formControlFocused = activeFocus
          Component.onCompleted: text = root.searchQuery
        }
        Button {
          id: clearSearchButton
          visible: root.searchQuery !== ""
          text: "Clear"
          iconText: "×"
          tooltipText: "Clear search"
          foreground: root.foreground
          focusable: true
          onActiveFocusChanged: root.formControlFocused = activeFocus
          onClicked: root.clearSearch()
        }
      }

      Flow {
        visible: !root.detailOpen && !root.settingsOpen && root.config.feeds.length > 0
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
        Button {
          text: "Saved"
          selected: root.readFilter === "starred"
          foreground: root.foreground
          focusable: true
          onActiveFocusChanged: root.formControlFocused = activeFocus
          onClicked: root.setReadFilter("starred")
        }
      }

      Dropdown {
        id: feedFilterDropdown
        visible: !root.detailOpen && !root.settingsOpen && root.config.feeds.length > 0
        width: parent.width
        showLabel: false
        foreground: root.foreground
        value: root.selectedFolder !== "" ? "folder:" + root.selectedFolder : root.selectedFeed
        options: [{ value: "", label: "All feeds" }].concat(
          root.configuredFolders().map(function(folder) {
            return { value: "folder:" + folder, label: "Folder: " + folder }
          })).concat(
          root.config.feeds.map(function(feed) {
            var v = root.feedDisplayName(feed)
            return { value: v, label: v }
          }))
        onHovered: function(isHovered) {}
        onChanged: function(value) {
          var selected = String(value || "")
          if (selected.indexOf("folder:") === 0) root.setSelectedFolder(selected.substring(7))
          else root.setSelectedFeed(selected)
        }
        onActiveFocusChanged: root.formControlFocused = activeFocus
      }

      Text {
        visible: !root.detailOpen && !root.settingsOpen && root.visibleArticles.length === 0
        text: root.loading ? "Fetching articles…" : (!root.config.feeds.length
          ? "No feeds configured yet. Add a source to start reading."
          : (root.articles.length
            ? "No articles match the current filters."
            : "Your feeds are configured, but no articles have been saved yet. Try Refresh."))
        color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.body
        wrapMode: Text.WordWrap; width: parent.width
      }

      Button {
        visible: !root.detailOpen && !root.settingsOpen && !root.loading && !root.config.feeds.length
        text: "Configure feeds"
        foreground: root.foreground
        focusable: true
        onActiveFocusChanged: root.formControlFocused = activeFocus
        onClicked: root.openSettings()
      }

      Flow {
        visible: root.detailOpen && !root.settingsOpen
        spacing: Style.space(8)
        width: parent.width
        Button {
          text: "Back"
          iconText: "←"
          tooltipText: "Back to article list"
          foreground: root.foreground
          focusable: true
          onActiveFocusChanged: root.formControlFocused = activeFocus
          onClicked: root.hideArticle()
        }
        Button {
          text: "Open in browser"
          iconText: "↗"
          tooltipText: "Open this article in the browser"
          foreground: root.foreground
          focusable: true
          onActiveFocusChanged: root.formControlFocused = activeFocus
          onClicked: root.openArticle(root.selectedArticle)
        }
        Button {
          text: "Close"
          iconText: "×"
          tooltipText: "Close article"
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
          color: root.foreground; font.family: root.fontFamily
          font.pixelSize: Style.font.heading; font.bold: true
          font.underline: titleHover.hovered === true
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
          text: root.selectedArticle ? root.articleMeta(root.selectedArticle) : ""
          color: root.dim; font.family: root.fontFamily
          font.pixelSize: Style.font.bodySmall; wrapMode: Text.WordWrap
          textFormat: Text.PlainText
        }
        Flow {
          width: parent.width
          spacing: Style.space(6)
          Button {
            text: root.selectedArticle && root.selectedArticle.starred ? "Saved" : "Save"
            tooltipText: root.selectedArticle && root.selectedArticle.starred
              ? "Remove from saved articles" : "Save this article"
            foreground: root.foreground
            selected: root.selectedArticle && root.selectedArticle.starred
            focusable: true
            onActiveFocusChanged: root.formControlFocused = activeFocus
            onClicked: root.toggleStar(root.selectedArticle)
          }
          TextField {
            id: tagsField
            width: Math.max(0, parent.width - saveTagsButton.implicitWidth - parent.spacing)
            text: root.tagDraft
            foreground: root.foreground
            placeholderText: "Tags, separated by commas"
            onTextChanged: if (activeFocus) root.tagDraft = text
            onActiveFocusChanged: root.formControlFocused = activeFocus
          }
          Button {
            id: saveTagsButton
            text: "Save tags"
            iconText: "✓"
            tooltipText: "Save tags for this article"
            foreground: root.foreground
            focusable: true
            onActiveFocusChanged: root.formControlFocused = activeFocus
            onClicked: root.saveArticleTags()
          }
        }
        Text {
          visible: root.selectedArticle && String(root.selectedArticle.summary || "") !== ""
          width: parent.width
          text: "RSS SUMMARY"
          color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.caption; font.bold: true
          font.letterSpacing: 0.5
        }
        Text {
          width: parent.width
          text: root.selectedArticle ? root.articleSummary(root.selectedArticle) : ""
          color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.body
          wrapMode: Text.WordWrap
          textFormat: Text.PlainText
          lineHeight: 1.4
          lineHeightMode: Text.ProportionalHeight
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
          text: root.articleError
          color: Color.urgent; font.family: root.fontFamily; font.pixelSize: Style.font.body
          wrapMode: Text.WordWrap
          textFormat: Text.PlainText
        }
        Text {
          visible: !root.articleLoading && root.articleError === "" && root.articleContent !== ""
          width: parent.width
          text: "FULL ARTICLE"
          color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.caption; font.bold: true
          font.letterSpacing: 0.5
        }
        Text {
          width: parent.width
          visible: !root.articleLoading && root.articleError === "" && root.articleContent !== ""
          text: root.articleContent
          color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.body
          wrapMode: Text.WordWrap
          textFormat: Text.PlainText
          lineHeight: 1.4
          lineHeightMode: Text.ProportionalHeight
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
            id: articleCard
            required property var modelData
            required property int index
            property bool cardHovered: false
            width: column.width
            height: badges.implicitHeight + title.implicitHeight + meta.implicitHeight + summary.implicitHeight + Style.space(27)
            color: index === root.selectedIndex
              ? Style.selectedFillFor(root.foreground, Color.accent)
              : (cardHovered
                ? Style.hoverFillFor(root.foreground, Color.accent)
                : Style.normalFillFor(root.foreground, Color.accent))
            radius: Style.cornerRadius
            borderSpec: index === root.selectedIndex
              ? Border.controlSpec("selected", root.foreground, Color.accent)
              : (cardHovered
                ? Border.controlSpec("hover-cursor", root.foreground, Color.accent)
                : Border.none())

            Behavior on color { ColorAnimation { duration: 120 } }

            Column {
              anchors.fill: parent; anchors.margins: Style.space(9); spacing: Style.space(3)
              Flow {
                id: badges
                width: parent.width
                spacing: Style.space(6)
                Text {
                  text: modelData.read ? "READ" : "UNREAD"
                  color: modelData.read ? root.dim : root.foreground
                  font.family: root.fontFamily; font.pixelSize: Style.font.caption; font.bold: true
                  font.letterSpacing: 0.5
                }
                Text {
                  visible: modelData.cached
                  text: "READY"
                  color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.caption
                  font.letterSpacing: 0.5
                }
                Text {
                  visible: modelData.starred
                  text: "SAVED"
                  color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.caption
                  font.letterSpacing: 0.5
                }
              }
              Text { id: title; width: parent.width; text: modelData.title; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.body; font.bold: !modelData.read; maximumLineCount: 3; elide: Text.ElideRight; wrapMode: Text.WordWrap; textFormat: Text.PlainText }
              Text { id: meta; width: parent.width; text: root.articleMeta(modelData); color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.bodySmall; maximumLineCount: 2; elide: Text.ElideRight; wrapMode: Text.WordWrap; textFormat: Text.PlainText }
              Text { id: summary; width: parent.width; text: root.articleSummary(modelData); color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.bodySmall; opacity: 0.8; maximumLineCount: 3; elide: Text.ElideRight; wrapMode: Text.WordWrap; textFormat: Text.PlainText }
            }
            MouseArea {
              anchors.fill: parent
              hoverEnabled: true
              cursorShape: Qt.PointingHandCursor
              onEntered: articleCard.cardHovered = true
              onExited: articleCard.cardHovered = false
              onClicked: { root.selectedIndex = index; root.showArticle(modelData) }
            }
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
          text: "Choose a compact interval for active feeds or a sparse interval to reduce network traffic."
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
          Button {
            text: "15 min"
            selected: root.refreshMinutesDraft === 15
            foreground: root.foreground
            focusable: true
            onActiveFocusChanged: root.formControlFocused = activeFocus
            onClicked: root.setRefreshMinutes(15)
          }
          Button {
            text: "30 min"
            selected: root.refreshMinutesDraft === 30
            foreground: root.foreground
            focusable: true
            onActiveFocusChanged: root.formControlFocused = activeFocus
            onClicked: root.setRefreshMinutes(30)
          }
          Button {
            text: "1 hour"
            selected: root.refreshMinutesDraft === 60
            foreground: root.foreground
            focusable: true
            onActiveFocusChanged: root.formControlFocused = activeFocus
            onClicked: root.setRefreshMinutes(60)
          }
          Button {
            text: "5 hours"
            selected: root.refreshMinutesDraft === 300
            foreground: root.foreground
            focusable: true
            onActiveFocusChanged: root.formControlFocused = activeFocus
            onClicked: root.setRefreshMinutes(300)
          }
        }

        PanelSeparator { foreground: root.foreground }

        PanelSectionHeader {
          text: "ARTICLE RETENTION"
          foreground: root.foreground
          fontFamily: root.fontFamily
        }

        Text {
          width: parent.width
          text: "Keep the newest articles locally. Read and unsaved articles are removed first; 0 keeps everything."
          color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.bodySmall
          wrapMode: Text.WordWrap
        }

        TextField {
          width: parent.width
          text: String(root.retentionItemsDraft)
          foreground: root.foreground
          placeholderText: "Stored articles (0 = unlimited)"
          inputMethodHints: Qt.ImhDigitsOnly
          onTextChanged: if (activeFocus) root.setRetentionItems(text)
          onActiveFocusChanged: root.formControlFocused = activeFocus
        }

        PanelSectionHeader {
          text: "SCROLL SPEED"
          foreground: root.foreground
          fontFamily: root.fontFamily
        }

        Text {
          width: parent.width
          text: "How far the article list moves per mouse wheel notch."
          color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.bodySmall
          wrapMode: Text.WordWrap
        }

        Flow {
          width: parent.width
          spacing: Style.space(6)
          Button {
            text: "Slow"
            selected: root.scrollStepDraft === 30
            foreground: root.foreground
            focusable: true
            onActiveFocusChanged: root.formControlFocused = activeFocus
            onClicked: root.setScrollStep(30)
          }
          Button {
            text: "Normal"
            selected: root.scrollStepDraft === 54
            foreground: root.foreground
            focusable: true
            onActiveFocusChanged: root.formControlFocused = activeFocus
            onClicked: root.setScrollStep(54)
          }
          Button {
            text: "Fast"
            selected: root.scrollStepDraft === 90
            foreground: root.foreground
            focusable: true
            onActiveFocusChanged: root.formControlFocused = activeFocus
            onClicked: root.setScrollStep(90)
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
            : "Give each feed a name and optional folder, or leave them blank to use defaults."
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
              required property string folder
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

                TextField {
                  width: parent.width
                  text: folder
                  foreground: root.foreground
                  placeholderText: "Folder (optional)"
                  onTextChanged: if (activeFocus) feedModel.setProperty(index, "folder", text)
                  onActiveFocusChanged: root.formControlFocused = activeFocus
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

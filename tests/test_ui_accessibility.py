import re
import unittest
from pathlib import Path


PANEL = (Path(__file__).parents[1] / "Panel.qml").read_text()
BAR = (Path(__file__).parents[1] / "BarWidget.qml").read_text()


class UiAccessibilityContractTests(unittest.TestCase):
    def test_panel_has_keyboard_focus_and_escape_contract(self):
        self.assertIn("focusTarget: keyCatcher", PANEL)
        self.assertIn("blocked: root.formControlFocused", PANEL)
        self.assertIn("id: keyCatcher\n      anchors.fill: parent\n      z: 0", PANEL)
        self.assertIn("id: scrollArea\n      anchors.fill: parent\n      z: 1", PANEL)
        self.assertIn("if (root.pendingConfirm !== \"\") root.cancelConfirm()", PANEL)
        self.assertIn("else root.close()", PANEL)
        self.assertIn("onTabRequested", PANEL)
        self.assertIn("onMoveRequested", PANEL)
        self.assertIn("onActivateRequested", PANEL)

    def test_primary_actions_are_keyboard_focusable_and_labeled(self):
        labels = ("Refresh", "Configure feeds", "Mark all read", "Mark all unread", "Close", "All", "Unread", "Read", "Saved", "1 min", "5 min", "15 min", "30 min", "1 hour", "5 hours", "Add feed", "Save feeds", "Cancel", "Remove")
        for label in labels:
            self.assertIn(f'text: "{label}"', PANEL)
        self.assertGreaterEqual(PANEL.count("focusable: true"), len(labels))

    def test_feed_filter_uses_dropdown(self):
        self.assertIn("id: feedFilterDropdown", PANEL)
        self.assertIn('{ value: "", label: "All feeds" }', PANEL)
        self.assertIn('return { value: "folder:" + folder, label: "Folder: " + folder }', PANEL)
        self.assertIn('root.setSelectedFolder(selected.substring(7))', PANEL)
        self.assertIn('root.setSelectedFeed(selected)', PANEL)

    def test_feed_inputs_have_placeholders_and_focus_handoff(self):
        self.assertIn('placeholderText: "Feed name (optional)"', PANEL)
        self.assertIn('placeholderText: "https://example.org/feed.xml"', PANEL)
        self.assertGreaterEqual(PANEL.count("onActiveFocusChanged: root.formControlFocused = activeFocus"), 6)
        self.assertNotIn("focus: root.settingsOpen && index === 0", PANEL)
        self.assertIn("Qt.callLater(forceActiveFocus)", PANEL)
        self.assertIn("id: feedFields", PANEL)
        self.assertIn("readonly property bool twoColumn: feedFields.width >= (minFieldWidth * 2 + feedFields.spacing)", PANEL)
        self.assertIn("width: feedFields.twoColumn", PANEL)
        self.assertNotIn("anchors.verticalCenter: parent.verticalCenter", PANEL)

    def test_layout_has_bounded_content_and_feed_limit(self):
        self.assertRegex(PANEL, r"contentWidth:\s+panel\.fittedContentWidth")
        self.assertIn("contentHeight: panel.fittedContentHeight(column.y + column.implicitHeight + Style.space(16))", PANEL)
        self.assertIn("contentWidth: width", PANEL)
        self.assertIn("contentHeight: column.y + column.implicitHeight + Style.space(16)", PANEL)
        self.assertIn("clip: true", PANEL)
        self.assertIn("Controls.ScrollBar.vertical", PANEL)
        self.assertIn("spacing: Style.space(10)", PANEL)
        self.assertIn("feedModel.count >= 8", PANEL)
        self.assertIn("width: Math.max(0, scrollArea.width - Style.space(32))", PANEL)

    def test_panel_uses_theme_tokens_and_notification_helper(self):
        self.assertNotIn("Color.primary", PANEL)
        self.assertIn("foreground: Color.popups.text", PANEL)
        self.assertIn("readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family", PANEL)
        self.assertIn("Style.selectedFillFor(root.foreground, Color.accent)", PANEL)
        self.assertIn("Style.normalFillFor(root.foreground, Color.accent)", PANEL)
        self.assertIn("omarchy-notification-send", PANEL)
        self.assertIn("stateReady", PANEL)
        self.assertIn('"New RSS post"', PANEL)
        self.assertIn("property var feedErrors: []", PANEL)
        self.assertIn("lastUpdated", PANEL)

    def test_panel_does_not_bundle_default_feeds(self):
        self.assertIn("feeds: [], maxItems: 200, retentionItems: 1000, refreshMinutes: 5,", PANEL)
        self.assertNotIn("lwn.net/headlines/rss", PANEL)
        self.assertNotIn("omarchy.org/feed.xml", PANEL)
        self.assertIn('feedModel.append({ name: "", url: "", folder: "" })', PANEL)

    def test_empty_feed_configuration_does_not_restore_cached_articles(self):
        self.assertIn("if (!config || !Array.isArray(config.feeds) || config.feeds.length === 0)", PANEL)
        self.assertIn("root.articles = []", PANEL)

    def test_config_reload_restores_persisted_state_after_load_order(self):
        self.assertIn("onLoaded: { root.loadConfig(text()); stateFile.reload(); root.refresh() }", PANEL)
        self.assertIn("function saveState()", PANEL)

    def test_refresh_preserves_local_read_state(self):
        # Read-state preservation now lives server-side in the Go backend
        # (internal/store.Upsert never overwrites the stored `read` flag);
        # the panel just filters whatever the backend returns down to the
        # feeds still configured.
        self.assertIn("var name = root.feedDisplayName(feed)", PANEL)
        self.assertIn("names[name] = true", PANEL)
        self.assertIn("names[String(article.feed || \"\")] === true", PANEL)

    def test_clicking_an_article_marks_the_opened_item_read(self):
        self.assertIn("var openedArticle = markRead(article)", PANEL)
        self.assertIn("selectedArticle = openedArticle", PANEL)
        self.assertIn("loadArticle(openedArticle)", PANEL)
        self.assertIn('String(article.id || "")', PANEL)
        self.assertIn('String(candidate.id || "") === articleId', PANEL)
        self.assertIn('String(candidate.url || "") === articleUrl', PANEL)

    def test_feed_names_are_inferred_but_custom_names_are_preserved(self):
        self.assertIn("function inferFeedName(url)", PANEL)
        self.assertIn("root.inferFeedName(url) || url", PANEL)
        self.assertIn("root.feedDisplayName(feed)", PANEL)
        self.assertIn("onEditingFinished", PANEL)

    def test_feed_form_declares_model_roles_explicitly(self):
        self.assertIn("required property string name", PANEL)
        self.assertIn("required property string url", PANEL)
        self.assertIn('text: name', PANEL)
        self.assertIn('text: url', PANEL)
        self.assertIn("Each feed URL must be unique.", PANEL)
        self.assertIn("Each feed name must be unique.", PANEL)

    def test_article_detail_loads_full_content(self):
        self.assertIn('articleProcess.command = [fetchBinary, "article", "--db", dbPath, String(article.url)]', PANEL)
        self.assertIn("root.mergeArticle(text)", PANEL)
        self.assertIn("root.articleContent", PANEL)
        self.assertIn("textFormat: Text.PlainText", PANEL)

    def test_unread_actions_and_counts_are_exposed(self):
        self.assertIn("function markAllRead()", PANEL)
        self.assertIn("function markAllUnread()", PANEL)
        self.assertIn("readonly property int unreadCount", PANEL)
        self.assertIn("readonly property int unreadFeedCount", PANEL)
        self.assertIn('text: root.unreadCount + " unread · " + root.unreadFeedCount + " feeds"', PANEL)

    def test_tray_and_refresh_interval_use_unread_feed_count(self):
        # The bar icon must stay a single, constant glyph: BarIconButton
        # renders `text` as one un-clipped optical glyph sized for its
        # fixed icon slot, so appending a live unread count (e.g. " 15")
        # used to bleed those digits past the slot and over the neighboring
        # bar widget. Unread state is now conveyed via `active` instead.
        self.assertIn('readonly property string label: ""', PANEL)
        self.assertIn("readonly property var refreshOptions", PANEL)
        self.assertIn('{ value: 15, label: "15 min" }', PANEL)
        self.assertIn('{ value: 30, label: "30 min" }', PANEL)
        self.assertIn('{ value: 60, label: "1 hour" }', PANEL)
        self.assertIn('{ value: 300, label: "5 hours" }', PANEL)
        self.assertIn("return root.normalizeRefreshMinutes(config && config.refreshMinutes) * 60", PANEL)
        self.assertIn('text: panelLoader.item ? panelLoader.item.label : ""', BAR)
        self.assertIn("active: panelLoader.item ? panelLoader.item.unreadCount > 0 : false", BAR)
        self.assertIn("tooltipText: panelLoader.item ? panelLoader.item.unreadSummary", BAR)

    def test_article_list_uses_theme_surface_tokens(self):
        self.assertIn("delegate: BorderSurface", PANEL)
        self.assertIn('Border.controlSpec("selected", root.foreground, Color.accent)', PANEL)
        self.assertIn("Border.none()", PANEL)
        self.assertNotIn("delegate: Rectangle", PANEL)

    def test_article_surface_covers_metadata_and_uses_compact_gap(self):
        self.assertIn("height: badges.implicitHeight + title.implicitHeight + meta.implicitHeight + summary.implicitHeight + Style.space(27)", PANEL)
        self.assertIn("spacing: Style.space(6)", PANEL)

    def test_article_cards_expose_rich_feed_metadata(self):
        self.assertIn("function feedDisplayName(feed)", PANEL)
        self.assertIn("function articleMeta(article)", PANEL)
        self.assertIn("String(article.author || \"\")", PANEL)
        self.assertIn("article.categories", PANEL)
        self.assertIn("text: modelData.read ? \"READ\" : \"UNREAD\"", PANEL)
        self.assertIn('text: "RSS SUMMARY"', PANEL)
        self.assertIn('text: "FULL ARTICLE"', PANEL)
        self.assertIn('text: root.selectedArticle && root.selectedArticle.starred ? "Saved" : "Save"', PANEL)
        self.assertIn('placeholderText: "Tags, separated by commas"', PANEL)

    def test_reader_ui_exposes_status_and_interaction_feedback(self):
        self.assertIn("id: inboxSummary", PANEL)
        self.assertIn('text: String(root.unreadCount)', PANEL)
        self.assertIn('iconSpinning: root.loading', PANEL)
        self.assertIn('tooltipText: root.loading ? "Refreshing feeds…" : "Refresh feeds now"', PANEL)
        self.assertIn("property int searchDebounceMs: 180", PANEL)
        self.assertIn("id: searchDebounce", PANEL)
        self.assertIn("function clearSearch()", PANEL)
        self.assertIn('text: "Clear"', PANEL)
        self.assertIn("property bool cardHovered: false", PANEL)
        self.assertIn("onEntered: articleCard.cardHovered = true", PANEL)
        self.assertIn('text: "Retry"', PANEL)
        self.assertIn('tooltipText: panelLoader.item ? panelLoader.item.unreadSummary', BAR)

    def test_text_and_actions_adapt_to_available_width(self):
        self.assertGreaterEqual(PANEL.count("Flow {"), 6)
        self.assertIn("width: parent.width", PANEL)
        self.assertIn("maximumLineCount: 3", PANEL)
        self.assertIn("elide: Text.ElideRight", PANEL)
        self.assertIn("wrapMode: Text.WordWrap", PANEL)

    def test_feed_settings_actions_are_before_the_feed_list(self):
        actions = PANEL.index('text: "Add feed"')
        feed_list = PANEL.index("model: feedModel")
        self.assertLess(actions, feed_list)
        self.assertIn("id: settingsColumn", PANEL)
        self.assertIn("id: feedList", PANEL)
        self.assertIn("height: feedCard.implicitHeight + Style.space(18)", PANEL)
        self.assertIn('text: "UPDATE INTERVAL"', PANEL)
        self.assertIn('text: "RSS FEEDS"', PANEL)
        self.assertIn('text: "ACTIONS"', PANEL)

    def test_settings_actions_stretch_to_fill_the_panel_width(self):
        self.assertIn("id: actionsFlow", PANEL)
        self.assertIn("readonly property bool twoColumn: actionsFlow.width >= (minButtonWidth * 2 + spacing)", PANEL)
        self.assertIn("width: actionsFlow.twoColumn ? (actionsFlow.width - actionsFlow.spacing) / 2 : actionsFlow.width", PANEL)
        self.assertGreaterEqual(PANEL.count("height: Style.space(44)"), 2)

    def test_settings_resizes_via_implicit_height_not_nested_childrenrect(self):
        # settingsColumn/feedList must rely on Column's own implicit sizing so
        # the panel resizes immediately when a feed is added or removed;
        # chaining childrenRect across nested Columns lags a layout pass.
        settings_start = PANEL.index("id: settingsColumn")
        feed_list_end = PANEL.index("Repeater {", PANEL.index("id: feedList"))
        settings_head = PANEL[settings_start:feed_list_end]
        self.assertNotIn("childrenRect", settings_head)

    def test_keyboard_cursor_scrolls_to_selected_article(self):
        self.assertIn("function ensureSelectedVisible()", PANEL)
        self.assertIn("articleRepeater.itemAt(selectedIndex)", PANEL)
        self.assertIn("scrollArea.contentY = bottom - scrollArea.height", PANEL)

    def test_filters_search_and_refresh_preference_are_persisted(self):
        self.assertIn('property string searchQuery: ""', PANEL)
        self.assertIn('property string readFilter: "all"', PANEL)
        self.assertIn('property string selectedFeed: ""', PANEL)
        self.assertIn('property string selectedFolder: ""', PANEL)
        self.assertIn("function articleMatches(article)", PANEL)
        self.assertIn("function setRefreshMinutes(value)", PANEL)
        self.assertIn("preferences: { searchQuery: searchQuery, readFilter: readFilter, selectedFeed: selectedFeed, selectedFolder: selectedFolder, selectedIndex: selectedIndex }", PANEL)
        self.assertIn('readFilter === "starred"', PANEL)

    def test_search_uses_the_backend_full_text_index(self):
        self.assertIn('[fetchBinary, "search", "--db", dbPath', PANEL)
        self.assertIn("id: searchProcess", PANEL)
        self.assertIn("function applySearchArticles(raw)", PANEL)
        self.assertIn("searchResultsActive = true", PANEL)

    def test_global_unread_counts_are_returned_by_the_backend_snapshot(self):
        self.assertIn("property int globalUnreadCount: -1", PANEL)
        self.assertIn("property int globalUnreadFeedCount: -1", PANEL)
        self.assertIn("function applySnapshotStats(result)", PANEL)
        self.assertIn('"--retention", String(root.resolvedRetentionItems)', PANEL)
        self.assertIn('"folder:" + folder', PANEL)

    def test_mark_all_actions_require_confirmation(self):
        self.assertIn('onClicked: root.requestConfirm("markAllRead")', PANEL)
        self.assertIn('onClicked: root.requestConfirm("markAllUnread")', PANEL)
        self.assertIn("function requestConfirm(action)", PANEL)
        self.assertIn("function confirmPending()", PANEL)
        self.assertIn("id: confirmDialog", PANEL)
        self.assertIn("onCanceled: root.cancelConfirm()", PANEL)
        self.assertIn("onConfirmed: root.confirmPending()", PANEL)
        self.assertIn('blocked: root.formControlFocused || root.pendingConfirm !== ""', PANEL)

    def test_keyboard_shortcuts_are_discoverable(self):
        self.assertIn("text: root.shortcutsHint()", PANEL)
        self.assertIn("function shortcutsHint()", PANEL)

    def test_keyboard_shortcuts_are_customizable(self):
        self.assertIn("readonly property var defaultShortcuts:", PANEL)
        self.assertIn("readonly property var resolvedShortcuts:", PANEL)
        self.assertIn("var overrides = config && config.shortcuts", PANEL)
        self.assertIn("if (key === shortcuts.refresh) root.refresh()", PANEL)
        self.assertIn("else if (key === shortcuts.settings) root.openSettings()", PANEL)
        self.assertIn("if (key === shortcuts.search) searchField.forceActiveFocus()", PANEL)
        self.assertIn('else if (key === shortcuts.markAllRead) root.requestConfirm("markAllRead")', PANEL)
        self.assertIn("if (root.detailOpen) root.openArticle(root.selectedArticle)", PANEL)
        self.assertIn('else if (key === shortcuts.filterAll) root.setReadFilter("all")', PANEL)

    def test_feed_errors_are_surfaced_in_the_list_view(self):
        self.assertIn("root.feedErrors.length > 0", PANEL)
        self.assertIn("Color.urgent", PANEL)

    def test_feed_error_server_fields_are_rendered_as_plain_text(self):
        match = re.search(
            r"model: root\.feedErrors\s+delegate: Text \{(?P<delegate>.*?)\n {12}\}\n {10}\}",
            PANEL,
            re.DOTALL,
        )
        self.assertIsNotNone(match)
        delegate = match.group("delegate")
        self.assertIn("textFormat: Text.PlainText", delegate)
        for field in ("modelData.name", "modelData.error", "modelData.message"):
            self.assertIn(field, delegate)

    def test_search_field_does_not_reset_while_typing(self):
        self.assertIn("id: searchField", PANEL)
        self.assertIn("onTextChanged: root.setSearchQuery(text)", PANEL)
        self.assertIn("Component.onCompleted: text = root.searchQuery", PANEL)

    def test_article_title_is_a_clickable_link(self):
        self.assertIn("id: articleTitle", PANEL)
        self.assertIn("id: titleHover", PANEL)
        self.assertIn("onClicked: root.openArticle(root.selectedArticle)", PANEL)

    def test_settings_cancel_discards_unsaved_feed_edits(self):
        self.assertIn("function resetFeedModel()", PANEL)
        self.assertIn("function closeSettings()", PANEL)
        self.assertIn("onClicked: root.closeSettings()", PANEL)
        self.assertIn("if (settingsOpen) { settingsOpen = false; resetFeedModel() }", PANEL)

    def test_bar_exposes_complete_panel_lifecycle(self):
        for function_name in ("open", "close", "toggle", "closeForPopoutSwitch"):
            self.assertRegex(BAR, rf"function {function_name}\(")


if __name__ == "__main__":
    unittest.main()

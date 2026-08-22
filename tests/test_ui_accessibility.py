import re
import unittest
from pathlib import Path


PANEL = (Path(__file__).parents[1] / "Panel.qml").read_text()
BAR = (Path(__file__).parents[1] / "BarWidget.qml").read_text()


class UiAccessibilityContractTests(unittest.TestCase):
    def test_panel_has_keyboard_focus_and_escape_contract(self):
        self.assertIn("focusTarget: keyCatcher", PANEL)
        self.assertIn("blocked: root.formControlFocused", PANEL)
        self.assertIn("onCloseRequested: root.close()", PANEL)
        self.assertIn("onTabRequested", PANEL)
        self.assertIn("onMoveRequested", PANEL)
        self.assertIn("onActivateRequested", PANEL)

    def test_primary_actions_are_keyboard_focusable_and_labeled(self):
        labels = ("Refresh", "Configure feeds", "Mark all read", "Mark all unread", "Close", "All", "Unread", "Read", "All feeds", "1 min", "5 min", "Add feed", "Save feeds", "Cancel", "Remove")
        for label in labels:
            self.assertIn(f'text: "{label}"', PANEL)
        self.assertGreaterEqual(PANEL.count("focusable: true"), len(labels))

    def test_feed_inputs_have_placeholders_and_focus_handoff(self):
        self.assertIn('placeholderText: "Feed name"', PANEL)
        self.assertIn('placeholderText: "https://example.org/feed.xml"', PANEL)
        self.assertGreaterEqual(PANEL.count("onActiveFocusChanged: root.formControlFocused = activeFocus"), 6)

    def test_layout_has_bounded_content_and_feed_limit(self):
        self.assertRegex(PANEL, r"contentWidth:\s+panel\.fittedContentWidth")
        self.assertRegex(PANEL, r"contentHeight:\s+panel\.fittedContentHeight")
        self.assertIn("contentWidth: width", PANEL)
        self.assertIn("contentHeight: column.implicitHeight", PANEL)
        self.assertIn("clip: true", PANEL)
        self.assertIn("Controls.ScrollBar.vertical", PANEL)
        self.assertIn("spacing: Style.space(12)", PANEL)
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
        self.assertIn("property var config: ({ feeds: [], maxItems: 200, refreshMinutes: 5 })", PANEL)
        self.assertNotIn("lwn.net/headlines/rss", PANEL)
        self.assertNotIn("omarchy.org/feed.xml", PANEL)
        self.assertIn('feedModel.append({ name: "", url: "" })', PANEL)

    def test_empty_feed_configuration_does_not_restore_cached_articles(self):
        self.assertIn("if (!config || !Array.isArray(config.feeds) || config.feeds.length === 0)", PANEL)
        self.assertIn("root.articles = []", PANEL)

    def test_config_reload_restores_persisted_state_after_load_order(self):
        self.assertIn("onLoaded: { root.loadConfig(text()); stateFile.reload(); root.refresh() }", PANEL)
        self.assertIn("function saveState()", PANEL)

    def test_refresh_preserves_local_read_state(self):
        self.assertIn("read: old.read === true ? true : Boolean(incoming.read)", PANEL)
        self.assertIn("configuredFeeds[String(feed.name)] = true", PANEL)
        self.assertIn("configuredFeeds[String(article.feed || \"\")] === true", PANEL)

    def test_feed_form_declares_model_roles_explicitly(self):
        self.assertIn("required property string name", PANEL)
        self.assertIn("required property string url", PANEL)
        self.assertIn('text: name', PANEL)
        self.assertIn('text: url', PANEL)
        self.assertIn("Each feed URL must be unique.", PANEL)
        self.assertIn("Each feed name must be unique.", PANEL)

    def test_article_detail_loads_full_content(self):
        self.assertIn('articleProcess.command = ["python3", fetchScript, "--article", String(article.url)]', PANEL)
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
        self.assertIn('readonly property string label: unreadCount', PANEL)
        self.assertIn('readonly property int refreshSeconds: Math.max(60, Math.min(300', PANEL)
        self.assertIn('text: panelLoader.item ? panelLoader.item.label : "󰓶"', BAR)
        self.assertIn('tooltipText: panelLoader.item ? panelLoader.item.unreadSummary', BAR)

    def test_article_list_uses_theme_surface_tokens(self):
        self.assertIn("delegate: BorderSurface", PANEL)
        self.assertIn('Border.controlSpec("selected", root.foreground, Color.accent)', PANEL)
        self.assertIn("Border.none()", PANEL)
        self.assertNotIn("delegate: Rectangle", PANEL)

    def test_article_surface_covers_metadata_and_uses_compact_gap(self):
        self.assertIn("height: title.implicitHeight + meta.implicitHeight + summary.implicitHeight", PANEL)
        self.assertIn("spacing: Style.space(6)", PANEL)

    def test_keyboard_cursor_scrolls_to_selected_article(self):
        self.assertIn("function ensureSelectedVisible()", PANEL)
        self.assertIn("articleRepeater.itemAt(selectedIndex)", PANEL)
        self.assertIn("scrollArea.contentY = bottom - scrollArea.height", PANEL)

    def test_filters_search_and_refresh_preference_are_persisted(self):
        self.assertIn('property string searchQuery: ""', PANEL)
        self.assertIn('property string readFilter: "all"', PANEL)
        self.assertIn('property string selectedFeed: ""', PANEL)
        self.assertIn("function articleMatches(article)", PANEL)
        self.assertIn("function setRefreshMinutes(value)", PANEL)
        self.assertIn("preferences: { searchQuery: searchQuery, readFilter: readFilter, selectedFeed: selectedFeed, selectedIndex: selectedIndex }", PANEL)

    def test_bar_exposes_complete_panel_lifecycle(self):
        for function_name in ("open", "close", "toggle", "closeForPopoutSwitch"):
            self.assertRegex(BAR, rf"function {function_name}\(")


if __name__ == "__main__":
    unittest.main()

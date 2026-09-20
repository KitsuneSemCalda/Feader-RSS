import re
import unittest
from pathlib import Path


PANEL = (Path(__file__).parents[1] / "Panel.qml").read_text()
BAR = (Path(__file__).parents[1] / "BarWidget.qml").read_text()


def button_blocks(source):
    """Yield each `Button { ... }` body reduced to its own top-level lines.

    Comments are dropped and nested objects (child items, handlers) are
    collapsed, so a property only matches when it is set on the Button itself.
    """
    source = re.sub(r'("(?:\\.|[^"\\\n])*")|//[^\n]*|/\*.*?\*/',
                    lambda m: m.group(1) or "", source, flags=re.S)
    for start in re.finditer(r"\bButton\s*\{", source):
        depth, own = 1, []
        for ch in source[start.end():]:
            depth += (ch == "{") - (ch == "}")
            if depth == 0:
                break
            if depth == 1 and ch not in "{}":
                own.append(ch)
        yield "".join(own)


class UiAccessibilityContractTests(unittest.TestCase):
    def test_panel_has_keyboard_focus_and_escape_contract(self):
        self.assertIn("focusTarget: keyCatcher", PANEL)
        self.assertIn("blocked: root.formControlFocused", PANEL)
        self.assertIn("if (root.pendingConfirm !== \"\") root.cancelConfirm()", PANEL)
        self.assertIn("else root.close()", PANEL)

    def test_primary_actions_are_keyboard_focusable_and_labeled(self):
        labels = ("Refresh", "Configure feeds", "Mark all read", "Mark all unread", "Close", "All", "Unread", "Read", "Saved", "1 min", "5 min", "15 min", "30 min", "1 hour", "5 hours", "Add feed", "Save feeds", "Cancel", "Remove")
        focusable = {}
        for props in button_blocks(PANEL):
            label = re.search(r'^\s*text: "([^"]*)"\s*$', props, re.M)
            if label:
                focusable.setdefault(label.group(1), []).append(
                    re.search(r"^\s*focusable: true\s*$", props, re.M) is not None
                )
        for label in labels:
            self.assertIn(label, focusable, f"no Button labeled {label!r}")
            self.assertTrue(all(focusable[label]), f"Button {label!r} is not focusable")

    def test_panel_does_not_bundle_default_feeds(self):
        self.assertIn("feeds: [], maxItems: 200, retentionItems: 1000, refreshMinutes: 5,", PANEL)
        self.assertNotIn("lwn.net/headlines/rss", PANEL)
        self.assertNotIn("omarchy.org/feed.xml", PANEL)

    def test_empty_feed_configuration_does_not_restore_cached_articles(self):
        self.assertIn("if (!config || !Array.isArray(config.feeds) || config.feeds.length === 0)", PANEL)
        self.assertIn("root.articles = []", PANEL)

    def test_theme_tokens_and_notification_helper_are_used(self):
        self.assertNotIn("Color.primary", PANEL)
        self.assertIn("omarchy-notification-send", PANEL)

    def test_full_article_text_is_rendered_as_plain_text(self):
        self.assertIn("textFormat: Text.PlainText", PANEL)

    def test_mark_all_actions_require_confirmation(self):
        self.assertIn('onClicked: root.requestConfirm("markAllRead")', PANEL)
        self.assertIn('onClicked: root.requestConfirm("markAllUnread")', PANEL)
        self.assertIn('blocked: root.formControlFocused || root.pendingConfirm !== ""', PANEL)

    def test_bar_icon_is_a_constant_glyph_with_unread_state_on_active(self):
        # A live count in the icon text bleeds past the fixed icon slot and
        # over the neighboring bar widget, so unread state uses `active`.
        self.assertRegex(PANEL, r'readonly property string label: "[\uE000-\uF8FF]"')
        self.assertIn("active: panelLoader.item ? panelLoader.item.unreadCount > 0 : false", BAR)

    def test_feed_settings_actions_are_before_the_feed_list(self):
        self.assertLess(PANEL.index('text: "Add feed"'), PANEL.index("model: feedModel"))

    def test_settings_resizes_via_implicit_height_not_nested_childrenrect(self):
        # Chaining childrenRect across nested Columns lags a layout pass, so
        # the panel would not resize immediately when a feed is added/removed.
        settings_start = PANEL.index("id: settingsColumn")
        feed_list_end = PANEL.index("Repeater {", PANEL.index("id: feedList"))
        self.assertNotIn("childrenRect", PANEL[settings_start:feed_list_end])

    def test_settings_cancel_discards_unsaved_feed_edits(self):
        self.assertIn("onClicked: root.closeSettings()", PANEL)
        self.assertIn("if (settingsOpen) { settingsOpen = false; resetFeedModel() }", PANEL)

    def test_feed_error_server_fields_are_plain_text_before_remote_binding(self):
        match = re.search(
            r"model: root\.feedErrors\s+delegate: Text \{(?P<delegate>.*?)\n {12}\}\n {10}\}",
            PANEL,
            re.DOTALL,
        )
        self.assertIsNotNone(match)
        delegate = match.group("delegate")
        text_format = "textFormat: Text.PlainText"
        text_binding = 'text: String(modelData.name || modelData.feed || modelData.url || "Feed")'
        self.assertIn(text_format, delegate)
        self.assertIn(text_binding, delegate)
        # Keep the security declaration before the remote binding. The
        # marketplace verifier reviews this delegate linearly and this order
        # makes it impossible to accidentally reintroduce the flagged shape.
        self.assertLess(delegate.index(text_format), delegate.index(text_binding))
        for field in ("modelData.name", "modelData.error", "modelData.message"):
            self.assertIn(field, delegate)

    def test_configure_feeds_icon_avoids_the_color_emoji_font(self):
        # U+2699 (plain Unicode gear) has no glyph in the bar's own font
        # (JetBrainsMono Nerd Font) but does in the system's color emoji
        # font (Noto Color Emoji), so Qt's font-fallback chain rendered a
        # full-color gear emoji here instead of a flat icon -- confirmed
        # visually on a live Quickshell instance. U+F013, the Nerd Font
        # "cog" glyph from the same private-use icon set as the bar's own
        # RSS glyph (`label` above), has no entry in the emoji font, so it
        # can't fall back to one.
        self.assertNotIn("⚙", PANEL, "the plain Unicode gear renders as a color emoji; use \\uF013 instead")
        self.assertIn('iconText: "\\uF013"', PANEL)

    def test_bar_exposes_complete_panel_lifecycle(self):
        for function_name in ("open", "close", "toggle", "closeForPopoutSwitch"):
            self.assertRegex(BAR, rf"function {function_name}\(")

    def test_center_hover_reveal_uses_setter_before_direct_assignment(self):
        # The bar object plugins receive exposes centerHoverRevealSuppressed
        # as read-only and only accepts writes through
        # setCenterHoverRevealSuppressed(value). Assigning the property
        # directly throws a TypeError that aborts close()/open() mid-call
        # (before root.controller.hide()/show() runs), which is what made
        # every panel button that routes through close()/toggle() look
        # broken. The setter must be tried first; the direct assignment may
        # only be a fallback for a bar stub without the setter.
        match = re.search(
            r"function setCenterHoverRevealSuppressed\(value\) \{(.*?)\n  \}",
            PANEL, re.S,
        )
        self.assertIsNotNone(match, "setCenterHoverRevealSuppressed function not found")
        body = match.group(1)
        setter_call = body.find("root.bar.setCenterHoverRevealSuppressed(value)")
        direct_assign = body.find("root.bar.centerHoverRevealSuppressed = value")
        self.assertNotEqual(setter_call, -1, "must call the bar's setter function")
        self.assertNotEqual(direct_assign, -1, "must keep a fallback for bars without the setter")
        self.assertLess(setter_call, direct_assign,
            "the setter call must be tried before the direct read-only assignment")


if __name__ == "__main__":
    unittest.main()

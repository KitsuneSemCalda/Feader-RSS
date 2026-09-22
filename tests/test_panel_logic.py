"""Runs the pure feed-limit and paging functions of Panel.qml under Node.

QML functions are plain JavaScript. Each one is lifted out of Panel.qml and
evaluated against a stub `root`, whose properties stand in for the QML scope
(hence `with`), so the tests exercise the shipped code rather than a copy.
"""
import json
import re
import shutil
import subprocess
import unittest
from pathlib import Path

PANEL = (Path(__file__).parents[1] / "Panel.qml").read_text()

FUNCTIONS = [
    "normalizeMaxFeeds", "sanitizeFeeds", "addFeed", "firstPageLimit", "currentPageLimit",
    "replaceArticles", "phrasePool", "pickPhrase", "refreshedLabel", "caughtUpMessage", "spinnerGlyph", "loadingLabel", "listFooterText", "setMaxFeeds", "loadMore", "appendMore", "maybeLoadMore",
]


def lift(name):
    match = re.search(rf"\n  function {name}\(.*?\n  \}}\n", PANEL, re.S)
    if match is None:
        raise AssertionError(f"function {name} not found in Panel.qml")
    return match.group(0).strip().replace(f"function {name}(", "function (", 1)


def run_js(scenario):
    """Evaluate `scenario` (JS using `root`) and return its JSON result."""
    lifted = "\n".join(f"root.{n} = {lift(n)};" for n in FUNCTIONS)
    program = f"""
const root = {{
  articlePageSize: 50, resolvedMaxItems: 200, articles: [], config: {{}},
  loadedCount: 0, moreAvailable: false, loadingMore: false, loading: false,
  preferencesReady: true, settingsOpen: false, detailOpen: false,
  searchResultsActive: false, searchProcessQuery: "", pagesGeneration: 0,
  moreRequestGeneration: 0, moreRequestSize: 0, spinnerTick: 0, maxFeedsDraft: 8, status: "",
  fetchBinary: "bin", dbPath: "db", feedModel: {{ count: 0, append(x) {{ this.count++; }} }},
  listProcess: {{ running: false }}, searchProcess: {{ running: false }},
  moreProcess: {{ running: false, command: null }},
  scrollArea: {{ contentHeight: 1000, contentY: 0, height: 400 }},
  trimmed: (v) => String(v || "").trim(),
  inferFeedName: (u) => new URL(u).hostname,
  configuredFeedNamesArray: () => ["A", "B"],
  filterToConfiguredFeeds: (items) => items.filter((i) => i.feed !== "gone"),
  loadJson: (raw, fallback) => {{ try {{ return JSON.parse(raw); }} catch (e) {{ return fallback; }} }},
}};
with (root) {{
  {lifted}
  root.listProcess = root.listProcess; root.searchProcess = root.searchProcess;
  root.moreProcess = root.moreProcess;
  const listProcess = root.listProcess, searchProcess = root.searchProcess, moreProcess = root.moreProcess;
  const scrollArea = root.scrollArea, feedModel = root.feedModel, fetchBinary = root.fetchBinary, dbPath = root.dbPath;
  const out = (() => {{ {scenario} }})();
  console.log(JSON.stringify(out));
}}
"""
    done = subprocess.run(["node", "-e", program], capture_output=True, text=True, timeout=30)
    if done.returncode != 0:
        raise AssertionError(done.stderr)
    return json.loads(done.stdout)


@unittest.skipUnless(shutil.which("node"), "node is required to run Panel.qml logic")
class FeedLimitTests(unittest.TestCase):
    def test_normalize_max_feeds_defaults_and_clamps(self):
        got = run_js("return [undefined, null, 'abc', 0, -3, 1, 8, 12.9, 100, 101, 5000].map(normalizeMaxFeeds);")
        self.assertEqual(got, [8, 8, 8, 8, 8, 1, 8, 12, 100, 100, 100])

    def test_sanitize_feeds_honours_the_limit_and_drops_invalid_entries(self):
        got = run_js("""
          const feeds = [];
          for (let i = 0; i < 20; i++) feeds.push({ name: 'F' + i, url: 'https://x.test/' + i });
          feeds.splice(1, 0, { url: 'ftp://bad' }, { url: 'https://x.test/0/' }, null);
          return [sanitizeFeeds(feeds, 8).length, sanitizeFeeds(feeds, 12).length,
                  sanitizeFeeds(feeds, 50).length, sanitizeFeeds('nope', 8).length];
        """)
        self.assertEqual(got, [8, 12, 20, 0])

    def test_add_feed_stops_at_the_configured_limit(self):
        got = run_js("""
          maxFeedsDraft = 3;
          const results = [];
          for (let i = 0; i < 5; i++) { addFeed(); results.push(feedModel.count); }
          return { results, status };
        """)
        self.assertEqual(got["results"], [1, 2, 3, 3, 3])
        self.assertEqual(got["status"], "You can configure up to 3 feeds.")

    def test_save_config_persists_max_feeds_and_guards_shrinking(self):
        self.assertIn("maxFeeds: maxFeedsDraft,", PANEL)
        self.assertIn("if (feedModel.count > maxFeedsDraft) {", PANEL)


@unittest.skipUnless(shutil.which("node"), "node is required to run Panel.qml logic")
class InfiniteScrollTests(unittest.TestCase):
    ITEMS = "Array.from({length: %d}, (_, i) => ({ id: 'a' + (%d + i), feed: 'A' }))"

    def test_first_page_is_capped_by_page_size_and_max_items(self):
        got = run_js("""
          const a = firstPageLimit();
          resolvedMaxItems = 20;
          return [a, firstPageLimit()];
        """)
        self.assertEqual(got, [50, 20])

    def test_refresh_keeps_what_was_scrolled_but_never_exceeds_max_items(self):
        got = run_js("""
          const out = [currentPageLimit()];
          loadedCount = 150; out.push(currentPageLimit());
          loadedCount = 900; out.push(currentPageLimit());
          return out;
        """)
        self.assertEqual(got, [50, 150, 200])

    def test_full_first_page_enables_more_and_a_short_one_does_not(self):
        got = run_js(f"""
          replaceArticles({self.ITEMS % (50, 0)}, 50); const full = moreAvailable;
          replaceArticles({self.ITEMS % (10, 0)}, 50); const short = moreAvailable;
          replaceArticles({self.ITEMS % (200, 0)}, 200); const capped = moreAvailable;
          return [full, short, capped, loadedCount];
        """)
        self.assertEqual(got, [True, False, False, 200])

    def test_load_more_requests_the_next_offset_for_list_and_search(self):
        got = run_js(f"""
          replaceArticles({self.ITEMS % (50, 0)}, 50);
          loadMore(); const list = moreProcess.command.slice();
          moreProcess.running = false; loadingMore = false;
          searchResultsActive = true; searchProcessQuery = 'rust';
          loadMore(); const search = moreProcess.command.slice();
          return {{ list, search }};
        """)
        self.assertEqual(got["list"], ["bin", "list", "--db", "db", "--limit", "50", "--offset", "50",
                                       "--feed", "A", "--feed", "B"])
        self.assertEqual(got["search"][:6], ["bin", "search", "--db", "db", "--query", "rust"])
        self.assertIn("--offset", got["search"])

    def test_last_page_is_trimmed_to_max_items(self):
        got = run_js(f"""
          resolvedMaxItems = 120;
          replaceArticles({self.ITEMS % (50, 0)}, 50);
          loadedCount = 100; moreAvailable = true; loadMore();
          return moreRequestSize;
        """)
        self.assertEqual(got, 20)

    def test_append_adds_new_rows_skips_duplicates_and_stops_when_exhausted(self):
        got = run_js(f"""
          replaceArticles({self.ITEMS % (50, 0)}, 50);
          loadMore();
          const page = {self.ITEMS % (50, 45)};   // overlaps the last 5 rows
          appendMore(JSON.stringify({{ items: page }}));
          const afterFirst = [articles.length, loadedCount, moreAvailable, loadingMore];
          moreRequestSize = 50; moreRequestGeneration = pagesGeneration; loadingMore = true;
          appendMore(JSON.stringify({{ items: [] }}));
          return [afterFirst, moreAvailable];
        """)
        self.assertEqual(got, [[95, 100, True, False], False])

    def test_stale_page_is_ignored_after_the_list_is_replaced(self):
        got = run_js(f"""
          replaceArticles({self.ITEMS % (50, 0)}, 50);
          loadMore();
          replaceArticles({self.ITEMS % (50, 1000)}, 50);   // e.g. a refresh landed
          appendMore(JSON.stringify({{ items: {self.ITEMS % (50, 50)} }}));
          return [articles.length, loadedCount, articles[0].id, loadingMore];
        """)
        self.assertEqual(got, [50, 50, "a1000", False])

    def test_bad_payload_disables_paging_instead_of_looping(self):
        got = run_js(f"""
          replaceArticles({self.ITEMS % (50, 0)}, 50);
          loadMore(); appendMore('not json');
          return [moreAvailable, articles.length];
        """)
        self.assertEqual(got, [False, 50])

    def test_no_paging_while_busy_or_in_settings_or_detail(self):
        got = run_js(f"""
          replaceArticles({self.ITEMS % (50, 0)}, 50);
          const blocked = [];
          for (const key of ['loadingMore', 'loading', 'settingsOpen', 'detailOpen']) {{
            root[key] = true; moreProcess.command = null; loadMore();
            blocked.push(moreProcess.command === null); root[key] = false;
          }}
          listProcess.running = true; loadMore(); blocked.push(moreProcess.command === null);
          return blocked;
        """)
        self.assertEqual(got, [True] * 5)

    def test_scrolling_near_the_bottom_triggers_a_load(self):
        got = run_js(f"""
          replaceArticles({self.ITEMS % (50, 0)}, 50);
          scrollArea.contentY = 100; maybeLoadMore(); const far = moreProcess.command;
          scrollArea.contentY = 500; maybeLoadMore(); const near = moreProcess.command;
          return [far === null, near !== null];
        """)
        self.assertEqual(got, [True, True])

    def test_panel_wires_scrolling_and_the_process_up(self):
        self.assertIn("onContentYChanged: root.maybeLoadMore()", PANEL)
        self.assertIn("id: moreProcess", PANEL)
        self.assertIn("onStreamFinished: root.appendMore(text)", PANEL)


@unittest.skipUnless(shutil.which("node"), "node is required to run Panel.qml logic")
class DelightTests(unittest.TestCase):
    def test_caught_up_message_rotates_daily_and_survives_bad_input(self):
        got = run_js("return [0, 1, 5, 6, -1, 'x', undefined, 2, 3, 4].map(caughtUpMessage);")
        self.assertEqual(got[0], "All caught up")
        self.assertNotEqual(got[0], got[1])
        self.assertEqual(got[6], got[0])        # garbage falls back to the first line
        self.assertEqual(got[4], got[1])        # negative days still land on a line
        self.assertEqual(len(set(got)), 7)      # 0,1,2,3,4,5,6 are all different lines

    def test_spinner_animates_and_verbs_rotate(self):
        got = run_js("""
          const glyphs = Array.from({length: 8}, (_, i) => spinnerGlyph(i));
          const labels = [0, 13, 14, 28].map((t) => loadingLabel(t, 0));
          return { glyphs, wraps: spinnerGlyph(8) === glyphs[0], labels,
                   seeded: loadingLabel(0, 3), junk: loadingLabel(undefined, 'x') };
        """)
        self.assertGreaterEqual(len(set(got["glyphs"])), 4)
        self.assertTrue(got["wraps"])
        self.assertTrue(got["labels"][0].endswith("Fetching…"))
        self.assertTrue(got["labels"][1].endswith("Fetching…"))       # same verb within a step
        self.assertTrue(got["labels"][2].endswith("Pondering…"))      # next verb after 14 ticks
        self.assertTrue(got["labels"][3].endswith("Simmering…"))
        self.assertTrue(got["seeded"].endswith("Herding feeds…"))
        self.assertTrue(got["junk"].endswith("Fetching…"))
        for label in got["labels"] + [got["seeded"]]:
            self.assertTrue(label.isprintable())

    def test_phrase_pools_are_big_unique_and_plain_text(self):
        pools = run_js("return Object.fromEntries(['loading','caughtUp','refreshed','empty'].map((k) => [k, phrasePool(k)]));")
        self.assertGreaterEqual(len(pools["loading"]), 30)
        self.assertGreaterEqual(len(pools["caughtUp"]), 10)
        self.assertGreaterEqual(len(pools["refreshed"]), 6)
        for kind, lines in pools.items():
            self.assertEqual(len(lines), len(set(lines)), f"duplicate line in {kind}")
            for line in lines:
                self.assertTrue(line.strip() and line.isprintable(), f"{kind}: {line!r}")
                self.assertNotRegex(line, r"[\U0001F300-\U0001FAFF]", f"{kind}: color emoji in {line!r}")
        self.assertEqual(run_js("return [phrasePool('nope'), pickPhrase('nope', 3)];"), [[], ""])

    def test_refreshed_label_keeps_the_time_and_varies_with_the_seed(self):
        got = run_js("return [0, 1, 2, 'x', -3].map((n) => refreshedLabel('14:02', n));")
        self.assertTrue(all(line.endswith(" 14:02") for line in got))
        self.assertEqual(len(set(got[:3])), 3)

    def test_list_footer_says_what_is_happening(self):
        got = run_js("""
          const out = [];
          moreAvailable = true; out.push(listFooterText(50));
          loadingMore = true; spinnerTick = 1; out.push(listFooterText(50));
          loadingMore = false; moreAvailable = false; out.push(listFooterText(1), listFooterText(7));
          return out;
        """)
        self.assertEqual(got, ["Scroll for more", "✢ Loading more…", "1 article · that's everything",
                               "7 articles · that's everything"])

    def test_emptying_the_feed_limit_field_keeps_the_draft(self):
        got = run_js("""
          maxFeedsDraft = 12; setMaxFeeds(''); const a = maxFeedsDraft;
          setMaxFeeds('  '); const b = maxFeedsDraft;
          setMaxFeeds('30'); const c = maxFeedsDraft;
          return [a, b, c];
        """)
        self.assertEqual(got, [12, 12, 30])


if __name__ == "__main__":
    unittest.main()

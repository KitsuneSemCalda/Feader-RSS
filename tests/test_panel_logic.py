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
    "normalizeMaxFeeds", "sanitizeFeeds", "addFeed", "setMaxFeeds",
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

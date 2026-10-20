import contextlib
import io
import json
import os
import re
import stat
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

import nalmeok as app


def write_state(folder, batch_id="batches/1", names=("01.m4a",)):
    files = []
    for index, name in enumerate(names, 1):
        path = Path(folder) / name
        if not path.exists():
            path.write_bytes(b"audio")
        files.append(app.snapshot(path, f"audio_{index:03d}"))
    app.save(app.state_path(folder), {
        "model": app.DEFAULT_MODEL,
        "files": files,
        "pdf": None,
        "batch_id": batch_id,
    })


def response_row(key, text="hello"):
    return {
        "key": key,
        "response": {
            "candidates": [{
                "finishReason": "STOP",
                "content": {"parts": [{"text": text}]},
            }],
        },
    }


class PriceTests(unittest.TestCase):
    def test_batch_multiplier(self):
        self.assertAlmostEqual(
            app.price(1_000_000, 0, 0, app.DEFAULT_MODEL, True), 0.375)
        self.assertAlmostEqual(
            app.price(1_000_000, 0, 0, app.DEFAULT_MODEL, False), 0.75)

    def test_duration_and_money(self):
        self.assertEqual(app.duration_text(59), "1분")
        self.assertEqual(app.duration_text(3600), "1시간 0분")
        self.assertEqual(app.clock_text(65), "01:05")
        self.assertIn("원", app.money(0.05))


class DisplayTests(unittest.TestCase):
    def test_display_width_handles_korean_and_ansi(self):
        self.assertEqual(app.display_width("abc"), 3)
        self.assertEqual(app.display_width("한글"), 4)
        self.assertEqual(app.display_width("\033[36m한글\033[0m"), 4)

    def test_clip(self):
        self.assertEqual(app.clip("abc", 10), "abc")
        self.assertTrue(app.clip("가나다라마", 5).endswith("…"))

    def test_natural_key(self):
        names = ["10.m4a", "2.m4a", "1.m4a"]
        self.assertEqual(sorted(names, key=app.natural_key),
                         ["1.m4a", "2.m4a", "10.m4a"])

    def test_human_size(self):
        self.assertEqual(app.human_size(0), "0 B")
        self.assertEqual(app.human_size(2048), "2.0 KB")

    def test_pulse_bar_fits_width(self):
        bar = app.pulse_bar(14, 3)
        self.assertEqual(app.display_width(bar), 14)
        self.assertIn("█", bar)
        self.assertIn("░", bar)

    def test_refresh_layout_clamps_to_terminal(self):
        with mock.patch.object(app, "term_size",
                               return_value=os.terminal_size((200, 40))):
            app.refresh_layout()
            self.assertLessEqual(app.WIDTH, app.MAX_WIDTH)
        with mock.patch.object(app, "term_size",
                               return_value=os.terminal_size((30, 40))):
            app.refresh_layout()
            self.assertGreaterEqual(app.WIDTH, app.MIN_WIDTH)
        app.refresh_layout()

    def test_menu_has_no_box_drawing(self):
        entries = [("a", "항목", "설명", False), ("b", "다른 항목", "", True)]
        captured = io.StringIO()
        with contextlib.redirect_stdout(captured):
            app.draw_menu(entries, [0, 1], 0, "Enter 선택")
        text = captured.getvalue()
        self.assertNotIn("╭", text)
        self.assertNotIn("│", text)
        self.assertIn("❯", text)
        self.assertIn("Enter 선택", text)

    def test_multi_menu_marks_selection(self):
        entries = [("a", "항목", "설명", False), ("b", "다른 항목", "설명", False)]
        captured = io.StringIO()
        with contextlib.redirect_stdout(captured):
            app.draw_multi(entries, [0, 1], 1, {0}, "Enter 확정")
        text = captured.getvalue()
        self.assertIn("●", text)
        self.assertIn("○", text)


class PathTests(unittest.TestCase):
    def test_clean_path_unquotes_and_unescapes(self):
        self.assertEqual(str(app.clean_path('"/tmp/my lecture"')),
                         "/tmp/my lecture")
        self.assertEqual(str(app.clean_path("'/tmp/my\\ lecture'")),
                         "/tmp/my lecture")

    def test_path_target(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            audio = folder / "01.m4a"
            audio.write_bytes(b"x")
            self.assertEqual(app.path_target(str(audio)), folder.resolve())
            self.assertEqual(app.path_target(str(folder)), folder.resolve())
            self.assertIsNone(app.path_target(str(folder / "missing")))

    def test_output_path(self):
        folder = Path("/tmp/lecture")
        self.assertEqual(app.output_path(folder), folder / "lecture.md")


class ConfigTests(unittest.TestCase):
    def test_recent_folders_order_dedupe_and_limit(self):
        with tempfile.TemporaryDirectory() as temp:
            base = Path(temp)
            env = {"XDG_CONFIG_HOME": str(base / "config")}
            with mock.patch.dict(os.environ, env, clear=False):
                for index in range(10):
                    folder = base / f"job{index}"
                    folder.mkdir()
                    app.remember_folder(folder)
                app.remember_folder(base / "job9")
                recent = app.recent_folders()
                self.assertEqual(len(recent), app.RECENT_LIMIT)
                self.assertEqual(recent[0], (base / "job9").resolve())
                self.assertEqual(len({p for p in recent}), len(recent))

    def test_config_is_owner_only(self):
        if os.name == "nt":
            self.skipTest("POSIX permission check")
        with tempfile.TemporaryDirectory() as temp:
            env = {"XDG_CONFIG_HOME": temp}
            with mock.patch.dict(os.environ, env, clear=False):
                app.remember_key("secret")
                mode = stat.S_IMODE(app.config_path().stat().st_mode)
                self.assertEqual(mode, 0o600)
                self.assertEqual(app.saved_key(), "secret")
                app.forget_key()
                self.assertIsNone(app.saved_key())


class AtomicWriteTests(unittest.TestCase):
    def test_write_is_atomic_and_cleans_temp(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "file.json"
            app.atomic_write_text(path, '{"a": 1}', mode=0o600)
            self.assertEqual(path.read_text(encoding="utf-8"), '{"a": 1}')
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            leftovers = [p for p in Path(temp).iterdir() if p != path]
            self.assertEqual(leftovers, [])


class StateTests(unittest.TestCase):
    def test_empty_ready_and_invalid(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            self.assertEqual(app.inspect_folder(folder)["stage"], "empty")
            (folder / "01.m4a").write_bytes(b"a")
            (folder / "참고.pdf").write_bytes(b"p")
            facts = app.inspect_folder(folder)
            self.assertEqual(facts["stage"], "ready")
            self.assertEqual(facts["m4a"], 1)
            self.assertEqual(facts["pdf"], 1)
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            for index in range(app.MAX_FILES + 1):
                (folder / f"{index:02d}.m4a").write_bytes(b"a")
            facts = app.inspect_folder(folder)
            self.assertEqual(facts["stage"], "invalid")
            self.assertIn(str(app.MAX_FILES), facts["reason"])

    def test_prepared_submitted_received_done(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            write_state(folder, batch_id=None)
            self.assertEqual(app.inspect_folder(folder)["stage"], "prepared")
            write_state(folder)
            self.assertEqual(app.inspect_folder(folder)["stage"], "submitted")
            app.atomic_write_text(folder / app.RESULT_FILE, "{}\n")
            self.assertEqual(app.inspect_folder(folder)["stage"], "received")
            app.atomic_write_text(app.output_path(folder), "done\n")
            self.assertEqual(app.inspect_folder(folder)["stage"], "done")

    def test_corrupt_state(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            (folder / "01.m4a").write_bytes(b"a")
            app.atomic_write_text(app.state_path(folder), "{not json")
            facts = app.inspect_folder(folder)
            self.assertEqual(facts["stage"], "corrupt")
            self.assertTrue(facts["job"])

    def test_non_object_state_is_corrupt(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            (folder / "01.m4a").write_bytes(b"a")
            app.atomic_write_text(app.state_path(folder), "[]")
            self.assertEqual(app.inspect_folder(folder)["stage"], "corrupt")

    def test_non_object_recovery_is_ignored(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            write_state(folder)
            app.atomic_write_text(folder / app.RECOVERY_FILE, "[]")
            facts = app.inspect_folder(folder)
            self.assertEqual(facts["stage"], "submitted")
            self.assertFalse(facts["pending_retry"])

    def test_prepared_changed_inputs(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            write_state(folder, batch_id=None)
            self.assertEqual(app.inspect_folder(folder)["stage"], "prepared")
            (folder / "01.m4a").write_bytes(b"changed audio")
            self.assertEqual(app.inspect_folder(folder)["stage"], "changed")

    def test_changed_inputs(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            write_state(folder)
            facts = app.inspect_folder(folder)
            self.assertEqual(facts["stage"], "submitted")
            (folder / "01.m4a").write_bytes(b"different audio")
            facts = app.inspect_folder(folder)
            self.assertEqual(facts["stage"], "changed")
            self.assertTrue(facts["reason"])

    def test_watching_uses_pid_file(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            write_state(folder)
            app.watch_pid_path(folder).write_text(str(os.getpid()),
                                                  encoding="utf-8")
            app.save(folder / app.WATCH_STATUS, {
                "label": "전사 중", "elapsed": 65, "updated": 0})
            try:
                facts = app.inspect_folder(folder)
                self.assertEqual(facts["stage"], "watching")
                self.assertIn("01:05", app.stage_label(facts))
            finally:
                app.watch_pid_path(folder).unlink()

    def test_missing_folder(self):
        self.assertEqual(
            app.inspect_folder("/tmp/definitely-not-a-real-folder-xyz")["stage"],
            "missing")

    def test_pending_retry_stays_submitted(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            write_state(folder)
            app.save(folder / app.RECOVERY_FILE, {
                "batch_id": "batches/1",
                "pending_batch": {"batch_id": "batches/retry", "keys": []},
            })
            app.atomic_write_text(folder / app.RESULT_FILE, "{}\n")
            facts = app.inspect_folder(folder)
            self.assertEqual(facts["stage"], "submitted")
            self.assertTrue(facts["pending_retry"])
            self.assertIn("재시도", app.stage_label(facts))

    def test_watcher_status_reset_clears_old_fields(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            app.save(folder / app.WATCH_STATUS, {"error": "old", "elapsed": 10})
            app.write_watch_status(folder, reset=True, state="DONE", label="완료")
            status = app.read_watch_status(folder)
            self.assertEqual(status["state"], "DONE")
            self.assertNotIn("error", status)
            self.assertNotIn("elapsed", status)

    def test_start_watcher_refuses_duplicate(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            write_state(folder)
            app.watch_pid_path(folder).write_text(str(os.getpid()), encoding="utf-8")
            try:
                ok, message = app.start_watcher(folder)
                self.assertFalse(ok)
                self.assertIn("이미", message)
            finally:
                app.watch_pid_path(folder).unlink()

    def test_start_watcher_requires_key(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            write_state(folder)
            env = {"XDG_CONFIG_HOME": str(Path(temp) / "config"),
                   "GEMINI_API_KEY": ""}
            with mock.patch.dict(os.environ, env, clear=False):
                ok, message = app.start_watcher(folder)
            self.assertFalse(ok)
            self.assertIn("API 키", message)
            self.assertFalse(app.watch_pid_path(folder).exists())

    def test_stop_watcher_cleans_dead_pid(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            write_state(folder)
            app.watch_pid_path(folder).write_text("999999", encoding="utf-8")
            app.write_watch_status(folder, state="JOB_STATE_RUNNING", label="전사 중")
            self.assertFalse(app.stop_watcher(folder))
            self.assertFalse(app.watch_pid_path(folder).exists())
            self.assertEqual(app.read_watch_status(folder)["state"], "STOPPED")

    def test_estimate_files_dedupes_mp4_with_m4a(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            for name in ("01.mp4", "01.m4a", "02.mp4"):
                (folder / name).write_bytes(b"x")
            names = [p.name for p in app.estimate_files(folder)]
            self.assertEqual(names, ["01.m4a", "02.mp4"])


class MergeTests(unittest.TestCase):
    def setUp(self):
        self.files = [
            {"key": "audio_001"},
            {"key": "audio_002"},
        ]

    def test_merge_orders_and_joins(self):
        raw = "\n".join(json.dumps(row) for row in (
            response_row("audio_002", "둘"),
            response_row("audio_001", "하나"),
        ))
        merged = app.merge_jsonl(raw, self.files)
        self.assertEqual(merged, "하나\n\n둘\n")

    def test_duplicate_key_fails(self):
        raw = "\n".join(json.dumps(row) for row in (
            response_row("audio_001"),
            response_row("audio_001"),
        ))
        with self.assertRaises(RuntimeError):
            app.merge_jsonl(raw, self.files)

    def test_unknown_and_missing_keys_fail(self):
        with self.assertRaises(RuntimeError):
            app.merge_jsonl(json.dumps(response_row("nope")), self.files)
        with self.assertRaises(RuntimeError):
            app.merge_jsonl(json.dumps(response_row("audio_001")), self.files)

    def test_truncated_and_empty_responses_fail(self):
        truncated = response_row("audio_001")
        truncated["response"]["candidates"][0]["finishReason"] = "MAX_TOKENS"
        with self.assertRaises(RuntimeError):
            app.merge_jsonl(json.dumps(truncated), self.files)
        empty = response_row("audio_001", "   ")
        with self.assertRaises(RuntimeError):
            app.merge_jsonl(json.dumps(empty), self.files)

    def test_result_rows(self):
        raw = json.dumps(response_row("audio_001"))
        rows = app.result_rows(raw, {"audio_001", "audio_002"})
        self.assertEqual(set(rows), {"audio_001"})


class UsageTests(unittest.TestCase):
    def test_corrupt_result_is_skipped(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            write_state(folder)
            app.atomic_write_text(folder / app.RESULT_FILE, "{broken\n")
            self.assertEqual(app.response_rows(folder / app.RESULT_FILE), [])
            self.assertIsNone(app.usage_entry(folder))

    def test_usage_entry_reads_result_tokens(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            write_state(folder)
            row = {
                "key": "audio_001",
                "response": {
                    "usageMetadata": {
                        "promptTokensDetails": [
                            {"modality": "AUDIO", "tokenCount": 90000},
                            {"modality": "TEXT", "tokenCount": 1000},
                        ],
                        "candidatesTokenCount": 10000,
                    },
                },
            }
            app.atomic_write_text(folder / app.RESULT_FILE,
                                  json.dumps(row) + "\n")
            entry = app.usage_entry(folder)
            self.assertEqual(entry["audio"], 90000)
            self.assertEqual(entry["seconds"], 3600)
            self.assertAlmostEqual(entry["cost"], 0.052875, places=6)
            lines = app.stats_lines_from([entry], root=folder.parent)
            self.assertIn("작업 1개", lines[0])

    def test_wait_target_prefers_pending_retry(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            write_state(folder, batch_id="batches/main")
            app.save(folder / app.RECOVERY_FILE, {
                "batch_id": "batches/main",
                "pending_batch": {"batch_id": "batches/retry", "keys": []},
            })
            job = app.load(folder)
            self.assertEqual(app.wait_target(folder, job), "batches/retry")
            app.save(folder / app.RECOVERY_FILE, {
                "batch_id": "batches/stale",
                "pending_batch": {"batch_id": "batches/other", "keys": []},
            })
            self.assertEqual(app.wait_target(folder, job), "batches/main")


class PackagingTests(unittest.TestCase):
    def test_version_matches_pyproject(self):
        text = (Path(__file__).resolve().parent.parent / "pyproject.toml").read_text(
            encoding="utf-8")
        match = re.search(r'^version = "([^"]+)"', text, re.MULTILINE)
        self.assertIsNotNone(match)
        self.assertEqual(match.group(1), app.__version__)


if __name__ == "__main__":
    unittest.main()

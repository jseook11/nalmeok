import contextlib
import io
import json
import os
import re
import stat
import sys
import tempfile
import unicodedata
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
        filled, empty = ("█", "░") if app.unicode_ok() else ("#", "-")
        self.assertIn(filled, bar)
        self.assertIn(empty, bar)

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
        self.assertIn(app.pointer(), text)
        self.assertIn("Enter 선택", text)

    def test_multi_menu_marks_selection(self):
        entries = [("a", "항목", "설명", False), ("b", "다른 항목", "설명", False)]
        captured = io.StringIO()
        with contextlib.redirect_stdout(captured):
            app.draw_multi(entries, [0, 1], 1, {0}, "Enter 확정")
        text = captured.getvalue()
        self.assertIn("●", text)
        self.assertIn("○", text)

    def test_menu_cursor_visible_on_faded_row(self):
        entries = [app.normalize(("a", "항목", "", True))]
        captured = io.StringIO()
        with contextlib.redirect_stdout(captured):
            app.draw_menu(entries, [0], 0, "Enter 선택")
        self.assertIn(app.pointer(), captured.getvalue())

    def test_display_width_normalizes_decomposed_hangul(self):
        nfd = unicodedata.normalize("NFD", "강의녹음/")
        self.assertEqual(app.display_width(nfd), 9)
        self.assertEqual(app.clip(nfd, 20), "강의녹음/")


class PathTests(unittest.TestCase):
    def test_clean_path_unquotes_and_unescapes(self):
        if os.name == "nt":
            self.assertEqual(str(app.clean_path('"C:\\Users\\me\\my lecture"')),
                             "C:\\Users\\me\\my lecture")
            self.assertEqual(str(app.clean_path("'C:\\Users\\me\\my lecture'")),
                             "C:\\Users\\me\\my lecture")
            return
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


class FolderBrowseTests(unittest.TestCase):
    def test_menu_move_skips_faded_entries(self):
        entries = [app.normalize(option) for option in (
            ("a", "A"), ("b", "B", "", True, True), ("c", "C"))]
        self.assertEqual(app._menu_move(entries, 0, 1), 2)
        self.assertEqual(app._menu_move(entries, 2, -1), 0)

    def test_menu_move_keeps_position_when_all_are_skipped(self):
        entries = [app.normalize(("a", "A", "", True, True))]
        self.assertEqual(app._menu_move(entries, 0, 1), 0)

    def test_scan_descendant_media_counts_nested_and_ignores_hidden(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "a" / "b").mkdir(parents=True)
            (root / ".hidden").mkdir()
            (root / ".hidden" / "01.m4a").write_bytes(b"x")
            self.assertEqual(app.scan_descendant_media(root), (0, 0, False, True))
            (root / "a" / "02.mp4").write_bytes(b"x")
            (root / "a" / "03.m4a").write_bytes(b"x")
            self.assertEqual(app.scan_descendant_media(root), (1, 1, False, True))

    def test_scan_descendant_media_counts_job_state(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            nested = root / "a" / "b"
            nested.mkdir(parents=True)
            self.assertEqual(app.scan_descendant_media(root), (0, 0, False, True))
            app.save(app.state_path(nested), {
                "model": app.DEFAULT_MODEL,
                "files": [],
                "pdf": None,
                "batch_id": None,
            })
            self.assertEqual(app.scan_descendant_media(root), (0, 0, True, True))

    def test_scan_descendant_media_gives_up_within_time_limit(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "a").mkdir()
            clock = mock.Mock(side_effect=[0.0, 0.0, 0.2])
            with mock.patch.object(app.time, "monotonic", clock):
                self.assertEqual(app.scan_descendant_media(root, seconds=0.1),
                                 (0, 0, False, False))

    def test_scan_descendant_media_does_not_follow_symlink_loops(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp) / "loop"
            root.mkdir()
            (root / "self").symlink_to(root)
            self.assertEqual(app.scan_descendant_media(root), (0, 0, False, True))

    def test_scan_descendant_media_respects_depth_and_dir_limits(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            deep = root
            for index in range(5):
                deep = deep / f"d{index}"
                deep.mkdir()
            (deep / "01.m4a").write_bytes(b"x")
            self.assertEqual(
                app.scan_descendant_media(root, seconds=1, max_depth=2),
                (0, 0, False, False))
            self.assertEqual(
                app.scan_descendant_media(root, seconds=1, max_dirs=1),
                (0, 0, False, False))
            self.assertEqual(
                app.scan_descendant_media(root, seconds=1, max_depth=9,
                                          max_dirs=100),
                (1, 0, False, True))

    def test_job_folders_respects_depth_limit(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            deep = root / "a" / "b" / "c"
            deep.mkdir(parents=True)
            (deep / app.STATE_FILE).write_text("{}", encoding="utf-8")
            self.assertEqual(app.job_folders(root, max_depth=1), [])
            self.assertEqual(app.job_folders(root, max_depth=3), [deep])

    def test_inspect_folder_light_reads_media_and_job(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            (folder / "01.m4a").write_bytes(b"x")
            facts = app.inspect_folder(folder, light=True)
            self.assertEqual(facts["media"], 1)
            self.assertEqual(facts["stage"], "ready")

    def test_update_lines_rewrites_only_changed_rows(self):
        previous = ["  a", "  b", "  c"]
        current = ["  a", "> b", "  c"]
        captured = io.StringIO()
        with contextlib.redirect_stdout(captured):
            updated = app.update_lines(previous, current, 3)
        self.assertTrue(updated)
        output = captured.getvalue()
        self.assertIn("\033[3A", output)
        self.assertIn("> b", output)
        self.assertNotIn("  a", output)
        self.assertNotIn("  c", output)

    def test_update_lines_falls_back_on_wrapping(self):
        self.assertFalse(app.update_lines(["a"], ["b"], 2))

    def test_menu_window_keeps_cursor_visible(self):
        rows = list(range(10))
        visible, start, total = app._menu_window(rows, 7, 4)
        self.assertEqual(total, 10)
        self.assertEqual(len(visible), 4)
        self.assertIn(7, visible)
        self.assertGreaterEqual(start, 0)

    def test_long_menu_does_not_clear_on_every_key(self):
        options = [(str(index), f"item{index}") for index in range(40)]
        keys = iter(["down"] * 10 + ["esc"])

        def fake_key():
            return next(keys)

        with mock.patch.object(app, "can_raw_input", return_value=True), \
                mock.patch.object(app, "vt_supported", return_value=True), \
                mock.patch.object(app, "read_key", side_effect=fake_key), \
                mock.patch.object(app.shutil, "get_terminal_size",
                                  return_value=os.terminal_size((80, 24))):
            captured = io.StringIO()
            with contextlib.redirect_stdout(captured):
                answer = app.select_menu("test", options, allow_back=True)
        self.assertIs(answer, app.BACK)
        self.assertEqual(captured.getvalue().count("\033[2J"), 1)

    def test_browse_shows_nested_media_counts(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "topic" / "week1").mkdir(parents=True)
            (root / "topic" / "week1" / "01.m4a").write_bytes(b"x")
            (root / "topic" / "week1" / "02.mp4").write_bytes(b"x")
            captured = {}

            def fake_menu(title, options, info=(), **kwargs):
                captured["options"] = options
                return app.BACK

            with mock.patch.object(app, "select_menu", fake_menu):
                app.browse_folder(root)
        desc = next(option[2] for option in captured["options"]
                    if option[1] == "topic/")
        self.assertEqual(desc, "하위 오디오 1개 · 하위 MP4 1개")

    @unittest.skipIf(os.name == "nt" or getattr(os, "geteuid", lambda: 1)() == 0,
                     "권한 없는 폴더 테스트는 일반 사용자에서만 동작합니다")
    def test_inspect_folder_survives_permission_denied(self):
        with tempfile.TemporaryDirectory() as temp:
            locked = Path(temp) / "locked"
            locked.mkdir()
            locked.chmod(0o000)
            try:
                facts = app.inspect_folder(locked)
            finally:
                locked.chmod(0o700)
        self.assertFalse(facts["output"])
        self.assertFalse(facts["job"])
        self.assertEqual(facts["stage"], "empty")

    @unittest.skipUnless(app.RAW_KEYS, "터미널 입력을 지원하는 플랫폼에서만 동작합니다")
    def test_menu_left_returns_up_when_left_up(self):
        with mock.patch.object(app, "can_raw_input", return_value=True), \
                mock.patch.object(app, "vt_supported", return_value=True), \
                mock.patch.object(app, "read_key", return_value="left"), \
                contextlib.redirect_stdout(io.StringIO()):
            self.assertIs(
                app.select_menu("폴더", [("a", "A")], allow_back=True,
                                left_up=True), app.UP)
            self.assertIs(
                app.select_menu("폴더", [("a", "A")], allow_back=True), app.BACK)

    def test_browse_left_arrow_moves_to_parent(self):
        with tempfile.TemporaryDirectory() as temp:
            child = Path(temp) / "child"
            child.mkdir()
            seen = []
            answers = iter([app.UP, app.BACK])

            def fake_menu(title, options, info=(), **kwargs):
                seen.append(next(value for label, value in info if label == "위치"))
                return next(answers)

            with mock.patch.object(app, "select_menu", fake_menu):
                self.assertIsNone(app.browse_folder(child))
        self.assertEqual(seen, [str(child.resolve()), str(Path(temp).resolve())])

    @unittest.skipIf(os.name == "nt" or getattr(os, "geteuid", lambda: 1)() == 0,
                     "권한 없는 폴더 테스트는 일반 사용자에서만 동작합니다")
    def test_browse_keeps_navigation_when_folder_is_unreadable(self):
        with tempfile.TemporaryDirectory() as temp:
            locked = Path(temp) / "locked"
            locked.mkdir()
            locked.chmod(0o000)
            captured = {}

            def fake_menu(title, options, info=(), **kwargs):
                captured["options"] = options
                captured["info"] = info
                return app.BACK

            try:
                with mock.patch.object(app, "select_menu", fake_menu):
                    self.assertIsNone(app.browse_folder(locked))
            finally:
                locked.chmod(0o700)
        labels = [option[1] for option in captured["options"]]
        self.assertIn(".. 상위 폴더", labels)
        self.assertIn("경로 직접 입력", labels)
        self.assertTrue(any("읽지 못했습니다" in value
                            for _, value in captured["info"]))


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
            if os.name != "nt":
                self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            leftovers = [p for p in Path(temp).iterdir() if p != path]
            self.assertEqual(leftovers, [])

    def test_write_retries_permission_error(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "file.json"
            real_replace = os.replace
            calls = {"count": 0}

            def flaky_replace(source, target):
                calls["count"] += 1
                if calls["count"] < 3:
                    raise PermissionError("locked")
                return real_replace(source, target)

            with mock.patch.object(app.os, "replace", side_effect=flaky_replace):
                app.atomic_write_text(path, "ok")
            self.assertEqual(path.read_text(encoding="utf-8"), "ok")
            self.assertEqual(calls["count"], 3)
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


class LiveGroupTests(unittest.TestCase):
    def entries(self, count, minutes=20):
        return [{"key": f"audio_{index:03d}", "seconds": minutes * 60}
                for index in range(1, count + 1)]

    def test_seven_twenty_minute_files_make_two_balanced_groups(self):
        groups = app.plan_live_groups(self.entries(7), 130 * 60)
        self.assertEqual(len(groups), 2)
        self.assertEqual(sorted(group["seconds"] for group in groups),
                         [3600.0, 4800.0])
        self.assertEqual([key for group in groups for key in group["files"]],
                         [f"audio_{index:03d}" for index in range(1, 8)])

    def test_six_files_fit_one_group(self):
        groups = app.plan_live_groups(self.entries(6), 130 * 60)
        self.assertEqual(len(groups), 1)
        self.assertEqual(groups[0]["seconds"], 7200.0)

    def test_eight_files_make_two_even_groups(self):
        groups = app.plan_live_groups(self.entries(8), 130 * 60)
        self.assertEqual([group["seconds"] for group in groups],
                         [4800.0, 4800.0])

    def test_ten_files_make_two_even_groups(self):
        groups = app.plan_live_groups(self.entries(10), 130 * 60)
        self.assertEqual([group["seconds"] for group in groups],
                         [6000.0, 6000.0])

    def test_cap_is_respected(self):
        entries = [
            {"key": "a", "seconds": 130 * 60},
            {"key": "b", "seconds": 130 * 60},
            {"key": "c", "seconds": 100 * 60},
        ]
        groups = app.plan_live_groups(entries, 130 * 60)
        self.assertEqual(len(groups), 3)
        self.assertTrue(all(group["seconds"] <= 130 * 60 for group in groups))


class LiveStateTests(unittest.TestCase):
    def test_live_stages_progress_to_done(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            (folder / "01.m4a").write_bytes(b"audio")
            app.save(app.live_state_path(folder), {
                "mode": "live",
                "model": app.DEFAULT_MODEL,
                "files": [],
                "groups": [
                    {"key": "group_001", "files": [], "seconds": 60},
                    {"key": "group_002", "files": [], "seconds": 60},
                ],
            })
            app.atomic_write_text(
                app.live_result_path(folder),
                json.dumps({"key": "group_001", "response": {}}) + "\n")
            facts = app.inspect_folder(folder)
            self.assertEqual(facts["stage"], "live")
            self.assertEqual(facts["live_completed"], 1)

            app.atomic_write_text(
                app.live_result_path(folder),
                json.dumps({"key": "group_001", "response": {}}) + "\n"
                + json.dumps({"key": "group_002", "response": {}}) + "\n")
            facts = app.inspect_folder(folder)
            self.assertEqual(facts["stage"], "received")
            self.assertTrue(facts["live_result"])

            app.atomic_write_text(app.output_path(folder), "done\n")
            self.assertEqual(app.inspect_folder(folder)["stage"], "done")

    def test_live_usage_is_standard_price(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            app.save(app.live_state_path(folder), {
                "mode": "live", "model": app.DEFAULT_MODEL,
                "files": [], "groups": [],
            })
            row = {
                "key": "group_001",
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
            app.atomic_write_text(app.live_result_path(folder),
                                  json.dumps(row) + "\n")
            entry = app.usage_entry(folder)
            self.assertTrue(entry["live"])
            self.assertAlmostEqual(entry["cost"], 0.10575, places=6)
            lines = app.stats_lines_from([entry], root=folder.parent)
            self.assertTrue(any("live" in line for line in lines))

    def test_archive_moves_live_state_and_segments(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            (folder / "01.m4a").write_bytes(b"audio")
            app.save(app.live_state_path(folder), {"mode": "live"})
            app.atomic_write_text(app.live_result_path(folder), "{}\n")
            app.atomic_write_text(app.output_path(folder), "done\n")
            segment = app.live_segment_dir(folder)
            segment.mkdir()
            (segment / "part001.m4a").write_bytes(b"part")
            destination = app.archive_job(folder)
            self.assertIsNotNone(destination)
            self.assertTrue((destination / app.LIVE_STATE_FILE).exists())
            self.assertTrue((destination / app.LIVE_RESULT_FILE).exists())
            self.assertTrue((destination / app.LIVE_SEGMENT_DIR).is_dir())
            self.assertFalse(app.live_state_path(folder).exists())
            self.assertTrue((folder / "01.m4a").exists())


class LiveGapTests(unittest.TestCase):
    def test_short_group_uses_rpm_floor(self):
        self.assertEqual(app.live_gap_seconds(20 * 60, 12), 12)

    def test_medium_group_uses_tpm_window(self):
        self.assertAlmostEqual(app.live_gap_seconds(60 * 60, 12), 30)

    def test_long_group_waits_a_full_minute(self):
        self.assertAlmostEqual(app.live_gap_seconds(130 * 60, 12), 60)

    def test_user_can_choose_a_longer_floor(self):
        self.assertEqual(app.live_gap_seconds(130 * 60, 80), 80)


class LiveArgTests(unittest.TestCase):
    def test_live_defaults(self):
        args = app.parse_args(["live", "."])
        self.assertEqual(args.command, "live")
        self.assertEqual(args.gap, app.LIVE_GAP_SECONDS)
        self.assertEqual(args.chunk_minutes, 130)

    def test_live_rejects_bad_values(self):
        with self.assertRaises(SystemExit):
            app.parse_args(["live", ".", "--gap", "-1"])
        with self.assertRaises(SystemExit):
            app.parse_args(["live", ".", "--chunk-minutes", "131"])

    def test_auto_accepts_yes(self):
        args = app.parse_args(["auto", ".", "--yes"])
        self.assertEqual(args.command, "auto")
        self.assertTrue(args.yes)


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


class KeyRegistryTests(unittest.TestCase):
    def test_legacy_migration_and_free_paid_labels(self):
        with tempfile.TemporaryDirectory() as temp:
            env = {"XDG_CONFIG_HOME": str(Path(temp) / "config"),
                   "GEMINI_API_KEY": "", "GEMINI_API_KEY_TIER": ""}
            with mock.patch.dict(os.environ, env, clear=False):
                app.remember_key("legacy-secret")
                entries = app.saved_key_entries()
                self.assertEqual(len(entries), 1)
                self.assertEqual(entries[0]["tier"], "paid")
                self.assertEqual(entries[0]["label"], "기본")

                free = app.add_key_entry("free1", "free", "free-secret")
                paid = app.add_key_entry("paid1", "paid", "paid-secret")
                self.assertEqual(free["tier"], "free")
                self.assertEqual(paid["tier"], "paid")
                self.assertEqual(app.key_for_tier("free"), "free-secret")
                self.assertEqual(app.key_for_tier("paid"), "legacy-secret")
                self.assertEqual(app.paid_key_for_batch("free-secret"),
                                 "legacy-secret")
                self.assertEqual(app.key_summary(), "무료 1 · 유료 2")
                self.assertTrue(app.mask_key("paid-secret").endswith("cret"))

                self.assertTrue(app.delete_key_entry(free["id"]))
                self.assertIsNone(app.key_for_tier("free"))
                self.assertEqual(app.paid_key_for_batch("free-secret"),
                                 "legacy-secret")

    def test_free_key_is_never_used_for_batch(self):
        with tempfile.TemporaryDirectory() as temp:
            env = {"XDG_CONFIG_HOME": str(Path(temp) / "config"),
                   "GEMINI_API_KEY": "", "GEMINI_API_KEY_TIER": ""}
            with mock.patch.dict(os.environ, env, clear=False):
                app.add_key_entry("free1", "free", "free-secret")
                self.assertEqual(app.key_for_tier("free"), "free-secret")
                self.assertIsNone(app.key_for_tier("paid"))
                self.assertIsNone(app.paid_key_for_batch("free-secret"))
                self.assertIsNone(app.paid_key_for_batch())
                self.assertIsNone(app.paid_key_for_batch("unknown-secret"))
                self.assertEqual(
                    app.paid_key_for_batch("unknown-secret", allow_unknown=True),
                    "unknown-secret")

    def test_same_key_updates_instead_of_duplicating(self):
        with tempfile.TemporaryDirectory() as temp:
            env = {"XDG_CONFIG_HOME": str(Path(temp) / "config"),
                   "GEMINI_API_KEY": "", "GEMINI_API_KEY_TIER": ""}
            with mock.patch.dict(os.environ, env, clear=False):
                app.add_key_entry("free1", "free", "same-secret")
                app.add_key_entry("renamed", "free", "same-secret")
                entries = app.saved_key_entries()
                self.assertEqual(len(entries), 1)
                self.assertEqual(entries[0]["label"], "renamed")


class MixedAllocationTests(unittest.TestCase):
    def keys(self, count, tier, limit=130):
        return [{"id": f"{tier}{index}", "label": f"{tier}{index}",
                 "tier": tier, "limit_minutes": limit}
                for index in range(1, count + 1)]

    def entries(self, minutes):
        return [{"key": f"u{index}", "seconds": value * 60}
                for index, value in enumerate(minutes, 1)]

    def test_canonical_300_minutes_splits_130_130_40(self):
        plan = app.allocate_mixed_entries(
            self.entries([130, 130, 40]), self.keys(2, "free"),
            self.keys(1, "paid"), 130 * 60)
        self.assertEqual([group["seconds"] for group in plan["groups"]],
                         [130 * 60, 130 * 60])
        self.assertEqual([group["seconds"] for group in plan["paid"]],
                         [40 * 60])
        self.assertFalse(plan["unassigned"])

    def test_short_work_uses_free_only(self):
        plan = app.allocate_mixed_entries(
            self.entries([60]), self.keys(2, "free"), self.keys(1, "paid"),
            130 * 60)
        self.assertEqual(len(plan["groups"]), 1)
        self.assertFalse(plan["paid"])
        self.assertEqual(plan["paid_seconds"], 0)

    def test_whole_files_are_not_split_across_free_keys(self):
        plan = app.allocate_mixed_entries(
            self.entries([100, 100, 100]), self.keys(2, "free"),
            self.keys(1, "paid"), 130 * 60)
        self.assertEqual([group["seconds"] for group in plan["groups"]],
                         [100 * 60, 100 * 60])
        self.assertEqual([group["seconds"] for group in plan["paid"]],
                         [100 * 60])

    def test_overflow_without_paid_is_marked_unassigned(self):
        plan = app.allocate_mixed_entries(
            self.entries([130, 130, 60]), self.keys(2, "free"), [],
            130 * 60)
        self.assertEqual([group["seconds"] for group in plan["groups"]],
                         [130 * 60, 130 * 60])
        self.assertEqual(plan["unassigned"], ["u3"])
        self.assertEqual(plan["paid_seconds"], 0)

    def test_paid_work_balances_across_paid_keys(self):
        plan = app.allocate_mixed_entries(
            self.entries([100, 100, 100]), [], self.keys(2, "paid"),
            130 * 60)
        self.assertFalse(plan["groups"])
        self.assertEqual(len(plan["paid"]), 2)
        self.assertEqual(plan["paid_seconds"], 300 * 60)
        self.assertEqual(sorted(group["seconds"] for group in plan["paid"]),
                         [100 * 60, 200 * 60])


class MixedPrepareTests(unittest.TestCase):
    def test_prepare_uses_whole_files_first(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            for name in ("01.m4a", "02.m4a", "03.m4a"):
                (folder / name).write_bytes(b"audio")
            free = [
                {"id": "free1", "label": "free1", "tier": "free",
                 "limit_minutes": 130},
                {"id": "free2", "label": "free2", "tier": "free",
                 "limit_minutes": 130},
            ]
            paid = [{"id": "paid1", "label": "paid1", "tier": "paid"}]
            with mock.patch.object(app, "probe_duration", return_value=6000):
                state = app.prepare_mixed(
                    folder, app.DEFAULT_MODEL, free, paid)
            self.assertEqual(state["mode"], "mixed")
            self.assertEqual([group["seconds"] for group in state["groups"]],
                             [6000, 6000])
            self.assertEqual([group["seconds"] for group in state["paid"]],
                             [6000])
            self.assertTrue(app.live_state_path(folder).exists())
            self.assertEqual(len(state["files"]), 3)

    def test_prepare_splits_long_file_at_130_minutes(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            (folder / "long.m4a").write_bytes(b"audio")

            def fake_split(path, duration, cap, segment_dir):
                self.assertEqual(cap, 130 * 60)
                segment_dir.mkdir(parents=True, exist_ok=True)
                parts = []
                for index, seconds in enumerate((130 * 60, 130 * 60, 40 * 60), 1):
                    part = segment_dir / f"{path.stem}.part{index:03d}{path.suffix}"
                    part.write_bytes(b"part")
                    parts.append((part, seconds))
                return parts

            free = [
                {"id": "free1", "label": "free1", "tier": "free",
                 "limit_minutes": 130},
                {"id": "free2", "label": "free2", "tier": "free",
                 "limit_minutes": 130},
            ]
            paid = [{"id": "paid1", "label": "paid1", "tier": "paid"}]
            with mock.patch.object(app, "probe_duration", return_value=300 * 60), \
                    mock.patch.object(app, "split_audio_for_live",
                                      side_effect=fake_split):
                state = app.prepare_mixed(
                    folder, app.DEFAULT_MODEL, free, paid)
            self.assertEqual([group["seconds"] for group in state["groups"]],
                             [130 * 60, 130 * 60])
            self.assertEqual([group["seconds"] for group in state["paid"]],
                             [40 * 60])
            self.assertEqual(len(state["files"]), 3)
            self.assertEqual(state["files"][0]["source"], "segment")
            self.assertEqual(state["files"][0]["parent"], "long.m4a")


class MixedStateTests(unittest.TestCase):
    def test_mixed_stages_include_paid_batch(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            (folder / "01.m4a").write_bytes(b"audio")
            state = {
                "mode": "mixed", "model": app.DEFAULT_MODEL,
                "files": [{"key": "u1", "name": "01.m4a", "seconds": 60},
                          {"key": "u2", "name": "01.m4a", "seconds": 60}],
                "groups": [{"key": "free_001", "key_id": "free1",
                            "label": "free1", "files": ["u1"],
                            "seconds": 60}],
                "paid": [{"key": "paid_001", "key_id": "paid1",
                          "label": "paid1", "files": ["u2"],
                          "seconds": 60, "done": False}],
            }
            app.save(app.live_state_path(folder), state)
            facts = app.inspect_folder(folder)
            self.assertEqual(facts["stage"], "live")
            self.assertTrue(facts["mixed"])
            self.assertEqual(facts["live_groups"], 1)
            self.assertEqual(facts["paid_groups"], 1)
            self.assertEqual(facts["paid_done"], 0)

            app.save_live_row(folder, state, "free_001",
                              response_row("free_001"))
            facts = app.inspect_folder(folder)
            self.assertEqual(facts["stage"], "live")
            self.assertEqual(facts["live_completed"], 1)

            state["paid"][0]["done"] = True
            app.save(app.live_state_path(folder), state)
            app.atomic_write_text(
                app.mixed_result_path(folder, state["paid"][0]),
                json.dumps(response_row("u2")) + "\n")
            facts = app.inspect_folder(folder)
            self.assertEqual(facts["stage"], "received")
            self.assertTrue(facts["live_result"])
            self.assertIn("혼합", app.stage_label(facts))

            app.atomic_write_text(app.output_path(folder), "done\n")
            self.assertEqual(app.inspect_folder(folder)["stage"], "done")

    def test_merged_text_keeps_free_then_paid_order(self):
        state = {
            "files": [{"key": "u1"}, {"key": "u2"}, {"key": "u3"}],
            "groups": [{"key": "free_001", "files": ["u1"]}],
            "paid": [{"key": "paid_001", "files": ["u2", "u3"]}],
        }
        free_rows = {"free_001": response_row("free_001", "free text")}
        paid_rows = {"u2": response_row("u2", "paid two"),
                     "u3": response_row("u3", "paid three")}
        text = app.merged_mixed_text(state, free_rows, paid_rows)
        self.assertEqual(text, "free text\n\npaid two\n\npaid three\n")


class MixedPreviewTests(unittest.TestCase):
    def test_default_preview_is_free_only_and_paid_preview_adds_batch(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            (folder / "01.m4a").write_bytes(b"audio")
            free = [{"id": "free1", "label": "free1", "tier": "free",
                     "api_key": "free-key-1", "limit_minutes": 130}]
            paid = [{"id": "paid1", "label": "paid1", "tier": "paid",
                     "api_key": "paid-key-1"}]
            with mock.patch.object(app, "key_entries_for_tier",
                                   side_effect=lambda tier: (
                                       free if tier == "free" else paid)), \
                    mock.patch.object(app, "probe_duration",
                                      return_value=200 * 60):
                free_preview = app.mixed_preview_lines(folder, paid_keys=[])
                paid_preview = app.mixed_preview_lines(folder)
            self.assertTrue(any("유료 키 필요" in line
                                for line in free_preview))
            self.assertTrue(any("유료 Batch" in line
                                for line in paid_preview))
            self.assertFalse(any("유료 키 필요" in line
                                 for line in paid_preview))


class FreeOnlyStageTests(unittest.TestCase):
    def test_free_only_mixed_state_is_not_labelled_paid(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            (folder / "01.m4a").write_bytes(b"audio")
            state = {
                "mode": "mixed", "model": app.DEFAULT_MODEL,
                "files": [{"key": "u1", "name": "01.m4a", "seconds": 60}],
                "groups": [{"key": "free_001", "key_id": "free1",
                            "label": "free1", "files": ["u1"],
                            "seconds": 60}],
                "paid": [],
            }
            app.save(app.live_state_path(folder), state)
            facts = app.inspect_folder(folder)
            self.assertTrue(facts["mixed"])
            self.assertEqual(facts["paid_groups"], 0)
            self.assertIn("무료 전사", app.stage_label(facts))
            self.assertNotIn("혼합", app.stage_label(facts))


class MixedRunTests(unittest.TestCase):
    def test_mixed_run_merges_free_and_paid_results(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            for name in ("01.m4a", "02.m4a", "03.m4a"):
                (folder / name).write_bytes(b"audio")
            free = [
                {"id": "free1", "label": "free1", "tier": "free",
                 "api_key": "free-key-1", "limit_minutes": 130},
                {"id": "free2", "label": "free2", "tier": "free",
                 "api_key": "free-key-2", "limit_minutes": 130},
            ]
            paid = [{"id": "paid1", "label": "paid1", "tier": "paid",
                     "api_key": "paid-key-1"}]

            class FakeResponse:
                def __init__(self, text):
                    self.text = text

                def model_dump(self, **kwargs):
                    return response_row("unused", self.text)["response"]

            class FakeClient:
                def close(self):
                    pass

            live_texts = iter(("free one", "free two"))

            def fake_live_generate(client, model, contents, gap, **kwargs):
                return FakeResponse(next(live_texts))

            def fake_uploads(folder, state, client, key_id, unit_keys):
                units = {entry["key"]: entry for entry in state["files"]}
                for unit_key in unit_keys:
                    units[unit_key].setdefault("uris", {})[key_id] = {
                        "uri": f"files/{unit_key}", "uploaded_at": 1e12}
                if state.get("pdf"):
                    state.setdefault("pdfs", {})[key_id] = {
                        "uri": "files/pdf", "uploaded_at": 1e12}
                return state

            def fake_submit(folder, state, group, client):
                group["batch_id"] = "batches/paid"
                app.save(app.live_state_path(folder), state)

            def fake_wait(folder, state, group, client, interval=30):
                unit = group["files"][0]
                path = app.mixed_result_path(folder, group)
                app.atomic_write_text(
                    path, json.dumps(response_row(unit, "paid three")) + "\n")
                group["done"] = True
                app.save(app.live_state_path(folder), state)

            with mock.patch.object(app, "key_entries_for_tier",
                                   side_effect=lambda tier: (
                                       free if tier == "free" else paid)), \
                    mock.patch.object(app, "key_entries",
                                      return_value=free + paid), \
                    mock.patch.object(app, "get_client",
                                      side_effect=lambda key: FakeClient()), \
                    mock.patch.object(app, "probe_duration",
                                      return_value=6000), \
                    mock.patch.object(app, "ensure_mixed_uploads",
                                      side_effect=fake_uploads), \
                    mock.patch.object(app, "live_generate",
                                      side_effect=fake_live_generate), \
                    mock.patch.object(app, "submit_mixed_paid",
                                      side_effect=fake_submit), \
                    mock.patch.object(app, "wait_mixed_paid",
                                      side_effect=fake_wait), \
                    contextlib.redirect_stdout(io.StringIO()):
                output = app.mixed_transcribe(folder)
            self.assertEqual(
                output.read_text(encoding="utf-8"),
                "free one\n\nfree two\n\npaid three\n")


class FreeOnlyRunTests(unittest.TestCase):
    def test_free_only_never_submits_paid_batch(self):
        with tempfile.TemporaryDirectory() as temp:
            folder = Path(temp)
            (folder / "01.m4a").write_bytes(b"audio")
            free = [{"id": "free1", "label": "free1", "tier": "free",
                     "api_key": "free-key-1", "limit_minutes": 130}]
            paid = [{"id": "paid1", "label": "paid1", "tier": "paid",
                     "api_key": "paid-key-1"}]

            class FakeResponse:
                text = "free only"

                def model_dump(self, **kwargs):
                    return response_row("unused", self.text)["response"]

            class FakeClient:
                def close(self):
                    pass

            def fake_uploads(folder, state, client, key_id, unit_keys):
                units = {entry["key"]: entry for entry in state["files"]}
                for unit_key in unit_keys:
                    units[unit_key].setdefault("uris", {})[key_id] = {
                        "uri": f"files/{unit_key}", "uploaded_at": 1e12}
                return state

            def forbidden_submit(*args, **kwargs):
                raise AssertionError("무료 전용 실행에서 Batch를 제출했습니다")

            with mock.patch.object(app, "key_entries_for_tier",
                                   side_effect=lambda tier: (
                                       free if tier == "free" else paid)), \
                    mock.patch.object(app, "key_entries",
                                      return_value=free + paid), \
                    mock.patch.object(app, "get_client",
                                      side_effect=lambda key: FakeClient()), \
                    mock.patch.object(app, "probe_duration",
                                      return_value=6000), \
                    mock.patch.object(app, "ensure_mixed_uploads",
                                      side_effect=fake_uploads), \
                    mock.patch.object(app, "live_generate",
                                      return_value=FakeResponse()), \
                    mock.patch.object(app, "submit_mixed_paid",
                                      side_effect=forbidden_submit), \
                    contextlib.redirect_stdout(io.StringIO()):
                output = app.mixed_transcribe(folder, include_paid=False)
            self.assertEqual(output.read_text(encoding="utf-8"), "free only\n")
            state = json.loads(app.live_state_path(folder).read_text())
            self.assertFalse(state["paid"])
            self.assertTrue(state["finished"])


class PackagingTests(unittest.TestCase):
    def test_version_matches_pyproject(self):
        text = (Path(__file__).resolve().parent.parent / "pyproject.toml").read_text(
            encoding="utf-8")
        match = re.search(r'^version = "([^"]+)"', text, re.MULTILINE)
        self.assertIsNotNone(match)
        self.assertEqual(match.group(1), app.__version__)


class PlatformTests(unittest.TestCase):
    def test_windows_key_mapping(self):
        self.assertEqual(app.map_windows_key("\xe0", "H"), "up")
        self.assertEqual(app.map_windows_key("\xe0", "P"), "down")
        self.assertEqual(app.map_windows_key("\xe0", "K"), "left")
        self.assertEqual(app.map_windows_key("\xe0", "M"), "right")
        self.assertEqual(app.map_windows_key("\x00", "H"), "up")
        self.assertEqual(app.map_windows_key("\xe0", "Z"), "other")
        self.assertEqual(app.map_windows_key("\r"), "enter")
        with self.assertRaises(KeyboardInterrupt):
            app.map_windows_key("\x03")
        with self.assertRaises(EOFError):
            app.map_windows_key("\x04")

    def test_ascii_symbol_fallback(self):
        with mock.patch.dict(os.environ, {"NALMEOK_ASCII": "1"}, clear=False):
            self.assertFalse(app.unicode_ok())
            self.assertEqual(app.pointer(), ">")
            self.assertEqual(app.spinner_frames(), "|/-\\")

    def test_unicode_symbol_override(self):
        with mock.patch.dict(os.environ, {"NALMEOK_UNICODE": "1"}, clear=False):
            self.assertTrue(app.unicode_ok())
            self.assertEqual(app.pointer(), "❯")

    def test_can_raw_input_requires_tty(self):
        with mock.patch.object(sys.stdin, "isatty", return_value=False):
            self.assertFalse(app.can_raw_input())

    def test_require_tui_rejects_non_tty(self):
        with mock.patch.object(app, "can_raw_input", return_value=False):
            with self.assertRaises(RuntimeError):
                app.require_tui()

    def test_find_tool_prefers_override_directory(self):
        name = "ffmpeg.exe" if os.name == "nt" else "ffmpeg"
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / name
            path.write_bytes(b"")
            with mock.patch.dict(os.environ, {"NALMEOK_FFMPEG": temp},
                                 clear=False):
                self.assertEqual(app.find_tool("ffmpeg"), str(path))

    def test_find_tool_missing_returns_none(self):
        with mock.patch.dict(os.environ, {"NALMEOK_FFMPEG": ""}, clear=False):
            self.assertIsNone(app.find_tool("nalmeok-no-such-tool"))

    def test_doctor_lines_include_environment(self):
        text = "\n".join(app.doctor_lines())
        for label in ("OS", "터미널", "VT/ANSI", "Python", "ffmpeg", "설정 파일"):
            self.assertIn(label, text)


class DropTests(unittest.TestCase):
    def make_temp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        return Path(temp.name)

    def test_normalize_keeps_folders(self):
        root = self.make_temp()
        folder = root / "lecture"
        folder.mkdir()
        with mock.patch.object(app, "drop_jobs_dir", return_value=root / "jobs"):
            folders, skipped = app.normalize_drop_paths([str(folder)])
        self.assertEqual(folders, [folder])
        self.assertEqual(skipped, [])

    def test_normalize_links_media_file_into_own_job_folder(self):
        root = self.make_temp()
        source = root / "week1.mp4"
        source.write_bytes(b"video")
        with mock.patch.object(app, "drop_jobs_dir", return_value=root / "jobs"):
            folders, skipped = app.normalize_drop_paths([str(source)])
        self.assertEqual(skipped, [])
        self.assertEqual(len(folders), 1)
        job = folders[0]
        self.assertEqual(job, root / "jobs" / "week1")
        self.assertEqual((job / "week1.mp4").read_bytes(), b"video")

    def test_normalize_copies_when_hardlink_fails(self):
        root = self.make_temp()
        source = root / "week1.m4a"
        source.write_bytes(b"audio")
        with mock.patch.object(app, "drop_jobs_dir", return_value=root / "jobs"):
            with mock.patch.object(app.os, "link", side_effect=OSError("no link")):
                folders, _ = app.normalize_drop_paths([str(source)])
        self.assertEqual((folders[0] / "week1.m4a").read_bytes(), b"audio")
        self.assertTrue(source.is_file())

    def test_normalize_skips_unsupported_and_missing(self):
        root = self.make_temp()
        notes = root / "notes.txt"
        notes.write_text("hello", encoding="utf-8")
        missing = root / "missing.mp4"
        folders, skipped = app.normalize_drop_paths([str(notes), str(missing)])
        self.assertEqual(folders, [])
        self.assertEqual([name for name, _ in skipped], [str(notes), str(missing)])
        self.assertIn("지원하지 않는", skipped[0][1])
        self.assertIn("찾을 수 없", skipped[1][1])

    def test_normalize_ignores_duplicate_arguments(self):
        root = self.make_temp()
        source = root / "week1.mp4"
        source.write_bytes(b"video")
        with mock.patch.object(app, "drop_jobs_dir", return_value=root / "jobs"):
            folders, _ = app.normalize_drop_paths([str(source), str(source)])
        self.assertEqual(len(folders), 1)

    def test_job_folder_for_same_stem_gets_suffix(self):
        root = self.make_temp()
        first = root / "a" / "week1.mp4"
        second = root / "b" / "week1.mp4"
        first.parent.mkdir()
        second.parent.mkdir()
        first.write_bytes(b"one")
        second.write_bytes(b"two")
        with mock.patch.object(app, "drop_jobs_dir", return_value=root / "jobs"):
            folders, _ = app.normalize_drop_paths([str(first), str(second)])
        self.assertEqual([folder.name for folder in folders], ["week1", "week1-2"])

    def test_copy_result_adds_suffix_to_documents_folder(self):
        root = self.make_temp()
        folder = root / "lecture"
        folder.mkdir()
        (folder / "lecture.md").write_text("first", encoding="utf-8")
        documents = root / "docs"
        first, error = app.copy_result_to_documents(folder, documents)
        self.assertEqual(error, "")
        self.assertEqual(first, documents / "lecture.md")
        second, _ = app.copy_result_to_documents(folder, documents)
        self.assertEqual(second, documents / "lecture (2).md")
        self.assertEqual((documents / "lecture (2).md").read_text(encoding="utf-8"),
                         "first")

    def test_copy_result_without_output_is_not_an_error(self):
        root = self.make_temp()
        folder = root / "empty"
        folder.mkdir()
        self.assertEqual(app.copy_result_to_documents(folder, root / "docs"),
                         (None, ""))

    def test_parse_args_drop_takes_multiple_paths(self):
        args = app.parse_args(["drop", "./lecture", "./week1.mp4"])
        self.assertEqual(args.command, "drop")
        self.assertEqual(args.paths, ["./lecture", "./week1.mp4"])

    def test_split_pasted_paths_unescapes_macos_spaces(self):
        text = r"/Users/me/My\ Folder/week1.mp4 /Users/me/other.m4a"
        with mock.patch.object(app, "IS_WINDOWS", False):
            paths = app.split_pasted_paths(text)
        self.assertEqual(paths, [
            "/Users/me/My Folder/week1.mp4",
            "/Users/me/other.m4a",
        ])

    def test_split_pasted_paths_accepts_quoted_macos_path(self):
        self.assertEqual(
            app.split_pasted_paths("'/Users/me/My Folder/week1.mp4'"),
            ["/Users/me/My Folder/week1.mp4"])

    def test_split_pasted_paths_handles_windows_quotes_and_backslashes(self):
        text = r'"C:\Users\me\My Folder\week1.mp4" "C:\Users\me\b.m4a"'
        with mock.patch.object(app, "IS_WINDOWS", True):
            paths = app.split_pasted_paths(text)
        self.assertEqual(paths, [
            r"C:\Users\me\My Folder\week1.mp4",
            r"C:\Users\me\b.m4a",
        ])

    def test_split_pasted_paths_keeps_plain_windows_path(self):
        with mock.patch.object(app, "IS_WINDOWS", True):
            self.assertEqual(app.split_pasted_paths(r"C:\lecture\week1.mp4"),
                             [r"C:\lecture\week1.mp4"])

    def test_split_pasted_paths_ignores_empty_text(self):
        self.assertEqual(app.split_pasted_paths("   "), [])

    def test_split_pasted_paths_falls_back_on_unbalanced_quote(self):
        self.assertEqual(app.split_pasted_paths('"/tmp/a b'),
                         ['"/tmp/a b'])

    def test_windows_key_reader_surfaces_bracketed_paste(self):
        payload = "C:\\a b\\week1.m4a"
        chars = iter(list("\x1b[200~" + payload + "\x1b[201~"))
        fake = mock.MagicMock()
        fake.getwch.side_effect = lambda: next(chars)
        fake.kbhit.return_value = True
        with mock.patch.object(app, "msvcrt", fake):
            key = app._read_key_windows()
        self.assertEqual(key, ("paste", payload))

    @unittest.skipIf(os.name == "nt", "POSIX pipe test")
    def test_posix_paste_reader_stops_at_terminator(self):
        read_fd, write_fd = os.pipe()
        try:
            os.write(write_fd, "강의 폴더/week1.m4a".encode("utf-8")
                     + b"\x1b[201~")
            os.close(write_fd)
            write_fd = -1
            self.assertEqual(app._read_paste_posix(read_fd),
                             "강의 폴더/week1.m4a")
        finally:
            os.close(read_fd)
            if write_fd != -1:
                os.close(write_fd)

    def test_documents_dir_follows_windows_known_folder(self):
        fake = mock.MagicMock()
        fake.HKEY_CURRENT_USER = object()
        fake.QueryValueEx.return_value = ("$USERPROFILE/OneDrive/Documents", 2)
        with mock.patch.object(app, "IS_WINDOWS", True), \
                mock.patch.object(app, "winreg", fake), \
                mock.patch.dict(os.environ, {"USERPROFILE": "/tmp/tester"}):
            path = app.documents_dir()
        self.assertEqual(path,
                         Path("/tmp/tester/OneDrive/Documents/nalmeok"))

    def test_documents_dir_falls_back_when_registry_fails(self):
        fake = mock.MagicMock()
        fake.HKEY_CURRENT_USER = object()
        fake.OpenKey.side_effect = OSError("missing")
        with mock.patch.object(app, "IS_WINDOWS", True), \
                mock.patch.object(app, "winreg", fake), \
                mock.patch.object(app.Path, "home",
                                  return_value=Path("/home/tester")):
            path = app.documents_dir()
        self.assertEqual(path, Path("/home/tester/Documents/nalmeok"))


if __name__ == "__main__":
    unittest.main()

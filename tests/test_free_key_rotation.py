"""무료 키 순환 및 명시적 유료 새 작업 흐름 (실제 API 호출 없음)."""
import contextlib
import io
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest import mock

try:
    from google.genai.errors import ClientError
except ImportError:
    ClientError = None

import nalmeok as app
from tests.test_nalmeok import response_row


def api_error(code=429):
    return ClientError(code, {"error": {"code": code, "message": "quota"}})


@unittest.skipIf(ClientError is None, "Gemini SDK 필요")
class RotationTests(unittest.TestCase):
    def setUp(self):
        self.stack = contextlib.ExitStack()
        self.addCleanup(self.stack.close)
        self.folder = Path(self.stack.enter_context(tempfile.TemporaryDirectory()))
        self.keys = [{"id": f"k{i}", "label": f"key{i}", "tier": "free",
                          "api_key": f"secret{i}", "limit_minutes": 130}
                     for i in range(3)]
        audio = self.folder / "01.m4a"
        audio.write_bytes(b"audio")
        state = {"mode": "mixed", "model": app.DEFAULT_MODEL,
                     "files": [{"key": "a1", "name": audio.name}], "pdf": None,
                     "groups": [{"key": "g1", "key_id": "k0", "label": "key0",
                                  "files": ["a1"], "seconds": 10}],
                     "paid": [], "free_seconds": 10, "paid_seconds": 0}
        app.save(app.live_state_path(self.folder), state)
        self.attempts = []
        self.uploads = []
        self.failures = {"secret0"}
        self.error_code = 429
        self.patch("key_entries", return_value=self.keys)
        self.patch("saved_key_entries", return_value=[])
        self.patch("get_client", side_effect=lambda key: SimpleNamespace(
            key=key, close=lambda: None))
        self.patch("ensure_mixed_uploads", side_effect=self.upload)
        self.patch("live_generate", side_effect=self.generate)
        self.notify = self.patch("notify")
        self.submit = self.patch("submit_mixed_paid")
        self.patch("time.sleep")
        self.stack.enter_context(contextlib.redirect_stdout(io.StringIO()))

    def patch(self, name, **kwargs):
        return self.stack.enter_context(mock.patch("nalmeok." + name, **kwargs))

    def upload(self, folder, state, client, key_id, unit_keys):
        self.uploads.append(key_id)
        for entry in state["files"]:
            entry.setdefault("uris", {})[key_id] = {"uri": f"{key_id}/audio"}

    def generate(self, client, model, contents, gap, **kwargs):
        self.attempts.append(client.key)
        self.assertTrue(kwargs["rotate_429"])
        self.assertIn(client.key.replace("secret", "k"),
                      contents[0]["parts"][1]["file_data"]["file_uri"])
        if client.key in self.failures:
            raise api_error(self.error_code)
        return SimpleNamespace(text="transcript", model_dump=lambda **kw:
                               response_row("unused", "transcript")["response"])

    def test_rotates_uploads_and_records_successful_key(self):
        output = app.mixed_transcribe(self.folder, include_paid=False)
        self.assertEqual(self.attempts, ["secret0", "secret1"])
        self.assertEqual(self.uploads, ["k0", "k1"])
        self.assertEqual(output.read_text(), "transcript\n")
        state = app.load_live_state(self.folder)
        self.assertEqual(state["groups"][0]["key_id"], "k1")
        self.assertEqual(app.live_row_map(self.folder)["g1"]["label"], "key1")
        self.submit.assert_not_called()

    def test_two_limited_keys_then_third_succeeds(self):
        self.failures = {"secret0", "secret1"}
        app.mixed_transcribe(self.folder, include_paid=False)
        self.assertEqual(self.attempts, ["secret0", "secret1", "secret2"])

    def test_limited_key_is_skipped_in_later_groups(self):
        state = app.load_live_state(self.folder)
        state["groups"].append({"key": "g2", "key_id": "k0", "label": "key0",
                                "files": ["a1"], "seconds": 10})
        app.save(app.live_state_path(self.folder), state)
        app.mixed_transcribe(self.folder, include_paid=False)
        self.assertEqual(self.attempts, ["secret0", "secret1", "secret1"])

    def test_all_keys_exhausted_once_and_retry_on_next_run(self):
        self.failures = {key["api_key"] for key in self.keys}
        with self.assertRaises(app.FreeKeysExhausted):
            app.mixed_transcribe(self.folder, include_paid=False)
        self.assertEqual(self.attempts, ["secret0", "secret1", "secret2"])
        self.notify.assert_called_once()
        self.submit.assert_not_called()
        self.failures.clear()
        self.attempts.clear()
        app.mixed_transcribe(self.folder, include_paid=False)
        self.assertEqual(len(self.attempts), 1)

    def test_completed_groups_are_skipped(self):
        state = app.load_live_state(self.folder)
        state["groups"].insert(0, {"key": "done", "key_id": "k0", "label": "key0",
                                         "files": ["a1"], "seconds": 10})
        app.save(app.live_state_path(self.folder), state)
        app.save_live_row(self.folder, state, "done", response_row("done", "earlier"))
        output = app.mixed_transcribe(self.folder, include_paid=False)
        self.assertEqual(self.attempts, ["secret0", "secret1"])
        self.assertEqual(output.read_text(), "earlier\n\ntranscript\n")

    def test_non_429_does_not_rotate(self):
        self.error_code = 403
        with self.assertRaises(ClientError):
            app.mixed_transcribe(self.folder, include_paid=False)
        self.assertEqual(self.attempts, ["secret0"])
        self.notify.assert_not_called()

    def test_upload_429_rotates_too(self):
        upload = self.upload

        def limited_upload(folder, state, client, key_id, unit_keys):
            if key_id == "k0":
                raise api_error()
            return upload(folder, state, client, key_id, unit_keys)

        self.patch("ensure_mixed_uploads", side_effect=limited_upload)
        app.mixed_transcribe(self.folder, include_paid=False)
        self.assertEqual(self.attempts, ["secret1"])


@unittest.skipIf(ClientError is None, "Gemini SDK 필요")
class GenerationTests(unittest.TestCase):
    def test_rotation_mode_skips_same_key_retry(self):
        for message in ("per day", "per minute"):
            with self.subTest(message=message):
                error = ClientError(429, {"error": {"message": message}})
                client = mock.Mock()
                client.models.generate_content.side_effect = error
                with mock.patch.object(app.time, "sleep") as sleep, \
                        self.assertRaises(ClientError):
                    app.live_generate(client, app.DEFAULT_MODEL, [], 60,
                                      rotate_429=True)
                self.assertEqual(client.models.generate_content.call_count, 1)
                sleep.assert_not_called()


class ExhaustionUITests(unittest.TestCase):
    def setUp(self):
        self.stack = contextlib.ExitStack()
        self.addCleanup(self.stack.close)
        self.folder = Path(self.stack.enter_context(tempfile.TemporaryDirectory()))
        (self.folder / "01.m4a").write_bytes(b"audio")
        app.save(app.live_state_path(self.folder), {
            "mode": "mixed", "model": "gemini-2.5-flash", "groups": []})
        self.before = app.live_state_path(self.folder).read_bytes()
        self.patch("folder_session", return_value="back")
        self.patch("wait")
        self.patch("estimate_values", return_value={"files": 1, "seconds": 60, "cost": .01})
        self.patch("sys.stdin.isatty", return_value=True)
        self.menu = self.patch("select_menu", return_value="stop")
        self.ensure = self.patch("paid_key_for_batch", return_value="paid-secret")
        self.add_key = self.patch("add_key_flow", return_value=None)
        self.upload = self.patch("upload_session", return_value=("paid-secret", True))
        self.wait = self.patch("wait_session", return_value="waiting")
        self.stack.enter_context(contextlib.redirect_stdout(io.StringIO()))

    def patch(self, name, **kwargs):
        return self.stack.enter_context(mock.patch("nalmeok." + name, **kwargs))

    def test_cancel_preserves_records(self):
        app.exhausted_free_session(self.folder, None)
        self.assertEqual(app.live_state_path(self.folder).read_bytes(), self.before)
        self.upload.assert_not_called()
        self.ensure.assert_not_called()

    def test_accept_archives_and_submits_entire_folder_with_original_model(self):
        self.menu.return_value = "paid"
        result = app.exhausted_free_session(self.folder, None)
        self.assertEqual(result, "waiting")
        self.upload.assert_called_once_with(self.folder, "paid-secret",
                                            model="gemini-2.5-flash")
        self.assertFalse(app.live_state_path(self.folder).exists())
        archives = list((self.folder / app.ARCHIVE_DIR).glob("*/" + app.LIVE_STATE_FILE))
        self.assertEqual(len(archives), 1)
        self.assertEqual(archives[0].read_bytes(), self.before)
        self.assertTrue((self.folder / "01.m4a").exists())

    def test_missing_paid_key_preserves_records(self):
        self.menu.return_value = "paid"
        self.ensure.return_value = None
        app.exhausted_free_session(self.folder, None)
        self.assertEqual(app.live_state_path(self.folder).read_bytes(), self.before)
        self.upload.assert_not_called()

    def test_registers_paid_key_before_archiving(self):
        self.menu.return_value = "paid"
        self.ensure.return_value = None
        self.add_key.return_value = "new-paid-secret"
        self.patch("key_tier_of", return_value="paid")
        app.exhausted_free_session(self.folder, None)
        self.add_key.assert_called_once_with(default_tier="paid")
        self.upload.assert_called_once_with(self.folder, "new-paid-secret",
                                            model="gemini-2.5-flash")

    def test_archive_failure_never_submits(self):
        self.menu.return_value = "paid"
        self.patch("archive_job", side_effect=OSError("archive failed"))
        app.exhausted_free_session(self.folder, None)
        self.upload.assert_not_called()
        self.assertEqual(app.live_state_path(self.folder).read_bytes(), self.before)

    def test_noninteractive_never_prompts_or_submits(self):
        self.patch("sys.stdin.isatty", return_value=False)
        with self.assertRaises(app.FreeKeysExhausted):
            app.exhausted_free_session(self.folder, None)
        self.menu.assert_not_called()
        self.upload.assert_not_called()
        self.assertEqual(app.live_state_path(self.folder).read_bytes(), self.before)

    def test_session_handles_exhaustion_after_releasing_lock(self):
        # Exercise the real job lock: the handler must be outside its context.
        state = app.load_live_state(self.folder)
        state.update(paid=[], groups=[])
        app.save(app.live_state_path(self.folder), state)
        self.patch("key_entries_for_tier", return_value=[])
        self.patch("status_bar")
        self.patch("_mixed_run", side_effect=app.FreeKeysExhausted("limited"))

        def handle(folder, key):
            with app.job_lock(folder, "test"):
                return "handled"

        self.patch("exhausted_free_session", side_effect=handle)
        self.assertEqual(app.mixed_session(self.folder, None, include_paid=False), "handled")

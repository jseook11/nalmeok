"""nalmeok.engine."""

import argparse
import contextlib
import getpass
import hashlib
import json
import math
import os
import platform
import re
import select
import shlex
import shutil
import signal
import subprocess
import sys
import tempfile
import threading
import time
import unicodedata
import uuid
import zipfile
from pathlib import Path

import nalmeok as app
from .constants import *  # noqa: F401,F403
from .constants import __version__
from . import keys  # noqa: F401
from . import media  # noqa: F401
from . import runtime  # noqa: F401
from . import term  # noqa: F401
from . import update  # noqa: F401



def watch_pid_path(folder):
    return Path(folder) / app.WATCH_PID


def watcher_running(folder):
    """백그라운드 대기 프로세스가 살아 있는지. 죽은 PID 파일은 정리합니다."""
    path = app.watch_pid_path(folder)
    try:
        pid = int(path.read_text(encoding="utf-8").strip())
    except (OSError, ValueError):
        if app.safe_exists(path):
            try:
                path.unlink()
            except OSError:
                pass
        return False
    if app.process_alive(pid):
        return True
    try:
        path.unlink()
    except OSError:
        pass
    return False


def read_watch_status(folder):
    try:
        data = json.loads((Path(folder) / app.WATCH_STATUS).read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return {}
    return data if isinstance(data, dict) else {}


def write_watch_status(folder, reset=False, **fields):
    data = {} if reset else app.read_watch_status(folder)
    data.update(fields)
    data["updated"] = time.time()
    try:
        app.save(Path(folder) / app.WATCH_STATUS, data)
    except OSError:
        pass


def start_watcher(folder, key=None, allow_unknown=False):
    """Batch 완료를 기다리는 프로세스를 분리해 띄웁니다. 메뉴는 계속 쓸 수 있습니다."""
    folder = Path(folder).resolve()
    if app.watcher_running(folder):
        return False, "이미 백그라운드에서 기다리는 중입니다."
    job = app.load(folder)
    if not job.get("batch_id"):
        return False, "아직 제출된 Batch가 없습니다."
    key = app.paid_key_for_batch(key, allow_unknown)
    if not key:
        return False, "유료 Batch에는 유료 API 키가 필요합니다. 설정에서 유료 키를 추가하세요."
    env = dict(os.environ)
    if key:
        env["GEMINI_API_KEY"] = key
    env["PYTHONUTF8"] = "1"
    env["PYTHONIOENCODING"] = "utf-8"
    log_path = folder / app.WATCH_LOG
    root = Path(app.__file__).resolve().parent
    if root.name == "nalmeok":
        root = root.parent
    if env.get("PYTHONPATH"):
        env["PYTHONPATH"] = str(root) + os.pathsep + env["PYTHONPATH"]
    else:
        env["PYTHONPATH"] = str(root)
    command = [sys.executable, "-m", "nalmeok",
               "_wait", str(folder), "--retry", "none"]
    kwargs = {"cwd": folder, "env": env, "stdin": subprocess.DEVNULL,
              "stderr": subprocess.STDOUT}
    if os.name == "nt":
        kwargs["creationflags"] = 0x00000008  # DETACHED_PROCESS
    else:
        kwargs["start_new_session"] = True

    claim = folder / app.WATCH_CLAIM
    try:
        handle = os.open(claim, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    except FileExistsError:
        try:
            age = time.time() - claim.stat().st_mtime
        except OSError:
            age = 0
        if age < 60:
            return False, "이미 백그라운드 대기를 시작하는 중입니다."
        try:
            claim.unlink()
        except OSError:
            pass
        try:
            handle = os.open(claim, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        except OSError:
            return False, "백그라운드 대기를 시작하지 못했습니다."
    except OSError as exc:
        return False, f"백그라운드 대기를 시작하지 못했습니다: {exc}"
    os.close(handle)

    process = None
    try:
        app.write_watch_status(folder, reset=True, state="STARTING", label="시작 중",
                           started=time.time())
        with open(log_path, "a", encoding="utf-8") as log_file:
            process = subprocess.Popen(command, stdout=log_file, **kwargs)
        app.atomic_write_text(app.watch_pid_path(folder), str(process.pid))
    except OSError as exc:
        if process is not None:
            try:
                process.terminate()
            except OSError:
                pass
        app.write_watch_status(folder, reset=True, state="ERROR", label="오류",
                           error=str(exc), finished=time.time())
        return False, f"백그라운드 대기를 시작하지 못했습니다: {exc}"
    finally:
        try:
            claim.unlink()
        except OSError:
            pass
    return True, f"백그라운드 대기 시작 (PID {process.pid})"


@contextlib.contextmanager
def job_lock(folder, purpose):
    """같은 폴더에 업로드/다운로드가 동시에 실행되지 않게 합니다."""
    path = Path(folder) / app.LOCK_FILE
    with open(path, "a+", encoding="utf-8") as handle:
        locked = False
        try:
            try:
                locked = app._lock_file(handle, blocking=False)
            except OSError:
                app.fail(f"다른 작업이 이 폴더를 쓰고 있습니다 ({purpose}). "
                     "백그라운드 대기가 끝난 뒤 다시 시도하세요.")
            yield
        finally:
            if locked:
                app._unlock_file(handle)


def get_client(key):
    app.ensure_sdk()
    api_key = key or app.env_key() or app.saved_key()
    if not api_key:
        app.fail("API 키가 필요합니다: --key API-KEY 또는 GEMINI_API_KEY 환경변수")
    try:
        from google import genai
    except Exception as exc:  # noqa: BLE001 - 설치 손상을 친절한 메시지로 바꿉니다.
        app.fail(f"{app.SDK} SDK를 불러오지 못했습니다: {exc}")
    try:
        from google.genai import types
        http_options = types.HttpOptions(timeout=30 * 60 * 1000)
    except Exception:  # noqa: BLE001 - 구버전 SDK는 기본 타임아웃을 씁니다.
        http_options = None
    if http_options is None:
        return genai.Client(api_key=api_key)
    return genai.Client(api_key=api_key, http_options=http_options)


def upload_safely(client, path, mime, display_name=None):
    """로컬 파일을 업로드합니다.

    SDK는 경로로 넘기면 파일명을 X-Goog-Upload-File-Name 헤더에 넣는데,
    HTTP 헤더는 ASCII만 허용돼 '한글.m4a' 같은 이름이면 전송이 깨집니다.
    파일 객체로 넘겨 그 헤더를 피하고, 원래 이름은 본문의 display_name 으로
    그대로 보존합니다.
    """
    with open(path, "rb") as handle:
        return client.files.upload(
            file=handle,
            config={"mime_type": mime, "display_name": display_name or path.name},
        )


def upload(folder, client, model=app.DEFAULT_MODEL, confirm=None, show_estimate=True):
    with app.job_lock(folder, "업로드"):
        return app._upload(folder, client, model, confirm, show_estimate)


def _upload(folder, client, model=app.DEFAULT_MODEL, confirm=None, show_estimate=True):
    path = app.state_path(folder)
    if path.exists():
        job = app.load(folder)
        if job.get("batch_id"):
            try:
                app.validate_local_files(folder, job)
            except RuntimeError:
                app.fail("제출 이후 폴더 안 파일이 바뀌었습니다. 추가한 파일은 기존 Batch에 "
                     "포함되지 않습니다. 새 폴더에서 새 작업을 시작하세요.")
            app.log(f"이미 제출된 Batch: {job['batch_id']}")
            return
        app.validate_local_files(folder, job)
    else:
        if app.needs_conversion(folder):
            app.log("M4A가 없어 MP4를 먼저 변환합니다.")
            app.convert(folder)
            app.log("")
        audios, pdf = app.list_media(folder)
        job = {
            "model": model,
            "files": [app.snapshot(p, f"audio_{i:03d}") for i, p in enumerate(audios, 1)],
            "pdf": app.snapshot(pdf) if pdf else None,
            "batch_id": None,
        }
        app.save(path, job)

    if not job.get("batch_id"):
        if model and job.get("model") != model:
            job["model"] = model
            app.save(path, job)
        try:
            lines = app.estimate_lines(folder, job["model"])
        except (RuntimeError, OSError) as exc:
            app.log(f"예상 비용을 계산하지 못했습니다: {exc}")
            lines = []
        if lines and show_estimate:
            for line in lines:
                app.log(line)
            app.log("")
        if not (confirm or app.confirm_spend)(lines):
            app.log("제출을 취소했습니다. 준비되면 upload를 다시 실행하세요.")
            return

    sources = job["files"] + ([job["pdf"]] if job["pdf"] else [])
    for item in sources:
        if item.get("uri"):
            continue  # 이전 실행에서 이미 업로드된 파일입니다.
        filepath = folder / item["name"]
        mime = "application/pdf" if filepath.suffix.lower() == ".pdf" else "audio/m4a"
        app.log(f"파일 업로드: {filepath.name}")
        remote = app.upload_safely(client, filepath, mime, filepath.name)
        if not remote.uri or not remote.name:
            app.fail(f"파일 업로드 응답이 불완전합니다: {filepath.name}")
        item.update(uri=remote.uri, remote_name=remote.name, mime_type=mime)
        app.save(path, job)

    requests = []
    for item in job["files"]:
        parts = [{"text": app.PROMPT}]
        if job.get("pdf"):
            parts.append({"file_data": {
                "file_uri": job["pdf"]["uri"], "mime_type": "application/pdf"}})
        parts.append({"file_data": {
            "file_uri": item["uri"], "mime_type": "audio/m4a"}})
        requests.append({
            "key": item["key"],
            "request": {
                "contents": [{"role": "user", "parts": parts}],
                "generation_config": {
                    "max_output_tokens": 65536,
                    "thinking_config": {"thinking_level": "low"},
                },
            },
        })

    jsonl_path = folder / app.REQUEST_FILE
    app.atomic_write_text(
        jsonl_path,
        "".join(json.dumps(req, ensure_ascii=False) + "\n" for req in requests),
    )
    if not job.get("batch_input_name"):
        app.log("요청 목록 업로드...")
        uploaded = app.upload_safely(client, jsonl_path, "jsonl", jsonl_path.name)
        if not uploaded.name:
            app.fail("Batch JSONL 업로드 응답이 불완전합니다.")
        job["batch_input_name"] = uploaded.name
        app.save(path, job)

    app.log(f"Batch 제출: {folder.name} ({len(job['files'])}개 오디오)")
    batch = client.batches.create(
        model=job["model"],
        src=job["batch_input_name"],
        config={"display_name": folder.name[:120]},
    )
    if not batch.name:
        app.fail("Batch 생성 응답에서 ID를 받지 못했습니다.")
    job["batch_id"] = batch.name
    app.save(path, job)
    app.log(f"제출 완료: {batch.name}\n상태 파일: {path}")


def batch_state(batch):
    return getattr(batch.state, "name", None) or str(batch.state)


def status(job, client):
    if not job.get("batch_id"):
        app.fail("아직 Batch가 제출되지 않았습니다. upload를 다시 실행하세요.")
    batch = client.batches.get(name=job["batch_id"])
    state = app.batch_state(batch)
    app.log(f"Batch: {job['batch_id']}\n상태: {app.format_state(state)}")
    counts = getattr(batch, "batch_stats", None)
    if counts:
        app.log(f"요청 통계: {counts}")
    if getattr(batch, "error", None):
        app.log(f"오류: {batch.error}")
    return batch, state


def merge_jsonl(raw, files):
    expected = {f["key"] for f in files}
    results = {}
    for index, line in enumerate(raw.splitlines(), 1):
        if not line.strip():
            continue
        item = json.loads(line)
        key = item.get("key") or (item.get("metadata") or {}).get("key")
        if key not in expected or key in results:
            app.fail(f"결과 {index}행에 알 수 없거나 중복된 key: {key!r}")
        if item.get("error"):
            app.fail(f"{key} 전사 오류: {item['error']}")
        candidates = (item.get("response") or {}).get("candidates") or []
        if not candidates:
            app.fail(f"{key} 전사 결과가 없습니다: "
                 f"{item.get('response', {}).get('promptFeedback', {})}")
        candidate = candidates[0]
        finish = candidate.get("finishReason", candidate.get("finish_reason"))
        if finish and finish not in ("STOP", "FINISH_REASON_STOP"):
            app.fail(f"{key} 전사가 정상 종료되지 않았습니다: {finish} (잘림 가능성)")
        parts = (candidate.get("content") or {}).get("parts") or []
        content = "\n".join(p["text"] for p in parts
                            if isinstance(p.get("text"), str) and not p.get("thought"))
        if not content.strip():
            app.fail(f"{key}의 전사 내용이 비었습니다.")
        results[key] = content.strip()
    if expected != results.keys():
        app.fail(f"누락된 결과: {', '.join(sorted(expected - results.keys()))}")
    return "\n\n".join(results[f["key"]] for f in files) + "\n"


def result_rows(raw, expected):
    rows = {}
    for line in raw.splitlines():
        if not line.strip():
            continue
        row = json.loads(line)
        key = row.get("key") or (row.get("metadata") or {}).get("key")
        if key not in expected or key in rows:
            app.fail(f"알 수 없거나 중복된 결과 key: {key!r}")
        rows[key] = row
    return rows


def batch_result(batch, client):
    name = getattr(getattr(batch, "dest", None), "file_name", None)
    if not name:
        app.fail("완료된 Batch의 결과 파일 경로를 찾을 수 없습니다.")
    raw = client.files.download(file=name)
    return raw.decode("utf-8") if isinstance(raw, bytes) else raw


def retry_live(item, request, model, client, attempts):
    from google.genai import errors, types
    config = dict(request.get("generation_config") or {})
    # 최신 SDK는 JSONL의 thinking_level을 그대로 받지만, 구버전은 extra_body가 필요합니다.
    if "thinking_level" not in getattr(types.ThinkingConfig, "model_fields", {}):
        thinking = config.pop("thinking_config", None)
        if thinking:
            config["http_options"] = {"extra_body": {"generationConfig": {
                "thinkingConfig": {"thinkingLevel": thinking["thinking_level"]}}}}
    for attempt in range(1, attempts + 1):
        app.log(f"live 재요청 ({attempt}/{attempts}): {item['name']}")
        try:
            response = client.models.generate_content(
                model=model, contents=request["contents"], config=config)
            row = {"key": item["key"], "response": response.model_dump(
                mode="json", by_alias=True, exclude_none=True)}
            app.merge_jsonl(json.dumps(row), [item])
            return row
        except errors.APIError as exc:
            if exc.code not in (429, 500, 502, 503, 504):
                raise
            app.log(f"API 오류: {exc}")
        except RuntimeError as exc:
            app.log(f"전사 실패: {exc}")
    return None


def recovery_state(folder, job):
    path = folder / app.RECOVERY_FILE
    recovery = {}
    if path.exists():
        try:
            recovery = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, ValueError):
            recovery = {}
        if not isinstance(recovery, dict):
            recovery = {}
    if recovery.get("batch_id") != job["batch_id"]:
        recovery = {"batch_id": job["batch_id"], "results": {}}
    if not isinstance(recovery.get("results"), dict):
        recovery["results"] = {}
    return path, recovery


def finish_pending_batch(folder, job, client, recovery, path, rows):
    """이전 실행이 남긴 재시도 Batch가 끝났는지 확인하고 결과를 흡수합니다."""
    pending = recovery.get("pending_batch")
    if not pending:
        return
    batch, state = app.status({"batch_id": pending["batch_id"]}, client)
    if state in ("JOB_STATE_PENDING", "JOB_STATE_RUNNING", "JOB_STATE_QUEUED"):
        app.fail("재시도 Batch가 아직 처리 중입니다. 완료 후 download를 다시 실행하세요.")
    if state == "JOB_STATE_SUCCEEDED":
        by_key = {item["key"]: item for item in job["files"]}
        for key, row in app.result_rows(app.batch_result(batch, client), set(pending["keys"])).items():
            try:
                app.merge_jsonl(json.dumps(row), [by_key[key]])
            except RuntimeError:
                continue
            recovery["results"][key] = row
            rows[key] = row
    elif state not in ("JOB_STATE_FAILED", "JOB_STATE_CANCELLED", "JOB_STATE_EXPIRED"):
        app.fail(f"재시도 Batch 상태를 확인하세요: {state}")
    recovery.setdefault("batch_history", []).append(pending)
    del recovery["pending_batch"]
    app.save(path, recovery)


def retry_batch(folder, job, client, recovery, path, failed, requests):
    retry_path = folder / app.RETRY_FILE
    app.atomic_write_text(retry_path, "".join(json.dumps(
        {"key": item["key"], "request": requests[item["key"]]},
        ensure_ascii=False) + "\n" for item in failed))
    uploaded = app.upload_safely(client, retry_path, "jsonl", retry_path.name)
    batch = client.batches.create(model=job["model"], src=uploaded.name,
        config={"display_name": (folder.name + " retry")[:120]})
    if not batch.name:
        app.fail("재시도 Batch 생성 응답에 ID가 없습니다.")
    recovery["pending_batch"] = {"batch_id": batch.name,
                                "keys": [item["key"] for item in failed]}
    app.save(path, recovery)
    app.log(f"실패한 {len(failed)}개만 Batch 제출: {batch.name}\n"
        f"완료 후 download를 다시 실행하세요.")


def download(folder, job, client, retry="none", attempts=3, choose_retry=None,
             validate=True):
    with app.job_lock(folder, "다운로드"):
        return app._download(folder, job, client, retry, attempts, choose_retry,
                         validate)


def _download(folder, job, client, retry="none", attempts=3, choose_retry=None,
              validate=True):
    if validate:
        app.validate_local_files(folder, job)
    batch, state = app.status(job, client)
    if state != "JOB_STATE_SUCCEEDED":
        app.fail("작업이 성공적으로 완료된 뒤 download를 실행하세요.")

    expected = {item["key"] for item in job["files"]}
    result_path = folder / app.RESULT_FILE
    # 업로드한 원격 파일은 만료되므로 저장해 둔 결과를 먼저 씁니다.
    raw = None
    if result_path.exists():
        try:
            candidate = result_path.read_text(encoding="utf-8")
            app.result_rows(candidate, expected)
            raw = candidate
        except (OSError, ValueError, RuntimeError):
            app.log("저장된 결과가 손상되어 다시 받습니다.")
    if raw is None:
        raw = app.batch_result(batch, client)
        app.atomic_write_text(result_path, raw)

    path, recovery = app.recovery_state(folder, job)
    rows = app.result_rows(raw, expected)
    if isinstance(recovery.get("results"), dict):
        rows.update(recovery["results"])
    app.finish_pending_batch(folder, job, client, recovery, path, rows)

    failed = []
    for item in job["files"]:
        try:
            app.merge_jsonl(json.dumps(rows.get(item["key"], {"key": item["key"]})), [item])
        except RuntimeError as exc:
            app.log(f"전사 실패: {item['name']}: {exc}")
            failed.append(item)

    if failed and choose_retry is not None:
        retry = choose_retry(len(failed))
        if retry == "back":
            return "back"
    if failed and retry == "none":
        app.fail(f"미완료 {len(failed)}개. --retry live 또는 --retry batch로 재요청하세요. "
             f"성공한 결과는 보존했습니다.")

    if failed:
        requests = {r["key"]: r["request"] for r in (
            json.loads(line) for line in
            (folder / app.REQUEST_FILE).read_text(encoding="utf-8").splitlines()
            if line.strip())}
        if retry == "batch":
            app.retry_batch(folder, job, client, recovery, path, failed, requests)
            return
        remaining = []
        for item in failed:
            row = app.retry_live(item, requests[item["key"]], job["model"], client, attempts)
            if row is None:
                remaining.append(item["name"])
                continue
            rows[item["key"]] = row
            recovery["results"][item["key"]] = row
            app.save(path, recovery)
        if remaining:
            app.fail("재요청 후에도 미완료: " + ", ".join(remaining)
                 + " (성공한 결과는 보존했습니다.)")

    merged = app.merge_jsonl("\n".join(json.dumps(row) for row in rows.values()), job["files"])
    output = app.output_path(folder)
    if output.exists():
        if output.read_text(encoding="utf-8") == merged:
            app.log(f"이미 저장된 파일과 동일합니다: {output}")
            return
        app.fail(f"기존 Markdown을 보존합니다. 파일을 확인하세요: {output}")
    app.atomic_write_text(output, merged)
    app.log(f"전사 완료: {output} ({len(job['files'])}개 파일, {len(merged):,}자)")
    app.log("")
    for line in app.cost_lines(folder, job):
        app.log(line)


def plan_live_groups(entries, cap_seconds=app.LIVE_CHUNK_SECONDS):
    """연속된 파일을 cap 이하의 그룹으로 묶습니다. 그룹 수를 최소화합니다."""
    if not entries:
        return []
    durations = [max(0.0, float(entry.get("seconds") or 0)) for entry in entries]
    total = sum(durations)
    if total <= 0:
        return [{"key": "group_001",
                 "files": [entry["key"] for entry in entries],
                 "seconds": 0.0}]
    minimum = max(1, math.ceil(total / (cap_seconds + 1.0)))
    n = len(entries)
    prefix = [0.0]
    for duration in durations:
        prefix.append(prefix[-1] + duration)
    for count in range(minimum, n + 1):
        infinity = float("inf")
        dp = [[infinity] * (n + 1) for _ in range(count + 1)]
        back = [[0] * (n + 1) for _ in range(count + 1)]
        dp[0][0] = 0.0
        for k in range(1, count + 1):
            for i in range(1, n + 1):
                for j in range(k - 1, i):
                    size = prefix[i] - prefix[j]
                    if size > cap_seconds + 1.0:
                        continue
                    value = max(dp[k - 1][j], size)
                    if value < dp[k][i]:
                        dp[k][i] = value
                        back[k][i] = j
        if dp[count][n] == infinity:
            continue
        bounds = []
        position = n
        for k in range(count, 0, -1):
            start = back[k][position]
            bounds.append((start, position))
            position = start
        bounds.reverse()
        return [
            {
                "key": f"group_{index:03d}",
                "files": [entries[i]["key"] for i in range(start, end)],
                "seconds": round(prefix[end] - prefix[start], 3),
            }
            for index, (start, end) in enumerate(bounds, 1)
        ]
    return [
        {"key": f"group_{index:03d}", "files": [entry["key"]],
         "seconds": durations[index - 1]}
        for index, entry in enumerate(entries, 1)
    ]


def allocate_mixed_entries(entries, free_keys, paid_keys,
                           chunk_seconds=app.DEFAULT_FREE_MINUTES * 60):
    """무료 키를 앞에서부터 채우고, 남는 항목은 유료 Batch 그룹으로 나눕니다.

    entries 는 이미 chunk_seconds 이하로 나뉜 순서 있는 목록이어야 합니다.
    무료 키 하나가 130분보다 큰 한도를 가져도 요청 하나가 chunk_seconds 를
    넘지 않도록 그룹을 여러 개로 나눕니다.
    """
    free_groups = []
    cursor = 0
    chunk_seconds = max(1, int(chunk_seconds))
    for key in free_keys:
        key_limit = max(1, int(key.get("limit_minutes") or
                               app.DEFAULT_FREE_MINUTES)) * 60
        used = 0.0
        while cursor < len(entries) and used < key_limit:
            room = min(chunk_seconds, key_limit - used)
            selected = []
            seconds = 0.0
            while cursor < len(entries):
                entry = entries[cursor]
                length = max(0.0, float(entry.get("seconds") or 0))
                if selected and seconds + length > room + 0.5:
                    break
                if not selected and length > room + 0.5:
                    break
                selected.append(entry)
                seconds += length
                cursor += 1
            if not selected:
                break
            free_groups.append({
                "key": f"free_{len(free_groups) + 1:03d}",
                "key_id": key["id"],
                "label": key["label"],
                "files": [entry["key"] for entry in selected],
                "seconds": round(seconds, 3),
            })
            used += seconds
    remaining = entries[cursor:]
    paid_groups = []
    if remaining and paid_keys:
        buckets = []
        for index, key in enumerate(paid_keys, 1):
            buckets.append({"key": f"paid_{index:03d}", "key_id": key["id"],
                            "label": key["label"], "files": [],
                            "seconds": 0.0})
        for entry in remaining:
            bucket = min(buckets, key=lambda item: item["seconds"])
            bucket["files"].append(entry["key"])
            bucket["seconds"] = round(
                bucket["seconds"] + max(0.0, float(entry.get("seconds") or 0)), 3)
        paid_groups = [bucket for bucket in buckets if bucket["files"]]
    unassigned = [] if paid_groups else [entry["key"] for entry in remaining]
    return {
        "groups": free_groups,
        "paid": paid_groups,
        "unassigned": unassigned,
        "free_seconds": round(sum(group["seconds"] for group in free_groups), 3),
        "paid_seconds": round(sum(group["seconds"] for group in paid_groups), 3),
    }


def split_audio_ranges(path, duration, cuts, segment_dir):
    """파일을 cuts(로컬 초)에서 여러 조각으로 나눕니다. copy 실패 시 재인코딩합니다."""
    ffmpeg = app.find_tool("ffmpeg")
    if not ffmpeg:
        app.fail("긴 파일 분할에는 ffmpeg가 필요합니다.")
    segment_dir.mkdir(parents=True, exist_ok=True)
    boundaries = [0.0] + sorted(
        cut for cut in cuts if 0.5 < cut < duration - 0.5) + [duration]
    parts = []
    for index in range(1, len(boundaries)):
        start = boundaries[index - 1]
        length = boundaries[index] - start
        if length <= 0.5:
            continue
        output = segment_dir / f"{path.stem}.part{index:03d}{path.suffix}"
        base = [ffmpeg, "-hide_banner", "-loglevel", "error", "-nostdin", "-n",
                "-ss", f"{start:.3f}", "-t", f"{length:.3f}", "-i", str(path),
                "-map", "0:a:0", "-avoid_negative_ts", "make_zero"]
        copy_command = base + ["-c", "copy", str(output)]
        result = subprocess.run(copy_command, check=False, capture_output=True,
                                text=True, encoding="utf-8", errors="replace")
        if result.returncode != 0 or not output.exists() or output.stat().st_size == 0:
            encode_command = base + ["-c:a", "aac", "-b:a", "128k",
                                     "-movflags", "+faststart", str(output)]
            result = subprocess.run(encode_command, check=False,
                                    capture_output=True, text=True,
                                    encoding="utf-8", errors="replace")
        if result.returncode != 0 or not output.exists() or output.stat().st_size == 0:
            app.fail(f"오디오 분할 실패: {path.name} ({start:.0f}s 부근)\n"
                 f"{(result.stderr or '').strip()}")
        actual = app.probe_duration(output) or length
        parts.append((output, actual))
    return parts


def split_audio_for_live(path, duration, cap_seconds, segment_dir):
    """cap을 넘는 파일을 ffmpeg로 나눕니다. copy 실패 시 재인코딩합니다."""
    cuts = []
    start = cap_seconds
    while start < duration - 0.5:
        cuts.append(start)
        start += cap_seconds
    return app.split_audio_ranges(path, duration, cuts, segment_dir)


def prepare_live(folder, model=app.DEFAULT_MODEL, chunk_seconds=app.LIVE_CHUNK_SECONDS):
    """live 작업 상태를 만들고, 긴 파일은 나눈 뒤 130분 그룹으로 묶습니다."""
    folder = Path(folder)
    state_path = app.live_state_path(folder)
    if state_path.exists():
        return app.load_live_state(folder)
    if app.needs_conversion(folder):
        app.log("M4A가 없어 MP4를 먼저 변환합니다.")
        app.convert(folder)
    audios, pdf = app.list_media(folder)
    segment_dir = app.live_segment_dir(folder)
    if segment_dir.exists():
        shutil.rmtree(segment_dir)
    entries = []
    for audio_index, audio in enumerate(audios, 1):
        duration = app.probe_duration(audio)
        if duration is None or duration <= 0:
            app.fail(f"live 모드에는 ffprobe가 필요합니다: {audio.name}")
        if duration > chunk_seconds + 1.0:
            parts = app.split_audio_for_live(audio, duration, chunk_seconds, segment_dir)
            app.log(f"live 분할: {audio.name} → {len(parts)}개")
            for part_index, (part, length) in enumerate(parts, 1):
                entry = app.snapshot(part, f"audio_{audio_index:03d}_{part_index:03d}")
                entry["name"] = part.relative_to(folder).as_posix()
                entry["seconds"] = round(length, 3)
                entry["source"] = "segment"
                entry["parent"] = audio.name
                entries.append(entry)
        else:
            entry = app.snapshot(audio, f"audio_{audio_index:03d}")
            entry["seconds"] = round(duration, 3)
            entry["source"] = "original"
            entries.append(entry)
    if not entries:
        app.fail("live로 전사할 오디오가 없습니다.")
    groups = app.plan_live_groups(entries, chunk_seconds)
    state = {
        "mode": "live",
        "model": model,
        "created": time.time(),
        "files": entries,
        "groups": groups,
        "pdf": app.snapshot(pdf) if pdf else None,
    }
    app.save(state_path, state)
    return state


def prepare_mixed(folder, model=app.DEFAULT_MODEL, free_keys=None, paid_keys=None):
    """무료 live + 유료 Batch 혼합 작업을 준비합니다.

    무료 키 한도 안에 들어오는 파일은 live 그룹으로, 남는 파일은 Batch 그룹으로
    나눕니다. 긴 파일은 무료 한도(기본 130분)에 맞춰 먼저 자릅니다.
    """
    folder = Path(folder)
    live_path = app.live_state_path(folder)
    if live_path.exists():
        state = app.load_live_state(folder)
        if not state or state.get("mode") != "mixed":
            app.fail("이미 다른 live 작업 기록이 있습니다. 새 작업으로 시작하세요.")
        return state
    if app.state_path(folder).exists():
        app.fail("이미 Batch 작업 기록이 있습니다. 새 작업으로 시작하세요.")
    free_keys = list(free_keys or [])
    paid_keys = list(paid_keys or [])
    if not free_keys and not paid_keys:
        app.fail("등록된 API 키가 없습니다. 설정에서 무료/유료 키를 추가하세요.")
    if app.needs_conversion(folder):
        app.log("M4A가 없어 MP4를 먼저 변환합니다.")
        app.convert(folder)
    audios, pdf = app.list_media(folder)
    if free_keys:
        chunk_seconds = min(
            max(1, int(key.get("limit_minutes") or app.DEFAULT_FREE_MINUTES)) * 60
            for key in free_keys)
        chunk_seconds = min(chunk_seconds, app.LIVE_CHUNK_SECONDS)
    else:
        chunk_seconds = 24 * 3600
    segment_dir = app.live_segment_dir(folder)
    if segment_dir.exists():
        shutil.rmtree(segment_dir)
    entries = []
    for audio_index, audio in enumerate(audios, 1):
        duration = app.probe_duration(audio)
        if duration is None or duration <= 0:
            app.fail(f"혼합 전사에는 ffprobe가 필요합니다: {audio.name}")
        if duration > chunk_seconds + 1.0:
            parts = app.split_audio_for_live(audio, duration, chunk_seconds, segment_dir)
            app.log(f"분할: {audio.name} → {len(parts)}개")
            for part_index, (part, length) in enumerate(parts, 1):
                entry = app.snapshot(part, f"audio_{audio_index:03d}_{part_index:03d}")
                entry["name"] = part.relative_to(folder).as_posix()
                entry["seconds"] = round(length, 3)
                entry["source"] = "segment"
                entry["parent"] = audio.name
                entries.append(entry)
        else:
            entry = app.snapshot(audio, f"audio_{audio_index:03d}")
            entry["seconds"] = round(duration, 3)
            entry["source"] = "original"
            entries.append(entry)
    if not entries:
        app.fail("전사할 오디오가 없습니다.")
    plan = app.allocate_mixed_entries(entries, free_keys, paid_keys, chunk_seconds)
    if plan["unassigned"]:
        unassigned = sum(entry["seconds"] for entry in entries
                         if entry["key"] in set(plan["unassigned"]))
        if segment_dir.exists():
            shutil.rmtree(segment_dir)
        app.fail(f"무료 키 한도 밖의 {app.duration_text(unassigned)}을 처리할 유료 키가 없습니다. "
             "유료 키를 추가하거나 l(무료 live 전체)로 실행하세요.")
    state = {
        "mode": "mixed",
        "model": model,
        "created": time.time(),
        "chunk_seconds": chunk_seconds,
        "files": entries,
        "groups": plan["groups"],
        "paid": plan["paid"],
        "free_seconds": plan["free_seconds"],
        "paid_seconds": plan["paid_seconds"],
        "pdf": app.snapshot(pdf) if pdf else None,
        "pdfs": {},
    }
    app.save(live_path, state)
    return state


def mixed_plan_lines(state):
    lines = []
    for group in state.get("groups") or []:
        lines.append(f"무료 {group['label']}  {app.duration_text(group['seconds'])} · live")
    for group in state.get("paid") or []:
        lines.append(f"유료 {group['label']}  {app.duration_text(group['seconds'])} · Batch")
    return lines or ["처리할 오디오가 없습니다."]


def mixed_preview_lines(folder, model=app.DEFAULT_MODEL, paid_keys=None):
    """시작 화면용 계획. prepare_mixed 와 같은 규칙으로 미리 계산합니다."""
    free_keys = app.key_entries_for_tier("free")
    if paid_keys is None:
        paid_keys = app.key_entries_for_tier("paid")
    if not free_keys:
        return []
    chunk_seconds = min(
        min(max(1, int(key.get("limit_minutes") or app.DEFAULT_FREE_MINUTES)) * 60
            for key in free_keys), app.LIVE_CHUNK_SECONDS)
    files = app.estimate_files(Path(folder))
    if not files:
        return []
    entries = []
    for file_index, path in enumerate(files, 1):
        duration = app.probe_duration(path)
        if duration is None or duration <= 0:
            return []
        if duration > chunk_seconds + 1.0:
            start = 0.0
            part = 1
            while start < duration - 0.5:
                length = min(chunk_seconds, duration - start)
                entries.append({"key": f"preview_{file_index}_{part}",
                                "seconds": length})
                start += length
                part += 1
        else:
            entries.append({"key": f"preview_{file_index}",
                            "seconds": duration})
    plan = app.allocate_mixed_entries(entries, free_keys, paid_keys,
                                  chunk_seconds)
    free_totals = {}
    for group in plan["groups"]:
        free_totals[group["label"]] = (
            free_totals.get(group["label"], 0.0) + group["seconds"])
    free_text = " · ".join(f"{label} {app.duration_text(seconds)}"
                           for label, seconds in free_totals.items())
    lines = [f"무료 {free_text}"]
    if plan["unassigned"]:
        remaining = sum(entry["seconds"] for entry in entries
                        if entry["key"] in set(plan["unassigned"]))
        lines.append(f"남는 {app.duration_text(remaining)} · 유료 키 필요")
    elif plan["paid_seconds"]:
        paid_totals = {}
        for group in plan["paid"]:
            paid_totals[group["label"]] = (
                paid_totals.get(group["label"], 0.0) + group["seconds"])
        paid_text = " · ".join(f"{label} {app.duration_text(seconds)}"
                               for label, seconds in paid_totals.items())
        paid_tokens = int(plan["paid_seconds"] * app.AUDIO_TOKENS_PER_SECOND)
        output_tokens = int(plan["paid_seconds"]
                            * sum(app.OUTPUT_TOKENS_PER_SECOND) / 2)
        cost = app.price(paid_tokens, 0, output_tokens, model, True)
        lines.append(f"유료 Batch {paid_text} · 약 {app.money(cost)}")
    else:
        lines.append("전체를 무료 키로 처리합니다 · Batch 없음")
    return lines

def ensure_live_uploads(folder, state, client):
    """live 그룹에 필요한 파일을 Files API에 올립니다. 47시간 지난 URI는 갱신합니다."""
    folder = Path(folder)
    deadline = 47 * 3600
    for entry in state.get("files") or []:
        uploaded_at = entry.get("uploaded_at")
        if (entry.get("uri") and isinstance(uploaded_at, (int, float))
                and time.time() - uploaded_at < deadline):
            continue
        path = folder / entry["name"]
        if not path.exists():
            app.fail(f"live 파일이 없습니다: {entry['name']}")
        app.log(f"live 업로드: {Path(entry['name']).name}")
        remote = app.upload_safely(client, path, "audio/m4a",
                               Path(entry["name"]).name)
        if not remote.uri or not remote.name:
            app.fail(f"live 업로드 응답이 불완전합니다: {entry['name']}")
        entry.update(uri=remote.uri, remote_name=remote.name,
                     mime_type="audio/m4a", uploaded_at=time.time())
        app.save(app.live_state_path(folder), state)
    pdf = state.get("pdf")
    if pdf:
        uploaded_at = pdf.get("uploaded_at")
        if not (pdf.get("uri") and isinstance(uploaded_at, (int, float))
                and time.time() - uploaded_at < deadline):
            path = folder / pdf["name"]
            if not path.exists():
                app.fail(f"live PDF가 없습니다: {pdf['name']}")
            app.log(f"live 업로드: {pdf['name']}")
            remote = app.upload_safely(client, path, "application/pdf", pdf["name"])
            if not remote.uri or not remote.name:
                app.fail(f"live PDF 업로드 응답이 불완전합니다: {pdf['name']}")
            pdf.update(uri=remote.uri, remote_name=remote.name,
                       mime_type="application/pdf", uploaded_at=time.time())
            app.save(app.live_state_path(folder), state)
    return state


def live_request_contents(state, group):
    by_key = {entry["key"]: entry for entry in state.get("files") or []}
    parts = [{"text": app.LIVE_PROMPT}]
    pdf = state.get("pdf")
    if pdf and pdf.get("uri"):
        parts.append({"file_data": {
            "file_uri": pdf["uri"], "mime_type": "application/pdf"}})
    for key in group.get("files") or []:
        entry = by_key.get(key)
        if not entry or not entry.get("uri"):
            app.fail(f"live 파일 업로드가 없습니다: {key}")
        parts.append({"file_data": {
            "file_uri": entry["uri"], "mime_type": "audio/m4a"}})
    return [{"role": "user", "parts": parts}]


def live_generation_config():
    from google.genai import types
    config = {"max_output_tokens": 65536,
              "thinking_config": {"thinking_level": "low"}}
    if "thinking_level" not in getattr(types.ThinkingConfig, "model_fields", {}):
        thinking = config.pop("thinking_config")
        config["http_options"] = {"extra_body": {"generationConfig": {
            "thinkingConfig": thinking}}}
    return config


@contextlib.contextmanager
def live_progress(label, started=None):
    """TUI 상태 줄에 경과 시간을 계속 갱신합니다. CLI 로그에는 영향이 없습니다."""
    if app._logger is not app.live_status or not app.vt_supported():
        yield
        return
    stop = threading.Event()
    base = started if started is not None else time.monotonic()

    def run():
        frames = app.spinner_frames()
        index = 0
        while not stop.wait(0.4):
            elapsed = max(0, int(time.monotonic() - base))
            if elapsed >= 3600:
                clock = f"{elapsed // 3600}시간 {(elapsed % 3600) // 60:02d}분"
            elif elapsed >= 60:
                clock = f"{elapsed // 60}분 {elapsed % 60:02d}초"
            else:
                clock = f"{elapsed}초"
            try:
                app.live_status(f"{label} · {clock} {frames[index % len(frames)]}")
            except (OSError, ValueError):
                return
            index += 1

    thread = threading.Thread(target=run, name="nalmeok-status", daemon=True)
    thread.start()
    try:
        yield
    finally:
        stop.set()
        thread.join(timeout=1)


def live_gap_seconds(seconds, minimum=app.LIVE_GAP_SECONDS):
    """무료 등급 RPM/TPM을 넘지 않는 요청 시작 간격(초).

    속도 제한은 오디오 1초를 32토큰으로 보고 프롬프트 여유 4,000토큰을 더해
    계산합니다. 60초 창에 들어갈 수 있는 요청 수와 RPM 5를 함께 보고 간격을
    정합니다. 사용자가 더 큰 값을 지정하면 그 값을 존중합니다.
    """
    tokens = int(max(0.0, float(seconds or 0)) *
                 app.RATE_TOKENS_PER_SECOND) + 4_000
    per_minute = max(1, app.FREE_TPM // max(1, tokens))
    tpm_gap = 60.0 / per_minute
    rpm_gap = 60.0 / app.FREE_RPM
    return max(float(minimum or 0), rpm_gap, tpm_gap)


def response_text_length(response):
    """응답 텍스트 길이. SDK 버전에 따라 text 접근이 실패해도 진행 로그는 남깁니다."""
    try:
        return len(response.text or "")
    except (AttributeError, TypeError, ValueError):
        return 0


class FreeKeysExhausted(RuntimeError):
    """이번 실행에서 모든 무료 키가 429로 제한되었습니다."""


def live_generate(client, model, contents, gap, *, rotate_429=False):
    from google.genai.errors import APIError
    for attempt in (1, 2):
        try:
            return client.models.generate_content(
                model=model, contents=contents, config=app.live_generation_config())
        except APIError as exc:
            if exc.code == 429 and rotate_429:
                raise
            detail = str(exc).lower()
            daily = "perday" in detail or "per day" in detail or "rpd" in detail
            if exc.code == 429 and daily:
                app.log("live 일일 한도를 다 쓴 것 같습니다. 내일 이어서 하세요.")
                raise
            if attempt == 2 or exc.code not in (429, 500, 502, 503, 504):
                raise
            app.log(f"live API 오류 {exc.code} · {gap}초 후 1회 재시도")
            if gap:
                time.sleep(gap)
    app.fail("live 요청에 실패했습니다.")


def save_live_row(folder, state, key, row):
    rows = app.live_row_map(folder)
    rows[key] = row
    ordered = []
    for group in state.get("groups") or []:
        group_key = group.get("key")
        if group_key in rows:
            ordered.append(rows[group_key])
    app.atomic_write_text(
        app.live_result_path(folder),
        "".join(json.dumps(item, ensure_ascii=False) + "\n" for item in ordered),
    )


def live_transcribe(folder, client, model=None, gap=app.LIVE_GAP_SECONDS,
                    chunk_seconds=app.LIVE_CHUNK_SECONDS):
    """무료 live 전사. 130분 그룹을 순차 요청하고, 그룹별로 이어서 합니다."""
    folder = Path(folder).resolve()
    with app.job_lock(folder, "live"):
        return app._live_transcribe(folder, client, model, gap, chunk_seconds)


def _live_transcribe(folder, client, model=None, gap=app.LIVE_GAP_SECONDS,
                     chunk_seconds=app.LIVE_CHUNK_SECONDS):
    folder = Path(folder).resolve()
    state = app.prepare_live(folder, model or app.DEFAULT_MODEL, chunk_seconds)
    model = state.get("model") or model or app.DEFAULT_MODEL
    with app.live_progress("live 파일 업로드 확인 중"):
        state = app.ensure_live_uploads(folder, state, client)
    groups = state.get("groups") or []
    if not groups:
        app.fail("live 그룹이 없습니다. 새 작업을 시작하세요.")
    rows = app.live_row_map(folder)
    last_start = None
    for index, group in enumerate(groups, 1):
        key = group.get("key")
        if not key or not group.get("files"):
            app.fail(f"live 그룹 정보가 올바르지 않습니다: {index}")
        if key in rows:
            try:
                app.merge_jsonl(json.dumps(rows[key]), [{"key": key}])
            except RuntimeError:
                rows.pop(key, None)
            else:
                app.log(f"live 건너뜀 ({index}/{len(groups)}): 완료된 그룹")
                continue
        effective_gap = app.live_gap_seconds(group.get("seconds") or 0, gap)
        if last_start is not None:
            remaining = effective_gap - (time.monotonic() - last_start)
            if remaining > 0:
                app.log(f"live 대기 {remaining:.0f}초 "
                    f"(자동 간격 {effective_gap:.0f}초)")
                time.sleep(remaining)
        contents = app.live_request_contents(state, group)
        app.log(f"live 요청 ({index}/{len(groups)}): "
            f"{app.duration_text(group.get('seconds') or 0)} · "
            f"{len(group['files'])}개 파일")
        last_start = time.monotonic()
        started = time.monotonic()
        with app.live_progress(f"live 응답 대기 ({index}/{len(groups)})"):
            response = app.live_generate(client, model, contents, effective_gap)
        elapsed = time.monotonic() - started
        chars = app.response_text_length(response)
        app.log(f"live 응답 받음 ({index}/{len(groups)}): "
            f"{elapsed:.0f}초 · {chars:,}자")
        row = {"key": key, "response": response.model_dump(
            mode="json", by_alias=True, exclude_none=True)}
        app.merge_jsonl(json.dumps(row), [{"key": key}])
        rows[key] = row
        app.save_live_row(folder, state, key, row)
        app.log(f"live 저장 완료 ({index}/{len(groups)})")
    missing = [group["key"] for group in groups if group["key"] not in rows]
    if missing:
        app.fail("live 결과가 누락되었습니다: " + ", ".join(missing))
    raw = "\n".join(json.dumps(rows[group["key"]], ensure_ascii=False)
                    for group in groups)
    app.log("결과 병합 중…")
    merged = app.merge_jsonl(raw, [{"key": group["key"]} for group in groups])
    output = app.output_path(folder)
    if output.exists():
        if output.read_text(encoding="utf-8") == merged:
            app.log(f"이미 저장된 파일과 동일합니다: {output}")
            return output
        app.fail(f"기존 Markdown을 보존합니다. 파일을 확인하세요: {output}")
    app.atomic_write_text(output, merged)
    app.log(f"live 전사 완료: {output} ({len(merged):,}자)")
    app.log("")
    for line in app.cost_lines(folder, state):
        app.log(line)
    return output


def mixed_request_path(folder, group):
    return Path(folder) / f"{app.MIXED_REQUEST_PREFIX}.{app._mixed_tag(group)}.jsonl"


def _upload_record_valid(record):
    return (isinstance(record, dict) and record.get("uri")
            and isinstance(record.get("uploaded_at"), (int, float))
            and time.time() - record["uploaded_at"] < 47 * 3600)


def ensure_mixed_uploads(folder, state, client, key_id, unit_keys):
    """한 키(프로젝트)에 필요한 오디오와 PDF를 올리고 URI를 상태에 저장합니다."""
    folder = Path(folder)
    units = {entry["key"]: entry for entry in state.get("files") or []}
    for unit_key in unit_keys:
        entry = units.get(unit_key)
        if not entry:
            app.fail(f"혼합 작업 파일 정보가 없습니다: {unit_key}")
        records = entry.setdefault("uris", {})
        if app._upload_record_valid(records.get(key_id)):
            continue
        path = folder / entry["name"]
        if not path.exists():
            app.fail(f"혼합 작업 파일이 없습니다: {entry['name']}")
        app.log(f"업로드: {Path(entry['name']).name}")
        remote = app.upload_safely(client, path, "audio/m4a",
                               Path(entry["name"]).name)
        if not remote.uri or not remote.name:
            app.fail(f"업로드 응답이 불완전합니다: {entry['name']}")
        records[key_id] = {"uri": remote.uri, "name": remote.name,
                           "uploaded_at": time.time()}
        app.save(app.live_state_path(folder), state)
    pdf = state.get("pdf")
    if pdf:
        records = state.setdefault("pdfs", {})
        if not app._upload_record_valid(records.get(key_id)):
            path = folder / pdf["name"]
            if not path.exists():
                app.fail(f"PDF가 없습니다: {pdf['name']}")
            app.log(f"업로드: {pdf['name']}")
            remote = app.upload_safely(client, path, "application/pdf", pdf["name"])
            if not remote.uri or not remote.name:
                app.fail(f"PDF 업로드 응답이 불완전합니다: {pdf['name']}")
            records[key_id] = {"uri": remote.uri, "name": remote.name,
                               "uploaded_at": time.time()}
            app.save(app.live_state_path(folder), state)
    return state


def mixed_request_parts(state, key_id, unit_keys, prompt):
    units = {entry["key"]: entry for entry in state.get("files") or []}
    parts = [{"text": prompt}]
    pdf = (state.get("pdfs") or {}).get(key_id)
    if pdf and pdf.get("uri"):
        parts.append({"file_data": {
            "file_uri": pdf["uri"], "mime_type": "application/pdf"}})
    for unit_key in unit_keys:
        entry = units.get(unit_key)
        record = ((entry or {}).get("uris") or {}).get(key_id)
        if not record or not record.get("uri"):
            app.fail(f"업로드된 파일이 없습니다: {unit_key}")
        parts.append({"file_data": {
            "file_uri": record["uri"], "mime_type": "audio/m4a"}})
    return [{"role": "user", "parts": parts}]


def submit_mixed_paid(folder, state, group, client):
    """유료 키 하나에 대한 Batch 를 만들고 상태에 ID를 저장합니다."""
    folder = Path(folder)
    key_id = group["key_id"]
    units = {entry["key"]: entry for entry in state.get("files") or []}
    app.ensure_mixed_uploads(folder, state, client, key_id, group["files"])
    requests = []
    for unit_key in group["files"]:
        if unit_key not in units:
            app.fail(f"유료 Batch 파일 정보가 없습니다: {unit_key}")
        requests.append({
            "key": unit_key,
            "request": {
                "contents": app.mixed_request_parts(state, key_id, [unit_key], app.PROMPT),
                "generation_config": {
                    "max_output_tokens": 65536,
                    "thinking_config": {"thinking_level": "low"},
                },
            },
        })
    path = app.mixed_request_path(folder, group)
    app.atomic_write_text(path, "".join(
        json.dumps(item, ensure_ascii=False) + "\n" for item in requests))
    uploaded = app.upload_safely(client, path, "jsonl", path.name)
    if not uploaded.name:
        app.fail("Batch 요청 목록 업로드 응답이 불완전합니다.")
    app.log(f"Batch 제출: {group['label']} · {len(group['files'])}개 요청")
    batch = client.batches.create(
        model=state["model"], src=uploaded.name,
        config={"display_name": f"{folder.name} {group['label']}"[:120]})
    if not batch.name:
        app.fail("Batch 생성 응답에서 ID를 받지 못했습니다.")
    group.update(batch_id=batch.name, input_name=uploaded.name,
                 submitted=time.time())
    app.save(app.live_state_path(folder), state)


def wait_mixed_paid(folder, state, group, client, interval=app.POLL_SECONDS):
    """유료 Batch 완료를 기다리고 결과 JSONL을 폴더에 저장합니다."""
    last = None
    started = time.monotonic()
    while True:
        batch = client.batches.get(name=group["batch_id"])
        current = app.batch_state(batch)
        if current != last:
            app.log(f"Batch {group['label']}: {app.format_state(current)}")
            last = current
        if current == "JOB_STATE_SUCCEEDED":
            app.log(f"Batch 결과 다운로드 중: {group['label']}")
            with app.live_progress(
                    f"Batch 결과 다운로드 ({group['label']})", started):
                raw = app.batch_result(batch, client)
            app.result_rows(raw, set(group["files"]))
            path = app.mixed_result_path(folder, group)
            app.atomic_write_text(path, raw)
            group.update(done=True, finished=time.time(),
                         result_name=path.name)
            app.save(app.live_state_path(folder), state)
            app.log(f"Batch 결과 저장 완료: {group['label']}")
            return
        if current in app.TERMINAL_STATES:
            app.fail(f"유료 Batch({group['label']})가 완료되지 않았습니다: "
                 f"{app.format_state(current)}")
        with app.live_progress(
                f"Batch {group['label']} · {app.format_state(current)}", started):
            time.sleep(interval)


def mixed_rows(folder, state):
    """완료된 무료 그룹과 유료 Batch 결과 행을 각각 돌려줍니다."""
    free_rows = app.live_row_map(folder)
    paid_rows = {}
    for group in state.get("paid") or []:
        path = app.mixed_result_path(folder, group)
        if not path.exists():
            continue
        paid_rows.update(app.result_rows(path.read_text(encoding="utf-8"),
                                     set(group["files"])))
    return free_rows, paid_rows


def merged_mixed_text(state, free_rows, paid_rows):
    """원래 오디오 순서대로 무료 live와 유료 Batch 결과를 합칩니다."""
    texts = []
    for group in state.get("groups") or []:
        row = free_rows.get(group["key"])
        if row is None:
            app.fail(f"무료 그룹 결과가 없습니다: {group['label']}")
        text = app.merge_jsonl(json.dumps(row), [{"key": group["key"]}])
        if text.strip():
            texts.append(text.strip())
    for entry in state.get("files") or []:
        row = paid_rows.get(entry["key"])
        if row is None:
            continue
        text = app.merge_jsonl(json.dumps(row), [entry])
        if text.strip():
            texts.append(text.strip())
    return "\n\n".join(texts) + "\n"


def mixed_transcribe(folder, model=None, gap=app.LIVE_GAP_SECONDS,
                     interval=app.POLL_SECONDS, include_paid=True):
    """무료 키를 먼저 쓰고, include_paid 이면 남는 오디오를 Batch로 보냅니다."""
    folder = Path(folder).resolve()
    with app.job_lock(folder, "혼합 전사"):
        return app._mixed_transcribe(folder, model, gap, interval, include_paid)


def _mixed_transcribe(folder, model=None, gap=app.LIVE_GAP_SECONDS,
                      interval=app.POLL_SECONDS, include_paid=True):
    clients = {}
    try:
        return app._mixed_run(folder, model, gap, interval, clients, include_paid)
    finally:
        for client in clients.values():
            with contextlib.suppress(Exception):
                client.close()


def _mixed_run(folder, model, gap, interval, clients, include_paid=True):
    state_path = app.live_state_path(folder)
    if state_path.exists():
        state = app.load_live_state(folder)
        if not state or state.get("mode") != "mixed":
            app.fail("혼합 작업 상태가 아닙니다.")
        if state.get("paid") and not include_paid:
            app.fail("이미 유료 Batch가 포함된 작업입니다. 이어서 실행하세요.")
    else:
        free_keys = app.key_entries_for_tier("free")
        paid_keys = app.key_entries_for_tier("paid") if include_paid else []
        state = app.prepare_mixed(folder, model or app.DEFAULT_MODEL,
                              free_keys, paid_keys)
    entries = {entry["id"]: entry for entry in app.key_entries()}
    # 환경변수와 같은 키를 저장해 둔 경우 key_entries 는 하나만 남기므로,
    # 예전 상태의 key_id 로도 이어서 할 수 있게 저장 항목을 보강합니다.
    for entry in app.saved_key_entries():
        entries.setdefault(entry["id"], entry)

    def client_for(key_id):
        entry = entries.get(key_id)
        if entry is None:
            app.fail(f"등록된 키를 찾을 수 없습니다: {key_id}. 설정에서 키를 확인하세요.")
        if key_id not in clients:
            clients[key_id] = app.get_client(entry["api_key"])
        return clients[key_id], entry

    if state.get("paid_seconds"):
        app.log("계획: " + " · ".join(app.mixed_plan_lines(state)))
        app.log("")
    if state.get("paid") and not any(
            entry["tier"] == "paid" for entry in entries.values()):
        app.fail("유료 Batch에 사용할 유료 키가 없습니다.")
    rows = app.live_row_map(folder)
    last_start = {}
    exhausted = set()
    free_ids = [identifier for identifier, entry in entries.items()
                if entry["tier"] == "free"]
    for index, group in enumerate(state.get("groups") or [], 1):
        if group["key"] in rows:
            app.log(f"live 건너뜀 ({index}/{len(state['groups'])}): 완료된 그룹")
            continue
        candidates = list(free_ids)
        if group["key_id"] in candidates:
            offset = candidates.index(group["key_id"])
            candidates = candidates[offset:] + candidates[:offset]
        for key_id in candidates:
            if key_id in exhausted:
                continue
            client, entry = client_for(key_id)
            group.update(key_id=key_id, label=entry["label"])
            app.save(state_path, state)
            try:
                with app.live_progress(f"파일 업로드 ({entry['label']})"):
                    app.ensure_mixed_uploads(folder, state, client, group["key_id"],
                                         group["files"])
                previous = last_start.get(group["key_id"])
                effective_gap = app.live_gap_seconds(group.get("seconds") or 0, gap)
                if previous is not None:
                    remaining = effective_gap - (time.monotonic() - previous)
                    if remaining > 0:
                        app.log(f"live 대기 {remaining:.0f}초 "
                            f"(같은 키 자동 간격 {effective_gap:.0f}초)")
                        time.sleep(remaining)
                app.log(f"live 요청 ({index}/{len(state['groups'])}): "
                    f"{entry['label']} · {app.duration_text(group['seconds'])} · "
                    f"{len(group['files'])}개 파일")
                last_start[group["key_id"]] = time.monotonic()
                contents = app.mixed_request_parts(state, group["key_id"],
                                               group["files"], app.LIVE_PROMPT)
                started = time.monotonic()
                with app.live_progress(
                        f"live 응답 대기 ({index}/{len(state['groups'])})"):
                    response = app.live_generate(client, state["model"], contents,
                                             effective_gap, rotate_429=True)
                elapsed = time.monotonic() - started
                chars = app.response_text_length(response)
                app.log(f"live 응답 받음 ({index}/{len(state['groups'])}): "
                    f"{elapsed:.0f}초 · {chars:,}자")
            except Exception as exc:
                api_error = app._api_error_class()
                if api_error is None or not isinstance(exc, api_error) or exc.code != 429:
                    raise
                exhausted.add(key_id)
                app.log(f"무료 키 {entry['label']} 제한(429) · 다음 무료 키 시도")
                continue
            break
        else:
            message = ("모든 무료 키가 429로 제한되었습니다. 완료된 그룹은 저장했습니다. "
                       "나중에 다시 실행하면 이어서 처리합니다.")
            app.notify("날먹 · 무료 키 제한", f"{folder.name}: {message}")
            raise app.FreeKeysExhausted(message)
        row = {"key": group["key"], "tier": "free",
               "label": entry["label"],
               "response": response.model_dump(
                   mode="json", by_alias=True, exclude_none=True)}
        app.merge_jsonl(json.dumps(row), [{"key": group["key"]}])
        app.save_live_row(folder, state, group["key"], row)
        rows[group["key"]] = row
        app.log(f"live 저장 완료 ({index}/{len(state['groups'])})")
    if state.get("paid"):
        app.log("")
        for group in state["paid"]:
            if group.get("done"):
                continue
            if not group.get("batch_id"):
                client, _ = client_for(group["key_id"])
                app.submit_mixed_paid(folder, state, group, client)
        app.log("")
        for group in state["paid"]:
            if group.get("done"):
                continue
            client, _ = client_for(group["key_id"])
            app.wait_mixed_paid(folder, state, group, client, interval)
    app.log("결과 병합 중…")
    free_rows, paid_rows = app.mixed_rows(folder, state)
    merged = app.merged_mixed_text(state, free_rows, paid_rows)
    output = app.output_path(folder)
    if output.exists():
        if output.read_text(encoding="utf-8") == merged:
            app.log(f"이미 저장된 파일과 동일합니다: {output}")
            return output
        app.fail(f"기존 Markdown을 보존합니다. 파일을 확인하세요: {output}")
    app.atomic_write_text(output, merged)
    state["finished"] = time.time()
    app.save(state_path, state)
    app.log(f"혼합 전사 완료: {output} ({len(merged):,}자)")
    app.log("")
    for line in app.mixed_cost_lines(folder, state):
        app.log(line)
    return output


def wait_target(folder, job):
    """대기할 Batch ID. 처리 중인 재시도 Batch가 있으면 그쪽을 우선합니다."""
    path = folder / app.RECOVERY_FILE
    if path.exists():
        try:
            recovery = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, ValueError):
            recovery = {}
        if (isinstance(recovery, dict)
                and recovery.get("batch_id") == job.get("batch_id")
                and recovery.get("pending_batch")):
            return recovery["pending_batch"]["batch_id"]
    return job.get("batch_id")


def notify(title, message):
    """완료를 알립니다. 터미널 벨 + macOS 알림(가능할 때만)."""
    if sys.stdout.isatty():
        print("\a", end="", flush=True)
    if sys.platform == "darwin":
        script = (f"display notification {json.dumps(message)} "
                  f"with title {json.dumps(title)}")
        try:
            subprocess.run(["osascript", "-e", script],
                           check=False, capture_output=True)
        except OSError:
            pass


def wait_for_completion(folder, job, client, interval=app.POLL_SECONDS,
                        track=False, allow_detach=False):
    """Batch가 끝날 때까지 폴링합니다.

    track=True 면 진행 상태를 폴더 안에 기록해 대시보드가 읽을 수 있게 합니다.
    allow_detach=True 면 b/Esc 로 빠져나와 백그라운드 대기로 전환할 수 있습니다.
    반환값은 (batch, state, detached) 입니다.
    """
    target = app.wait_target(folder, job)
    if not target:
        app.fail("아직 Batch가 제출되지 않았습니다. upload를 다시 실행하세요.")
    live = app.vt_supported()
    started = time.monotonic()
    started_wall = time.time()
    last = None
    frames = app.spinner_frames()
    if track:
        app.write_watch_status(folder, reset=True, state="STARTING", label="대기 시작",
                           started=started_wall)
    with app.cbreak_keys() as keys:
        view = {"state": "JOB_STATE_PENDING", "elapsed": 0, "frame": 0}

        def draw():
            if not live:
                return
            view["frame"] += 1
            frame = frames[view["frame"] % len(frames)]
            hint = "b 백그라운드" if (keys and allow_detach) else "Ctrl+C 중단"
            sys.stdout.write(
                f"\r\033[K  {app.accent(frame)} {app.format_state(view['state'])}"
                f"   {app.clock_text(view['elapsed'])}   {app.pulse_bar(14, view['frame'])}"
                f"   {app.dim(hint)}")
            sys.stdout.flush()

        while True:
            batch = client.batches.get(name=target)
            state = app.batch_state(batch)
            elapsed = int(time.monotonic() - started)
            clock = f"{elapsed // 60}분 {elapsed % 60:02d}초"
            view["state"], view["elapsed"] = state, elapsed
            if track:
                app.write_watch_status(folder, state=state, label=app.format_state(state),
                                   elapsed=elapsed, started=started_wall)
            if live:
                draw()
            elif state != last:
                app.log(f"{app.format_state(state)} · 경과 {clock}")
            last = state
            if state in app.TERMINAL_STATES:
                if live:
                    sys.stdout.write(f"\r\033[K  {app.accent('●')} "
                                     f"{app.format_state(state)} · 총 {app.clock_text(elapsed)}\n")
                    sys.stdout.flush()
                if track:
                    app.write_watch_status(folder, state=state, label=app.format_state(state),
                                       elapsed=elapsed, finished=time.time())
                return batch, state, False
            pressed = app._wait_for_key(interval, allow_detach and keys, tick=draw)
            if pressed in ("b", "esc"):
                if live:
                    sys.stdout.write("\n")
                    sys.stdout.flush()
                if track:
                    app.write_watch_status(folder, state=state, label=app.format_state(state),
                                       elapsed=elapsed, detached=True)
                return batch, state, True


def background_wait(folder, retry="none", attempts=3):
    """분리된 프로세스에서 완료를 기다리고 결과까지 병합합니다."""
    folder = Path(folder).resolve()
    client = None
    try:
        job = app.load(folder)
        client = app.get_client(None)
        _, state, _ = app.wait_for_completion(folder, job, client, track=True)
        if state != "JOB_STATE_SUCCEEDED":
            app.fail(f"작업이 완료되지 않았습니다: {app.format_state(state)}")
        app.notify("날먹", f"{folder.name} 전사 완료")
        app.download(folder, app.load(folder), client, retry, attempts, validate=False)
        app.write_watch_status(folder, reset=True, state="DONE", label="완료",
                           finished=time.time())
    except BaseException as exc:
        app.write_watch_status(folder, state="ERROR", label="오류",
                           error=str(exc), finished=time.time())
        raise
    finally:
        if client is not None:
            client.close()
        try:
            if app.watch_pid_path(folder).read_text(encoding="utf-8").strip() == str(os.getpid()):
                app.watch_pid_path(folder).unlink()
        except OSError:
            pass


def check_key(key):
    """과금 없이 키 유효성만 확인합니다. (상태, 메시지)를 돌려줍니다."""
    try:
        client = app.get_client(key)
    except (RuntimeError, OSError, ValueError) as exc:
        return "error", str(exc)
    try:
        from google.genai.errors import APIError
        try:
            for _ in client.models.list():
                break
        except APIError as exc:
            if exc.code in (401, 403):
                return "invalid", f"키 오류 ({exc.code})"
            return "warn", f"확인하지 못했습니다 ({exc.code})"
        except Exception as exc:  # noqa: BLE001 - 네트워크 오류도 그대로 진행합니다.
            return "warn", f"확인하지 못했습니다: {exc}"
        return "ok", "키 확인됨"
    finally:
        client.close()


def stop_watcher(folder):
    """백그라운드 대기 프로세스를 종료하고 PID 파일을 정리합니다."""
    path = app.watch_pid_path(folder)
    try:
        pid = int(path.read_text(encoding="utf-8").strip())
    except (OSError, ValueError):
        pid = 0
    status = app.read_watch_status(folder)
    updated = status.get("updated")
    fresh = isinstance(updated, (int, float)) and time.time() - updated < 600
    terminated = False
    if pid > 0 and fresh and app.process_alive(pid):
        try:
            os.kill(pid, signal.SIGTERM)
            terminated = True
        except OSError:
            pass
    try:
        path.unlink()
    except OSError:
        pass
    app.write_watch_status(folder, state="STOPPED", label="감시 중단",
                       finished=time.time(), terminated=terminated)
    return terminated


def _api_error_class():
    try:
        from google.genai.errors import APIError
        return APIError
    except ImportError:
        return None

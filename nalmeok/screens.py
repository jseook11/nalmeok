"""nalmeok.screens."""

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
from . import engine  # noqa: F401
from . import keys  # noqa: F401
from . import media  # noqa: F401
from . import runtime  # noqa: F401
from . import term  # noqa: F401
from . import update  # noqa: F401



def status_bar_line(key=None, extra=""):
    """하단 상태 한 줄을 문자열로 만듭니다."""
    caps = app.capabilities()
    entries = app.key_entries()
    has_key = bool(key or entries or app.current_key())
    label = f"API {app.accent('✓') if has_key else app.warn('✗')}"
    if entries:
        label += f" {app.key_summary(entries)}"
    parts = [label,
             f"ffmpeg {app.accent('✓') if caps['ffmpeg'] else app.warn('✗')}",
             f"SDK {app.accent('✓') if caps['sdk'] else app.warn('✗')}"]
    if extra:
        parts.append(extra)
    return f"  {app.dim(' · '.join(parts))}"


def status_bar(key=None, extra=""):
    """하단 한 줄. 화면마다 키·ffmpeg·SDK 상태를 같은 자리에 보여줍니다."""
    print(app.status_bar_line(key, extra))


def _count_media(folder):
    m4a = mp4 = pdf = 0
    try:
        entries = list(folder.iterdir())
    except OSError:
        return 0, 0, 0
    for path in entries:
        if not path.is_file():
            continue
        suffix = path.suffix.lower()
        if suffix == ".m4a":
            m4a += 1
        elif suffix == ".mp4":
            mp4 += 1
        elif suffix == ".pdf":
            pdf += 1
    return m4a, mp4, pdf


def inspect_folder(folder, light=False):
    """네트워크 없이 폴더의 상태를 한 번에 읽습니다. 메뉴와 대시보드 전용입니다.

    light=True 는 폴더 탐색 목록용으로, 미디어 개수와 작업 흔적만 빠르게 봅니다.
    """
    folder = Path(folder)
    try:
        exists = folder.is_dir()
    except OSError:
        exists = False
    facts = {
        "folder": folder, "exists": exists,
        "m4a": 0, "mp4": 0, "pdf": 0, "media": 0,
        "needs_convert": False, "job": False, "submitted": False,
        "result": False, "output": False, "recovery": False,
        "pending_retry": False, "corrupt": False, "changed": False,
        "reason": "", "watcher": False, "watch": {}, "stage": "missing",
        "live": False, "live_state": {}, "live_groups": 0,
        "live_completed": 0, "live_result": False, "live_broken": False,
        "mixed": False, "paid_groups": 0, "paid_done": 0,
    }
    if not facts["exists"]:
        return facts
    m4a, mp4, pdf = app._count_media(folder)
    facts.update(m4a=m4a, mp4=mp4, pdf=pdf, media=m4a + mp4)
    if light:
        facts["job"] = (app.safe_exists(app.state_path(folder))
                        or app.safe_exists(app.live_state_path(folder)))
        facts["output"] = app.safe_exists(app.output_path(folder))
        if facts["output"]:
            facts["stage"] = "done"
        elif facts["job"]:
            facts["stage"] = "prepared"
        elif facts["media"]:
            facts["stage"] = "ready"
        else:
            facts["stage"] = "empty"
        return facts
    try:
        facts["needs_convert"] = app.needs_conversion(folder)
    except OSError:
        facts["needs_convert"] = False
    facts["output"] = app.safe_exists(app.output_path(folder))
    facts["result"] = app.safe_exists(folder / app.RESULT_FILE)
    facts["watcher"] = app.watcher_running(folder)
    facts["watch"] = app.read_watch_status(folder)

    live_path = folder / app.LIVE_STATE_FILE
    if app.safe_exists(live_path):
        live = app.read_json_object(live_path)
        facts["live"] = True
        if live is None:
            facts["live_broken"] = True
        else:
            facts["live_state"] = live
            groups = [group for group in (live.get("groups") or [])
                      if isinstance(group, dict) and group.get("key")]
            rows = app.live_row_map(folder)
            facts["live_groups"] = len(groups)
            facts["live_completed"] = sum(
                1 for group in groups if group["key"] in rows)
            facts["mixed"] = live.get("mode") == "mixed"
            paid = []
            if facts["mixed"]:
                paid = [group for group in (live.get("paid") or [])
                        if isinstance(group, dict) and group.get("key")]
                facts["paid_groups"] = len(paid)
                facts["paid_done"] = sum(1 for group in paid
                                         if group.get("done"))
            facts["live_result"] = (
                facts["live_completed"] == len(groups)
                and facts["paid_done"] == len(paid)
                and (bool(groups) or bool(paid)))

    job = None
    if app.safe_exists(app.state_path(folder)):
        facts["job"] = True
        job = app.read_json_object(app.state_path(folder))
        if job is None:
            facts["corrupt"] = True
        else:
            facts["submitted"] = bool(job.get("batch_id"))
            recovery = app.read_json_object(folder / app.RECOVERY_FILE) or {}
            facts["pending_retry"] = bool(
                recovery.get("batch_id") == job.get("batch_id")
                and recovery.get("pending_batch"))
            facts["recovery"] = bool(recovery.get("results"))
            if job.get("files"):
                try:
                    app.validate_local_files(folder, job)
                except (RuntimeError, OSError, KeyError, TypeError) as exc:
                    facts["changed"] = True
                    facts["reason"] = str(exc)

    if facts["live_broken"]:
        facts["stage"] = "corrupt"
    elif facts["live"]:
        if facts["output"]:
            facts["stage"] = "done"
        elif facts["live_result"]:
            facts["stage"] = "received"
        else:
            facts["stage"] = "live"
    elif facts["corrupt"]:
        facts["stage"] = "corrupt"
    elif facts["job"]:
        if facts["changed"]:
            facts["stage"] = "changed"
        elif not facts["submitted"]:
            facts["stage"] = "prepared"
        elif facts["watcher"]:
            facts["stage"] = "watching"
        elif facts["output"]:
            facts["stage"] = "done"
        elif facts["pending_retry"]:
            facts["stage"] = "submitted"
        elif facts["result"] or facts["recovery"]:
            facts["stage"] = "received"
        else:
            facts["stage"] = "submitted"
    elif facts["media"] == 0:
        facts["stage"] = "empty"
    elif m4a > app.MAX_FILES or pdf > 1:
        facts["stage"] = "invalid"
        facts["reason"] = (f"오디오 {m4a}개 (최대 {app.MAX_FILES}개)"
                           if m4a > app.MAX_FILES else "PDF가 2개 이상입니다")
    else:
        facts["stage"] = "ready"
    return facts


def stage_label(facts):
    stage = facts["stage"]
    if facts.get("mixed") and stage in ("live", "received"):
        has_paid = bool(facts.get("paid_groups"))
        if stage == "received":
            return "혼합 결과 병합 대기" if has_paid else "무료 결과 병합 대기"
        free = (f"무료 {facts.get('live_completed', 0)}/"
                f"{facts.get('live_groups', 0)}")
        paid = ""
        if has_paid:
            paid = (f" · Batch {facts.get('paid_done', 0)}/"
                    f"{facts.get('paid_groups', 0)}")
        return f"{'혼합' if has_paid else '무료'} 전사 · {free}{paid}"
    if stage == "live":
        return (f"live 전사 · {facts.get('live_completed', 0)}/"
                f"{facts.get('live_groups', 0)} 그룹")
    if stage == "watching":
        watch = facts.get("watch") or {}
        label = watch.get("label") or "대기 중"
        elapsed = watch.get("elapsed")
        if isinstance(elapsed, (int, float)):
            clock = (app.clock_text(elapsed) if elapsed < 3600
                     else app.duration_text(elapsed))
            return f"전사 중 · {clock} 경과"
        if watch.get("state") == "STARTING":
            return "전사 중 · 시작 중"
        return f"전사 중 · {label}"
    if stage == "submitted" and facts.get("pending_retry"):
        return "재시도 제출됨"
    label = app.STAGE_LABELS.get(stage, stage)
    watch = facts.get("watch") or {}
    if watch.get("state") == "ERROR":
        return f"{label} · 대기 실패"
    if watch.get("state") == "STOPPED":
        return f"{label} · 감시 중단"
    return label


def folder_summary(folder, facts=None):
    facts = facts or app.inspect_folder(folder)
    parts = [app.stage_label(facts)]
    if facts["m4a"]:
        parts.append(f"오디오 {facts['m4a']}개")
    if facts["mp4"]:
        parts.append(f"MP4 {facts['mp4']}개")
    if facts["pdf"]:
        parts.append("PDF")
    if facts["output"] and facts["stage"] != "done":
        parts.append("결과 있음")
    return " · ".join(parts)


def usage_folders():
    folders = []
    scanned = app.job_folders(Path.cwd(), max_depth=app.JOB_SCAN_MAX_DEPTH,
                              max_dirs=app.JOB_SCAN_MAX_DIRS)
    for path in list(scanned) + app.recent_folders():
        try:
            path = path.resolve()
        except OSError:
            continue
        if path.is_dir() and path not in folders:
            folders.append(path)
    return folders


def dashboard_folders():
    """최근 작업과 현재 폴더 아래의 작업을 최근 순서대로 모읍니다."""
    folders = []

    def add(path):
        try:
            path = Path(path).resolve()
        except OSError:
            return
        if path.is_dir() and path not in folders:
            folders.append(path)

    for path in app.recent_folders():
        add(path)
    extras = []
    for path in app.job_folders(Path.cwd(), max_depth=app.JOB_SCAN_MAX_DEPTH,
                            max_dirs=app.JOB_SCAN_MAX_DIRS):
        if path.resolve() not in folders:
            extras.append(path)
    def mtime(path):
        try:
            return path.stat().st_mtime
        except OSError:
            return 0

    extras.sort(key=mtime, reverse=True)
    for path in extras:
        add(path)
    return folders[:app.RECENT_LIMIT * 2]


def scan_descendant_media(folder, seconds=None, max_depth=None, max_dirs=None):
    """하위 미디어 개수와 작업 흔적을 제한 안에서 셉니다.

    (오디오, MP4, 작업 흔적, 끝까지 봤는지) 를 돌려줍니다. 심볼릭 링크
    디렉터리는 따라가지 않습니다. 시간·깊이·방문 폴더 수를 넘기면 그때까지
    센 값과 False 를 돌려줍니다. Windows 기본값이 더 짧습니다.
    """
    seconds = app.SCAN_SECONDS if seconds is None else seconds
    max_depth = app.SCAN_MAX_DEPTH if max_depth is None else max_depth
    max_dirs = app.SCAN_MAX_DIRS if max_dirs is None else max_dirs
    deadline = time.monotonic() + seconds
    pending = [(Path(folder), 0)]
    m4a = mp4 = 0
    job = False
    visited = 0
    truncated = False
    while pending:
        if time.monotonic() > deadline or visited >= max_dirs:
            return m4a, mp4, job, False
        current, depth = pending.pop()
        visited += 1
        try:
            if app.state_path(current).exists() or app.live_state_path(current).exists():
                job = True
            with os.scandir(current) as scan:
                for item in scan:
                    if item.name.startswith("."):
                        continue
                    if time.monotonic() > deadline:
                        return m4a, mp4, job, False
                    try:
                        if item.is_dir(follow_symlinks=False):
                            if depth >= max_depth:
                                truncated = True
                            elif item.name.casefold() in app.SKIP_SCAN_NAMES:
                                truncated = True
                            else:
                                pending.append((Path(item.path), depth + 1))
                        elif item.is_file():
                            suffix = item.name.lower()
                            if suffix.endswith(".m4a"):
                                m4a += 1
                            elif suffix.endswith(".mp4"):
                                mp4 += 1
                    except OSError:
                        continue
        except OSError:
            continue
    return m4a, mp4, job, not truncated


def nested_summary(m4a, mp4, complete):
    """하위 폴더의 미디어 개수 요약. 다 못 셌으면 '이상'을 붙입니다."""
    parts = []
    if m4a:
        parts.append(f"하위 오디오 {m4a}개" + ("" if complete else " 이상"))
    if mp4:
        parts.append(f"하위 MP4 {mp4}개" + ("" if complete else " 이상"))
    return " · ".join(parts)


def browse_folder(start=None):
    """폴더를 직접 돌아다니며 작업 폴더를 고릅니다. Esc 로 취소합니다."""
    current = Path(start or Path.cwd()).expanduser().resolve()
    while True:
        facts = app.inspect_folder(current)
        read_error = ""
        try:
            subdirs = sorted(
                (p for p in current.iterdir()
                 if p.is_dir() and not p.name.startswith(".")),
                key=lambda p: app.natural_key(p.name))
        except OSError as exc:
            subdirs = []
            read_error = str(exc)
        options = []
        if facts["media"] or facts["job"]:
            options.append(("__select__", "이 폴더 선택",
                            app.folder_summary(current, facts)))
        for index, path in enumerate(subdirs):
            # Windows 홈의 AppData/OneDrive처럼 큰 폴더는 열지 않고 표시만 합니다.
            if (index >= app.BROWSE_DETAIL_LIMIT
                    or path.name.casefold() in app.SKIP_SCAN_NAMES):
                options.append((str(path), path.name + "/", "폴더", False, False))
                continue
            child = app.inspect_folder(path, light=True)
            if child["media"] or child["job"]:
                options.append((str(path), path.name + "/",
                                app.folder_summary(path, child), False, False))
                continue
            if index < app.BROWSE_DESCENDANT_LIMIT:
                m4a, mp4, job, complete = app.scan_descendant_media(path)
            else:
                m4a, mp4, job, complete = 0, 0, False, False
            usable = bool(m4a or mp4 or job or not complete)
            desc = app.nested_summary(m4a, mp4, complete) if m4a or mp4 else (
                "하위 폴더" if usable else "폴더")
            options.append((str(path), path.name + "/", desc,
                            not usable, not usable))
        if current.parent != current:
            options.append(("__up__", ".. 상위 폴더", app.short_path(current.parent)))
        options += [
            ("__path__", "경로 직접 입력", "폴더나 MP4/M4A 파일을 붙여넣기"),
            ("__cancel__", "취소", "", True),
        ]
        info = [("위치", str(current))]
        if read_error:
            info.append(("안내", "폴더를 읽지 못했습니다. " + read_error))
        elif not facts["media"] and not facts["job"]:
            info.append(("안내", "이 폴더에는 MP4/M4A가 없습니다. 하위 폴더를 열어 보세요."))
        answer = app.select_menu(
            "폴더 열기", options, info, allow_back=True, left_up=True,
            footer="↑↓ 이동 · ← 상위 · Enter 열기 · Esc 나가기")
        if answer is app.BACK or answer == "__cancel__":
            return None
        if answer is app.UP or answer == "__up__":
            if current.parent != current:
                current = current.parent
            continue
        if answer == "__select__":
            return current
        if answer == "__path__":
            app.screen("폴더 경로 지정", "폴더 또는 MP4/M4A 파일 · 빈 값이면 취소")
            typed = app.prompt_box("경로")
            target = app.path_target(typed) if typed else None
            if target is None:
                app.notice(f"폴더나 미디어 파일을 찾지 못했습니다: {typed}")
                app.wait()
                continue
            return target
        current = Path(answer)


_REMOTE_CACHE = {}


def add_key_flow(default_tier=None):
    """무료/유료 등급과 이름을 붙여 키를 하나 등록합니다."""
    options = [
        ("free", "무료 키", "live 전용 · 기본 130분/키"),
        ("paid", "유료 키", "Batch 전용 · 남는 오디오 처리"),
        ("back", "← 뒤로", "", True),
    ]
    if default_tier in ("free", "paid"):
        options = [option for option in options if option[0] != default_tier]
        options.insert(0, (default_tier,
                           "무료 키" if default_tier == "free" else "유료 키",
                           "live 전용" if default_tier == "free" else "Batch 전용"))
    answer = app.select_menu(
        "키 등급", options,
        [("API 키", "GEMINI_API_KEY 환경변수도 사용할 수 있습니다")],
        heading="키 추가", subtitle="free / paid", allow_back=True)
    if answer is app.BACK or answer == "back":
        return None
    tier = answer
    label_default = app.key_default_label(tier)
    app.header("키 추가", f"{app.key_tier_label(tier)} · {app.short_path(app.config_path())}")
    print(f"  {app.dim('Gemini API 키를 등록합니다. GEMINI_API_KEY 환경변수도 쓸 수 있습니다.')}")
    print(f"  {app.dim('이름은 키를 구분하는 라벨입니다. 비워두면 자동으로 정합니다.')}")
    print()
    label = app.prompt_box(f"이름 ({label_default})") or label_default
    print(f"  {app.dim('API 키를 붙여넣고 Enter. 입력은 화면에 남지 않습니다.')}")
    print()
    typed = app.read_secret("API 키")
    if typed is None:
        app.notice("키 입력을 취소했습니다.")
        app.wait()
        return None
    sys.stdout.write(f"  {app.dim('키 확인 중…')}")
    sys.stdout.flush()
    state, message = app.check_key(typed)
    sys.stdout.write("\r\033[K")
    if state == "invalid":
        app.notice(f"{message}. 다시 시도하세요.")
        app.wait()
        return None
    try:
        entry = app.add_key_entry(label, tier, typed)
    except ValueError as exc:
        app.notice(str(exc))
        app.wait()
        return None
    mark = app.accent("✓") if state == "ok" else app.warn("!")
    print(f"  {mark} {message} · {entry['label']} ({app.key_tier_label(tier)}) 저장했습니다")
    print()
    print(f"  {app.dim('설정 파일 ' + app.short_path(app.config_path()))}")
    app.status_bar()
    time.sleep(0.4)
    return entry["api_key"]


def manage_keys(key=None):
    """등록된 키를 목록으로 보여주고 추가/삭제합니다."""
    while True:
        entries = app.saved_key_entries()
        env = app.env_key_entry()
        info = [("등록", app.key_summary(entries))]
        if env:
            info.append(("환경변수", app.key_row_desc(env)))
        options = []
        for entry in entries:
            options.append((f"delete:{entry['id']}", entry["label"],
                            f"{app.key_row_desc(entry)} · Enter로 삭제"))
        options += [
            None,
            ("add", "키 추가", "무료 또는 유료 키를 등록합니다"),
            ("back", "← 뒤로", "", True),
        ]
        answer = app.select_menu(
            "API 키 관리", options, info, allow_back=True,
            heading="API 키", subtitle="free / paid")
        if answer is app.BACK or answer == "back":
            return app.current_key()
        if answer == "add":
            app.add_key_flow()
            continue
        if isinstance(answer, str) and answer.startswith("delete:"):
            identifier = answer[len("delete:"):]
            target = next((entry for entry in entries
                           if entry["id"] == identifier), None)
            if target is None:
                continue
            confirmed = app.select_menu(
                "키 삭제", [
                    ("yes", "삭제", f"{target['label']} · {app.mask_key(target['api_key'])}"),
                    ("no", "취소", "", True),
                ], [("등급", app.key_tier_label(target["tier"])),
                    ("이름", target["label"])],
                allow_back=True, heading="키 삭제", subtitle=target["label"])
            if confirmed == "yes":
                app.delete_key_entry(identifier)
                app.notice(f"{target['label']} 키를 삭제했습니다.")
                app.wait()


def key_screen(key=None):
    """키가 없으면 등록하고, 있으면 관리 화면을 엽니다."""
    if app.saved_key_entries():
        return app.manage_keys(key)
    return app.add_key_flow() or app.current_key(key)


def ask_key():
    return app.add_key_flow() or app.current_key()


def run_with_client(key, action, purpose="작업", tier=None):
    """클라이언트를 만들고 action(client) 을 실행합니다.

    401/403 이면 같은 등급(free/paid)의 다른 키로 한 번 더 시도합니다. 무료 live 가
    유료 키로, 유료 Batch 가 무료 키로 바뀌지 않도록 tier 를 넘깁니다. 업로드와
    다운로드는 체크포인트가 있어 재시도해도 중복 청구되지 않습니다.
    """
    force_prompt = False
    while True:
        if force_prompt:
            key = app.ask_key()
            force_prompt = False
        else:
            key = app.current_key(key)
            if not key:
                key = app.ask_key()
        if not key:
            app.notice("API 키가 없어 작업을 취소했습니다.")
            return key, None
        if tier and app.key_tier_of(key) != tier:
            app.notice(f"{app.key_tier_label(tier)} 키가 필요합니다. 키를 확인하세요.")
            return key, None
        try:
            client = app.get_client(key)
        except (RuntimeError, OSError, ValueError) as exc:
            app.notice(str(exc))
            return key, None
        try:
            from google.genai.errors import APIError
            try:
                return key, action(client)
            except APIError as exc:
                if exc.code in (401, 403):
                    app.notice(f"API 키 오류 ({exc.code}): {exc}")
                    failed = key
                    if exc.code == 401:
                        for entry in app.saved_key_entries():
                            if entry["api_key"] == failed:
                                app.delete_key_entry(entry["id"])
                    candidates = (app.key_entries_for_tier(tier) if tier
                                  else app.key_entries())
                    key = next((entry["api_key"] for entry in candidates
                                if entry["api_key"] != failed), None)
                    force_prompt = key is None
                    continue
                app.notice(f"API 오류: {exc}")
                return key, None
            except (RuntimeError, OSError, ValueError, KeyError) as exc:
                app.notice(str(exc))
                return key, None
        finally:
            client.close()


def estimate_action(folder):
    app.screen("예상 비용", folder.name)
    try:
        for line in app.estimate_lines(folder, app.folder_model(folder)):
            app.log(line)
    except (RuntimeError, OSError) as exc:
        app.notice(str(exc))
    app.wait()


def convert_action(folder):
    app.screen("MP4 → M4A 변환", folder.name)
    try:
        app.convert(folder)
    except (RuntimeError, OSError) as exc:
        app.notice(str(exc))
    app.wait()


def status_flow(folder, key):
    key = app.ensure_paid_key(key)
    if not key:
        return key

    def action(client):
        job = app.load(folder)
        app.status(job, client)
        path = folder / app.RECOVERY_FILE
        if path.exists():
            try:
                recovery = json.loads(path.read_text(encoding="utf-8"))
            except (OSError, ValueError):
                recovery = {}
            if (isinstance(recovery, dict)
                    and recovery.get("batch_id") == job.get("batch_id")
                    and recovery.get("pending_batch")):
                app.log("")
                app.log("재시도 Batch")
                app.status(recovery["pending_batch"], client)
        return True

    key, _ = app.run_with_client(key, action, "상태 확인", "paid")
    app.wait()
    return key


def merge_flow(folder, key):
    key = app.ensure_paid_key(key)
    if not key:
        return key

    def action(client):
        return app.download(folder, app.load(folder), client, "none", 3, app.choose_retry,
                        validate=False)

    key, result = app.run_with_client(key, action, "결과 병합", "paid")
    if result != "back":
        app.wait()
    return key


def open_result_action(folder):
    app.screen("결과 열기", app.output_path(folder).name)
    try:
        app.open_result(folder)
    except (RuntimeError, OSError) as exc:
        app.notice(str(exc))
    app.wait()


def archive_job(folder):
    with app.job_lock(folder, "보관"):
        return app._archive_job(folder)


def _archive_job(folder):
    """기존 작업 기록을 보관 폴더로 옮깁니다. 미디어 파일은 건드리지 않습니다."""
    if app.watcher_running(folder):
        app.fail("백그라운드 대기 중에는 새 작업을 시작할 수 없습니다. "
             "대기가 끝난 뒤 다시 시도하세요.")
    names = [app.STATE_FILE, app.REQUEST_FILE, app.RESULT_FILE, app.RECOVERY_FILE, app.RETRY_FILE,
             app.WATCH_LOG, app.WATCH_STATUS, app.WATCH_CLAIM, app.LIVE_STATE_FILE,
             app.LIVE_RESULT_FILE, app.LIVE_SEGMENT_DIR, app.output_path(folder).name]
    names += [path.name for path in folder.glob(".gemini_mixed.*")
              if path.name not in names]
    existing = [name for name in names if (folder / name).exists()]
    if not existing:
        return None
    stamp = time.strftime("%Y%m%d-%H%M%S")
    destination = folder / app.ARCHIVE_DIR / stamp
    suffix = 1
    while destination.exists():
        destination = folder / app.ARCHIVE_DIR / f"{stamp}-{suffix}"
        suffix += 1
    destination.mkdir(parents=True)
    for name in existing:
        shutil.move(str(folder / name), str(destination / name))
    try:
        app.watch_pid_path(folder).unlink()
    except OSError:
        pass
    return destination


def new_job_flow(folder):
    facts = app.inspect_folder(folder)
    if facts["stage"] in ("ready", "empty", "missing"):
        app.notice("이미 새 작업을 시작할 수 있는 상태입니다.")
        app.wait()
        return
    info = [("폴더", app.short_path(folder)),
            ("미디어", "MP4/M4A는 그대로 둡니다")]
    if facts["submitted"]:
        info.insert(1, ("주의", "원격 Batch가 진행 중이면 계속 과금될 수 있습니다"))
    answer = app.select_menu("새 작업 시작", [
        ("yes", "기록 보관 후 새 작업", f"{app.ARCHIVE_DIR}/ 로 상태와 결과를 옮깁니다"),
        ("no", "취소", "", True),
    ], info, allow_back=True)
    if answer != "yes":
        return
    try:
        destination = app.archive_job(folder)
    except (RuntimeError, OSError) as exc:
        app.notice(str(exc))
        app.wait()
        return
    app.screen("새 작업", folder.name)
    if destination:
        app.log(f"기존 기록을 옮겼습니다: {app.short_path(destination)}")
    else:
        app.log("보관할 기록이 없습니다.")
    app.log("같은 폴더의 MP4/M4A로 새 전사를 시작할 수 있습니다.")
    app.wait()


def file_list_screen(folder):
    facts = app.inspect_folder(folder)
    app.screen("폴더 내용", app.short_path(folder))
    app.log(f"{app.pad('상태', 8)}  {app.stage_label(facts)}")
    if facts["reason"]:
        app.log(f"{app.pad('이유', 8)}  {facts['reason']}")
    app.log("")
    try:
        entries = sorted(folder.iterdir(),
                         key=lambda p: (not p.is_dir(), app.natural_key(p.name)))
    except OSError as exc:
        app.notice(str(exc))
        app.wait()
        return
    shown = hidden = 0
    for path in entries:
        if path.name.startswith("."):
            hidden += 1
            continue
        if path.is_dir():
            app.log(f"  {app.bold(path.name + '/')}")
        else:
            try:
                size = app.human_size(path.stat().st_size)
            except OSError:
                size = "?"
            app.log(f"  {app.pad(path.name, 38)} {app.dim(size)}")
        shown += 1
    if not shown:
        app.log("  (표시할 파일이 없습니다)")
    if hidden:
        app.log("")
        app.log(app.dim(f"  숨김 파일 {hidden}개 (상태·체크포인트)"))
    app.wait()


def progress_screen(folder):
    facts = app.inspect_folder(folder)
    app.screen("진행 상황", folder.name)
    watch = facts["watch"]
    if watch:
        app.log(f"{app.pad('상태', 8)}  {watch.get('label') or '대기 중'}")
        elapsed = watch.get("elapsed")
        if isinstance(elapsed, (int, float)):
            app.log(f"{app.pad('경과', 8)}  {app.duration_text(elapsed)}")
        if watch.get("error"):
            app.notice(str(watch["error"]))
        if watch.get("state") == "STOPPED":
            app.log("감시가 중단되었습니다. 필요하면 다시 대기를 시작하세요.")
    elif facts["watcher"]:
        app.log("대기 프로세스는 살아 있지만 상태 파일이 없습니다.")
    else:
        app.log(f"{app.pad('상태', 8)}  {app.stage_label(facts)}")
        app.log("백그라운드에서 기다리는 프로세스가 없습니다.")
    if app.safe_exists(folder / app.WATCH_LOG):
        app.log_tail(folder / app.WATCH_LOG)
    app.wait()


def wait_flow(folder, key, autodownload=True):
    def action(client):
        try:
            _, state, detached = app.wait_for_completion(
                folder, app.load(folder), client, track=True, allow_detach=True)
        except KeyboardInterrupt:
            if sys.stdout.isatty():
                print()
            return "detach"
        if detached:
            return "detach"
        if state != "JOB_STATE_SUCCEEDED":
            app.notice(f"작업이 완료되지 않았습니다: {app.format_state(state)}")
            return "finished"
        app.notify("날먹", f"{folder.name} 전사 완료")
        if not autodownload:
            app.log("전사가 완료되었습니다.")
            return "finished"
        app.log("전사가 완료되었습니다. 결과를 병합합니다.")
        app.log("")
        return "back" if app.download(
            folder, app.load(folder), client, "none", 3, app.choose_retry,
            validate=False) == "back" else "finished"

    key, outcome = app.run_with_client(key, action, "완료 대기", "paid")
    if outcome == "detach":
        _, message = app.start_watcher(folder, key)
        app.notice(message)
        app.wait()
    elif outcome != "back":
        app.wait()
    return key


def post_submit(folder, key):
    answer = app.select_menu("지금 기다릴까요?", [
        ("wait", "완료까지 기다리기", "Esc 또는 b 로 백그라운드 전환"),
        ("bg", "백그라운드로 보내기", "메뉴로 돌아가 다른 작업을 할 수 있습니다"),
        ("later", "나중에 확인", "작업 메뉴에서 이어서"),
    ], [
        ("접수", "Batch가 접수되었습니다"),
        ("안내", "보통 10분~1시간, 최대 24시간"),
        ("폴더", app.short_path(folder)),
    ], allow_back=True, heading="제출 완료", subtitle=app.short_path(folder))
    if answer is app.BACK or answer == "later":
        return key
    if answer == "bg":
        _, message = app.start_watcher(folder, key)
        app.notice(message)
        app.wait()
        return key
    return app.wait_flow(folder, key, autodownload=True)


def transcribe_flow(folder, key):
    facts = app.inspect_folder(folder)
    caps = app.capabilities()
    if facts["stage"] not in ("ready", "prepared"):
        app.notice(f"지금은 전사를 시작할 수 없습니다: {app.stage_label(facts)}")
        app.wait()
        return key
    key = app.ensure_paid_key(key)
    if not key:
        return key
    if facts["needs_convert"] and not caps["ffmpeg"]:
        app.screen("유료 Batch", folder.name)
        app.notice("MP4 → M4A 변환에 ffmpeg가 필요합니다.")
        app.log("  macOS: brew install ffmpeg")
        app.log("  Ubuntu: sudo apt install ffmpeg")
        app.wait()
        return key
    if facts["needs_convert"]:
        app.screen("MP4 → M4A 변환", folder.name)
        try:
            app.convert(folder)
        except (RuntimeError, OSError) as exc:
            app.notice(str(exc))
            app.wait()
            return key
        app.log("")
    if not app.current_key(key):
        key = app.ask_key()
        if not key:
            app.notice("API 키가 없어 취소했습니다.")
            app.wait()
            return key
    model = app.folder_model(folder)
    app.screen("예상 비용", folder.name)
    lines = []
    try:
        lines = app.estimate_lines(folder, model)
        for line in lines:
            app.log(line)
        app.log("")
    except (RuntimeError, OSError) as exc:
        app.notice(str(exc))
        app.notice("길이를 읽지 못해 예상 비용 없이 제출합니다.")
        app.log("")
    if lines:
        if not app.choose_spend(lines, folder):
            return key
    elif app.select_menu("계속 제출할까요?", [
        ("yes", "제출", "예상 비용을 계산하지 못했습니다"),
        ("no", "취소", "", True),
    ], allow_back=True) != "yes":
        return key

    def action(client):
        app.upload(folder, client, model, confirm=lambda _lines: True,
               show_estimate=False)
        return True

    key, done = app.run_with_client(key, action, "유료 Batch", "paid")
    if not done:
        app.wait()
        return key
    return app.post_submit(folder, key)


def remote_label(folder):
    return app._REMOTE_CACHE.get(str(Path(folder).resolve()))


def refresh_remote(folders, key):
    targets = []
    for folder in folders:
        facts = app.inspect_folder(folder)
        if facts["submitted"] and not facts["output"]:
            targets.append(folder)
    if not targets:
        app.notice("확인할 제출 작업이 없습니다.")
        app.wait()
        return key
    key = app.ensure_paid_key(key)
    if not key:
        return key

    def action(client):
        from google.genai.errors import APIError
        for folder in targets:
            try:
                job = json.loads(app.state_path(folder).read_text(encoding="utf-8"))
                target = app.wait_target(folder, job)
                if not target:
                    continue
                batch = client.batches.get(name=target)
                app._REMOTE_CACHE[str(folder.resolve())] = app.format_state(app.batch_state(batch))
            except APIError as exc:
                if exc.code in (401, 403):
                    raise
                app._REMOTE_CACHE[str(folder.resolve())] = f"오류 {exc.code}"
            except Exception:  # noqa: BLE001 - 한 폴더 실패가 전체를 막지 않습니다.
                app._REMOTE_CACHE[str(folder.resolve())] = "확인 실패"
        return True

    key, _ = app.run_with_client(key, action, "상태 새로고침", "paid")
    app.screen("원격 상태", f"{len(targets)}개 확인")
    for folder in targets:
        app.log(f"  {app.pad(folder.name, 28)} {app.remote_label(folder) or '-'}")
    app.wait()
    return key


def home_lines(entries, cursor, key, folder_count):
    """화면 2 한 프레임을 줄 목록으로 만듭니다."""
    app.refresh_layout()
    lines = [""]
    lines.append(f"  {app.bold('날먹')}   {app.dim('강의 음성을 Batch·live로 전사')}   {app.dim('v' + app.__version__)}")
    lines.append(f"  {app.dim('─' * app.WIDTH)}")
    lines.append("")
    labels = [entry for entry in entries if entry]
    label_width = min(
        max((app.display_width(entry[1]) for entry in labels), default=8), 26)
    if folder_count:
        lines.append(f"  {app.dim('작업')}")
        lines.append("")
    else:
        lines.append(f"  {app.dim('최근 작업이 없습니다. [새 전사]로 시작하세요.')}")
        lines.append("")
    for index, entry in enumerate(entries):
        if entry is None:
            lines.append(f"  {app.dim('─' * app.WIDTH)}")
            continue
        label, desc, faded = entry[1], entry[2], entry[3]
        text = app.pad(app.clip(label, label_width), label_width)
        room = max(8, app.WIDTH - label_width - 5)
        tail = app.dim(app.clip(desc, room)) if desc else ""
        if faded:
            lines.append(f"    {app.dim(text)}   {tail}")
        elif index == cursor:
            lines.append(f"  {app.accent(app.pointer())} {app.bold(text)}   {tail}")
        else:
            lines.append(f"    {text}   {tail}")
    lines.append("")
    lines.append(app.status_bar_line(key))
    lines.append(f"  {app.dim('↑↓ 이동 · Enter 선택 · d 드롭 · v 업데이트 · k 키 · u 사용량 · s 설정 · m 유료 일괄 · q 종료')}")
    return lines


def draw_home(entries, cursor, key, folder_count):
    """화면 2. 상자 대신 목록과 얇은 구분선으로 그립니다."""
    app.clear_screen()
    return app.draw_lines(app.home_lines(entries, cursor, key, folder_count))


def help_screen():
    app.header("도움말", "날먹 v" + app.__version__)
    for label, desc in (
        ("Enter", "선택 · 전사"),
        ("d", "드롭 · 붙여넣기 (폴더/MP4/M4A)"),
        ("v", "업데이트 확인 (패키징 설치)"),
        ("Esc / ←", "뒤로"),
        ("k", "API 키 변경"),
        ("s", "설정 · 환경 점검"),
        ("m", "여러 폴더 유료 Batch"),
        ("u", "사용량 · 누적 비용"),
        ("r", "제출한 Batch 원격 상태 새로고침"),
        ("a", "고급 메뉴 (재시도 · 보관 · 상태)"),
        ("l", "무료 live 전사 (Batch 없이)"),
        ("b", "대기 중 백그라운드로 전환"),
        ("?", "이 도움말"),
        ("q", "종료"),
    ):
        print(f"  {app.pad(label, 12)}  {app.dim(desc)}")
    app.footer("아무 키나 누르면 돌아갑니다")
    app.read_key()


def home_screen(key):
    """화면 2. 최근 작업과 새 전사. 선택 키 또는 BACK 을 돌려줍니다."""
    app.require_tui()
    cursor = 0

    def movable(entries, index):
        entry = entries[index] if 0 <= index < len(entries) else None
        return entry is not None and not entry[3]

    while True:
        rows = [(folder, app.inspect_folder(folder))
                for folder in app.dashboard_folders()]
        entries = []
        for folder, facts in rows:
            desc = app.folder_summary(folder, facts)
            live = app.remote_label(folder)
            if live and facts["submitted"] and not facts["output"]:
                desc += f" · {live}"
            entries.append((f"folder:{folder}", folder.name or ".", desc))
        candidates = sum(1 for _, facts in rows
                         if facts["stage"] in ("ready", "prepared"))
        has_remote = any(facts["submitted"] and not facts["output"]
                         for _, facts in rows)
        entries += [
            None,
            ("open", "새 전사", "폴더를 고르거나 경로를 붙여넣기"),
            ("drop", "드롭 · 붙여넣기", "끌어다 놓으면 바로 전사 · 직접 입력도 가능"),
            ("multi", "여러 폴더 유료 Batch", f"{candidates}개 준비됨", not candidates),
            ("refresh", "원격 상태 새로고침", "제출한 Batch 상태 확인",
             not has_remote),
            ("stats", "사용량", "지금까지 쓴 금액과 평균"),
            ("update", "업데이트 확인", "새 버전이 있으면 받기"),
            ("settings", "설정", "키 · 환경 · 최근 목록"),
            ("quit", "종료", ""),
        ]
        entries = [app.normalize(entry) for entry in entries]
        if not movable(entries, cursor):
            cursor = next((index for index in range(len(entries))
                           if movable(entries, index)), 0)
        lines = app.home_lines(entries, cursor, key, len(rows))
        app.clear_screen()
        height = app.draw_lines(lines)
        while True:
            pressed = app.read_key()
            if isinstance(pressed, tuple) and pressed[0] == "paste":
                return ("drop-paste", pressed[1])
            if pressed == "up":
                for _ in range(len(entries)):
                    cursor = (cursor - 1) % len(entries)
                    if movable(entries, cursor):
                        break
            elif pressed == "down":
                for _ in range(len(entries)):
                    cursor = (cursor + 1) % len(entries)
                    if movable(entries, cursor):
                        break
            elif pressed in ("enter", "right"):
                return entries[cursor][0]
            elif pressed == "q":
                return "quit"
            elif pressed == "s":
                return "settings"
            elif pressed == "m":
                return "multi"
            elif pressed == "d":
                return "drop"
            elif pressed == "r":
                return "refresh"
            elif pressed == "u":
                return "stats"
            elif pressed == "v":
                return "update"
            elif pressed == "k":
                return "changekey"
            elif pressed in ("left", "esc"):
                return app.BACK
            elif pressed in ("?", "h"):
                app.help_screen()
                break
            else:
                continue
            new_lines = app.home_lines(entries, cursor, key, len(rows))
            if not app.update_lines(lines, new_lines, height):
                app.clear_screen()
                height = app.draw_lines(new_lines)
            lines = new_lines


def folder_menu(folder, facts):
    stage = facts["stage"]
    caps = app.capabilities()
    options = []
    if stage in ("empty", "invalid"):
        options.append(("browse", "폴더 내용 확인",
                        facts["reason"] or "MP4/M4A를 넣어 주세요"))
    elif stage == "ready":
        free_keys = app.key_entries_for_tier("free")
        paid_keys = app.key_entries_for_tier("paid")
        if free_keys:
            options.append(("mixed_free", "무료 키로 전사",
                            "무료 live · Batch 없음 · 부족하면 안내"))
            if paid_keys:
                options.append(("mixed_paid", "무료 + 유료 Batch",
                                "무료를 먼저 쓰고 남는 분량만 Batch"))
            options.append(("start", "유료 Batch로만 전사",
                            "유료 키로 한 번에 제출"))
        else:
            options.append(("keys", "무료 키 추가",
                            "무료 live로 전사하려면 키가 필요합니다"))
            if paid_keys:
                options.append(("start", "유료 Batch로 전사",
                                "유료 키로 한 번에 제출"))
    elif stage == "prepared":
        options.append(("start", "제출 이어서 하기", "이미 준비된 작업을 제출합니다"))
    elif stage == "live":
        if facts.get("mixed"):
            label = ("혼합 이어서 하기" if facts.get("paid_groups")
                     else "무료 전사 이어서 하기")
        else:
            label = "live 이어서 하기"
        options.append(("live", label, app.stage_label(facts)))
    elif stage == "submitted":
        label = ("재시도 완료까지 기다리기" if facts["pending_retry"]
                 else "완료까지 기다리기")
        options.append(("watch", label, "Esc 또는 b 로 백그라운드 전환"))
    elif stage == "watching":
        options.append(("progress", "진행 상황 보기", "백그라운드 상태와 로그"))
    elif stage == "received":
        if facts.get("live"):
            options.append(("live", "live 결과 마무리", "Markdown 생성"))
        else:
            options.append(("merge", "결과 병합", "받은 결과로 Markdown 생성"))
    elif stage == "done":
        options.append(("open", "결과 열기", app.output_path(folder).name))
    elif stage in ("changed", "corrupt"):
        options.append(("newjob", "새 작업 시작", "기존 기록은 보관합니다"))
        if facts["output"]:
            options.append(("open", "이전 결과 열기", app.output_path(folder).name))

    extra = []
    if stage == "ready" and facts["mp4"]:
        extra.append(("convert", "MP4 → M4A 변환", "원본은 그대로 둡니다"))
    if stage in ("ready", "prepared"):
        if app.key_entries_for_tier("free"):
            extra.append(("live", "무료 live (첫 키)", "Batch 없이 130분 그룹으로"))
        extra.append(("estimate", "예상 비용 계산", "길이와 토큰을 계산"))
    if stage == "prepared":
        extra.append(("newjob", "새 작업 시작", "기존 준비 기록은 보관합니다"))
    if stage == "live" or (stage == "received" and facts.get("live")):
        extra.append(("newjob", "새 작업 시작", "기존 live 기록은 보관합니다"))
    if stage in ("submitted", "changed") and facts["submitted"]:
        extra.append(("watch", "완료까지 기다리기", "Esc 또는 b 로 백그라운드 전환"))
        if not facts["watcher"]:
            extra.append(("bg", "백그라운드로 보내기", "메뉴로 돌아갑니다"))
        extra.append(("status", "상태 확인", "Batch 원격 상태 조회"))
    if stage == "submitted":
        extra.append(("newjob", "새 작업 시작", "이 작업을 포기하고 기록 보관"))
    if stage == "watching":
        extra.append(("stopwatch", "백그라운드 감시 중단", "대기 프로세스를 종료합니다"))
    if stage not in ("watching",) and (
            facts["watch"] or app.safe_exists(folder / app.WATCH_LOG)):
        extra.append(("progress", "대기 기록 보기", "마지막 백그라운드 상태와 로그"))
    if stage == "changed" and (facts["result"] or facts["recovery"]):
        extra.append(("merge", "결과 병합", "받은 결과로 Markdown 생성"))
    if stage == "received":
        extra.append(("cost", "비용 보기", "실제 토큰과 청구액"))
    if stage == "done":
        extra.append(("cost", "비용 보기", "실제 토큰과 청구액"))
        extra.append(("newjob", "새 작업 시작", "기존 기록은 보관합니다"))
    if facts["output"] and stage != "done":
        extra.append(("open", "결과 열기", app.output_path(folder).name))
    if facts["output"]:
        extra.append(("copy", "결과 경로 복사", app.short_path(app.output_path(folder))))
    extra.append(("browse", "폴더 내용 확인", "파일과 숨김 상태 파일"))

    seen = {option[0] for option in options if option}
    unique = []
    for option in extra:
        if option[0] not in seen:
            seen.add(option[0])
            unique.append(option)
    options += [None] + unique + [None, ("back", "← 뒤로", "", True)]
    info = [
        ("폴더", app.short_path(folder)),
        ("상태", app.stage_label(facts)),
        ("환경", app.caps_line(caps)),
    ]
    if facts["reason"]:
        info.append(("이유", facts["reason"]))
    if facts["watcher"]:
        info.append(("감시", "백그라운드에서 기다리는 중"))
    if facts["pending_retry"]:
        info.append(("재시도", "새 Batch가 처리 중입니다"))
    watch = facts.get("watch") or {}
    if watch.get("error") and stage != "watching":
        info.append(("대기", app.clip(str(watch["error"]), app.WIDTH - 10)))
    return app.select_menu("작업", options, info, allow_back=True,
                       heading=folder.name or ".",
                       subtitle=app.short_path(folder),
                       footer="↑↓ 이동 · Enter 실행 · Esc 뒤로")


def folder_session(folder, key):
    while True:
        facts = app.inspect_folder(folder)
        if not facts["exists"]:
            app.notice("폴더가 사라졌습니다.")
            app.wait()
            return key
        try:
            answer = app.folder_menu(folder, facts)
        except KeyboardInterrupt:
            return key
        if answer is app.BACK or answer == "back":
            return key
        if answer == "start":
            key = app.transcribe_flow(folder, key)
        elif answer == "keys":
            key = app.manage_keys(key)
        elif answer == "mixed_free":
            key = app.mixed_session(folder, key, include_paid=False)
        elif answer == "mixed_paid":
            key = app.mixed_session(folder, key, include_paid=True)
        elif answer == "live":
            key = app.live_session(folder, key)
        elif answer == "watch":
            key = app.wait_flow(folder, key, autodownload=True)
        elif answer == "bg":
            _, message = app.start_watcher(folder, key)
            app.notice(message)
            app.wait()
        elif answer == "progress":
            app.progress_screen(folder)
        elif answer == "stopwatch":
            try:
                pid = app.watch_pid_path(folder).read_text(encoding="utf-8").strip()
            except OSError:
                pid = "?"
            answer = app.select_menu("감시 중단", [
                ("yes", "중단", "대기 프로세스를 종료하고 PID 파일을 정리합니다"),
                ("no", "취소", "", True),
            ], [("PID", pid),
                ("안내", "원격 Batch는 계속 진행 중일 수 있습니다")], allow_back=True)
            if answer == "yes":
                try:
                    terminated = app.stop_watcher(folder)
                except OSError as exc:
                    app.notice(str(exc))
                else:
                    app.notice("감시를 중단했습니다." if terminated
                           else "PID 파일을 정리했습니다.")
                app.wait()
        elif answer == "merge":
            key = app.merge_flow(folder, key)
        elif answer == "status":
            key = app.status_flow(folder, key)
        elif answer == "estimate":
            app.estimate_action(folder)
        elif answer == "convert":
            app.convert_action(folder)
        elif answer == "browse":
            app.file_list_screen(folder)
        elif answer == "open":
            app.open_result_action(folder)
        elif answer == "copy":
            path = str(app.output_path(folder))
            app.notice("경로를 복사했습니다." if app.copy_to_clipboard(path)
                   else "클립보드 도구를 찾지 못했습니다.")
            app.log(f"  {path}")
            app.wait()
        elif answer == "cost":
            job = app.read_json_object(app.state_path(folder))
            if job is None:
                job = app.read_json_object(app.live_state_path(folder))
            if job:
                try:
                    lines = app.cost_lines(folder, job)
                except (RuntimeError, OSError, ValueError) as exc:
                    lines = [str(exc)]
            else:
                lines = ["아직 작업 기록이 없습니다."]
            app.show_lines("비용", folder.name, lines)
        elif answer == "newjob":
            app.new_job_flow(folder)


def multi_submit(key):
    folders = app.dashboard_folders()
    candidates = []
    for folder in folders:
        facts = app.inspect_folder(folder)
        if facts["stage"] in ("ready", "prepared"):
            candidates.append((folder, facts))
    if not candidates:
        app.notice("제출할 수 있는 폴더가 없습니다. MP4/M4A가 든 폴더를 먼저 열어 주세요.")
        app.wait()
        return key
    key = app.ensure_paid_key(key)
    if not key:
        return key
    chosen = app.select_many(
        "여러 폴더 제출",
        [(str(folder), folder.name or ".", app.folder_summary(folder, facts))
         for folder, facts in candidates],
        [("대상", f"{len(candidates)}개 준비됨")])
    if chosen is app.BACK:
        return key
    if not chosen:
        app.notice("선택한 폴더가 없습니다.")
        app.wait()
        return key
    selected = [Path(value) for value in chosen]
    caps = app.capabilities()
    if (any(app.inspect_folder(folder)["needs_convert"] for folder in selected)
            and not caps["ffmpeg"]):
        app.notice("MP4 → M4A 변환에 ffmpeg가 필요합니다.")
        app.wait()
        return key

    app.screen("여러 폴더 제출", f"{len(selected)}개")
    totals = {"files": 0, "seconds": 0.0, "cost": 0.0}
    unestimated = []
    for folder in selected:
        facts = app.inspect_folder(folder)
        if facts["needs_convert"]:
            app.log(f"변환: {folder.name}")
            try:
                app.convert(folder)
            except (RuntimeError, OSError) as exc:
                app.notice(f"{folder.name}: {exc}")
                app.wait()
                return key
        try:
            values = app.estimate_values(folder, app.folder_model(folder))
        except (RuntimeError, OSError) as exc:
            app.notice(f"{folder.name}: {exc}")
            unestimated.append(folder)
            continue
        totals["files"] += values["files"]
        totals["seconds"] += values["seconds"]
        totals["cost"] += values["cost"] or 0
    if not app.current_key(key):
        key = app.ask_key()
        if not key:
            app.notice("API 키가 없어 취소했습니다.")
            app.wait()
            return key
    info = [
        ("폴더", f"{len(selected)}개"),
        ("오디오", f"{totals['files']}개 · 총 {app.duration_text(totals['seconds'])}"),
    ]
    if totals["cost"]:
        info.append(("예상 비용", app.money(totals["cost"])))
    if unestimated:
        info.append(("예상 불가", f"{len(unestimated)}개 · 그래도 제출합니다"))
    if app.select_menu("이대로 제출할까요?", [
        ("yes", "일괄 제출", "폴더마다 Batch를 하나씩 만듭니다"),
        ("no", "취소", "", True),
    ], info, allow_back=True,
        heading="여러 폴더 제출", subtitle=f"{len(selected)}개") != "yes":
        return key

    successes, failures = [], []

    def action(client):
        successes.clear()
        failures.clear()
        for folder in selected:
            try:
                app.upload(folder, client, app.folder_model(folder),
                       confirm=lambda _lines: True, show_estimate=False)
                app.remember_folder(folder)
                successes.append(folder)
            except (RuntimeError, OSError, ValueError, KeyError) as exc:
                failures.append((folder, str(exc)))
        return True

    key, _ = app.run_with_client(key, action, "일괄 제출", "paid")
    watchers = []
    if successes:
        answer = app.select_menu("백그라운드로 감시할까요?", [
            ("yes", "모두 백그라운드 대기", "완료되면 자동으로 결과까지 병합"),
            ("no", "나중에", "", True),
        ], [("성공", f"{len(successes)}개"), ("실패", f"{len(failures)}개")],
            allow_back=True,
            heading="일괄 제출 결과", subtitle=f"{len(successes)}개 성공")
        if answer == "yes":
            for folder in successes:
                _, message = app.start_watcher(folder, key)
                watchers.append((folder, message))
    app.screen("일괄 제출 결과", f"{len(successes)}개 성공")
    for folder in successes:
        app.log(f"  {app.accent('✓')} {folder.name}")
    for folder, message in failures:
        app.log(f"  {app.warn('✗')} {folder.name}: {message}")
    if watchers:
        app.log("")
        for folder, message in watchers:
            app.log(f"  {folder.name}: {message}")
    app.wait()
    return key


def settings_menu(key):
    while True:
        caps = app.capabilities(refresh=True)
        entries = app.key_entries()
        env = app.env_key_entry()
        info = [
            ("API 키", app.key_summary(entries)),
            ("ffmpeg", caps["ffmpeg"] or "없음"),
            ("ffprobe", caps["ffprobe"] or "없음"),
            ("SDK", "설치됨" if caps["sdk"] else "없음"),
            ("설정", app.short_path(app.config_path())),
        ]
        if env:
            info.append(("환경변수", app.key_row_desc(env)))
        answer = app.select_menu("설정", [
            ("keys", "API 키 관리", f"{app.key_summary(entries)} · free / paid"),
            ("recent", "최근 작업 목록 비우기", f"{len(app.recent_folders())}개",
             not app.recent_folders()),
            ("diag", "진단 정보", "환경과 경로를 자세히 봅니다"),
            ("back", "← 뒤로", "", True),
        ], info, allow_back=True, heading="설정", subtitle="키 · 환경 · 최근 작업")
        if answer is app.BACK or answer == "back":
            return key
        if answer == "keys":
            key = app.manage_keys(key)
        elif answer == "recent":
            app.clear_recent()
            app.notice("최근 작업 목록을 비웠습니다.")
            app.wait()
        elif answer == "diag":
            app.show_lines("진단 정보", app.short_path(Path.cwd()), app.diagnostics_lines())


def ensure_key(key):
    existing = (key or app.key_for_tier("free") or app.key_for_tier("paid")
                or app.current_key())
    if existing:
        return existing
    return app.key_screen()


def message_screen(folder, title, lines, hint="Enter 돌아가기"):
    app.header(title, app.short_path(folder))
    for line in lines:
        print(f"  {line}")
    app.footer(hint)
    app.wait_key()


def start_screen(folder, facts, key):
    """화면 3-1. 무료 계획을 먼저 보여주고 Enter 로 시작을 받습니다."""
    if facts["mp4"] and not app.find_tool("ffmpeg"):
        app.ensure_ffmpeg()
    free_keys = app.key_entries_for_tier("free")
    paid_keys = app.key_entries_for_tier("paid")
    prepared = facts["stage"] == "prepared"
    lines = []
    error = ""
    if not free_keys or prepared:
        try:
            lines = app.estimate_lines(folder, app.folder_model(folder))
        except (RuntimeError, OSError) as exc:
            error = str(exc)
    while True:
        app.header("전사", app.short_path(folder))
        detail = f"오디오 {facts['m4a'] + facts['mp4']}개"
        if facts["pdf"]:
            detail += " · PDF 참고자료"
        if facts["needs_convert"]:
            detail += " · MP4 먼저 변환"
        print(f"  {app.dim(detail)}")
        print()
        if lines:
            for line in lines:
                print(f"  {line}")
        elif not free_keys or prepared:
            app.notice("예상 비용을 계산하지 못했습니다.")
            if error:
                print(f"  {app.dim(app.clip(error, app.WIDTH - 4))}")
        preview = []
        overflow = False
        if free_keys and not prepared:
            try:
                preview = app.mixed_preview_lines(folder, app.folder_model(folder),
                                              paid_keys=[])
            except (RuntimeError, OSError, ValueError):
                preview = []
            overflow = any("유료 키 필요" in line for line in preview)
        if preview:
            print()
            for line in preview:
                print(f"  {line}")
        elif free_keys and not prepared:
            app.notice("무료 계획을 계산하지 못했습니다. e로 예상 비용을 확인하세요.")
        print(f"  {app.dim('l 무료 live · 첫 번째 무료 키 · 130분 그룹 · 자동 간격')}")
        print()
        app.status_bar(key)
        if prepared:
            app.footer("Enter 제출 이어서 하기 · p 유료 Batch · l 무료 live · "
                   "e 비용 · b 파일 · a 고급 · Esc 뒤로")
        elif free_keys and overflow:
            app.footer("Enter 무료 키만으론 부족 · p 유료 Batch 포함 · "
                   "k 키 관리 · e 비용 · b 파일 · a 고급 · Esc 뒤로")
        elif free_keys:
            app.footer("Enter 무료 전사 · p 유료 Batch 포함 · l 무료 live · "
                   "e 비용 · b 파일 · a 고급 · Esc 뒤로")
        else:
            app.footer("Enter 무료 키 추가 · p 유료 Batch · "
                   "e 비용 · b 파일 · a 고급 · Esc 뒤로")
        pressed = app.read_key()
        if pressed in ("enter", "right"):
            if prepared:
                return "start"
            if not free_keys:
                return "keys"
            if overflow:
                print()
                app.notice("무료 키 용량을 넘는 분량이 있습니다. p를 눌러 "
                       "유료 Batch를 포함하거나 k에서 무료 키를 추가하세요.")
                app.wait()
                continue
            return "mixed_free"
        if pressed == "p":
            if prepared:
                return "start"
            if not paid_keys:
                print()
                app.notice("유료 Batch에 사용할 유료 키가 없습니다. 설정에서 "
                       "유료 키를 추가하거나 Enter로 무료 전사를 실행하세요.")
                app.wait()
                continue
            if not free_keys:
                return "start"
            return "mixed_paid"
        if pressed in ("esc", "left"):
            return "back"
        if pressed == "l":
            if not free_keys:
                print()
                app.notice("무료 live에는 무료 키가 필요합니다. k에서 키를 추가하세요.")
                app.wait()
                continue
            return "live"
        if pressed == "k":
            return "keys"
        if pressed == "a":
            return "advanced"
        if pressed == "e":
            app.estimate_action(folder)
        elif pressed == "b":
            app.file_list_screen(folder)


def upload_session(folder, key, model=None):
    """제출 화면. 업로드 로그를 한 줄 상태로 보여줍니다."""
    model = model or app.folder_model(folder)
    app.header("업로드", folder.name)
    print(f"  {app.dim('오디오와 요청 목록을 올린 뒤 Batch를 제출합니다.')}")
    print()

    def action(client):
        with app.capture_logs(app.live_status):
            app.upload(folder, client, model, confirm=lambda _lines: True,
                   show_estimate=False)
        return True

    key, done = app.run_with_client(key, action, "유료 Batch", "paid")
    sys.stdout.write("\r\033[K")
    sys.stdout.flush()
    return key, done


def exhausted_free_session(folder, key):
    """무료 작업을 보존하거나, 동의 후 폴더 전체를 유료로 새로 시작합니다."""
    if not sys.stdin.isatty():
        raise app.FreeKeysExhausted("무료 키가 모두 제한되었습니다. 나중에 다시 실행하세요.")
    model = app.folder_model(folder)
    try:
        values = app.estimate_values(folder, model)
    except (RuntimeError, OSError) as exc:
        app.notice(f"유료 예상 비용을 계산하지 못했습니다: {exc}")
        app.wait()
        return app.folder_session(folder, key)
    answer = app.select_menu("무료 키가 모두 제한되었습니다. 유료 키로 진행할까요?", [
        ("paid", "유료 새 작업", "기존 기록을 보관하고 폴더 전체를 다시 전사합니다"),
        ("stop", "중단", "무료 작업 기록을 유지합니다"),
    ], [("범위", "폴더 전체 · 무료 완료분도 다시 전사"),
        ("오디오", f"{values['files']}개 · {app.duration_text(values['seconds'])}"),
        ("예상 Batch 비용", app.money(values["cost"]))], allow_back=True,
        heading="무료 키 제한", subtitle=folder.name)
    if answer != "paid":
        return app.folder_session(folder, key)
    paid_key = app.paid_key_for_batch(key)
    if not paid_key:
        added_key = app.add_key_flow(default_tier="paid")
        if not added_key or app.key_tier_of(added_key) != "paid":
            app.notice("유료 키 등록을 취소했습니다. 무료 작업 기록을 유지합니다.")
            return app.folder_session(folder, key)
        paid_key = added_key
    try:
        app.archive_job(folder)
    except (RuntimeError, OSError) as exc:
        app.notice(str(exc))
        app.wait()
        return app.folder_session(folder, key)
    key, done = app.upload_session(folder, paid_key, model=model)
    if done:
        return app.wait_session(folder, key)
    return app.folder_session(folder, key)


def mixed_session(folder, key, include_paid=True):
    """무료 live를 먼저 돌리고, 필요할 때만 유료 Batch를 붙입니다."""
    state = app.read_json_object(app.live_state_path(folder))
    resume = bool(state and state.get("mode") == "mixed")
    paid_keys = app.key_entries_for_tier("paid") if include_paid else []
    show_paid = include_paid and (
        bool(state.get("paid")) if resume else bool(paid_keys))
    app.header("자동 전사" if show_paid else "무료 전사", folder.name)
    if show_paid:
        print(f"  {app.dim('무료 키를 먼저 쓰고, 남는 오디오는 유료 Batch로 보냅니다.')}")
    else:
        print(f"  {app.dim('무료 키만 사용합니다 · Batch 없음')}")
    print()
    plan = []
    if resume:
        plan = app.mixed_plan_lines(state)
    else:
        try:
            plan = app.mixed_preview_lines(folder, app.folder_model(folder),
                                       paid_keys=paid_keys)
        except (RuntimeError, OSError, ValueError):
            plan = []
    if plan:
        for line in plan:
            print(f"  {line}")
    else:
        print(f"  {app.dim('ffprobe로 길이를 읽지 못해 계획을 미리 표시하지 못했습니다.')}")
    if any("유료 키 필요" in line for line in plan):
        print()
        if include_paid:
            app.notice("유료 키로도 남는 분량이 있습니다. 유료 키를 추가하세요.")
        else:
            app.notice("무료 키 용량을 넘는 분량이 있습니다. 유료 Batch를 포함하거나 "
                   "무료 키를 추가하세요.")
        app.wait()
        return app.folder_session(folder, key)
    will_use_paid = (bool(state.get("paid")) if resume
                     else any("유료 Batch" in line for line in plan))
    if include_paid and will_use_paid and not resume:
        print()
        app.footer("Enter 유료 Batch 포함 실행 · Esc 취소")
        if app.read_key() not in ("enter", "right"):
            return app.folder_session(folder, key)
    print()
    app.status_bar(key)
    print()
    try:
        with app.capture_logs(app.live_status):
            app.mixed_transcribe(folder, model=app.folder_model(folder),
                             include_paid=include_paid)
    except app.FreeKeysExhausted as exc:
        sys.stdout.write("\r\033[K")
        sys.stdout.flush()
        app.notice(str(exc))
        return app.exhausted_free_session(folder, key)
    except KeyboardInterrupt:
        sys.stdout.write("\r\033[K")
        sys.stdout.flush()
        app.notice("중단했습니다. 완료된 그룹은 저장했습니다. 다시 실행하면 이어서 합니다.")
        app.wait()
        return app.folder_session(folder, key)
    except (RuntimeError, OSError, ValueError, KeyError) as exc:
        sys.stdout.write("\r\033[K")
        sys.stdout.flush()
        app.notice(str(exc))
        app.wait()
        return app.folder_session(folder, key)
    except Exception as exc:  # API 오류만 화면으로 보여줍니다.
        api_error = app._api_error_class()
        if api_error is None or not isinstance(exc, api_error):
            raise
        sys.stdout.write("\r\033[K")
        sys.stdout.flush()
        app.notice(f"API 오류: {exc}")
        app.wait()
        return app.folder_session(folder, key)
    sys.stdout.write("\r\033[K")
    sys.stdout.flush()
    return app.done_screen(folder, key)


def live_session(folder, key):
    """무료 live 전사. 130분 그룹을 순차 요청하고 그룹별로 이어서 합니다."""
    state = app.read_json_object(app.live_state_path(folder))
    if state and state.get("mode") == "mixed":
        return app.mixed_session(folder, key)
    free = app.key_entries_for_tier("free")
    if free:
        key = free[0]["api_key"]
    app.header("무료 live 전사", folder.name)
    print(f"  {app.dim('130분 그룹 · 순차 요청 · 자동 간격 · Batch 없이')}")
    print()

    def action(client):
        with app.capture_logs(app.live_status):
            app.live_transcribe(folder, client)
        return True

    try:
        key, done = app.run_with_client(key, action, "live 전사", "free")
    except KeyboardInterrupt:
        sys.stdout.write("\r\033[K")
        sys.stdout.flush()
        app.notice("중단했습니다. 다시 실행하면 이어서 합니다.")
        app.wait()
        return app.folder_session(folder, key)
    sys.stdout.write("\r\033[K")
    sys.stdout.flush()
    if not done:
        app.notice("완료된 그룹은 저장했습니다. 다시 실행하면 이어서 합니다.")
        app.wait()
        return app.folder_session(folder, key)
    return app.done_screen(folder, key)


def wait_session(folder, key):
    """완료 대기와 결과 병합. Esc 는 백그라운드 전환입니다."""
    key = app.ensure_paid_key(key)
    if not key:
        return key

    def action(client):
        try:
            _, state, detached = app.wait_for_completion(
                folder, app.load(folder), client, track=True, allow_detach=True)
        except KeyboardInterrupt:
            if sys.stdout.isatty():
                print()
            return "detach"
        if detached:
            return "detach"
        if state != "JOB_STATE_SUCCEEDED":
            app.notice(f"작업이 완료되지 않았습니다: {app.format_state(state)}")
            return "error"
        app.notify("날먹", f"{folder.name} 전사 완료")
        print(f"  {app.accent('✓')} 전사 완료 · 결과를 병합합니다")
        print()
        with app.capture_logs(app.live_status):
            result = app.download(folder, app.load(folder), client, "none", 3,
                              app.choose_retry, validate=False)
        return "back" if result == "back" else "done"

    key, outcome = app.run_with_client(key, action, "완료 대기", "paid")
    sys.stdout.write("\r\033[K")
    sys.stdout.flush()
    if outcome == "detach":
        _, message = app.start_watcher(folder, key)
        app.message_screen(folder, "백그라운드 전환", [
            message,
            "완료되면 알림과 함께 결과가 자동으로 병합됩니다.",
        ], hint="Enter 돌아가기")
    elif outcome == "done":
        app.done_screen(folder, key)
    elif outcome == "error":
        app.message_screen(folder, "완료되지 않음", [
            "고급 메뉴(a)에서 상태를 확인하거나 재시도할 수 있습니다.",
        ])
    return key


def merge_session(folder, key):
    key = app.ensure_paid_key(key)
    if not key:
        return key
    app.header("결과 병합", folder.name)
    print(f"  {app.dim('받은 결과로 Markdown 을 만듭니다.')}")
    print()
    outcome = {}

    def action(client):
        with app.capture_logs(app.live_status):
            outcome["result"] = app.download(folder, app.load(folder), client, "none", 3,
                                         app.choose_retry, validate=False)
        return True

    key, _ = app.run_with_client(key, action, "결과 병합", "paid")
    sys.stdout.write("\r\033[K")
    sys.stdout.flush()
    if outcome.get("result") != "back":
        app.done_screen(folder, key)
    return key


def done_screen(folder, key):
    """완료·변경·비어 있음 등 마무리 상태를 한 화면에서 처리합니다."""
    while True:
        facts = app.inspect_folder(folder)
        app.header("완료" if facts["output"] else "상태", app.short_path(folder))
        if facts["output"]:
            print(f"  {app.accent('✓')} {app.bold(app.output_path(folder).name)}")
            print()
        print(f"  {app.pad('상태', app.LABEL_WIDTH)}  {app.stage_label(facts)}")
        print(f"  {app.pad('폴더', app.LABEL_WIDTH)}  {app.short_path(folder)}")
        lines = []
        job = app.read_json_object(app.state_path(folder))
        if job is None:
            job = app.read_json_object(app.live_state_path(folder))
        if job:
            try:
                lines = app.cost_lines(folder, job)
            except (RuntimeError, OSError, ValueError):
                lines = []
        if lines:
            print()
            for line in lines:
                print(f"  {line}")
        print()
        app.status_bar(key)
        app.footer("Enter 결과 열기 · c 비용 · d 폴더 · n 새 작업 · a 고급 · Esc 뒤로")
        pressed = app.read_key()
        if pressed in ("esc", "left", "q"):
            return key
        if pressed in ("enter", "right") and facts["output"]:
            app.open_result_action(folder)
        elif pressed == "c":
            app.show_lines("비용", folder.name, lines or ["아직 비용 기록이 없습니다."])
        elif pressed == "d":
            app.file_list_screen(folder)
        elif pressed == "n":
            app.new_job_flow(folder)
            return key
        elif pressed == "a":
            return app.folder_session(folder, key)


def session_screen(folder, key):
    """화면 3. 폴더 상태에 맞는 작업을 한 화면에서 끝냅니다."""
    facts = app.inspect_folder(folder)
    if not facts["exists"]:
        app.message_screen(folder, "폴더 없음", ["폴더가 사라졌습니다."])
        return key
    stage = facts["stage"]
    if stage in ("ready", "prepared"):
        key = app.ensure_key(key)
        if not app.current_key(key):
            return key
        choice = app.start_screen(folder, facts, key)
        if choice == "back":
            return key
        if choice == "advanced":
            return app.folder_session(folder, key)
        if choice == "keys":
            key = app.manage_keys(key)
            return app.session_screen(folder, key)
        if choice == "mixed_free":
            return app.mixed_session(folder, key, include_paid=False)
        if choice == "mixed_paid" or choice == "mixed":
            return app.mixed_session(folder, key, include_paid=True)
        if choice == "live":
            return app.live_session(folder, key)
        if facts["needs_convert"]:
            if not app.capabilities()["ffmpeg"]:
                app.message_screen(folder, "ffmpeg 필요", [
                    "MP4 → M4A 변환에는 ffmpeg가 필요합니다.",
                    "macOS: brew install ffmpeg",
                    "Ubuntu: sudo apt install ffmpeg",
                ])
                return key
            app.header("변환", folder.name)
            print(f"  {app.dim('MP4 → M4A · 원본은 그대로 둡니다')}")
            print()
            try:
                app.convert(folder)
            except (RuntimeError, OSError) as exc:
                app.message_screen(folder, "변환 실패", [str(exc)])
                return key
            print()
        key = app.ensure_paid_key(key)
        if not key:
            return key
        key, done = app.upload_session(folder, key)
        if not done:
            app.message_screen(folder, "제출하지 못했습니다", [
                "업로드가 끝나지 않았습니다. 다시 시도하거나 고급 메뉴(a)를 쓰세요.",
            ])
            return key
        return app.wait_session(folder, key)
    if stage == "live":
        state = app.read_json_object(app.live_state_path(folder))
        if state and state.get("mode") == "mixed":
            return app.mixed_session(folder, key)
        return app.live_session(folder, key)
    if facts["watcher"]:
        app.progress_screen(folder)
        return key
    if facts["submitted"]:
        return app.wait_session(folder, key)
    if stage == "received":
        if facts.get("live"):
            return app.live_session(folder, key)
        return app.merge_session(folder, key)
    if stage == "done":
        return app.done_screen(folder, key)
    # 파일 없음 · 입력 확인 필요 · 변경됨 · 손상 → 고급 메뉴로 위임
    return app.folder_session(folder, key)


def show_drop_summary(results):
    """드롭한 항목별 결과와 문서 폴더 위치를 보여줍니다."""
    app.header("드롭 결과", f"{len(results)}개 항목")
    copied = 0
    for folder, target, error in results:
        name = folder.name if folder is not None else "건너뜀"
        if target:
            copied += 1
            print(f"  완료  {name}  →  {app.short_path(target)}")
        elif error:
            print(f"  실패  {name}  {error}")
        else:
            print(f"  대기  {name}  결과가 아직 없습니다.")
    print()
    if copied:
        print(f"  결과 폴더  {app.short_path(app.documents_dir())}")
        print()
    app.wait("Enter 닫기")


def drop_paths_screen():
    """터미널에 드롭한 경로 한 줄을 받습니다. 빈 입력이면 취소입니다."""
    app.header("드롭 · 붙여넣기", "폴더 또는 MP4/M4A")
    print("  파일이나 폴더를 이 창에 끌어다 놓으세요.")
    print(f"  {app.dim('경로가 붙여넣어지면 Enter. 직접 입력해도 됩니다.')}")
    app.footer("Enter 시작 · Ctrl+C 취소")
    try:
        text = app.prompt_box("경로")
    except (EOFError, KeyboardInterrupt):
        return []
    return app.split_pasted_paths(text)


def run_drop_folders(folders, skipped, key):
    """이미 전체 화면인 상태에서 드롭 항목을 차례로 전사하고 요약합니다."""
    results = []
    for folder in folders:
        app.remember_folder(folder)
        try:
            key = app.session_screen(folder, key)
            target, error = app.copy_result_to_documents(folder)
            results.append((folder, target, error))
        except KeyboardInterrupt:
            results.append((folder, None, "중단됨"))
            break
        except (RuntimeError, OSError, ValueError) as exc:
            results.append((folder, None, str(exc)))
    for name, reason in skipped:
        results.append((None, None, f"{name}: {reason}"))
    app.show_drop_summary(results)
    return key


def drop_session(raw_paths, key):
    """드롭·붙여넣은 경로를 정리해 전사합니다. 전체 화면 안에서 호출합니다."""
    folders, skipped = app.normalize_drop_paths(raw_paths)
    if not folders:
        app.header("드롭 · 붙여넣기", "항목 없음")
        app.notice("전사할 폴더나 MP4/M4A 파일이 없습니다.")
        for name, reason in skipped:
            print(f"  · {app.clip(name, app.WIDTH - 12)}: {reason}")
        app.wait()
        return key
    return app.run_drop_folders(folders, skipped, key)


def drop_flow(raw_paths):
    """바로가기·CLI로 받은 폴더·파일을 차례로 전사하고 결과를 모읍니다."""
    if not sys.stdin.isatty():
        app.fail("드롭 실행은 실제 터미널에서 해야 합니다.")
    app.require_tui()
    folders, skipped = app.normalize_drop_paths(raw_paths)
    if not folders:
        details = "".join(f"\n  · {name}: {reason}" for name, reason in skipped)
        app.fail("전사할 폴더나 MP4/M4A 파일이 없습니다." + details)
    with app.fullscreen():
        key = app.current_key()
        if not key:
            key = app.key_screen()
        if not app.current_key(key):
            app.fail("API 키가 없어 전사를 시작할 수 없습니다.")
        app.run_drop_folders(folders, skipped, key)


def interactive():
    if not sys.stdin.isatty():
        app.fail("터미널 메뉴는 터미널에서 실행하세요. 명령어 사용법: nalmeok --help")
    app.require_tui()
    with app.fullscreen():
        key = app.current_key()
        if not key:
            key = app.key_screen()
        while True:
            try:
                answer = app.home_screen(key)
            except KeyboardInterrupt:
                print()
                return
            except EOFError:
                return
            if answer is app.BACK or answer == "quit":
                return
            if isinstance(answer, tuple) and answer[0] == "drop-paste":
                paths = app.split_pasted_paths(answer[1])
                if paths:
                    key = app.drop_session(paths, key)
                continue
            if answer == "changekey":
                key = app.key_screen()
                continue
            try:
                if answer == "open":
                    folder = app.browse_folder(Path.cwd())
                    if folder is None:
                        continue
                    app.remember_folder(folder)
                    key = app.ensure_key(key)
                    if not app.current_key(key):
                        continue
                    key = app.session_screen(folder, key)
                elif answer == "multi":
                    key = app.ensure_key(key)
                    if not app.current_key(key):
                        continue
                    key = app.multi_submit(key)
                elif answer == "drop":
                    paths = app.drop_paths_screen()
                    if paths:
                        key = app.drop_session(paths, key)
                elif answer == "update":
                    app.update_screen()
                elif answer == "refresh":
                    key = app.refresh_remote(app.dashboard_folders(), key)
                elif answer == "stats":
                    jobs = [job for job in (app.usage_entry(folder)
                                            for folder in app.usage_folders()) if job]
                    app.show_lines("사용량", app.short_path(Path.cwd()),
                               app.stats_lines_from(jobs))
                elif answer == "settings":
                    key = app.settings_menu(key)
                elif isinstance(answer, str) and answer.startswith("folder:"):
                    folder = Path(answer[len("folder:"):])
                    if not folder.is_dir():
                        app.message_screen(folder, "폴더 없음", ["폴더가 사라졌습니다."])
                        continue
                    app.remember_folder(folder)
                    key = app.ensure_key(key)
                    if not app.current_key(key):
                        continue
                    key = app.session_screen(folder, key)
            except KeyboardInterrupt:
                continue
            except (RuntimeError, OSError, ValueError, KeyError, AttributeError) as exc:
                app.notice(str(exc))
                try:
                    app.wait()
                except (KeyboardInterrupt, EOFError):
                    return

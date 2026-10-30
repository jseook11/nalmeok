"""nalmeok.cli."""

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
from . import screens  # noqa: F401
from . import term  # noqa: F401
from . import update  # noqa: F401



def parse_args(argv=None):
    argv = list(sys.argv[1:] if argv is None else argv)
    # 서브커맨드가 이미 앞에 있으면 옵션 --download 등을 legacy 로 바꾸지 않습니다.
    if not argv or argv[0] not in app.COMMANDS:
        legacy = [flag for flag in ("--upload", "--status", "--download", "--convert")
                  if flag in argv]
        if len(legacy) == 1:
            argv.remove(legacy[0])
            argv.insert(0, legacy[0][2:])
    parser = argparse.ArgumentParser(
        prog="nalmeok",
        description="폴더 단위 Gemini 음성 전사 (Batch · 무료 live)",
        epilog="인자 없이 실행하면 터미널 메뉴를 엽니다.")
    parser.add_argument("--version", action="version", version=f"%(prog)s {app.__version__}")
    commands = parser.add_subparsers(dest="command")
    for name, help_text in (
        ("convert", "폴더의 모든 MP4를 M4A로 변환 (원본 유지)"),
        ("estimate", "길이·토큰·예상 비용 계산"),
        ("upload", "M4A 업로드 및 Batch 제출"),
        ("status", "Batch 상태 조회"),
        ("wait", "Batch 완료까지 대기 (--download 로 자동 다운로드)"),
        ("download", "결과 다운로드 및 Markdown 병합"),
        ("live", "무료 등급용 live 전사 (130분 그룹 · Batch 없이)"),
        ("auto", "무료 키 먼저 → 남는 오디오는 유료 Batch"),
        ("stats", "지금까지 쓴 금액 합계와 평균"),
        ("doctor", "환경 진단 정보 출력 (문제 보고용)"),
        ("drop", "폴더·MP4·M4A를 받아 전사하고 문서 폴더에 모음"),
        ("update", "앱 코드·SDK 업데이트 확인 (패키징 설치용)"),
    ):
        command = commands.add_parser(name, help=help_text)
        if name == "drop":
            command.add_argument("paths", nargs="+",
                                 help="폴더 또는 MP4/M4A 파일 (여러 개 가능)")
            continue
        if name == "update":
            command.add_argument("--check", action="store_true",
                                 help="확인만 하고 적용하지 않습니다")
            continue
        if name == "stats":
            command.add_argument("folder", nargs="?", default=".",
                                 help="집계할 상위 폴더 (기본값: 현재 폴더)")
            continue
        command.add_argument("folder", nargs="?", default=".",
                             help="음성 폴더 (기본값: 현재 폴더)")
        if name in ("estimate", "upload", "live", "auto"):
            command.add_argument("--model", default=app.DEFAULT_MODEL,
                                 choices=sorted(app.MODELS), help="사용할 모델")
        if name in ("upload", "status", "wait", "download", "live"):
            command.add_argument("--key",
                                 help="API 키 (기본값: GEMINI_API_KEY 환경변수)")
        if name == "auto":
            command.add_argument("--yes", action="store_true",
                                 help="예상 비용 확인 없이 바로 시작 (자동 실행용)")
        if name == "live":
            command.add_argument("--gap", type=int, default=app.LIVE_GAP_SECONDS,
                                 help="요청 시작 간 최소 간격(초, 기본값: 12; 실제 TPM에 따라 자동 증가)")
            command.add_argument("--chunk-minutes", type=int, default=130,
                                 help="한 요청에 묶을 최대 오디오 길이(분, 기본값: 130)")
        if name == "upload":
            command.add_argument("--yes", action="store_true",
                                 help="예상 비용 확인 없이 바로 제출 (자동 실행용)")
        if name == "wait":
            command.add_argument("--download", action="store_true",
                                 help="완료되면 자동으로 결과 다운로드")
            command.add_argument("--background", action="store_true",
                                 help="기다리지 않고 백그라운드 감시로 넘깁니다")
        if name in ("wait", "download"):
            command.add_argument("--retry", choices=("none", "live", "batch"),
                                 default="none",
                                 help="실패 파일 재시도: none=안 함, live=일반 API, "
                                      "batch=새 Batch (기본값: none)")
            command.add_argument("--attempts", type=int, default=3,
                                 help="live 재시도 횟수, 파일당 (기본값: 3)")
    args = parser.parse_args(argv)
    if args.command in ("wait", "download"):
        if args.attempts < 1:
            parser.error("--attempts는 1 이상이어야 합니다.")
        if "--attempts" in argv and args.retry != "live":
            parser.error("--attempts는 --retry live에서만 사용합니다.")
    if args.command == "live":
        if args.gap < 0:
            parser.error("--gap은 0 이상이어야 합니다.")
        if not 1 <= args.chunk_minutes <= 130:
            parser.error("--chunk-minutes는 1~130 사이여야 합니다.")
    return args


def run_command(command, folder, client, model=app.DEFAULT_MODEL, retry="none",
                attempts=3, choose_retry=None, confirm=None, wait_download=False):
    if command == "convert":
        return app.convert(folder)
    if command == "upload":
        return app.upload(folder, client, model, confirm)
    if command == "status":
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
                app.log("\n재시도 Batch")
                app.status(recovery["pending_batch"], client)
        return
    if command == "wait":
        job = app.load(folder)
        _, state, _ = app.wait_for_completion(folder, job, client)
        if state != "JOB_STATE_SUCCEEDED":
            app.fail(f"작업이 완료되지 않았습니다: {app.format_state(state)}")
        app.notify("날먹", f"{folder.name} 전사 완료")
        app.log("전사가 완료되었습니다. 결과를 내려받습니다." if wait_download
            else "전사가 완료되었습니다. download로 결과를 받으세요.")
        if not wait_download:
            return
        app.log("")
        return app.download(folder, app.load(folder), client, retry, attempts, choose_retry)
    return app.download(folder, app.load(folder), client, retry, attempts, choose_retry)


def background_entry(argv):
    """백그라운드 대기 프로세스의 내부 진입점. 도움말에는 노출하지 않습니다."""
    parser = argparse.ArgumentParser(add_help=False)
    parser.add_argument("folder")
    parser.add_argument("--retry", choices=("none", "live", "batch"), default="none")
    parser.add_argument("--attempts", type=int, default=3)
    args = parser.parse_args(argv)
    folder = Path(args.folder).expanduser().resolve()
    if not folder.is_dir():
        app.fail(f"폴더가 없습니다: {folder}")
    app.background_wait(folder, args.retry, args.attempts)


def main():
    if len(sys.argv) > 1 and sys.argv[1] == "_wait":
        app.background_entry(sys.argv[2:])
        return
    if len(sys.argv) == 1:
        app.interactive()
        return
    args = app.parse_args()
    if args.command is None:
        app.interactive()
        return
    if args.command == "doctor":
        app.show_doctor()
        return
    if args.command == "drop":
        app.drop_flow(args.paths)
        return
    if args.command == "update":
        app.update_command(check=args.check)
        return
    folder = Path(args.folder).expanduser().resolve()
    if not folder.is_dir():
        app.fail(f"폴더가 없습니다: {folder}")
    if args.command == "stats":
        app.section("지금까지 사용량", str(folder))
        for line in app.stats_lines(folder):
            app.log(line)
        return
    if args.command == "estimate":
        app.section("예상 비용", str(folder))
        for line in app.estimate_lines(folder, args.model):
            app.log(line)
        return
    if args.command == "convert":
        app.convert(folder)
        return
    if args.command in ("upload", "status", "wait", "download", "live", "auto"):
        app.remember_folder(folder)
    if args.command == "auto":
        if not app.key_entries():
            app.fail("등록된 API 키가 없습니다. 먼저 설정에서 무료/유료 키를 추가하세요.")
        free_keys = app.key_entries_for_tier("free")
        paid_keys = app.key_entries_for_tier("paid")
        preview = app.mixed_preview_lines(folder, args.model) if free_keys else []
        if not preview:
            try:
                preview = app.estimate_lines(folder, args.model)
            except (RuntimeError, OSError) as exc:
                preview = [f"예상 비용을 계산하지 못했습니다: {exc}"]
        app.section("자동 전사", str(folder))
        for line in preview:
            app.log(line)
        if any("유료 키 필요" in line for line in preview):
            app.fail("무료 키 용량을 넘는 분량이 있습니다. 유료 키를 추가하거나 "
                 "live로 전체를 무료 처리하세요.")
        if free_keys:
            needs_paid = any("유료 Batch" in line for line in preview)
        else:
            needs_paid = bool(paid_keys)
        if (needs_paid and not getattr(args, "yes", False)
                and not app.confirm_spend(preview)):
            return
        app.mixed_transcribe(folder, model=args.model)
        return
    if args.command == "live":
        free = app.key_entries_for_tier("free")
        if args.key:
            tier = app.key_tier_of(args.key)
            if tier == "paid":
                app.fail("live는 무료 키 전용입니다. 무료 키를 등록하거나 "
                     "GEMINI_API_KEY_TIER=free 로 지정하세요.")
        key = args.key or (free[0]["api_key"] if free else None)
        if not key:
            app.fail("무료 키가 없습니다. 무료 키를 등록하거나 "
                 "GEMINI_API_KEY_TIER=free 로 지정하세요.")
        client = app.get_client(key)
        try:
            app.live_transcribe(folder, client, model=args.model, gap=args.gap,
                            chunk_seconds=args.chunk_minutes * 60)
        finally:
            client.close()
        return
    if args.key and app.key_tier_of(args.key) == "free":
        app.fail("무료 키는 Batch API에 사용할 수 없습니다. "
             "유료 키를 등록하거나 live/auto 모드를 사용하세요.")
    batch_key = args.key or app.key_for_tier("paid")
    if not batch_key:
        app.fail("유료 Batch에는 유료 API 키가 필요합니다. 설정에서 유료 키를 추가하세요.")
    if args.command == "wait" and getattr(args, "background", False):
        ok, message = app.start_watcher(folder, batch_key, allow_unknown=True)
        print(message)
        if not ok:
            sys.exit(1)
        return
    confirm = (lambda lines: True) if getattr(args, "yes", False) else None
    model = getattr(args, "model", app.DEFAULT_MODEL) or app.DEFAULT_MODEL
    client = app.get_client(batch_key)
    try:
        app.run_command(args.command, folder, client, model,
                    getattr(args, "retry", "none"), getattr(args, "attempts", 3),
                    confirm=confirm,
                    wait_download=getattr(args, "download", False))
    finally:
        client.close()


def cli():
    """콘솔 스크립트와 __main__ 진입점. 사용자 오류를 traceback 없이 보여줍니다."""
    app.bootstrap_platform()
    try:
        app.main()
    except (KeyboardInterrupt, EOFError):
        print("\n종료했습니다.")
    except (RuntimeError, OSError, ValueError, KeyError) as exc:
        print(f"오류: {exc}", file=sys.stderr)
        sys.exit(1)
    except Exception as exc:
        api_error = app._api_error_class()
        if api_error is None or not isinstance(exc, api_error):
            raise
        print(f"API 오류: {exc}", file=sys.stderr)
        sys.exit(1)

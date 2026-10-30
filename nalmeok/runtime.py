"""nalmeok.runtime."""

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



def fail(message):
    raise RuntimeError(message)


def log(message=""):
    app._logger(message)


def _print_logger(message=""):
    text = str(message)
    if not text:
        print(flush=True)
        return
    print("\n".join(f"  {line}" if line else "" for line in text.splitlines()),
          flush=True)
    if os.getenv("NALMEOK_DEBUG"):
        try:
            path = app.debug_log_path()
            path.parent.mkdir(parents=True, exist_ok=True)
            stamp = time.strftime("%Y-%m-%d %H:%M:%S")
            with open(path, "a", encoding="utf-8") as handle:
                handle.write(f"[{stamp}] {text}\n")
        except OSError:
            pass


_logger = _print_logger


def set_logger(writer):
    global _logger
    app._logger = writer


@contextlib.contextmanager
def capture_logs(render):
    """log() 출력을 화면 갱신 함수로 돌립니다. 끝나면 원래 logger 로 복구합니다."""
    global _logger
    previous = app._logger
    app._logger = render
    try:
        yield
    finally:
        app._logger = previous


def configure_stdio():
    """리다이렉트·SSH 환경에서도 한글이 깨지지 않도록 표준 스트림을 UTF-8로 맞춥니다."""
    for stream in (sys.stdout, sys.stderr):
        try:
            stream.reconfigure(encoding="utf-8", errors="replace")
        except (AttributeError, ValueError, OSError):
            pass


def _default_file_mode():
    mask = os.umask(0)
    os.umask(mask)
    return 0o666 & ~mask


def atomic_write_text(path, text, mode=None):
    """같은 폴더의 고유한 임시 파일에 쓴 뒤 교체합니다.

    고정된 `.tmp` 이름을 쓰면 메뉴와 백그라운드 대기가 동시에 쓸 때
    서로의 임시 파일을 지울 수 있어 mkstemp 로 이름을 분리합니다.
    """
    path = Path(path)
    handle, temp_name = tempfile.mkstemp(
        prefix=".nalmeok-", suffix=".tmp", dir=path.parent)
    try:
        with os.fdopen(handle, "w", encoding="utf-8") as file:
            file.write(text)
            file.flush()
            os.fsync(file.fileno())
        try:
            os.chmod(temp_name, app._default_file_mode() if mode is None else mode)
        except OSError:
            pass
        # Windows는 AV/인덱서가 대상 파일을 잠시 잡고 있으면 교체가 실패할 수 있습니다.
        last_error = None
        for attempt in range(5):
            try:
                os.replace(temp_name, path)
                return
            except PermissionError as exc:
                last_error = exc
                time.sleep(0.05 * (attempt + 1))
        assert last_error is not None
        raise last_error
    except BaseException:
        try:
            os.unlink(temp_name)
        except OSError:
            pass
        raise


def save(path, data):
    app.atomic_write_text(path, json.dumps(data, ensure_ascii=False, indent=2))


def config_path():
    """API 키와 최근 작업 목록을 두는 설정 파일. XDG 규약을 따릅니다."""
    base = os.getenv("XDG_CONFIG_HOME")
    if not base and os.name == "nt":
        base = os.getenv("APPDATA")
    root = Path(base) if base else Path.home() / ".config"
    return root / "nalmeok" / "config.json"


def load_config():
    try:
        data = json.loads(app.config_path().read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return {}
    return data if isinstance(data, dict) else {}


def debug_log_path():
    return app.config_path().with_name("nalmeok.log")


def save_config(data):
    path = app.config_path()
    path.parent.mkdir(parents=True, exist_ok=True)
    # API 키가 들어가므로 소유자만 읽도록 0600 으로 씁니다.
    app.atomic_write_text(path, json.dumps(data, ensure_ascii=False, indent=2), mode=0o600)


def _lock_file(handle, blocking=True):
    """파일 잠금을 잡습니다. 잠금 수단이 없는 플랫폼이면 False."""
    if app.fcntl is not None:
        mode = app.fcntl.LOCK_EX if blocking else app.fcntl.LOCK_EX | app.fcntl.LOCK_NB
        app.fcntl.flock(handle.fileno(), mode)
        return True
    if os.name == "nt" and app.msvcrt is not None:
        handle.seek(0)
        mode = app.msvcrt.LK_LOCK if blocking else app.msvcrt.LK_NBLCK
        app.msvcrt.locking(handle.fileno(), mode, 1)
        return True
    return False


def _unlock_file(handle):
    try:
        if app.fcntl is not None:
            app.fcntl.flock(handle.fileno(), app.fcntl.LOCK_UN)
        elif os.name == "nt" and app.msvcrt is not None:
            handle.seek(0)
            app.msvcrt.locking(handle.fileno(), app.msvcrt.LK_UNLCK, 1)
    except OSError:
        pass


@contextlib.contextmanager
def config_lock():
    """설정 파일 read-modify-write 를 프로세스 간에 직렬화합니다."""
    path = app.config_path().with_suffix(".lock")
    path.parent.mkdir(parents=True, exist_ok=True)
    with open(path, "a+", encoding="utf-8") as handle:
        locked = False
        try:
            locked = app._lock_file(handle, blocking=True)
            yield
        finally:
            if locked:
                app._unlock_file(handle)


def short_path(path):
    try:
        relative = Path(path).relative_to(Path.home())
        return str(Path("~") / relative)
    except ValueError:
        return str(path)


def process_alive(pid):
    if pid <= 0:
        return False
    if os.name == "nt":
        # Windows 의 os.kill(pid, 0) 은 프로세스를 종료시킵니다. 핸들로 확인합니다.
        import ctypes

        synchronize = 0x00100000
        wait_timeout = 0x00000102
        handle = ctypes.windll.kernel32.OpenProcess(synchronize, False, pid)
        if not handle:
            return False
        try:
            return ctypes.windll.kernel32.WaitForSingleObject(handle, 0) == wait_timeout
        finally:
            ctypes.windll.kernel32.CloseHandle(handle)
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    except OSError:
        return False
    return True


def safe_exists(path):
    """권한이 없거나 읽을 수 없는 경로는 False 로 봅니다."""
    try:
        return Path(path).exists()
    except OSError:
        return False


def natural_key(filename):
    return [int(v) if v.isdigit() else v.casefold()
            for v in re.split(r"(\d+)", filename)]


def output_path(folder):
    return folder / (folder.name + ".md")


def documents_dir():
    """드래그앤드롭으로 만든 결과를 모아 두는 사용자 문서 폴더.

    Windows에서는 셸이 알고 있는 Documents 경로를 씁니다. OneDrive로 리디렉트된
    PC에서 `Path.home()/Documents`를 새로 만들면 탐색기의 '문서'에 보이지 않습니다.
    """
    if app.IS_WINDOWS and app.winreg is not None:
        try:
            with app.winreg.OpenKey(
                    app.winreg.HKEY_CURRENT_USER,
                    r"Software\Microsoft\Windows\CurrentVersion"
                    r"\Explorer\User Shell Folders") as key:
                value, _ = app.winreg.QueryValueEx(key, "Personal")
            if isinstance(value, str) and value.strip():
                return Path(os.path.expandvars(value)) / "nalmeok"
        except OSError:
            pass
    return Path.home() / "Documents" / "nalmeok"


def drop_jobs_dir():
    """드롭한 단일 파일을 전사할 별도 작업 폴더의 상위 경로."""
    if app.IS_WINDOWS:
        base = os.environ.get("LOCALAPPDATA")
        if base:
            return Path(base) / "nalmeok" / "jobs"
    base = os.environ.get("XDG_DATA_HOME")
    if base:
        return Path(base) / "nalmeok" / "jobs"
    return Path.home() / ".local" / "share" / "nalmeok" / "jobs"


def _available_job_dir(stem):
    base = app.drop_jobs_dir()
    candidate = base / stem
    index = 2
    while candidate.exists():
        candidate = base / f"{stem}-{index}"
        index += 1
    return candidate


def split_pasted_paths(text):
    """터미널에 드롭·붙여넣은 경로 문자열을 인자 목록으로 나눕니다.

    macOS Terminal/iTerm2는 공백을 `\\ `로 이스케이프하고, Windows Terminal은
    경로를 큰따옴표로 감쌉니다. 두 형식을 모두 받아들입니다.
    """
    text = str(text).strip()
    if not text:
        return []
    try:
        tokens = shlex.split(text, posix=not app.IS_WINDOWS)
    except ValueError:
        tokens = [text]
    paths = []
    for token in tokens:
        token = token.strip()
        if len(token) >= 2 and token[0] == token[-1] and token[0] in "\"'":
            token = token[1:-1]
        if token:
            paths.append(token)
    return paths


def file_job_folder(source):
    """드롭한 미디어 파일 하나를 자기만의 작업 폴더로 가져옵니다.

    같은 폴더의 다른 강의와 섞이지 않도록 별도 폴더를 만들고, 원본은 하드링크로
    연결합니다(다른 볼륨이거나 링크가 안 되면 복사). 원본 파일은 그대로 둡니다.
    """
    folder = app._available_job_dir(source.stem)
    folder.mkdir(parents=True)
    target = folder / source.name
    try:
        os.link(source, target)
    except OSError:
        shutil.copy2(source, target)
    return folder


def normalize_drop_paths(raw_paths):
    """드롭 인자를 작업 폴더 목록과 건너뛴 항목으로 나눕니다."""
    folders = []
    skipped = []
    seen = set()
    for raw in raw_paths:
        text = str(raw).strip().strip('"')
        if not text:
            continue
        path = Path(text).expanduser()
        try:
            marker = path.resolve()
        except OSError:
            marker = path.absolute()
        if marker in seen:
            continue
        seen.add(marker)
        if path.is_dir():
            folders.append(path)
            continue
        if path.is_file() and path.suffix.lower() in app.DROP_FILE_SUFFIXES:
            folders.append(app.file_job_folder(path))
            continue
        reason = "지원하지 않는 형식" if path.is_file() else "경로를 찾을 수 없음"
        skipped.append((text, reason))
    return folders, skipped


def result_copy_path(directory, name):
    """문서 폴더에서 겹치지 않는 결과 파일 경로를 고릅니다."""
    directory = Path(directory)
    candidate = directory / f"{name}.md"
    index = 2
    while candidate.exists():
        candidate = directory / f"{name} ({index}).md"
        index += 1
    return candidate


def copy_result_to_documents(folder, directory=None):
    """완료된 결과를 문서 폴더로 복사합니다. (복사본, 오류)를 돌려줍니다."""
    source = app.output_path(folder)
    if not source.is_file():
        return None, ""
    target_dir = Path(directory) if directory else app.documents_dir()
    try:
        target_dir.mkdir(parents=True, exist_ok=True)
        target = app.result_copy_path(target_dir, folder.name)
        shutil.copy2(source, target)
    except OSError as exc:
        return None, str(exc)
    return target, ""


def state_path(folder):
    return folder / app.STATE_FILE


def read_json_object(path):
    """JSON 객체만 읽습니다. 없거나 손상됐거나 객체가 아니면 None."""
    try:
        data = json.loads(Path(path).read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return None
    return data if isinstance(data, dict) else None


def clean_path(text):
    """드래그 앤 드롭으로 들어온 따옴표·역슬래시 이스케이프를 풀어냅니다."""
    text = text.strip()
    if len(text) >= 2 and text[0] == text[-1] and text[0] in ('"', "'"):
        text = text[1:-1]
    if os.name != "nt":
        text = text.replace("\\ ", " ")
    return Path(text).expanduser()


def path_target(text):
    """입력한 경로가 폴더면 그대로, 미디어 파일이면 부모 폴더를 돌려줍니다."""
    try:
        path = app.clean_path(text).resolve()
    except (OSError, RuntimeError):
        return None
    if path.is_file():
        return path.parent if path.suffix.lower() in (".mp4", ".m4a") else None
    return path if path.is_dir() else None

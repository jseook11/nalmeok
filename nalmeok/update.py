"""nalmeok.update."""

import argparse
import contextlib
import getpass
import hashlib
import io
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
from . import runtime  # noqa: F401
from . import term  # noqa: F401



def sdk_importable():
    try:
        from google import genai  # noqa: F401
        return True
    except Exception:  # noqa: BLE001 - 손상된 설치도 '없음'으로 표시합니다.
        return False


def venv_path():
    """앱 코드 옆의 .venv. 패키지 배포에서는 패키지의 부모 폴더를 씁니다."""
    here = Path(app.__file__).resolve()
    root = here.parent.parent if here.name == "__init__.py" else here.parent
    return root / ".venv"


def project_python():
    """이 스크립트 옆 .venv 의 인터프리터. 없으면 None."""
    venv = app.venv_path()
    for name in ("bin/python", "bin/python3", "Scripts/python.exe"):
        candidate = venv / name
        if candidate.exists():
            return candidate
    return None


def has_sdk(python):
    result = subprocess.run([str(python), "-c", "import google.genai"],
                            check=False, capture_output=True)
    return result.returncode == 0


def python_usable(python):
    """venv 인터프리터가 실제로 실행되는지. Homebrew 업그레이드 등으로 깨질 수 있습니다."""
    try:
        result = subprocess.run([str(python), "-c", "import sys"],
                                check=False, capture_output=True, timeout=10)
    except (OSError, subprocess.SubprocessError):
        return False
    return result.returncode == 0


def packaged_mode():
    return os.getenv("NALMEOK_PACKAGED") == "1"


def auto_yes():
    return os.getenv("NALMEOK_YES") == "1"


def restart(python):
    """같은 인자로 .venv 인터프리터에서 이어서 실행합니다."""
    # 패키지 분리 뒤에는 __file__ 이 update.py 라서 그대로 실행하면 상대 import가
    # 깨집니다. 패키지 루트를 PYTHONPATH에 넣고 `-m nalmeok`으로 다시 시작합니다.
    root = Path(__file__).resolve().parent.parent
    parts = [str(root)]
    parts += [part for part in os.environ.get("PYTHONPATH", "").split(os.pathsep)
              if part]
    os.environ["PYTHONPATH"] = os.pathsep.join(parts)
    os.execv(str(python), [str(python), "-m", "nalmeok", *sys.argv[1:]])


def ensure_sdk():
    """SDK가 없으면 앱 코드 옆 .venv 로 갈아타거나, 만들어 설치한 뒤 갈아탑니다.

    시스템 파이썬은 건드리지 않습니다. 패키징 모드(NALMEOK_PACKAGED=1)에서는
    pyproject.toml이 없으므로 uv sync 대신 .venv의 pip를 씁니다.
    """
    if app.sdk_importable():
        return
    python = app.project_python()
    if python and app.has_sdk(python):
        app.restart(python)
    if python and not app.python_usable(python):
        shutil.rmtree(app.venv_path(), ignore_errors=True)
        python = None

    if not sys.stdin.isatty() and not app.auto_yes():
        app.fail(f"{app.SDK} SDK가 필요합니다.\n  uv run app.py\n  uv sync && ./app.py")
    where = "앱 데이터 폴더" if app.packaged_mode() else "저장소 안"
    print(f"\n{app.SDK} SDK가 필요합니다. {where}에 .venv 를 만들어 설치합니다.")
    print("(시스템 파이썬은 건드리지 않습니다.)")
    if app.auto_yes():
        print("자동으로 설치합니다.")
    elif input("지금 설치할까요? [y/N] ").strip().lower() not in ("y", "yes"):
        app.fail("설치를 건너뛰었습니다. 나중에: uv sync && ./app.py")

    uv = None if app.packaged_mode() else shutil.which("uv")
    if uv:
        command = [uv, "sync"]
    else:
        if not python:
            subprocess.call([sys.executable, "-m", "venv", str(app.venv_path())])
            python = app.project_python()
        if not python:
            app.fail("가상환경을 만들지 못했습니다. uv 를 설치해 주세요: https://docs.astral.sh/uv/")
        command = [str(python), "-m", "pip", "install", "-q", "-U", app.SDK_SPEC]
    print(f"$ {' '.join(command)}\n", flush=True)
    if subprocess.call(command) != 0:
        app.fail("자동 설치에 실패했습니다. 직접 설치해 주세요:\n  uv sync")

    python = app.project_python()
    if python and app.has_sdk(python):
        app.restart(python)
    app.fail(f"{app.SDK} 설치를 확인하지 못했습니다. 직접 확인해 주세요: uv sync")


def parse_version(text):
    """'0.10.1' 같은 버전을 비교 가능한 숫자 튜플로 바꿉니다."""
    parts = [int(part) for part in re.findall(r"\d+", str(text))[:4]]
    return tuple(parts) or (0,)


def fetch_manifest(url=None, timeout=15):
    import urllib.request

    url = url or os.getenv("NALMEOK_MANIFEST_URL") or app.manifest_url()
    with urllib.request.urlopen(url, timeout=timeout) as response:
        data = json.loads(response.read().decode("utf-8"))
    if not isinstance(data, dict):
        raise ValueError("업데이트 정보 형식이 올바르지 않습니다.")
    return data


def download_bytes(url, timeout=60):
    import urllib.request

    with urllib.request.urlopen(url, timeout=timeout) as response:
        return response.read()


def platform_key():
    return "windows" if app.IS_WINDOWS else "macos"


def platform_section(manifest):
    """manifest의 플랫폼 섹션. 예전(최상위 python/ffmpeg) 형식도 읽습니다."""
    platforms = manifest.get("platforms")
    if isinstance(platforms, dict):
        section = platforms.get(app.platform_key())
        if isinstance(section, dict):
            return section
    return {key: manifest[key] for key in ("python", "ffmpeg") if key in manifest}


def update_target(manifest):
    """manifest에서 (버전, URL, SHA-256)을 꺼냅니다."""
    info = manifest.get("nalmeok")
    if not isinstance(info, dict):
        raise ValueError("업데이트 정보에 nalmeok 항목이 없습니다.")
    version = str(info.get("version") or "").strip()
    url = str(info.get("url") or "").strip()
    digest = str(info.get("sha256") or "").strip().lower()
    if not version or not url or not digest:
        raise ValueError("업데이트 정보가 불완전합니다.")
    return version, url, digest


def package_root():
    """(실행 중인 패키지 경로, 그 부모). 패키지 배포에서는 부모가 앱 폴더입니다."""
    package = Path(app.__file__).resolve().parent
    return package, package.parent


def _validate_package(directory, version, source):
    init = directory / "__init__.py"
    constants = directory / "constants.py"
    if not init.is_file() or not constants.is_file():
        raise RuntimeError("압축 파일에 nalmeok 패키지가 없습니다.")
    text = constants.read_text(encoding="utf-8")
    if f'__version__ = "{version}"' not in text:
        raise RuntimeError("내려받은 파일의 버전이 일치하지 않습니다.")
    for path in directory.rglob("*.py"):
        compile(path.read_text(encoding="utf-8"), str(path), "exec")


def apply_update(manifest):
    """앱 패키지(nalmeok/)를 통째로 교체합니다. 다음 실행부터 적용됩니다."""
    if not app.packaged_mode():
        raise RuntimeError("소스 실행에서는 git pull로 업데이트하세요.")
    version, url, digest = app.update_target(manifest)
    if app.parse_version(version) <= app.parse_version(app.__version__):
        return f"이미 최신 버전입니다: {app.__version__}"

    data = app.download_bytes(url)
    actual = hashlib.sha256(data).hexdigest()
    if actual != digest:
        raise RuntimeError("내려받은 파일의 체크섬이 맞지 않습니다.")
    if not zipfile.is_zipfile(io.BytesIO(data)):
        raise RuntimeError("내려받은 파일이 앱 패키지(ZIP)가 아닙니다.")

    target, root = package_root()
    staging = root / ".nalmeok-next"
    backup = root / ".nalmeok-old"
    shutil.rmtree(staging, ignore_errors=True)
    try:
        with zipfile.ZipFile(io.BytesIO(data)) as archive:
            for name in archive.namelist():
                parts = Path(name).parts
                if name.startswith("/") or ".." in parts:
                    raise RuntimeError("압축 파일 경로가 올바르지 않습니다.")
            archive.extractall(staging)
        incoming = staging / "nalmeok"
        _validate_package(incoming, version, url)
        shutil.rmtree(backup, ignore_errors=True)
        if target.exists():
            target.rename(backup)
        try:
            incoming.rename(target)
        except OSError:
            if backup.exists() and not target.exists():
                backup.rename(target)
            raise
        shutil.rmtree(backup, ignore_errors=True)
    finally:
        shutil.rmtree(staging, ignore_errors=True)
    return f"nalmeok {version}로 업데이트했습니다. 다음 실행부터 적용됩니다."


def installed_sdk_version():
    try:
        from importlib.metadata import version

        return version(app.SDK)
    except Exception:  # noqa: BLE001 - 미설치·손상 모두 '없음'으로 봅니다.
        return None


def update_sdk(manifest):
    """manifest의 SDK 버전이 더 새로울 때만 .venv에서 갱신합니다."""
    info = manifest.get("sdk")
    if not isinstance(info, dict):
        return ""
    target = str(info.get("version") or "").strip()
    if not target:
        return ""
    current = app.installed_sdk_version()
    if current is None:
        return "SDK가 아직 설치되지 않았습니다. 다음 실행에서 설치합니다."
    if app.parse_version(current) >= app.parse_version(target):
        return f"SDK {current} (최신)"
    python = app.project_python() or Path(sys.executable)
    command = [str(python), "-m", "pip", "install", "-q", "-U",
               f"{app.SDK}=={target}"]
    if subprocess.call(command) != 0:
        raise RuntimeError("SDK 업데이트에 실패했습니다.")
    return f"SDK {current} → {target} (다음 실행부터 적용)"


def file_sha256(path):
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def download_file(url, destination, timeout=180):
    import urllib.request

    destination = Path(destination)
    with urllib.request.urlopen(url, timeout=timeout) as response, \
            destination.open("wb") as handle:
        total = int(response.headers.get("Content-Length") or 0)
        done = 0
        while True:
            chunk = response.read(1024 * 256)
            if not chunk:
                break
            handle.write(chunk)
            done += len(chunk)
            if total and sys.stdout.isatty():
                percent = done * 100 // total
                sys.stdout.write(
                    f"\r  {percent:3d}%  {done // (1024 * 1024)}/"
                    f"{total // (1024 * 1024)}MB")
                sys.stdout.flush()
        if total and sys.stdout.isatty():
            sys.stdout.write("\n")
            sys.stdout.flush()
    return destination


def update_screen():
    """홈에서 수동으로 업데이트를 확인합니다."""
    app.header("업데이트", "nalmeok v" + app.__version__)
    print("  최신 버전을 확인합니다...")
    try:
        manifest = app.fetch_manifest()
        latest = app.update_target(manifest)[0]
    except (OSError, ValueError, RuntimeError) as exc:
        app.notice(f"확인하지 못했습니다: {exc}")
        app.wait()
        return
    if app.parse_version(latest) <= app.parse_version(app.__version__):
        app.notice(f"이미 최신 버전입니다: {app.__version__}")
        app.wait()
        return
    app.header("업데이트", f"{app.__version__} → {latest}")
    print("  앱 코드와 SDK를 업데이트합니다.")
    print(f"  {app.dim('다음 실행부터 새 버전이 적용됩니다.')}")
    app.footer("Enter 업데이트 · Esc 취소")
    if app.read_key() not in ("enter", "right"):
        return
    try:
        app.log(app.apply_update(manifest))
        app.log(app.update_sdk(manifest))
    except (OSError, ValueError, RuntimeError) as exc:
        app.notice(str(exc))
    app.wait()


def update_command(check=False):
    manifest = app.fetch_manifest()
    latest = app.update_target(manifest)[0]
    app.section("업데이트", f"현재 {app.__version__} · 최신 {latest}")
    if check:
        app.log(f"현재: {app.__version__}")
        app.log(f"최신: {latest}")
        return
    app.log(app.apply_update(manifest))
    app.log(app.update_sdk(manifest))

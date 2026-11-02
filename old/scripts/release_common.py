"""릴리스 공통 산출물: 플랫폼 공통 앱 payload와 하나의 manifest.

앱 코드(nalmeok 패키지)는 순수 파이썬이라 Windows와 macOS가 같은 파일을
쓴다. 그래서 payload는 하나만 만들고, manifest도 하나에 플랫폼별 섹션
(``platforms.windows`` / ``platforms.macos``)만 나눠 담는다.

빌드 스크립트 두 개가 같은 체크아웃에서 연달아 실행돼도 서로의 섹션을
지우지 않도록 ``merge_manifest``는 기존 파일을 읽어 자기 섹션만 갱신한다.
"""
from __future__ import annotations

import hashlib
import json
import shutil
import zipfile
from pathlib import Path

APP_PAYLOAD = "nalmeok-app.zip"
MANIFEST = "manifest.json"
RELEASE_BASE = "https://github.com/jseook11/nalmeok/releases/download/v{version}/"
ZIP_DATE_TIME = (2026, 1, 1, 0, 0, 0)
CHUNK = 128 * 1024


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(CHUNK), b""):
            digest.update(chunk)
    return digest.hexdigest()


def write_app_payload(package_dir: Path, payload: Path) -> Path:
    """패키지의 .py 파일을 결정적 타임스탬프로 압축합니다."""
    files = sorted(package_dir.glob("*.py"))
    if not files:
        raise RuntimeError(f"no python files in {package_dir}")
    payload.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(
        payload, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=6
    ) as archive:
        for path in files:
            info = zipfile.ZipInfo(f"nalmeok/{path.name}", date_time=ZIP_DATE_TIME)
            info.compress_type = zipfile.ZIP_DEFLATED
            info.create_system = 0
            info.external_attr = 0
            with path.open("rb") as source, archive.open(info, "w") as target:
                shutil.copyfileobj(source, target, CHUNK)
    return payload


def merge_manifest(path: Path, platform: str, common: dict, section: dict) -> Path:
    """공통 필드와 해당 플랫폼 섹션만 갱신합니다."""
    data = {}
    if path.is_file():
        data = json.loads(path.read_text(encoding="utf-8"))
    for legacy in ("python", "ffmpeg"):
        data.pop(legacy, None)
    data.update(common)
    platforms = data.setdefault("platforms", {})
    platforms[platform] = section
    path.write_text(
        json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
    return path

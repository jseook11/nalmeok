#!/usr/bin/env python3
"""Build the Windows ZIP distribution for nalmeok.

The default (thin) ZIP contains only the application, scripts, and
bootstrap.ps1. On first run it downloads Python and google-genai; ffmpeg is
downloaded when MP4 conversion is first needed. This matches the macOS
distribution model and keeps updates small.

    python scripts/build_windows.py

The optional offline variant bundles CPython, google-genai, and ffmpeg:

    python scripts/build_windows.py --offline

Outputs:
    dist/nalmeok-<version>-win64.zip
    dist/nalmeok-<version>-win64-offline.zip   (with --offline)
    dist/SHA256SUMS.txt

The script itself uses only the Python standard library. The offline build
needs Windows; the thin build can run anywhere. All downloads are cached under
build/cache/ and verified against the pinned SHA-256 values below.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tarfile
import urllib.request
import zipfile
from pathlib import Path, PurePosixPath

ROOT = Path(__file__).resolve().parent.parent
SCRIPTS_DIR = ROOT / "scripts"
BUILD_DIR = ROOT / "build"
CACHE_DIR = BUILD_DIR / "cache"
STAGE_DIR = BUILD_DIR / "nalmeok-offline"
THIN_STAGE_DIR = BUILD_DIR / "nalmeok-thin"
DIST_DIR = ROOT / "dist"

# --- Pinned inputs -----------------------------------------------------------
# Verified on 2026-10-30 by downloading each asset and computing SHA-256.
PYTHON_RELEASE_TAG = "20261026"
PYTHON_VERSION = "3.12.16"
PYTHON_ARCHIVE = (
    "cpython-3.12.16%2B20261026-x86_64-pc-windows-msvc-install_only.tar.gz"
)
PYTHON_URL = (
    "https://github.com/astral-sh/python-build-standalone/releases/download/"
    f"{PYTHON_RELEASE_TAG}/{PYTHON_ARCHIVE}"
)
PYTHON_SHA256 = "590dfcbe3a34c07301e6332e13e2188b98655cd072b08afa45a7998dc4c22bc5"

PYTHON_BUILD_STANDALONE_LICENSE_URL = (
    "https://raw.githubusercontent.com/astral-sh/python-build-standalone/"
    f"{PYTHON_RELEASE_TAG}/LICENSE"
)
PYTHON_BUILD_STANDALONE_LICENSE_SHA256 = (
    "1f256ecad192880510e84ad60474eab7589218784b9a50bc7ceee34c2b91f1d5"
)

FFMPEG_RELEASE_TAG = "autobuild-2026-10-29-19-17"
FFMPEG_VERSION = "8.1.3"
FFMPEG_ASSET = "ffmpeg-n8.1.3-25-g611cec792a-win64-lgpl-8.1.zip"
FFMPEG_URL = (
    "https://github.com/BtbN/FFmpeg-Builds/releases/download/"
    f"{FFMPEG_RELEASE_TAG}/{FFMPEG_ASSET}"
)
FFMPEG_SHA256 = "74e7863feef0d40b72236caf53823b132bcd429657be742d0a9213c0581cb573"

# uv.lock 과 같은 버전으로 고정합니다. 앱의 허용 범위는 >=2.31.0,<3 입니다.
GOOGLE_GENAI_REQUIREMENT = "google-genai==2.31.0"

ZIP_DATE_TIME = (2026, 1, 1, 0, 0, 0)
CHUNK_SIZE = 1024 * 1024
ZIP_SIZE_WARNING = 150_000_000


class BuildError(RuntimeError):
    """A build step failed; the message is shown to the user."""


# --- Helpers -----------------------------------------------------------------
def configure_output() -> None:
    for stream in (sys.stdout, sys.stderr):
        try:
            stream.reconfigure(encoding="utf-8", errors="replace")
        except (AttributeError, OSError, ValueError):
            pass


def log(message: str) -> None:
    print(f"[build] {message}", flush=True)


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(CHUNK_SIZE), b""):
            digest.update(chunk)
    return digest.hexdigest()


def human_size(size: int) -> str:
    value = float(size)
    for unit in ("B", "KiB", "MiB", "GiB"):
        if value < 1024 or unit == "GiB":
            return f"{value:.1f} {unit}"
        value /= 1024
    return f"{size} B"


def download(url: str, destination: Path, expected_sha256: str) -> Path:
    """Download ``url`` into ``destination`` and verify its SHA-256."""
    destination.parent.mkdir(parents=True, exist_ok=True)

    if destination.is_file():
        actual = sha256_file(destination)
        if actual == expected_sha256:
            size = human_size(destination.stat().st_size)
            log(f"using cached {destination.name} ({size})")
            return destination
        log(f"cached {destination.name} has the wrong SHA-256; downloading it again")
        destination.unlink()

    temporary = destination.with_name(destination.name + ".part")
    if temporary.exists():
        temporary.unlink()

    log(f"downloading {url}")
    request = urllib.request.Request(
        url,
        headers={
            "User-Agent": (
                "nalmeok-build/1.0 (+https://github.com/jseook11/nalmeok)"
            )
        },
    )
    try:
        with urllib.request.urlopen(request, timeout=120) as response:
            total = int(response.headers.get("Content-Length") or 0)
            received = 0
            with temporary.open("wb") as handle:
                while True:
                    chunk = response.read(CHUNK_SIZE)
                    if not chunk:
                        break
                    handle.write(chunk)
                    received += len(chunk)
                    if total:
                        percent = received * 100 // total
                        print(
                            f"\r[build]   {percent:3d}%  "
                            f"{human_size(received)} / {human_size(total)}",
                            end="",
                            flush=True,
                        )
        print(flush=True)
    except Exception as exc:  # urllib raises several unrelated exception types
        if temporary.exists():
            temporary.unlink()
        raise BuildError(f"download failed: {url}\n  {exc}") from exc

    actual = sha256_file(temporary)
    if actual != expected_sha256:
        temporary.unlink()
        raise BuildError(
            f"SHA-256 mismatch for {destination.name}\n"
            f"  expected: {expected_sha256}\n"
            f"  actual:   {actual}\n"
            "Refusing to build with an unverified download."
        )
    os.replace(temporary, destination)
    log(f"SHA-256 OK  {destination.name}  {expected_sha256}")
    return destination


def remove_tree(path: Path) -> None:
    if not path.exists():
        return

    def force_remove(function, target, _exc_info):  # legacy onerror signature
        os.chmod(target, 0o700)
        function(target)

    shutil.rmtree(path, onerror=force_remove)


def read_app_version() -> str:
    source = (ROOT / "nalmeok" / "constants.py").read_text(encoding="utf-8")
    match = re.search(r'^__version__\s*=\s*"([^"]+)"', source, re.MULTILINE)
    if not match:
        raise BuildError("could not find __version__ in nalmeok/constants.py")
    version = match.group(1)
    if not re.fullmatch(r"[0-9A-Za-z][0-9A-Za-z._+-]*", version):
        raise BuildError(f"unexpected version string: {version!r}")
    return version


def subprocess_env() -> dict[str, str]:
    env = dict(os.environ)
    # Keep the bundled runtime isolated from the developer's venv/PYTHONPATH.
    for key in ("VIRTUAL_ENV", "PYTHONHOME", "PYTHONPATH", "PIP_USER", "PIP_TARGET"):
        env.pop(key, None)
    env.update(
        {
            "PYTHONUTF8": "1",
            "PYTHONIOENCODING": "utf-8",
            "PYTHONNOUSERSITE": "1",
            "PIP_DISABLE_PIP_VERSION_CHECK": "1",
            "PIP_NO_INPUT": "1",
            "UV_LINK_MODE": "copy",
        }
    )
    return env


def run_live(command: list[str], description: str) -> None:
    log(description)
    result = subprocess.run(
        [str(part) for part in command],
        env=subprocess_env(),
        check=False,
    )
    if result.returncode != 0:
        raise BuildError(f"{description} failed with exit code {result.returncode}")


def run_capture(command: list[str]) -> str:
    result = subprocess.run(
        [str(part) for part in command],
        env=subprocess_env(),
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        check=False,
    )
    if result.returncode != 0:
        detail = (result.stderr or result.stdout or "").strip()
        raise BuildError(
            f"command failed with exit code {result.returncode}: "
            f"{' '.join(str(p) for p in command)}\n{detail}"
        )
    return result.stdout


def copy_file(source: Path, destination: Path) -> None:
    if not source.is_file():
        raise BuildError(f"required file is missing: {source}")
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, destination)


def copy_package(source: Path, destination: Path) -> None:
    """Copy the nalmeok package (Python files only) into the staging area."""
    if not (source / "__init__.py").is_file():
        raise BuildError("nalmeok/__init__.py is missing")
    for path in sorted(source.glob("*.py")):
        copy_file(path, destination / path.name)


def copy_cmd_as_crlf(source: Path, destination: Path) -> None:
    """Copy a .cmd template, normalizing it to UTF-8 without BOM and CRLF."""
    if not source.is_file():
        raise BuildError(f"required file is missing: {source}")
    text = source.read_bytes().decode("utf-8-sig")
    text = text.replace("\r\n", "\n").replace("\r", "\n")
    text = text.replace("\n", "\r\n")
    if not text.endswith("\r\n"):
        text += "\r\n"
    destination.write_bytes(text.encode("utf-8"))


def write_text(path: Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(text.encode("utf-8"))


# --- Build steps -------------------------------------------------------------
def extract_python(archive: Path, runtime_dir: Path) -> None:
    """Extract the standalone Python archive, dropping its ``python/`` prefix."""
    log(f"extracting {archive.name} -> {runtime_dir}")
    runtime_dir.mkdir(parents=True, exist_ok=True)
    with tarfile.open(archive, "r:gz") as tar:
        for member in tar:
            parts = PurePosixPath(member.name).parts
            if not parts or parts[0] != "python":
                continue
            relative = parts[1:]
            if not relative:
                continue
            if ".." in relative or PurePosixPath(member.name).is_absolute():
                raise BuildError(f"unsafe path in Python archive: {member.name}")
            target = runtime_dir.joinpath(*relative)
            if member.isdir():
                target.mkdir(parents=True, exist_ok=True)
                continue
            if not member.isfile():
                raise BuildError(
                    f"unsupported entry in Python archive: {member.name} "
                    f"(type {member.type!r})"
                )
            target.parent.mkdir(parents=True, exist_ok=True)
            extracted = tar.extractfile(member)
            if extracted is None:
                raise BuildError(f"could not read {member.name} from Python archive")
            with extracted, target.open("wb") as handle:
                shutil.copyfileobj(extracted, handle, CHUNK_SIZE)

    python_exe = runtime_dir / "python.exe"
    if not python_exe.is_file():
        raise BuildError(f"Python runtime is incomplete: {python_exe} is missing")
    license_file = runtime_dir / "LICENSE.txt"
    if not license_file.is_file():
        raise BuildError("Python runtime LICENSE.txt is missing from the archive")


def install_packages(runtime_dir: Path) -> None:
    python_exe = runtime_dir / "python.exe"
    command = [
        str(python_exe),
        "-m",
        "pip",
        "install",
        "--disable-pip-version-check",
        "--no-input",
        GOOGLE_GENAI_REQUIREMENT,
    ]
    log(f"installing {GOOGLE_GENAI_REQUIREMENT} with the bundled runtime pip")
    run_live(command, "installing google-genai into the bundled runtime")

    version = run_capture(
        [
            str(python_exe),
            "-s",
            "-c",
            "import google.genai, importlib.metadata as m; "
            "print(m.version('google-genai'))",
        ]
    ).strip()
    if not version:
        raise BuildError("google-genai imported but reported no version")
    log(f"google-genai {version} installed")


def extract_ffmpeg(archive: Path, stage_dir: Path, licenses_dir: Path) -> None:
    log(f"extracting {archive.name}")
    prefix = FFMPEG_ASSET.removesuffix(".zip")
    wanted = {
        f"{prefix}/bin/ffmpeg.exe": stage_dir / "ffmpeg.exe",
        f"{prefix}/bin/ffprobe.exe": stage_dir / "ffprobe.exe",
    }
    license_member = f"{prefix}/LICENSE.txt"
    extracted_names: set[str] = set()

    with zipfile.ZipFile(archive) as bundle:
        names = set(bundle.namelist())
        for member_name, destination in wanted.items():
            if member_name not in names:
                raise BuildError(f"missing {member_name} in {archive.name}")
            destination.parent.mkdir(parents=True, exist_ok=True)
            with bundle.open(member_name) as source, destination.open("wb") as handle:
                shutil.copyfileobj(source, handle, CHUNK_SIZE)
            if destination.stat().st_size < 1024 * 1024:
                raise BuildError(f"extracted {destination.name} looks too small")
            extracted_names.add(member_name)

        if license_member not in names:
            raise BuildError(f"missing {license_member} in {archive.name}")
        with bundle.open(license_member) as source:
            license_text = source.read().decode("utf-8", errors="replace")
        write_text(licenses_dir / "ffmpeg-LGPL.txt", license_text)

    log("ffmpeg.exe and ffprobe.exe extracted")


def installed_packages(runtime_dir: Path) -> list[tuple[str, str]]:
    code = (
        "import importlib.metadata as m, json\n"
        "rows = {(d.metadata.get('Name') or '?', d.version) "
        "for d in m.distributions()}\n"
        "print(json.dumps(sorted(([n, v] for n, v in rows if n != '?'), "
        "key=lambda row: row[0].lower())))\n"
    )
    output = run_capture([str(runtime_dir / "python.exe"), "-s", "-c", code])
    try:
        rows = json.loads(output)
    except json.JSONDecodeError as exc:
        raise BuildError(f"could not read installed package list: {exc}") from exc
    return [(str(name), str(version)) for name, version in rows]


def write_third_party_notices(
    licenses_dir: Path,
    packages: list[tuple[str, str]],
) -> None:
    lines = [
        "# Third-party notices",
        "",
        "This ZIP bundles third-party components. The nalmeok application source",
        "itself is covered by the MIT License in the ZIP root (LICENSE).",
        "",
        "## CPython runtime",
        "",
        f"- CPython {PYTHON_VERSION} (x86_64-pc-windows-msvc)",
        "- Built and packaged by python-build-standalone:",
        "  https://github.com/astral-sh/python-build-standalone",
        f"- Release: {PYTHON_RELEASE_TAG}",
        f"- License text: licenses/Python-LICENSE.txt",
        "- python-build-standalone is distributed under MPL-2.0:",
        "  https://github.com/astral-sh/python-build-standalone/blob/main/LICENSE",
        "  License text: licenses/python-build-standalone-LICENSE.txt",
        "",
        "## FFmpeg",
        "",
        "- FFmpeg n8.1.3-25-g611cec792a, LGPL build for win64",
        "  (invoked as a separate process; no FFmpeg code is linked into nalmeok)",
        "- Build: BtbN FFmpeg-Builds",
        f"  https://github.com/BtbN/FFmpeg-Builds/releases/tag/{FFMPEG_RELEASE_TAG}",
        f"- Asset: {FFMPEG_ASSET}",
        f"- Asset SHA-256: {FFMPEG_SHA256}",
        f"- License text: licenses/ffmpeg-LGPL.txt",
        f"- Source notice: licenses/ffmpeg-SOURCE.txt",
        "",
        "## Python packages in runtime/Lib/site-packages",
        "",
        "Each package keeps its own license files in its `.dist-info` directory",
        "inside `runtime/Lib/site-packages`. Versions installed for this build:",
        "",
        "| Package | Version |",
        "| --- | --- |",
    ]
    for name, version in packages:
        lines.append(f"| {name} | {version} |")
    lines.append("")
    write_text(licenses_dir / "THIRD-PARTY.md", "\n".join(lines))


def write_source_notices(licenses_dir: Path) -> None:
    write_text(
        licenses_dir / "ffmpeg-SOURCE.txt",
        "\n".join(
            [
                "FFmpeg corresponding source and build information",
                "",
                f"FFmpeg source commit: 611cec792a (n8.1.3-25-g611cec792a)",
                "https://github.com/FFmpeg/FFmpeg/tree/611cec792a",
                "",
                f"BtbN FFmpeg-Builds release: {FFMPEG_RELEASE_TAG}",
                "https://github.com/BtbN/FFmpeg-Builds/releases/tag/"
                f"{FFMPEG_RELEASE_TAG}",
                "",
                f"Asset: {FFMPEG_ASSET}",
                f"Asset SHA-256: {FFMPEG_SHA256}",
                "",
                "The bundled binaries are an LGPL build. They are executed as",
                "separate processes by nalmeok; they are not linked into the app.",
                "",
            ]
        ),
    )
    write_text(
        licenses_dir / "python-build-standalone-SOURCE.txt",
        "\n".join(
            [
                "python-build-standalone source and license information",
                "",
                f"Release tag: {PYTHON_RELEASE_TAG}",
                "https://github.com/astral-sh/python-build-standalone/releases/tag/"
                f"{PYTHON_RELEASE_TAG}",
                "",
                "License: MPL-2.0",
                "https://github.com/astral-sh/python-build-standalone/"
                "blob/main/LICENSE",
                "License text: licenses/python-build-standalone-LICENSE.txt",
                "",
                f"Asset: {PYTHON_ARCHIVE.replace('%2B', '+')}",
                f"Asset SHA-256: {PYTHON_SHA256}",
                "",
            ]
        ),
    )


def verify_cmd_files(stage_dir: Path) -> None:
    """Check the .cmd launchers.

    Batch files must be pure ASCII. cmd.exe re-reads a batch file while it
    runs, and after ``chcp 65001`` it resumes mid-line on UTF-8 input, which
    makes it execute fragments of Korean text as commands. Korean messages
    belong in the UTF-8-BOM PowerShell scripts instead.
    """
    for path in (stage_dir / "installer.cmd",
                 stage_dir / "app" / "nalmeok.cmd",
                 stage_dir / "app" / "nalmeok-drop.cmd"):
        name = path.name
        if not path.is_file():
            raise BuildError(f"{name} is missing from the staging directory")
        data = path.read_bytes()
        try:
            data.decode("ascii")
        except UnicodeDecodeError as exc:
            raise BuildError(
                f"{name} must be pure ASCII; move non-ASCII text into a "
                f"UTF-8-BOM PowerShell script ({exc})"
            ) from exc
        without_crlf = data.replace(b"\r\n", b"")
        if b"\n" in without_crlf or b"\r" in without_crlf:
            raise BuildError(f"{name} must use CRLF line endings")
        if not data.startswith(b"@echo off\r\n"):
            raise BuildError(f"{name} must start with '@echo off'")


def create_zip(stage_dir: Path, zip_path: Path) -> None:
    files = sorted(
        (path for path in stage_dir.rglob("*") if path.is_file()),
        key=lambda path: path.relative_to(stage_dir).as_posix(),
    )
    if not files:
        raise BuildError("staging directory is empty")

    temporary = zip_path.with_name(zip_path.name + ".part")
    if temporary.exists():
        temporary.unlink()

    log(f"creating {zip_path.name} ({len(files)} files)")
    with zipfile.ZipFile(
        temporary,
        "w",
        compression=zipfile.ZIP_DEFLATED,
        compresslevel=6,
    ) as archive:
        for path in files:
            relative = path.relative_to(stage_dir).as_posix()
            info = zipfile.ZipInfo(relative, date_time=ZIP_DATE_TIME)
            info.compress_type = zipfile.ZIP_DEFLATED
            info.create_system = 0
            info.external_attr = 0
            with path.open("rb") as source, archive.open(info, "w") as destination:
                shutil.copyfileobj(source, destination, CHUNK_SIZE)
    os.replace(temporary, zip_path)


def bootstrap_env_text() -> str:
    return (
        f"PBS_VERSION={PYTHON_VERSION}\n"
        f"PBS_URL={PYTHON_URL}\n"
        f"PBS_SHA256={PYTHON_SHA256}\n"
        f"FFMPEG_VERSION={FFMPEG_VERSION}\n"
        f"FFMPEG_URL={FFMPEG_URL}\n"
        f"FFMPEG_SHA256={FFMPEG_SHA256}\n"
    )


def copy_ps1(source: Path, destination: Path) -> None:
    """Copy a PowerShell script byte for byte.

    Scripts with non-ASCII text must start with a UTF-8 BOM, otherwise Windows
    PowerShell 5.1 decodes them with the ANSI code page and mangles Korean.
    """
    if not source.is_file():
        raise BuildError(f"required file is missing: {source}")
    data = source.read_bytes()
    if any(byte > 0x7F for byte in data) and not data.startswith(b"\xef\xbb\xbf"):
        raise BuildError(f"{source.name} contains non-ASCII text without a UTF-8 BOM")
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_bytes(data)


def copy_common_files(stage: Path, thin: bool) -> None:
    """Stage the distribution.

    The ZIP root shows a single ``installer.cmd``; everything else lives under
    ``app/`` so non-developers cannot miss the one file they need. installer.cmd
    copies ``app\\*`` into ``%LOCALAPPDATA%\\Programs\\nalmeok``, so the installed
    layout stays flat.
    """
    app = stage / "app"
    app.mkdir(parents=True, exist_ok=True)
    copy_cmd_as_crlf(SCRIPTS_DIR / "installer.cmd", stage / "installer.cmd")
    copy_package(ROOT / "nalmeok", app / "nalmeok")
    copy_file(ROOT / "app.py", app / "app.py")
    copy_file(ROOT / "pyproject.toml", app / "pyproject.toml")
    copy_file(ROOT / "README.md", app / "README.md")
    copy_file(ROOT / "LICENSE", app / "LICENSE")
    copy_cmd_as_crlf(SCRIPTS_DIR / "nalmeok.cmd", app / "nalmeok.cmd")
    copy_cmd_as_crlf(SCRIPTS_DIR / "nalmeok-drop.cmd",
                     app / "nalmeok-drop.cmd")
    copy_ps1(SCRIPTS_DIR / "install.ps1", app / "install.ps1")
    copy_ps1(SCRIPTS_DIR / "messages.ps1", app / "messages.ps1")
    copy_ps1(SCRIPTS_DIR / "shortcuts.ps1", app / "shortcuts.ps1")
    copy_ps1(SCRIPTS_DIR / "bootstrap.ps1", app / "bootstrap.ps1")
    write_text(app / "bootstrap.env", bootstrap_env_text())

    icon = ROOT / "assets" / "nalmeok.ico"
    if not icon.is_file():
        icon = ROOT / "nalmeok.ico"
    if icon.is_file():
        copy_file(icon, app / "nalmeok.ico")
        log("copied nalmeok.ico")
    else:
        log(
            "WARNING: nalmeok.ico is missing; the ZIP is built without an icon "
            "and install.ps1 falls back to the Windows default icon."
        )

    if thin:
        licenses = app / "licenses"
        licenses.mkdir(parents=True, exist_ok=True)
        write_text(
            licenses / "THIRD-PARTY.md",
            "This package downloads its runtime dependencies at first use.\n"
            "\n"
            f"- Python {PYTHON_VERSION} (MPL-2.0): "
            "https://github.com/astral-sh/python-build-standalone\n"
            f"- google-genai {GOOGLE_GENAI_REQUIREMENT}: PyPI\n"
            f"- FFmpeg {FFMPEG_VERSION} (LGPL): "
            "https://github.com/BtbN/FFmpeg-Builds\n",
        )


def verify_thin_files(stage: Path) -> None:
    app = stage / "app"
    if not (stage / "installer.cmd").is_file():
        raise BuildError("installer.cmd is missing from the ZIP root")
    for name in ("bootstrap.ps1", "bootstrap.env", "shortcuts.ps1",
                 "install.ps1", "messages.ps1",
                 "nalmeok.cmd", "nalmeok-drop.cmd",
                 "nalmeok/__init__.py", "nalmeok/__main__.py"):
        if not (app / name).is_file():
            raise BuildError(
                f"app/{name} is missing from the thin staging directory")
    values = {}
    for line in (app / "bootstrap.env").read_text(encoding="utf-8").splitlines():
        if "=" in line:
            key, value = line.split("=", 1)
            values[key] = value
    for key in ("PBS_VERSION", "PBS_URL", "PBS_SHA256",
                "FFMPEG_URL", "FFMPEG_SHA256"):
        if not values.get(key):
            raise BuildError(f"bootstrap.env is missing {key}")


def write_sums(version: str) -> None:
    DIST_DIR.mkdir(parents=True, exist_ok=True)
    zips = sorted(DIST_DIR.glob(f"nalmeok-{version}-win64*.zip"))
    if not zips:
        raise BuildError("no Windows ZIP found for SHA256SUMS")
    write_text(
        DIST_DIR / "SHA256SUMS.txt",
        "".join(f"{sha256_file(path)}  {path.name}\n" for path in zips),
    )


def write_manifest(version: str) -> None:
    payload = DIST_DIR / "nalmeok-app.zip"
    stage = BUILD_DIR / "app-payload"
    remove_tree(stage)
    copy_package(ROOT / "nalmeok", stage / "nalmeok")
    create_zip(stage, payload)
    sdk_version = GOOGLE_GENAI_REQUIREMENT.split("==", 1)[-1]
    manifest = {
        "app_version": version,
        "nalmeok": {
            "version": version,
            "url": (
                "https://github.com/jseook11/nalmeok/releases/download/"
                f"v{version}/nalmeok-app.zip"
            ),
            "sha256": sha256_file(payload),
        },
        "sdk": {"name": "google-genai", "version": sdk_version},
        "python": {
            "version": PYTHON_VERSION,
            "url": PYTHON_URL,
            "sha256": PYTHON_SHA256,
        },
        "ffmpeg": {
            "version": FFMPEG_VERSION,
            "url": FFMPEG_URL,
            "sha256": FFMPEG_SHA256,
        },
    }
    write_text(
        DIST_DIR / "windows-manifest.json",
        json.dumps(manifest, ensure_ascii=False, indent=2) + "\n",
    )


def build_thin() -> None:
    version = read_app_version()
    log(f"nalmeok thin Windows build {version}")
    remove_tree(THIN_STAGE_DIR)
    THIN_STAGE_DIR.mkdir(parents=True)
    copy_common_files(THIN_STAGE_DIR, thin=True)
    verify_cmd_files(THIN_STAGE_DIR)
    verify_thin_files(THIN_STAGE_DIR)

    DIST_DIR.mkdir(parents=True, exist_ok=True)
    zip_path = DIST_DIR / f"nalmeok-{version}-win64.zip"
    create_zip(THIN_STAGE_DIR, zip_path)
    write_sums(version)
    write_manifest(version)
    zip_size = zip_path.stat().st_size
    log(f"ZIP: {zip_path.relative_to(ROOT)}  {human_size(zip_size)}")
    log(f"SHA-256: {sha256_file(zip_path)}")
    log(f"wrote {(DIST_DIR / 'SHA256SUMS.txt').relative_to(ROOT)}")


def build_offline() -> None:
    if os.name != "nt":
        raise BuildError(
            "build_windows.py builds a Windows ZIP and must run on Windows "
            "or GitHub Actions windows-latest."
        )

    version = read_app_version()
    log(f"nalmeok version {version}")

    python_archive = download(
        PYTHON_URL, CACHE_DIR / "python-runtime.tar.gz", PYTHON_SHA256
    )
    pbs_license = download(
        PYTHON_BUILD_STANDALONE_LICENSE_URL,
        CACHE_DIR / "python-build-standalone-LICENSE.txt",
        PYTHON_BUILD_STANDALONE_LICENSE_SHA256,
    )
    ffmpeg_archive = download(
        FFMPEG_URL, CACHE_DIR / FFMPEG_ASSET, FFMPEG_SHA256
    )

    remove_tree(STAGE_DIR)
    STAGE_DIR.mkdir(parents=True)
    app_dir = STAGE_DIR / "app"
    app_dir.mkdir()
    runtime_dir = app_dir / "runtime"
    licenses_dir = app_dir / "licenses"
    licenses_dir.mkdir(parents=True)

    extract_python(python_archive, runtime_dir)
    copy_file(runtime_dir / "LICENSE.txt", licenses_dir / "Python-LICENSE.txt")
    copy_file(
        pbs_license,
        licenses_dir / "python-build-standalone-LICENSE.txt",
    )
    install_packages(runtime_dir)
    packages = installed_packages(runtime_dir)

    extract_ffmpeg(ffmpeg_archive, app_dir, licenses_dir)
    write_source_notices(licenses_dir)

    copy_common_files(STAGE_DIR, thin=False)
    write_third_party_notices(licenses_dir, packages)
    verify_cmd_files(STAGE_DIR)

    DIST_DIR.mkdir(parents=True, exist_ok=True)
    zip_path = DIST_DIR / f"nalmeok-{version}-win64-offline.zip"
    create_zip(STAGE_DIR, zip_path)

    log("app/ file hashes:")
    for path in sorted(app_dir.iterdir(), key=lambda item: item.name.lower()):
        if path.is_file():
            print(
                f"  {sha256_file(path)}  {human_size(path.stat().st_size):>10}  "
                f"{path.name}",
                flush=True,
            )

    zip_hash = sha256_file(zip_path)
    write_sums(version)
    write_manifest(version)
    sums_path = DIST_DIR / "SHA256SUMS.txt"

    stage_size = sum(
        path.stat().st_size
        for path in STAGE_DIR.rglob("*")
        if path.is_file()
    )
    zip_size = zip_path.stat().st_size
    log(f"staging size: {human_size(stage_size)}")
    log(f"ZIP: {zip_path.relative_to(ROOT)}  {human_size(zip_size)}")
    log(f"SHA-256: {zip_hash}")
    log(f"wrote {sums_path.relative_to(ROOT)}")
    if zip_size > ZIP_SIZE_WARNING:
        log(
            "WARNING: the ZIP is larger than 150 MB. Revisit the ffmpeg "
            "on-demand option in docs/windows-support.md section 8.5."
        )


def main() -> int:
    configure_output()
    parser = argparse.ArgumentParser(description="Build the nalmeok Windows ZIP")
    parser.add_argument("--offline", action="store_true",
                        help="bundle Python and ffmpeg (~165MB) instead of "
                             "downloading them on first use")
    args = parser.parse_args()
    try:
        if args.offline:
            build_offline()
        else:
            build_thin()
    except BuildError as exc:
        print(f"\n[build] ERROR: {exc}", file=sys.stderr, flush=True)
        return 1
    except OSError as exc:
        print(f"\n[build] ERROR: {exc}", file=sys.stderr, flush=True)
        return 1
    except KeyboardInterrupt:
        print("\n[build] interrupted", file=sys.stderr, flush=True)
        return 130
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

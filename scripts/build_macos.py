#!/usr/bin/env python3
"""Builds the macOS .app bundle and DMG.

Usage:
    python scripts/build_macos.py

Environment:
    NALMEOK_SIGN_IDENTITY   Developer ID Application identity (optional)
    NALMEOK_NOTARY_PROFILE  notarytool keychain profile (optional)
    NALMEOK_NOTARY_APPLE_ID / NALMEOK_NOTARY_TEAM_ID / NALMEOK_NOTARY_PASSWORD
                            notarytool credentials (optional alternative)

Without signing variables the script builds an unsigned DMG for local/CI smoke
tests. Release builds must set NALMEOK_SIGN_IDENTITY and notarization credentials.
"""

from __future__ import annotations

import argparse
import hashlib
import os
import platform
import plistlib
import re
import shutil
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
MACOS_DIR = ROOT / "macos"
BUILD_DIR = ROOT / "build" / "macos"
DIST_DIR = ROOT / "dist"
APP_NAME = "nalmeok"
BUNDLE_ID = "com.jseook11.nalmeok"
MIN_MACOS = "12.0"
ARCH = "arm64"

PBS_VERSION = "3.12.16"
PBS_TAG = "20261026"
PBS_ASSET = f"cpython-{PBS_VERSION}+{PBS_TAG}-aarch64-apple-darwin-install_only.tar.gz"
PBS_URL = (
    "https://github.com/astral-sh/python-build-standalone/releases/download/"
    f"{PBS_TAG}/{PBS_ASSET.replace('+', '%2B')}"
)
PBS_SHA256 = "fbf4afef53c2a6b72bb741bc06d5671a9dfaf87bd84e5aa73ac9b3d0d589f14e"


class BuildError(RuntimeError):
    pass


def log(message: str) -> None:
    print(message, flush=True)


def run(command: list[str], log_command: bool = True, **kwargs) -> None:
    if log_command:
        log("+ " + " ".join(command))
    subprocess.run(command, check=True, **kwargs)


def app_version() -> str:
    text = (ROOT / "nalmeok.py").read_text(encoding="utf-8")
    match = re.search(r'^__version__ = "([^"]+)"', text, re.MULTILINE)
    if not match:
        raise BuildError("nalmeok.py에서 __version__을 찾지 못했습니다")
    return match.group(1)


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def build_icon(destination: Path) -> None:
    source = ROOT / "assets" / "nalmeok.ico"
    if not source.is_file():
        raise BuildError("assets/nalmeok.ico가 없습니다")
    iconset = BUILD_DIR / "nalmeok.iconset"
    shutil.rmtree(iconset, ignore_errors=True)
    iconset.mkdir(parents=True)
    base = BUILD_DIR / "icon-256.png"
    run(["sips", "-s", "format", "png", str(source), "--out", str(base)],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

    sizes = {
        "icon_16x16.png": 16,
        "icon_16x16@2x.png": 32,
        "icon_32x32.png": 32,
        "icon_32x32@2x.png": 64,
        "icon_128x128.png": 128,
        "icon_128x128@2x.png": 256,
        "icon_256x256.png": 256,
        "icon_256x256@2x.png": 512,
        "icon_512x512.png": 512,
        "icon_512x512@2x.png": 1024,
    }
    for name, pixels in sizes.items():
        run(["sips", "-z", str(pixels), str(pixels), str(base),
             "--out", str(iconset / name)],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    run(["iconutil", "-c", "icns", str(iconset), "-o", str(destination)])


def bootstrap_env() -> str:
    return (
        f"PBS_VERSION={PBS_VERSION}\n"
        f"PBS_URL={PBS_URL}\n"
        f"PBS_SHA256={PBS_SHA256}\n"
    )


def build_app(version: str) -> Path:
    if platform.machine() != "arm64":
        raise BuildError("이 스크립트는 Apple Silicon(arm64)에서 실행해야 합니다")
    if not shutil.which("swiftc"):
        raise BuildError("swiftc가 없습니다. Xcode Command Line Tools를 설치하세요")

    stage = BUILD_DIR / "app"
    shutil.rmtree(stage, ignore_errors=True)
    app = stage / f"{APP_NAME}.app"
    contents = app / "Contents"
    macos_bin = contents / "MacOS"
    resources = contents / "Resources"
    macos_bin.mkdir(parents=True)
    resources.mkdir(parents=True)

    run(["swiftc", "-O", "-target", f"arm64-apple-macos{MIN_MACOS}",
         "-o", str(macos_bin / APP_NAME), str(MACOS_DIR / "launcher.swift")])

    info = (MACOS_DIR / "Info.plist").read_text(encoding="utf-8")
    (contents / "Info.plist").write_text(
        info.replace("__VERSION__", version), encoding="utf-8")

    shutil.copy2(ROOT / "nalmeok.py", resources / "nalmeok.py")
    for name in ("run.command", "bootstrap.command"):
        target = resources / name
        shutil.copy2(MACOS_DIR / name, target)
        target.chmod(0o755)
    (resources / "bootstrap.env").write_text(bootstrap_env(), encoding="utf-8")

    if (ROOT / "LICENSE").is_file():
        shutil.copy2(ROOT / "LICENSE", resources / "LICENSE")

    build_icon(resources / "nalmeok.icns")

    plist = plistlib.loads((contents / "Info.plist").read_bytes())
    if plist.get("CFBundleIdentifier") != BUNDLE_ID:
        raise BuildError("Info.plist의 번들 ID가 예상과 다릅니다")
    if plist.get("CFBundleShortVersionString") != version:
        raise BuildError("Info.plist 버전 치환이 실패했습니다")

    binary = macos_bin / APP_NAME
    if "arm64" not in subprocess.run(["file", str(binary)],
                                     capture_output=True, text=True).stdout:
        raise BuildError("런처가 arm64 Mach-O가 아닙니다")
    log(f"app: {app} ({sum(p.stat().st_size for p in app.rglob('*') if p.is_file())} bytes)")
    return app


def sign(path: Path, identity: str) -> None:
    run(["codesign", "--force", "--options", "runtime", "--timestamp",
         "--sign", identity, str(path)])


def verify_signature(path: Path) -> None:
    run(["codesign", "--verify", "--deep", "--strict", "--verbose=2", str(path)])


def verify_gatekeeper(path: Path) -> None:
    result = subprocess.run(["spctl", "-a", "-vv", "--type", "execute", str(path)],
                            capture_output=True, text=True)
    if result.returncode != 0:
        raise BuildError("spctl 검증 실패:\n" + result.stderr)


def verify_dmg_gatekeeper(dmg: Path) -> None:
    mount = BUILD_DIR / "mnt"
    shutil.rmtree(mount, ignore_errors=True)
    mount.mkdir(parents=True)
    run(["hdiutil", "attach", str(dmg), "-nobrowse", "-readonly",
         "-mountpoint", str(mount)])
    try:
        verify_gatekeeper(mount / f"{APP_NAME}.app")
    finally:
        run(["hdiutil", "detach", str(mount), "-force"])


def notarize(dmg: Path) -> bool:
    profile = os.getenv("NALMEOK_NOTARY_PROFILE")
    apple_id = os.getenv("NALMEOK_NOTARY_APPLE_ID")
    team_id = os.getenv("NALMEOK_NOTARY_TEAM_ID")
    password = os.getenv("NALMEOK_NOTARY_PASSWORD")
    base = ["xcrun", "notarytool", "submit", str(dmg), "--wait"]
    if profile:
        base += ["--keychain-profile", profile]
    elif apple_id and team_id and password:
        base += ["--apple-id", apple_id, "--team-id", team_id,
                 "--password", password]
    else:
        log("notarization credentials not set; skipping notarization")
        return False
    log("+ xcrun notarytool submit <dmg> --wait (credentials hidden)")
    subprocess.run(base, check=True)
    run(["xcrun", "stapler", "staple", str(dmg)])
    run(["xcrun", "stapler", "validate", str(dmg)])
    return True


def create_dmg(app: Path, version: str) -> Path:
    root = BUILD_DIR / "dmg"
    shutil.rmtree(root, ignore_errors=True)
    root.mkdir(parents=True)
    shutil.copytree(app, root / app.name, symlinks=True)
    os.symlink("/Applications", root / "Applications")
    DIST_DIR.mkdir(parents=True, exist_ok=True)
    dmg = DIST_DIR / f"nalmeok-{version}-{ARCH}.dmg"
    if dmg.exists():
        dmg.unlink()
    run(["hdiutil", "create", "-volname", APP_NAME, "-srcfolder", str(root),
         "-ov", "-format", "UDZO", str(dmg)])
    return dmg


def main() -> int:
    parser = argparse.ArgumentParser(description="Build the nalmeok macOS app")
    parser.add_argument("--unsigned", action="store_true",
                        help="서명/공증을 건너뜁니다")
    args = parser.parse_args()

    version = app_version()
    log(f"nalmeok macOS {version} ({ARCH})")
    app = build_app(version)

    identity = os.getenv("NALMEOK_SIGN_IDENTITY")
    if identity and not args.unsigned:
        sign(app, identity)
        verify_signature(app)
    else:
        log("unsigned build (NALMEOK_SIGN_IDENTITY not set)")

    dmg = create_dmg(app, version)
    if identity and not args.unsigned:
        sign(dmg, identity)
        if notarize(dmg):
            verify_dmg_gatekeeper(dmg)

    digest = sha256(dmg)
    sums = DIST_DIR / "SHA256SUMS.txt"
    sums.write_text(f"{digest}  {dmg.name}\n", encoding="utf-8")
    log(f"dmg: {dmg} ({dmg.stat().st_size} bytes)")
    log(f"sha256: {digest}")
    log(f"sums: {sums}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (BuildError, subprocess.CalledProcessError) as exc:
        print(f"빌드 실패: {exc}", file=sys.stderr)
        raise SystemExit(1)

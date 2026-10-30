"""nalmeok.keys."""

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
from . import runtime  # noqa: F401



def env_key():
    key = os.getenv("GEMINI_API_KEY")
    return key.strip() if key and key.strip() else None


def _clean_key_entry(entry, index):
    """설정에 저장된 키 하나를 검증하고 표준 모양으로 바꿉니다."""
    if not isinstance(entry, dict):
        return None
    api_key = str(entry.get("api_key") or "").strip()
    if not api_key:
        return None
    tier = str(entry.get("tier") or "paid").strip().lower()
    if tier not in ("free", "paid"):
        tier = "paid"
    label = str(entry.get("label") or f"key{index}").strip() or f"key{index}"
    try:
        limit = int(entry.get("limit_minutes") or app.DEFAULT_FREE_MINUTES)
    except (TypeError, ValueError):
        limit = app.DEFAULT_FREE_MINUTES
    limit = max(1, min(limit, 24 * 60))
    ident = str(entry.get("id") or f"key_{index:03d}").strip() or f"key_{index:03d}"
    return {"id": ident, "label": label, "tier": tier,
            "api_key": api_key, "limit_minutes": limit, "source": "config"}


def env_key_entry():
    """환경변수 키도 하나의 키로 취급합니다. 등급은 GEMINI_API_KEY_TIER 로 정합니다."""
    api_key = app.env_key()
    if not api_key:
        return None
    tier = os.getenv("GEMINI_API_KEY_TIER", "paid").strip().lower()
    if tier not in ("free", "paid"):
        tier = "paid"
    return {"id": "env", "label": "환경변수", "tier": tier,
            "api_key": api_key, "limit_minutes": app.DEFAULT_FREE_MINUTES,
            "source": "env"}


def key_entries(include_env=True):
    """등록된 모든 키. 예전 단일 api_key 설정도 유료 키로 읽습니다."""
    data = app.load_config()
    entries = []
    raw = data.get("keys")
    if isinstance(raw, list):
        for index, item in enumerate(raw, 1):
            entry = app._clean_key_entry(item, index)
            if entry:
                entries.append(entry)
    elif isinstance(data.get("api_key"), str) and data["api_key"].strip():
        entries.append({"id": "legacy", "label": "기본", "tier": "paid",
                        "api_key": data["api_key"].strip(),
                        "limit_minutes": app.DEFAULT_FREE_MINUTES,
                        "source": "config"})
    if include_env:
        env = app.env_key_entry()
        if env:
            entries = [entry for entry in entries
                       if entry["api_key"] != env["api_key"]]
            entries.insert(0, env)
    seen = set()
    unique = []
    for entry in entries:
        if entry["api_key"] in seen:
            continue
        seen.add(entry["api_key"])
        unique.append(entry)
    return unique


def saved_key_entries():
    return [entry for entry in app.key_entries(include_env=False)
            if entry.get("source") == "config"]


def key_entries_for_tier(tier):
    return [entry for entry in app.key_entries() if entry["tier"] == tier]


def key_tier_of(api_key):
    for entry in app.key_entries():
        if entry["api_key"] == api_key:
            return entry["tier"]
    return None


def paid_key_for_batch(key=None, allow_unknown=False):
    """Batch에 쓸 유료 키. 알려진 무료 키는 절대 돌려주지 않습니다."""
    if key:
        tier = app.key_tier_of(key)
        if tier == "paid":
            return key
        if tier is None and allow_unknown:
            return key  # CLI --key 처럼 등록되지 않은 명시적 키는 허용
    return app.key_for_tier("paid")


def key_for_tier(tier):
    entries = app.key_entries_for_tier(tier)
    return entries[0]["api_key"] if entries else None


def key_entry_by_id(identifier):
    for entry in app.key_entries():
        if entry["id"] == identifier:
            return entry
    return None


def key_counts(entries=None):
    entries = app.key_entries() if entries is None else entries
    free = sum(1 for entry in entries if entry["tier"] == "free")
    return free, len(entries) - free


def mask_key(api_key):
    api_key = str(api_key or "")
    if len(api_key) <= 8:
        return "••••"
    return "••••" + api_key[-4:]


def _key_payload(entry):
    return {name: entry[name] for name in
            ("id", "label", "tier", "api_key", "limit_minutes")}


def _write_key_entries(entries):
    data = app.load_config()
    data["keys"] = [app._key_payload(entry) for entry in entries]
    data.pop("api_key", None)
    app.save_config(data)


def add_key_entry(label, tier, api_key, limit_minutes=None):
    """키를 추가하거나 같은 키의 이름/등급을 갱신합니다."""
    api_key = str(api_key or "").strip()
    if not api_key:
        raise ValueError("API 키가 비어 있습니다.")
    tier = tier if tier in ("free", "paid") else "paid"
    try:
        limit = int(limit_minutes or app.DEFAULT_FREE_MINUTES)
    except (TypeError, ValueError):
        limit = app.DEFAULT_FREE_MINUTES
    limit = max(1, min(limit, 24 * 60))
    label = str(label or "").strip() or ("free" if tier == "free" else "paid")
    with app.config_lock():
        entries = app.saved_key_entries()
        existing = next((entry for entry in entries
                         if entry["api_key"] == api_key), None)
        if existing:
            existing.update(label=label, tier=tier, limit_minutes=limit)
            target = existing
        else:
            base = label
            suffix = 2
            labels = {entry["label"] for entry in entries}
            while label in labels:
                label = f"{base}{suffix}"
                suffix += 1
            target = {"id": "key_" + uuid.uuid4().hex[:8], "label": label,
                      "tier": tier, "api_key": api_key,
                      "limit_minutes": limit, "source": "config"}
            entries.append(target)
        app._write_key_entries(entries)
    return target


def update_key_entry(identifier, **fields):
    with app.config_lock():
        entries = app.saved_key_entries()
        target = next((entry for entry in entries
                       if entry["id"] == identifier), None)
        if target is None:
            return None
        if "api_key" in fields:
            api_key = str(fields["api_key"] or "").strip()
            if api_key:
                target["api_key"] = api_key
        if "label" in fields and str(fields["label"]).strip():
            target["label"] = str(fields["label"]).strip()
        if fields.get("tier") in ("free", "paid"):
            target["tier"] = fields["tier"]
        if "limit_minutes" in fields:
            try:
                limit = int(fields["limit_minutes"])
            except (TypeError, ValueError):
                limit = app.DEFAULT_FREE_MINUTES
            target["limit_minutes"] = max(1, min(limit, 24 * 60))
        app._write_key_entries(entries)
    return target


def delete_key_entry(identifier):
    with app.config_lock():
        entries = app.saved_key_entries()
        remaining = [entry for entry in entries if entry["id"] != identifier]
        if len(remaining) == len(entries):
            return False
        app._write_key_entries(remaining)
    return True


def saved_key():
    entries = app.saved_key_entries()
    for entry in entries:
        if entry["tier"] == "paid":
            return entry["api_key"]
    return entries[0]["api_key"] if entries else None


def remember_key(key):
    entries = app.saved_key_entries()
    if not entries:
        app.add_key_entry("기본", "paid", key)
        return
    target = next((entry for entry in entries if entry["tier"] == "paid"),
                  entries[0])
    app.update_key_entry(target["id"], api_key=key)


def forget_key():
    with app.config_lock():
        app._write_key_entries([])


def recent_folders():
    """설정에 남겨 둔 최근 작업 폴더. 아직 있는 폴더만 돌려줍니다."""
    folders = []
    for item in app.load_config().get("recent") or []:
        try:
            path = Path(str(item)).expanduser().resolve()
        except OSError:
            continue
        if path.is_dir() and path not in folders:
            folders.append(path)
    return folders[:app.RECENT_LIMIT]


def remember_folder(folder):
    folder = Path(folder).expanduser().resolve()
    with app.config_lock():
        data = app.load_config()
        data["recent"] = [str(folder)] + [str(p) for p in app.recent_folders()
                                          if p != folder]
        app.save_config(data)


def clear_recent():
    with app.config_lock():
        data = app.load_config()
        if data.pop("recent", None) is not None:
            app.save_config(data)


def key_source():
    """API 키가 어디서 왔는지. 화면 표시용입니다."""
    if app.env_key():
        return "환경변수"
    if app.saved_key():
        return "저장됨"
    return None


def key_summary(entries=None):
    entries = app.key_entries() if entries is None else entries
    free, paid = app.key_counts(entries)
    if not entries:
        return "없음"
    parts = []
    if free:
        parts.append(f"무료 {free}")
    if paid:
        parts.append(f"유료 {paid}")
    return " · ".join(parts)


def current_key(key=None):
    return key or app.env_key() or app.saved_key()


def key_tier_label(tier):
    return "무료" if tier == "free" else "유료"


def key_default_label(tier):
    base = "free" if tier == "free" else "paid"
    labels = {entry["label"] for entry in app.saved_key_entries()}
    index = 1
    while f"{base}{index}" in labels:
        index += 1
    return f"{base}{index}"


def key_row_desc(entry):
    parts = [app.key_tier_label(entry["tier"])]
    if entry["tier"] == "free":
        parts.append(f"{entry['limit_minutes']}분")
    parts.append(app.mask_key(entry["api_key"]))
    if entry.get("source") == "env":
        parts.append("환경변수")
    return " · ".join(parts)

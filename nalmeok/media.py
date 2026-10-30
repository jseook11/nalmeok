"""nalmeok.media."""

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
from . import runtime  # noqa: F401
from . import term  # noqa: F401
from . import update  # noqa: F401



def ensure_ffmpeg():
    """패키징 설치에서 ffmpeg/ffprobe를 한 번 받아 옵니다."""
    if app.find_tool("ffmpeg") and app.find_tool("ffprobe"):
        return True
    if not app.packaged_mode():
        return False
    try:
        manifest = app.fetch_manifest()
        info = manifest.get("ffmpeg") or {}
        url = str(info.get("url") or "").strip()
        digest = str(info.get("sha256") or "").strip().lower()
    except (OSError, ValueError, RuntimeError):
        return False
    if not url or not digest:
        return False

    print("\nMP4 변환에는 ffmpeg가 필요합니다. (약 160MB)")
    if os.getenv("NALMEOK_FFMPEG_YES") != "1":
        if not sys.stdin.isatty():
            return False
        if input("지금 받을까요? [Y/n] ").strip().lower() not in ("", "y", "yes"):
            return False

    home = Path(os.getenv("NALMEOK_HOME") or Path(__file__).resolve().parent)
    bin_dir = home / "bin"
    bin_dir.mkdir(parents=True, exist_ok=True)
    archive = bin_dir / "ffmpeg-download.zip"
    suffix = ".exe" if app.IS_WINDOWS else ""
    try:
        app.download_file(url, archive)
        actual = app.file_sha256(archive)
        if actual != digest:
            raise RuntimeError("ffmpeg 체크섬이 맞지 않습니다.")
        with zipfile.ZipFile(archive) as bundle:
            names = bundle.namelist()
            for base in ("ffmpeg", "ffprobe"):
                exe = base + suffix
                matches = [name for name in names
                           if name.lower() == exe.lower()
                           or name.lower().endswith("/" + exe.lower())]
                if not matches:
                    raise RuntimeError(f"{exe}를 찾지 못했습니다.")
                target = bin_dir / exe
                with bundle.open(matches[0]) as source, \
                        target.open("wb") as output:
                    shutil.copyfileobj(source, output)
    finally:
        if archive.exists():
            archive.unlink()
    if app.find_tool("ffmpeg") and app.find_tool("ffprobe"):
        return True
    app.fail("ffmpeg 설치를 확인하지 못했습니다.")


def folder_model(folder):
    """폴더에 저장된 모델. 아직 작업이 없으면 기본 모델입니다."""
    data = (app.read_json_object(app.state_path(folder))
            or app.read_json_object(app.live_state_path(folder)))
    if data and data.get("model") in app.MODELS:
        return data["model"]
    return app.DEFAULT_MODEL


def duration_text(seconds):
    minutes = round(seconds / 60)
    if minutes < 60:
        return f"{minutes}분"
    return f"{minutes // 60}시간 {minutes % 60}분"


def money(usd):
    return f"${usd:.4f} (약 {usd * app.USD_TO_KRW:,.0f}원)"


def price(prompt_audio, prompt_text, output, model, batch=True):
    text_in, audio_in, output_rate = app.MODELS[model]
    factor = app.BATCH_MULTIPLIER if batch else 1.0
    return (prompt_audio * audio_in + prompt_text * text_in
            + output * output_rate) / 1_000_000 * factor


def price_lines(prompt_audio, prompt_text, low_output, high_output, model):
    if model not in app.MODELS:
        return [f"※ {model} 가격 정보가 없어 비용은 계산하지 않았습니다."]
    middle = (low_output + high_output) / 2
    batch = app.price(prompt_audio, prompt_text, middle, model, True)
    standard = app.price(prompt_audio, prompt_text, middle, model, False)
    return [f"{app.pad('Batch 비용', app.LABEL_WIDTH)}  {app.money(batch)}",
            f"{app.pad('일반 비용', app.LABEL_WIDTH)}  {app.money(standard)}"]


def find_tool(name):
    """ffmpeg/ffprobe 실행 파일. NALMEOK_FFMPEG → 앱 폴더(번들) → PATH 순."""
    exe = name + ".exe" if os.name == "nt" else name
    override = os.getenv("NALMEOK_FFMPEG")
    if override:
        path = Path(override).expanduser()
        if path.is_dir():
            candidate = path / exe
            if candidate.exists():
                return str(candidate)
        elif path.exists():
            if path.name.lower() == exe.lower():
                return str(path)
            sibling = path.with_name(exe)
            if sibling.exists():
                return str(sibling)
    app = Path(__file__).resolve().parent
    candidate = app / exe
    if candidate.exists():
        return str(candidate)
    return shutil.which(name)


def probe_duration(path):
    ffprobe = app.find_tool("ffprobe")
    if not ffprobe:
        return None
    result = subprocess.run(
        [ffprobe, "-v", "error", "-show_entries", "format=duration",
         "-of", "csv=p=0", str(path)],
        check=False, capture_output=True, text=True,
        encoding="utf-8", errors="replace")
    try:
        return float(result.stdout.strip())
    except ValueError:
        return None


def estimate_files(folder):
    """변환 전 MP4와 변환된 M4A가 같이 있어도 한 번만 셉니다."""
    audio = [p for p in folder.iterdir()
             if p.is_file() and p.suffix.lower() == ".m4a"]
    converted = {p.stem for p in audio}
    video = [p for p in folder.iterdir()
             if p.is_file() and p.suffix.lower() == ".mp4"
             and p.stem not in converted]
    return sorted(audio + video, key=lambda p: app.natural_key(p.name))


def estimate_values(folder, model):
    """예상 비용 계산에 쓰는 숫자들을 한 번에 모읍니다."""
    files = app.estimate_files(folder)
    if not files:
        app.fail(f"오디오 파일이 없습니다: {folder}")
    durations = [app.probe_duration(p) for p in files]
    unknown = [p.name for p, seconds in zip(files, durations) if seconds is None]
    if unknown:
        app.fail("ffmpeg(ffprobe)로 길이를 읽지 못했습니다: " + ", ".join(unknown))
    seconds = sum(durations)
    prompt_text = (len(app.PROMPT) // 4) * len(files)
    prompt_audio = int(seconds * app.AUDIO_TOKENS_PER_SECOND)
    low_output = int(seconds * app.OUTPUT_TOKENS_PER_SECOND[0])
    high_output = int(seconds * app.OUTPUT_TOKENS_PER_SECOND[1])
    middle = (low_output + high_output) / 2
    return {
        "files": len(files),
        "seconds": seconds,
        "prompt_audio": prompt_audio,
        "prompt_text": prompt_text,
        "low_output": low_output,
        "high_output": high_output,
        "cost": (app.price(prompt_audio, prompt_text, middle, model, True)
                 if model in app.MODELS else None),
    }


def estimate_lines(folder, model):
    values = app.estimate_values(folder, model)
    lines = [
        (f"{app.pad('파일', app.LABEL_WIDTH)}  {values['files']}개 · "
         f"총 {app.duration_text(values['seconds'])}"),
        (f"{app.pad('입력 토큰', app.LABEL_WIDTH)}  "
         f"{values['prompt_audio'] + values['prompt_text']:,} "
         f"(오디오 {values['prompt_audio']:,} + 프롬프트 {values['prompt_text']:,})"),
        (f"{app.pad('출력 토큰', app.LABEL_WIDTH)}  "
         f"{values['low_output']:,} ~ {values['high_output']:,}"),
    ]
    lines += app.price_lines(values["prompt_audio"], values["prompt_text"],
                         values["low_output"], values["high_output"], model)
    return lines


def response_rows(path):
    """결과 파일에서 응답 행을 꺼냅니다. jsonl과 복구용 json 둘 다 받습니다.

    사용량 화면은 참고용이므로 손상된 파일 하나가 전체 화면을 죽이지 않게 건너뜁니다.
    """
    try:
        text = path.read_text(encoding="utf-8")
    except OSError:
        return []
    if path.suffix == ".json":
        try:
            data = json.loads(text)
        except ValueError:
            return []
        results = data.get("results") if isinstance(data, dict) else {}
        return [row for row in (results or {}).values() if isinstance(row, dict)]
    rows = []
    for line in text.splitlines():
        if not line.strip():
            continue
        try:
            row = json.loads(line)
        except ValueError:
            continue
        if isinstance(row, dict):
            rows.append(row)
    return rows


def usage_totals(rows):
    """행들에서 (오디오 입력, 텍스트 입력, 출력) 토큰을 모읍니다."""
    audio = text_in = output = 0
    for row in rows:
        usage = (row.get("response") or {}).get("usageMetadata") or {}
        for detail in usage.get("promptTokensDetails") or []:
            if detail.get("modality") == "AUDIO":
                audio += detail.get("tokenCount") or 0
            elif detail.get("modality") == "TEXT":
                text_in += detail.get("tokenCount") or 0
        output += usage.get("candidatesTokenCount") or 0
    return audio, text_in, output


def folder_usage(folder):
    data = (app.read_json_object(app.state_path(folder))
            or app.read_json_object(app.live_state_path(folder)))
    model = data.get("model") if data else None
    names = [app.RESULT_FILE, app.RECOVERY_FILE]
    if app.live_state_path(folder).exists():
        names.append(app.LIVE_RESULT_FILE)
    names += [path.name for path in
              folder.glob(f"{app.MIXED_RESULT_PREFIX}.*.jsonl")]
    rows = []
    for name in names:
        path = folder / name
        if path.exists():
            rows += app.response_rows(path)
    return model, app.usage_totals(rows)


def job_folders(root, max_depth=None, max_dirs=None):
    """작업 폴더만 찾습니다. 숨김·무거운 폴더는 건너뜁니다.

    max_depth/max_dirs 를 주면 그 안에서만 찾습니다. 대시보드는 Windows에서
    홈 전체를 훑지 않도록 짧은 제한을 넘깁니다.
    """
    root = Path(root)
    if max_dirs is None:
        max_dirs = app.JOB_SCAN_MAX_DIRS * 10
    found = []
    visited = 0
    for current, dirnames, filenames in os.walk(root):
        visited += 1
        if visited > max_dirs:
            break
        try:
            depth = len(Path(current).relative_to(root).parts)
        except ValueError:
            depth = 0
        keep = []
        for name in dirnames:
            if name.startswith(".") or name.casefold() in app.SKIP_SCAN_NAMES:
                continue
            if max_depth is not None and depth >= max_depth:
                continue
            keep.append(name)
        dirnames[:] = keep
        if app.STATE_FILE in filenames or app.LIVE_STATE_FILE in filenames:
            found.append(Path(current))
    return sorted(found)


def usage_entry(folder):
    state = app.read_json_object(app.live_state_path(folder))
    if state and state.get("mode") == "mixed":
        model, free, paid, cost = app.mixed_usage(folder, state)
        audio = free[0] + paid[0]
        text_in = free[1] + paid[1]
        output = free[2] + paid[2]
        if not audio and not output:
            return None
        return {
            "folder": folder,
            "model": model,
            "audio": audio,
            "seconds": audio / app.AUDIO_TOKENS_PER_SECOND,
            "live": False,
            "mixed": True,
            "cost": cost,
        }
    model, (audio, text_in, output) = app.folder_usage(folder)
    if not audio and not output:
        return None
    live = app.live_state_path(folder).exists()
    return {
        "folder": folder,
        "model": model,
        "audio": audio,
        "seconds": audio / app.AUDIO_TOKENS_PER_SECOND,
        "live": live,
        "cost": (app.price(audio, text_in, output, model, not live)
                 if model in app.MODELS else None),
    }


def collect_usage(root):
    return [job for job in (app.usage_entry(folder) for folder in app.job_folders(root))
            if job]


def stats_lines_from(jobs, root=None):
    if not jobs:
        return ["아직 집계할 작업이 없습니다."]
    known = [job for job in jobs if job["cost"] is not None]
    seconds = sum(job["seconds"] for job in jobs)
    cost = sum(job["cost"] for job in known)
    rows = []
    for job in jobs:
        if root is not None:
            try:
                name = str(job["folder"].relative_to(root)) or "."
            except ValueError:
                name = job["folder"].name
        else:
            name = app.short_path(job["folder"])
        amount = app.money(job["cost"]) if job["cost"] is not None else "가격 정보 없음"
        rows.append((app.duration_text(job["seconds"]), amount, name))
    time_width = max(app.display_width(row[0]) for row in rows)
    cost_width = max(app.display_width(row[1]) for row in rows)
    lines = [f"작업 {len(jobs)}개 · 오디오 {app.duration_text(seconds)} · 총 {app.money(cost)}", ""]
    for time_text, amount, name in rows:
        lines.append(f"{app.align_right(time_text, time_width)}   "
                     f"{app.align_right(amount, cost_width)}   {name}")
    if seconds:
        lines += ["", f"평균 {app.money(cost / seconds * 3600)} / 시간"]
    if len(known) != len(jobs):
        lines.append(f"※ 가격표에 없는 모델 {len(jobs) - len(known)}개는 위 합계에서 빠졌습니다.")
    if any(job.get("live") for job in jobs):
        lines.append("※ live 사용량은 일반 요금 기준입니다. 무료 등급이면 실제 청구는 $0입니다.")
    if any(job.get("mixed") for job in jobs):
        lines.append("※ 혼합 작업은 무료 live $0 + 유료 Batch 요금으로 계산했습니다.")
    if any(not job.get("live") for job in jobs):
        lines.append("※ Batch 작업의 live 재시도분은 일반 요금인데 Batch 요금으로 계산했습니다.")
    return lines


def stats_lines(root):
    return app.stats_lines_from(app.collect_usage(root), root)


def needs_conversion(folder):
    """M4A가 아직 없는 MP4가 하나라도 있는지."""
    converted = {p.stem for p in folder.iterdir()
                 if p.is_file() and p.suffix.lower() == ".m4a"}
    return any(p.is_file() and p.suffix.lower() == ".mp4" and p.stem not in converted
               for p in folder.iterdir())


def clock_text(seconds):
    seconds = max(0, int(seconds))
    return f"{seconds // 60:02d}:{seconds % 60:02d}"


def _progress_enabled():
    return app.vt_supported()


def convert_progress_line(index, total, name, seconds, duration):
    if duration:
        percent = min(99, int(seconds / duration * 100))
        timing = f"{percent:3d}%  {app.clock_text(seconds)} / {app.clock_text(duration)}"
    else:
        timing = f"{app.clock_text(seconds)} 경과"
    return (f"  {app.accent('●')} 변환 ({index}/{total})   {timing}   "
            f"{app.dim(app.clip(name, 34))}")


def convert(folder):
    """폴더 안 모든 MP4를 M4A로 바꿉니다. 원본과 이미 만든 M4A는 건드리지 않습니다."""
    videos = sorted((p for p in folder.iterdir()
                     if p.is_file() and p.suffix.lower() == ".mp4"),
                    key=lambda p: app.natural_key(p.name))
    if not videos:
        app.log("변환할 MP4 파일이 없습니다.")
        return
    ffmpeg = app.find_tool("ffmpeg")
    if not ffmpeg and app.ensure_ffmpeg():
        ffmpeg = app.find_tool("ffmpeg")
    if not ffmpeg:
        app.fail("ffmpeg가 필요합니다. Windows는 설치 폴더의 ffmpeg.exe를 확인하거나 "
             "NALMEOK_FFMPEG를 지정하세요. macOS: brew install ffmpeg")
    converted = skipped = 0
    failures = []
    live = app._progress_enabled()
    for index, source in enumerate(videos, 1):
        output = source.with_suffix(".m4a")
        if output.exists() or output.is_symlink():
            app.log(f"건너뜀 (M4A 있음): {output.name}")
            skipped += 1
            continue
        duration = app.probe_duration(source)
        app.log(f"변환 ({index}/{len(videos)}): {source.name} → {output.name}")
        # 실패한 출력이 남지 않도록 임시 폴더에서 만든 뒤에만 공개합니다.
        with tempfile.TemporaryDirectory(prefix=".gemini_convert_", dir=folder) as temp:
            pending = Path(temp) / "audio.m4a"
            process = subprocess.Popen([
                ffmpeg, "-hide_banner", "-loglevel", "error", "-nostdin", "-n",
                "-progress", "pipe:1", "-nostats",
                "-i", str(source), "-map", "0:a:0", "-vn",
                "-c:a", "aac", "-b:a", "128k", "-movflags", "+faststart",
                str(pending),
            ], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
               encoding="utf-8", errors="replace")
            started = time.monotonic()
            assert process.stdout is not None
            for line in process.stdout:
                if not line.startswith("out_time="):
                    continue
                stamp = line.strip().split("=", 1)[1]
                try:
                    hours, minutes, secs = stamp.split(":")
                    seconds = int(hours) * 3600 + int(minutes) * 60 + float(secs)
                except ValueError:
                    seconds = time.monotonic() - started
                if live:
                    sys.stdout.write("\r\033[K" + app.convert_progress_line(
                        index, len(videos), source.name, seconds, duration))
                    sys.stdout.flush()
            _, stderr = process.communicate()
            if live:
                sys.stdout.write("\r\033[K")
                sys.stdout.flush()
            if process.returncode != 0:
                failures.append(source.name)
                app.log(f"변환 실패: {source.name}\n{(stderr or '').strip()}")
                continue
            try:
                # 동시 실행이 이미 만든 M4A를 덮어쓰지 않도록 링크로 게시합니다.
                os.link(pending, output)
            except FileExistsError:
                app.log(f"건너뜀 (M4A 있음): {output.name}")
                skipped += 1
            else:
                converted += 1
                app.log(f"변환 완료: {source.name} → {output.name}")
    app.log(f"변환 결과: 완료 {converted}개 · 건너뜀 {skipped}개 · 실패 {len(failures)}개")
    if failures:
        app.fail("변환하지 못한 파일: " + ", ".join(failures))


def list_media(folder):
    audio = sorted((p for p in folder.iterdir()
                    if p.is_file() and p.suffix.lower() == ".m4a"),
                   key=lambda p: app.natural_key(p.name))
    if not 1 <= len(audio) <= app.MAX_FILES:
        app.fail(f".m4a 파일은 1~{app.MAX_FILES}개여야 합니다 (현재 {len(audio)}개).")
    pdfs = [p for p in folder.iterdir()
            if p.is_file() and p.suffix.lower() == ".pdf"]
    if len(pdfs) > 1:
        app.fail("참고 PDF는 폴더에 최대 1개까지 둘 수 있습니다.")
    return audio, (pdfs[0] if pdfs else None)


def snapshot(path, key=None):
    stat = path.stat()
    obj = {"name": path.name, "size": stat.st_size, "mtime_ns": stat.st_mtime_ns}
    if key:
        obj["key"] = key
    return obj


def load(folder):
    path = app.state_path(folder)
    if not path.exists():
        app.fail(f"작업 정보가 없습니다: {path}\n먼저 upload를 실행하세요.")
    data = app.read_json_object(path)
    if data is None:
        app.fail(f"작업 정보가 올바르지 않습니다: {path}")
    return data


def validate_local_files(folder, job):
    audios, pdf = app.list_media(folder)
    if [app.snapshot(p, f"audio_{i:03d}") for i, p in enumerate(audios, 1)] != [
        {k: item[k] for k in ("name", "size", "mtime_ns", "key")}
        for item in job["files"]
    ] or (app.snapshot(pdf) if pdf else None) != (
        {k: job["pdf"][k] for k in ("name", "size", "mtime_ns")}
        if job.get("pdf") else None
    ):
        app.fail("폴더의 입력 파일이 작업 생성 당시와 달라졌습니다. "
             "이미 제출한 작업과 섞이지 않도록 확인하세요.")


def mixed_usage(folder, state=None):
    """혼합 작업의 무료 live/유료 Batch 토큰과 유료 비용을 따로 집계합니다."""
    folder = Path(folder)
    state = state or app.read_json_object(app.live_state_path(folder)) or {}
    free_rows = app.response_rows(folder / app.LIVE_RESULT_FILE)
    free = app.usage_totals(free_rows)
    paid_rows = []
    for group in state.get("paid") or []:
        path = app.mixed_result_path(folder, group)
        if path.exists():
            paid_rows += app.response_rows(path)
    paid = app.usage_totals(paid_rows)
    model = state.get("model")
    cost = None
    if model in app.MODELS:
        cost = app.price(paid[0], paid[1], paid[2], model, True)
    return model, free, paid, cost


def mixed_cost_lines(folder, job):
    _model, free, paid, cost = app.mixed_usage(folder, job)
    if not any(free) and not any(paid):
        return []
    lines = []
    if any(free):
        lines.append(f"{app.pad('무료 live', app.LABEL_WIDTH)}  "
                     f"입력 {free[0] + free[1]:,} · 출력 {free[2]:,} · $0")
    if any(paid):
        lines.append(f"{app.pad('유료 Batch', app.LABEL_WIDTH)}  "
                     f"입력 {paid[0] + paid[1]:,} · 출력 {paid[2]:,}")
    if cost is not None:
        total_seconds = (free[0] + paid[0]) / app.AUDIO_TOKENS_PER_SECOND
        lines.append(f"{app.pad('실제 비용', app.LABEL_WIDTH)}  {app.money(cost)}")
        if total_seconds:
            lines.append(f"{app.pad('시간당', app.LABEL_WIDTH)}  "
                         f"{app.money(cost / (total_seconds / 3600))}")
        lines.append("※ 무료 등급 live는 $0, 남은 오디오는 유료 Batch 요금입니다.")
    return lines


def cost_lines(folder, job):
    if job.get("mode") == "mixed":
        return app.mixed_cost_lines(folder, job)
    _, (audio, text_in, output) = app.folder_usage(folder)
    if not audio and not output:
        return []
    lines = [f"{app.pad('실제 토큰', app.LABEL_WIDTH)}  입력 {audio + text_in:,} · 출력 {output:,}"]
    if job["model"] in app.MODELS:
        live = job.get("mode") == "live" or app.live_state_path(folder).exists()
        actual = app.price(audio, text_in, output, job["model"], not live)
        hours = audio / app.AUDIO_TOKENS_PER_SECOND / 3600
        lines.append(f"{app.pad('실제 비용', app.LABEL_WIDTH)}  {app.money(actual)}")
        if hours:
            lines.append(f"{app.pad('시간당', app.LABEL_WIDTH)}  {app.money(actual / hours)}")
        if live:
            lines.append("※ 무료 등급이면 실제 청구는 $0입니다 (유료 등급은 일반 요금).")
        else:
            lines.append("※ live 재시도분은 일반 요금입니다 (Batch 요금으로 계산).")
    return lines


def live_state_path(folder):
    return Path(folder) / app.LIVE_STATE_FILE


def live_result_path(folder):
    return Path(folder) / app.LIVE_RESULT_FILE


def live_segment_dir(folder):
    return Path(folder) / app.LIVE_SEGMENT_DIR


def load_live_state(folder):
    path = app.live_state_path(folder)
    if not path.exists():
        return None
    state = app.read_json_object(path)
    if state is None:
        app.fail(f"live 상태 파일이 손상되었습니다: {path}")
    return state


def live_row_map(folder):
    """완료된 live 그룹 행. 손상된 줄은 건너뜁니다."""
    path = app.live_result_path(folder)
    try:
        text = path.read_text(encoding="utf-8")
    except OSError:
        return {}
    rows = {}
    for line in text.splitlines():
        if not line.strip():
            continue
        try:
            row = json.loads(line)
        except ValueError:
            continue
        if not isinstance(row, dict):
            continue
        key = row.get("key") or (row.get("metadata") or {}).get("key")
        if key:
            rows[key] = row
    return rows


def _mixed_tag(group):
    return re.sub(r"[^A-Za-z0-9_.-]+", "_", str(group["key"]))


def mixed_result_path(folder, group):
    return Path(folder) / f"{app.MIXED_RESULT_PREFIX}.{app._mixed_tag(group)}.jsonl"


_CAP_CACHE = {}


def capabilities(refresh=False):
    """환경 상태. 무거운 확인은 한 번만 하고 키 정보는 매번 새로 읽습니다."""
    if refresh or not app._CAP_CACHE:
        app._CAP_CACHE.update(
            ffmpeg=app.find_tool("ffmpeg"),
            ffprobe=app.find_tool("ffprobe"),
            sdk=app.sdk_importable(),
        )
    entries = app.key_entries()
    return {
        **app._CAP_CACHE,
        "key": app.env_key() or app.saved_key(),
        "key_source": app.key_source(),
        "keys": entries,
        "key_summary": app.key_summary(entries),
    }


def caps_line(caps=None):
    caps = caps or app.capabilities()

    def mark(value):
        return app.accent("✓") if value else app.warn("✗")

    return (f"ffmpeg {mark(caps['ffmpeg'])} · ffprobe {mark(caps['ffprobe'])} · "
            f"SDK {mark(caps['sdk'])} · 키 {caps['key_source'] or app.warn('없음')}")


def console_attached():
    """프로세스에 콘솔이 붙어 있는지. stdout이 파일로 리다이렉트돼도 Windows는 참일 수 있습니다."""
    if os.name != "nt":
        return sys.stdout.isatty() or sys.stdin.isatty()
    try:
        import ctypes

        return bool(ctypes.windll.kernel32.GetConsoleWindow())
    except (AttributeError, OSError):
        return False


def terminal_name():
    if os.getenv("WT_SESSION"):
        return "Windows Terminal"
    if os.name == "nt":
        return "Windows console (conhost)"
    return os.getenv("TERM_PROGRAM") or os.getenv("TERM") or "unknown"


def doctor_lines():
    """문제 보고에 붙일 수 있는 환경 진단 정보. 키/SDK 없이 동작합니다."""
    caps = app.capabilities(refresh=True)
    try:
        cwd = app.short_path(Path.cwd())
    except OSError:
        cwd = "?"
    return [
        f"nalmeok {app.__version__}",
        f"{app.pad('OS', 12)}  {platform.platform()}",
        f"{app.pad('터미널', 12)}  {app.terminal_name()}",
        f"{app.pad('VT/ANSI', 12)}  {'가능' if app.vt_supported() else '불가'}",
        f"{app.pad('콘솔', 12)}  attached={app.console_attached()} "
        f"stdin_isatty={sys.stdin.isatty()}",
        f"{app.pad('stdout', 12)}  isatty={sys.stdout.isatty()} "
        f"encoding={getattr(sys.stdout, 'encoding', '?')}",
        f"{app.pad('Python', 12)}  {sys.version.split()[0]}",
        f"{app.pad('실행 파일', 12)}  {app.short_path(Path(sys.executable))}",
        f"{app.pad('앱 폴더', 12)}  {app.short_path(Path(__file__).resolve().parent)}",
        f"{app.pad('ffmpeg', 12)}  {caps['ffmpeg'] or '없음'}",
        f"{app.pad('ffprobe', 12)}  {caps['ffprobe'] or '없음'}",
        f"{app.pad('SDK', 12)}  {'설치됨' if caps['sdk'] else '없음'}",
        f"{app.pad('API 키', 12)}  {caps['key_summary']}",
        f"{app.pad('설정 파일', 12)}  {app.short_path(app.config_path())}",
        f"{app.pad('로그', 12)}  {app.short_path(app.debug_log_path())}",
        f"{app.pad('작업 폴더', 12)}  {cwd}",
    ]


def show_doctor():
    app.configure_stdio()
    print("\n".join(app.doctor_lines()))


def diagnostics_lines():
    caps = app.capabilities()
    return [
        f"{app.pad('OS', 12)}  {platform.platform()}",
        f"{app.pad('터미널', 12)}  {app.terminal_name()}",
        f"{app.pad('VT/ANSI', 12)}  {'가능' if app.vt_supported() else '불가'}",
        f"{app.pad('콘솔', 12)}  attached={app.console_attached()} "
        f"stdin_isatty={sys.stdin.isatty()}",
        f"{app.pad('stdout', 12)}  isatty={sys.stdout.isatty()} "
        f"encoding={getattr(sys.stdout, 'encoding', '?')}",
        f"{app.pad('Python', 12)}  {sys.version.split()[0]}",
        f"{app.pad('실행 파일', 12)}  {app.short_path(Path(sys.executable))}",
        f"{app.pad('ffmpeg', 12)}  {caps['ffmpeg'] or '없음'}",
        f"{app.pad('ffprobe', 12)}  {caps['ffprobe'] or '없음'}",
        f"{app.pad('SDK', 12)}  {'설치됨' if caps['sdk'] else '없음'}",
        f"{app.pad('API 키', 12)}  {caps['key_summary']}",
        f"{app.pad('키 출처', 12)}  {caps['key_source'] or '없음'}",
        f"{app.pad('설정 파일', 12)}  {app.short_path(app.config_path())}",
        f"{app.pad('작업 폴더', 12)}  {app.short_path(Path.cwd())}",
    ]

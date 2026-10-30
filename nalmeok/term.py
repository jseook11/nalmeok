"""nalmeok.term."""

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



_VT_READY = None


def bootstrap_platform():
    """실행 시작 시 플랫폼 기능을 한 번 준비합니다."""
    app.configure_stdio()
    app.vt_supported()


def _enable_windows_vt():
    """Windows 콘솔에서 ANSI 시퀀스를 쓸 수 있게 VT 처리를 켭니다."""
    try:
        import ctypes

        kernel32 = ctypes.windll.kernel32
        enable_vt = 0x0004  # ENABLE_VIRTUAL_TERMINAL_PROCESSING
        kernel32.GetStdHandle.restype = ctypes.c_void_p
        kernel32.GetConsoleMode.argtypes = [
            ctypes.c_void_p, ctypes.POINTER(ctypes.c_uint32)]
        kernel32.SetConsoleMode.argtypes = [ctypes.c_void_p, ctypes.c_uint32]
        enabled = False
        for stream_id in (-11, -12):  # STD_OUTPUT_HANDLE, STD_ERROR_HANDLE
            handle = kernel32.GetStdHandle(stream_id)
            mode = ctypes.c_uint32()
            if not kernel32.GetConsoleMode(handle, ctypes.byref(mode)):
                continue
            if kernel32.SetConsoleMode(handle, mode.value | enable_vt):
                if stream_id == -11:
                    enabled = True
            elif stream_id == -11:
                enabled = False
        return enabled
    except (AttributeError, OSError, ValueError):
        return False


def vt_supported():
    """ANSI 화면 제어를 쓸 수 있는 터미널인지. Windows는 VT 처리를 한 번 켜봅니다."""
    if os.name != "nt":
        return sys.stdout.isatty() and os.getenv("TERM") != "dumb"
    global _VT_READY
    if app._VT_READY is None:
        app._VT_READY = app._enable_windows_vt()
    return app._VT_READY


def can_raw_input():
    """Enter 없이 키를 읽을 수 있는 터미널인지."""
    if not sys.stdin.isatty():
        return False
    if os.name == "nt":
        return app.WINDOWS_TERMINAL
    return app.POSIX_TERMINAL


def require_tui():
    """TUI를 그릴 수 없는 환경이면 실행 전에 이해하기 쉬운 오류로 멈춥니다."""
    if not app.can_raw_input():
        app.fail("터미널 메뉴를 사용할 수 없습니다. Windows Terminal이나 실제 터미널에서 "
             "실행하거나 `nalmeok --help`의 서브커맨드를 사용하세요.")
    if not app.vt_supported():
        app.fail("이 터미널은 화면 제어(ANSI)를 지원하지 않습니다. "
             "Windows Terminal에서 실행해 주세요.")


def unicode_ok():
    """터미널 글꼴이 스피너·화살표 같은 기호를 안정적으로 그릴 수 있는지."""
    if os.getenv("NALMEOK_ASCII"):
        return False
    if os.getenv("NALMEOK_UNICODE"):
        return True
    if os.name != "nt":
        return True
    # Windows Terminal은 기호 렌더링이 안정적입니다. conhost는 ASCII로 폴백합니다.
    return bool(os.getenv("WT_SESSION"))


def pointer():
    return "❯" if app.unicode_ok() else ">"


def spinner_frames():
    return "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏" if app.unicode_ok() else "|/-\\"


def live_status(message):
    """진행 중인 마지막 로그 한 줄을 현재 줄에 덮어씁니다."""
    text = str(message).strip()
    if not text:
        return
    last = text.splitlines()[-1]
    if app.vt_supported():
        sys.stdout.write("\r\033[K  " + app.dim(app.clip(last, app.WIDTH - 4)))
    else:
        sys.stdout.write("  " + app.dim(app.clip(last, app.WIDTH - 4)) + "\n")
    sys.stdout.flush()


def format_state(state):
    return app.STATE_LABELS.get(state, state)


def ensure_paid_key(key=None):
    """TUI Batch 진입점. 유료 키가 없으면 안내하고 None 을 돌려줍니다."""
    candidate = app.paid_key_for_batch(key)
    if candidate:
        return candidate
    app.notice("유료 Batch에는 유료 API 키가 필요합니다. 설정에서 유료 키를 추가하거나 "
           "l(무료 live)로 실행하세요.")
    app.wait()
    return None
LABEL_WIDTH = 11


def display_width(text):
    """터미널에서 차지하는 칸 수. 한글·한자 같은 전각 문자는 2칸입니다.

    macOS 파일명처럼 자모가 분리된(NFD) 한글은 그대로 세면 폭이 부풀어
    열이 밀리므로 NFC 로 합쳐서 셉니다.
    """
    text = unicodedata.normalize("NFC", app.ANSI_RE.sub("", text))
    return sum(2 if unicodedata.east_asian_width(ch) in ("W", "F") else 1
               for ch in text)


def pad(text, width):
    return text + " " * max(0, width - app.display_width(text))


def align_right(text, width):
    return " " * max(0, width - app.display_width(text)) + text


def confirm_spend(lines):
    """CLI에서 제출 전 예상 비용을 확인합니다. 자동 실행은 --yes 로 건너뜁니다."""
    if not sys.stdin.isatty():
        app.fail("제출 전 비용 확인이 필요한데 터미널이 아닙니다. 자동 실행에는 --yes 를 붙이세요.")
    print(f"  {app.warn('?')} 위 예상 비용으로 제출할까요? [y/N] ", end="", flush=True)
    try:
        return input().strip().lower() in ("y", "yes")
    except EOFError:
        print()
        return False


@contextlib.contextmanager
def cbreak_keys():
    """대기 화면에서 Enter 없이 키 하나를 읽을 수 있게 합니다."""
    if not app.can_raw_input():
        yield False
        return
    if os.name == "nt":
        # msvcrt.getwch() 는 에코 없이 한 키씩 읽으므로 모드 변경이 필요 없습니다.
        yield True
        return
    fd = sys.stdin.fileno()
    saved = app.termios.tcgetattr(fd)
    try:
        app.tty.setcbreak(fd)
        yield True
    finally:
        app.termios.tcsetattr(fd, app.termios.TCSADRAIN, saved)


def _key_ready(window):
    """window 초 동안 키 입력을 기다립니다. 입력이 준비되면 True."""
    if not app.can_raw_input():
        time.sleep(window)
        return False
    if os.name == "nt":
        deadline = time.monotonic() + window
        while True:
            if app.msvcrt.kbhit():
                return True
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                return False
            time.sleep(min(0.03, remaining))
    return bool(select.select([sys.stdin], [], [], window)[0])


def _wait_for_key(interval, allow_detach, tick=None, step=0.12):
    """interval 동안 키를 기다립니다. tick 이 있으면 step 간격으로 화면을 갱신합니다."""
    deadline = time.monotonic() + interval
    while True:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            return None
        if tick is not None:
            tick()
        window = min(step, remaining)
        if allow_detach:
            if app._key_ready(window):
                return app.read_key()
        else:
            time.sleep(window)


def _color_enabled():
    return app.vt_supported() and not os.getenv("NO_COLOR")


def _paint(*codes):
    if not app._color_enabled():
        return lambda text: text
    prefix = "\033[" + ";".join(codes) + "m"
    return lambda text: f"{prefix}{text}\033[0m"


def dim(text):
    return _paint("2")(text)


def bold(text):
    return _paint("1")(text)


def accent(text):
    return _paint("36")(text)


def warn(text):
    return _paint("33")(text)

WIDTH = 64


def clear_screen():
    """보이는 화면만 지우고 커서를 맨 위로 올립니다. 스크롤백은 남깁니다."""
    if app.vt_supported():
        print("\033[2J\033[H", end="", flush=True)


MIN_WIDTH = 46
MAX_WIDTH = 84


def term_size():
    return shutil.get_terminal_size((80, 24))


def refresh_layout():
    """터미널 폭에 맞춰 WIDTH 를 다시 계산합니다."""
    global WIDTH
    app.WIDTH = max(app.MIN_WIDTH, min(app.MAX_WIDTH, app.term_size().columns - 4))


@contextlib.contextmanager
def fullscreen():
    """전체 화면 버퍼를 쓰고 나올 때 스크롤백을 원래대로 돌려놓습니다."""
    if not app.vt_supported():
        yield
        return
    sys.stdout.write("\033[?1049h\033[?25l")
    sys.stdout.flush()
    try:
        yield
    finally:
        sys.stdout.write("\033[?25h\033[?1049l")
        sys.stdout.flush()


def _title_line(title, subtitle=""):
    room = max(8, app.WIDTH - app.display_width(title) - 4)
    tail = f"   {app.dim(app.clip(subtitle, room))}" if subtitle else ""
    print(f"  {app.bold(title)}{tail}")
    print(f"  {app.dim('─' * app.WIDTH)}")
    print()


def header(title="", subtitle=""):
    """화면 상단을 새로 그립니다. 제목이 없으면 로고를 씁니다."""
    app.refresh_layout()
    app.clear_screen()
    print()
    if title:
        app._title_line(title, subtitle)
    else:
        print(f"  {app.bold('날먹')}   {app.dim('강의 음성을 Batch·live로 전사')}")
        print(f"  {app.dim('─' * app.WIDTH)}")
        print()


def footer(hint):
    print()
    print(f"  {app.dim(hint)}")


def pulse_bar(width, step, size=3):
    """진행률을 모르는 대기용 막대. 밝은 칸이 왼쪽에서 오른쪽으로 흐릅니다."""
    width = max(size, width)
    filled, empty = ("█", "░") if app.unicode_ok() else ("#", "-")
    cells = [empty] * width
    for offset in range(size):
        cells[(step + offset) % width] = filled
    return "".join(app.accent(cell) if cell == filled else app.dim(cell) for cell in cells)


def banner():
    app.refresh_layout()
    print()
    print(f"  {app.bold('날먹')}   {app.dim('강의 음성을 Batch·live로 전사')}")
    print(f"  {app.dim('─' * app.WIDTH)}")
    print()


def section(title, subtitle=""):
    app.refresh_layout()
    print()
    app._title_line(title, subtitle)


def screen(title="", subtitle=""):
    """TUI 화면 하나를 새로 그립니다. 이전 화면은 지웁니다."""
    app.refresh_layout()
    app.clear_screen()
    if title:
        print()
        app._title_line(title, subtitle)
    else:
        app.banner()


def notice(message):
    print(f"  {app.warn('!')} {message}")


def clip(text, width):
    """표시 폭 기준으로 자르고 말줄임표를 붙입니다."""
    text = unicodedata.normalize("NFC", text)
    if app.display_width(text) <= width:
        return text
    out, used = "", 0
    for char in text:
        size = 2 if unicodedata.east_asian_width(char) in ("W", "F") else 1
        if used + size > width - 1:
            break
        out += char
        used += size
    return out + "…"


def prompt_box(label, hidden=False):
    """한 줄 입력. 상자 없이 화면 헤더와 같은 톤으로 그립니다."""
    print()
    try:
        if hidden:
            return getpass.getpass(f"  {app.accent(app.pointer())} {label}  ").strip()
        return input(f"  {app.accent(app.pointer())} {label}  ").strip()
    finally:
        print()


def wait(title="Enter 돌아가기"):
    app.footer(title)
    try:
        input()
    except EOFError:
        print()


def normalize(option):
    """(key, label, desc, faded, skip) 로 맞춥니다. skip 은 커서가 건너뛸 항목입니다."""
    if option is None:
        return None
    option = list(option) + [None] * (5 - len(option))
    return (option[0], option[1], option[2] or "", bool(option[3]),
            bool(option[4]))


def _line_count(text):
    """터미널 폭을 고려해 실제로 차지하는 줄 수를 셉니다."""
    columns = shutil.get_terminal_size((app.WIDTH + 4, 24)).columns
    return max(1, (app.display_width(text) + columns - 1) // columns)


def draw_lines(lines):
    """한 프레임을 한 번에 출력합니다. 실제로 차지한 줄 수를 돌려줍니다."""
    sys.stdout.write("\n".join(lines) + "\n")
    sys.stdout.flush()
    return sum(app._line_count(line) for line in lines)


def update_lines(previous, current, height):
    """이미 그려진 프레임에서 달라진 줄만 다시 씁니다.

    커서가 프레임 마지막 줄 다음에 있고, 모든 줄이 한 줄에 들어갈 때만 동작합니다.
    아니면 False 를 돌려주고 호출자가 전체를 다시 그립니다.
    """
    if (height != len(current) or len(previous) != len(current)
            or height != len(previous)):
        return False
    if any(app._line_count(line) != 1 for line in current):
        return False
    chunks = [f"\033[{height}A"]
    for old, new in zip(previous, current):
        if old == new:
            chunks.append("\033[B")
        else:
            chunks.append("\r\033[K" + new + "\n")
    sys.stdout.write("".join(chunks))
    sys.stdout.flush()
    return True


def menu_lines(entries, rows, cursor, footer=None, allow_back=False):
    """메뉴 한 프레임을 줄 목록으로 만듭니다. rows 의 None 은 구분선입니다."""
    app.refresh_layout()
    with_desc = [entry for entry in entries if entry[2]]
    label_width = min(
        max((app.display_width(entry[1]) for entry in with_desc), default=8), 28)
    rendered = []
    for row in rows:
        if row is None:
            rendered.append(f"  {app.dim('─' * app.WIDTH)}")
            continue
        entry = entries[row]
        label, desc, faded = entry[1], entry[2], entry[3]
        text = app.pad(app.clip(label, label_width), label_width)
        room = max(8, app.WIDTH - label_width - 5)
        tail = app.dim(app.clip(desc, room)) if desc else ""
        if row == cursor:
            name = app.dim(text) if faded else app.bold(text)
            rendered.append(f"  {app.accent(app.pointer())} {name}   {tail}")
        elif faded:
            rendered.append(f"    {app.dim(text)}   {tail}")
        else:
            rendered.append(f"    {text}   {tail}")
    if footer is None:
        footer = ("↑↓ 이동 · Enter 선택"
                  + (" · Esc 뒤로" if allow_back else " · Ctrl+C 종료"))
    rendered.append("")
    rendered.append(f"  {app.dim(app.clip(footer, app.WIDTH - 2))}")
    return rendered


def draw_menu(entries, rows, cursor, footer=None, allow_back=False):
    """메뉴를 목록으로 그립니다. rows 의 None 은 구분선입니다."""
    return app.draw_lines(app.menu_lines(entries, rows, cursor, footer, allow_back))


def map_windows_key(char, next_char=None):
    """Windows 콘솔 키 코드를 공통 키 이름으로 바꿉니다."""
    if char in ("\x00", "\xe0"):
        return app.WIN_ARROWS.get(next_char, "other")
    if char in ("\r", "\n"):
        return "enter"
    if char == "\x03":
        raise KeyboardInterrupt
    if char == "\x04":
        raise EOFError
    return char


def _paste_mode(enabled):
    """붙여넣기(드래그 포함)를 ESC[200~ ... ESC[201~ 로 감싸 받습니다.

    화면 제어가 되는 터미널에서만 켭니다. 입력 줄(input/getpass)을 쓰는 동안은
    read_key 밖이라 자동으로 꺼져 있어 API 키 붙여넣기가 오염되지 않습니다.
    """
    if not app.vt_supported():
        return
    sys.stdout.write("\033[?2004h" if enabled else "\033[?2004l")
    sys.stdout.flush()


def _read_paste_posix(fd):
    data = b""
    end = b"\x1b[201~"
    deadline = time.monotonic() + 2.0
    while len(data) < app.PASTE_LIMIT and time.monotonic() < deadline:
        if not select.select([fd], [], [], 0.2)[0]:
            continue
        data += os.read(fd, 4096)
        if end in data:
            data = data.split(end, 1)[0]
            break
    return data.decode("utf-8", "replace")


def _read_paste_windows():
    text = ""
    end = "\x1b[201~"
    deadline = time.monotonic() + 2.0
    while len(text) < app.PASTE_LIMIT and time.monotonic() < deadline:
        if app.msvcrt.kbhit():
            text += app.msvcrt.getwch()
            if text.endswith(end):
                return text[:-len(end)]
        else:
            time.sleep(0.005)
    return text


def _read_key_windows():
    char = app.msvcrt.getwch()
    if char in ("\x00", "\xe0"):
        return app.map_windows_key(char, app.msvcrt.getwch())
    if char == "\x1b":
        rest = ""
        deadline = time.monotonic() + 0.05
        while len(rest) < 2 and time.monotonic() < deadline:
            if app.msvcrt.kbhit():
                rest += app.msvcrt.getwch()
            else:
                time.sleep(0.005)
        if not rest:
            return "esc"
        if rest == "[2":
            deadline = time.monotonic() + 0.5
            while len(rest) < 5 and time.monotonic() < deadline:
                if app.msvcrt.kbhit():
                    rest += app.msvcrt.getwch()
                else:
                    time.sleep(0.005)
            if rest == "[200~":
                return ("paste", app._read_paste_windows())
        return app.ARROWS.get(rest, "other")
    return app.map_windows_key(char)


def read_key():
    """raw 모드로 키 하나를 읽습니다. 방향키는 up/down/left/right 로 돌려줍니다.

    붙여넣기·드래그는 ("paste", 텍스트) 튜플로 돌려줍니다.
    """
    app._paste_mode(True)
    try:
        if os.name == "nt":
            return app._read_key_windows()
        fd = sys.stdin.fileno()
        saved = app.termios.tcgetattr(fd)
        try:
            app.tty.setraw(fd)
            char = os.read(fd, 1).decode("utf-8", "ignore")
            if char in ("\r", "\n"):
                return "enter"
            if char == "\x03":
                raise KeyboardInterrupt
            if char == "\x04":
                raise EOFError
            if char != "\x1b":
                return char
            rest = ""
            while len(rest) < 2 and select.select([fd], [], [], 0.05)[0]:
                rest += os.read(fd, 1).decode("utf-8", "ignore")
            if not rest:
                return "esc"
            if rest == "[2":
                while len(rest) < 5 and select.select([fd], [], [], 0.5)[0]:
                    rest += os.read(fd, 1).decode("utf-8", "ignore")
                if rest == "[200~":
                    return ("paste", app._read_paste_posix(fd))
            return app.ARROWS.get(rest, "other")
        finally:
            app.termios.tcsetattr(fd, app.termios.TCSADRAIN, saved)
    finally:
        app._paste_mode(False)


def wait_key():
    """Enter 한 번을 기다립니다. raw 입력이 없으면 input()으로 대체합니다."""
    if app.can_raw_input():
        try:
            app.read_key()
        except (KeyboardInterrupt, EOFError):
            pass
        return
    try:
        input()
    except (KeyboardInterrupt, EOFError):
        pass


def _menu_entries(options):
    entries, rows = [], []
    for option in options:
        if option is None:
            rows.append(None)
            continue
        rows.append(len(entries))
        entries.append(app.normalize(option))
    return entries, rows


def _draw_info(info):
    if not info:
        return
    labels = [label for label, _ in info if label]
    width = min(max((app.display_width(label) for label in labels), default=6), 12)
    for label, value in info:
        if label:
            print(f"  {app.dim(app.pad(label, width))}   {value}")
        else:
            print(f"  {value}")
    print()


def _menu_move(entries, cursor, step):
    """skip 표시된 항목을 건너뛴 다음 커서 위치. 모두 skip 이면 제자리입니다."""
    total = len(entries)
    for offset in range(1, total + 1):
        index = (cursor + step * offset) % total
        if not entries[index][4]:
            return index
    return cursor


def _menu_limit(info=()):
    """헤더·정보·푸터를 제외하고 메뉴에 보여줄 수 있는 줄 수."""
    rows = shutil.get_terminal_size((app.WIDTH + 4, 24)).lines
    return max(4, rows - 9 - len(info))


def _menu_window(rows, cursor, limit):
    """커서가 보이도록 rows 의 일부 구간을 돌려줍니다. (구간, 시작, 전체)"""
    total = len(rows)
    if total <= limit:
        return rows, 0, total
    positions = [index for index, row in enumerate(rows) if row == cursor]
    position = positions[0] if positions else 0
    start = max(0, min(position - limit // 2, total - limit))
    return rows[start:start + limit], start, total


def select_menu(title, options, info=(), allow_back=False, heading=None,
                subtitle="", footer=None, left_up=False):
    """방향키로 고르고 Enter 로 확정합니다. 선택된 항목의 key 를 돌려줍니다.

    left_up 이면 왼쪽 화살표가 BACK 대신 UP 을 돌려줍니다.
    """
    app.require_tui()
    entries, rows = app._menu_entries(options)
    if not entries:
        app.fail("선택할 항목이 없습니다.")
    cursor = next((index for index, entry in enumerate(entries) if not entry[4]), 0)

    def build_menu():
        limit = app._menu_limit(info)
        visible, start, total = app._menu_window(rows, cursor, limit)
        line_footer = footer
        if total > len(visible):
            base = line_footer or (
                "↑↓ 이동 · Enter 선택"
                + (" · Esc 뒤로" if allow_back else " · Ctrl+C 종료"))
            line_footer = (f"{base}  "
                           f"({start + 1}-{start + len(visible)}/{total})")
        return app.menu_lines(entries, visible, cursor, line_footer, allow_back)

    def render():
        app.screen(heading or title, subtitle)
        app._draw_info(info)
        return build_menu()

    lines = render()
    height = app.draw_lines(lines)
    while True:
        pressed = app.read_key()
        if pressed == "up":
            cursor = app._menu_move(entries, cursor, -1)
        elif pressed == "down":
            cursor = app._menu_move(entries, cursor, 1)
        elif pressed in ("right", "enter"):
            return entries[cursor][0]
        elif pressed in ("left", "esc"):
            if allow_back:
                if pressed == "left" and left_up:
                    return app.UP
                return app.BACK
            if pressed == "left":
                cursor = app._menu_move(entries, cursor, -1)
        else:
            continue
        new_lines = build_menu()
        terminal_rows = shutil.get_terminal_size((app.WIDTH + 4, 24)).lines
        if height >= terminal_rows - 1:
            lines = render()
            height = app.draw_lines(lines)
        elif not app.update_lines(lines, new_lines, height):
            sys.stdout.write(f"\033[{height}A")
            height = app.draw_lines(new_lines)
            lines = new_lines
        else:
            lines = new_lines


def multi_lines(entries, rows, cursor, selected, footer=None):
    """여러 선택 메뉴 한 프레임을 줄 목록으로 만듭니다."""
    app.refresh_layout()
    with_desc = [entry for entry in entries if entry[2]]
    label_width = min(
        max((app.display_width(entry[1]) for entry in with_desc), default=8), 28)
    rendered = []
    for row in rows:
        if row is None:
            rendered.append(f"  {app.dim('─' * app.WIDTH)}")
            continue
        entry = entries[row]
        label, desc, faded = entry[1], entry[2], entry[3]
        text = app.pad(app.clip(label, label_width), label_width)
        room = max(8, app.WIDTH - label_width - 7)
        tail = app.dim(app.clip(desc, room)) if desc else ""
        check = app.accent('●') if row in selected else app.dim('○')
        if row == cursor:
            name = app.dim(text) if faded else app.bold(text)
            rendered.append(f"  {app.accent(app.pointer())} {check} {name}   {tail}")
        elif faded:
            rendered.append(f"    {app.dim(text)}   {tail}")
        else:
            rendered.append(f"    {check} {text}   {tail}")
    rendered.append("")
    footer = footer or "↑↓ 이동 · Space 선택 · Enter 확정 · Esc 뒤로"
    rendered.append(f"  {app.dim(app.clip(footer, app.WIDTH - 2))}")
    return rendered


def draw_multi(entries, rows, cursor, selected, footer=None):
    """여러 선택 메뉴. rows 의 None 은 구분선입니다."""
    return app.draw_lines(app.multi_lines(entries, rows, cursor, selected, footer))


def select_many(title, options, info=(), heading=None, subtitle=""):
    """여러 항목을 Space 로 표시하고 Enter 로 확정합니다. BACK 을 돌려줄 수 있습니다."""
    app.require_tui()
    entries, rows = app._menu_entries(options)
    if not entries:
        app.fail("선택할 항목이 없습니다.")
    cursor = 0
    selected = set()

    def build_menu():
        limit = app._menu_limit(info)
        visible, start, total = app._menu_window(rows, cursor, limit)
        line_footer = None
        if total > len(visible):
            line_footer = (
                "↑↓ 이동 · Space 선택 · Enter 확정 · Esc 뒤로  "
                f"({start + 1}-{start + len(visible)}/{total})")
        return app.multi_lines(entries, visible, cursor, selected, line_footer)

    def render():
        app.screen(heading or title, subtitle)
        app._draw_info(info)
        return build_menu()

    lines = render()
    height = app.draw_lines(lines)
    while True:
        pressed = app.read_key()
        if pressed == "up":
            cursor = (cursor - 1) % len(entries)
        elif pressed == "down":
            cursor = (cursor + 1) % len(entries)
        elif pressed in (" ", "space"):
            if cursor in selected:
                selected.discard(cursor)
            else:
                selected.add(cursor)
        elif pressed in ("right", "enter"):
            return [entries[index][0] for index in sorted(selected)]
        elif pressed in ("left", "esc"):
            return app.BACK
        else:
            continue
        new_lines = build_menu()
        terminal_rows = shutil.get_terminal_size((app.WIDTH + 4, 24)).lines
        if height >= terminal_rows - 1:
            lines = render()
            height = app.draw_lines(lines)
        elif not app.update_lines(lines, new_lines, height):
            sys.stdout.write(f"\033[{height}A")
            height = app.draw_lines(new_lines)
            lines = new_lines
        else:
            lines = new_lines


def choose_retry(count):
    answer = app.select_menu("재시도 방식", [
        ("live", "live", "지금 재요청 (일반 API 요금, 최대 3회)"),
        ("batch", "batch", "실패한 파일만 새 Batch로 제출"),
        ("none", "none", "재시도하지 않음"),
        None,
        ("back", "이전", "", True),
    ], [("미완료", f"{count}개 · 성공한 파일은 보존됩니다")], allow_back=True)
    return "back" if answer is app.BACK else answer


def choose_spend(lines, folder=None):
    """제출 전 예상 비용을 보여주고 확인받습니다."""
    info = [("", line) for line in lines]
    answer = app.select_menu("이대로 제출할까요?", [
        ("yes", "제출", "위 예상 비용으로 Batch 제출"),
        ("no", "취소", "나중에 다시 시도", True),
    ], info, allow_back=True)
    if answer != "yes":
        app.clear_screen()
        return False
    if folder is not None:
        # 확인 화면을 지우고 예상 비용을 다시 그려 제출 로그와 겹치지 않게 합니다.
        app.screen("Batch 제출", str(folder))
        for line in lines:
            app.log(line)
        app.log("")
    return True


def show_lines(title, subtitle, lines, pause=True):
    app.screen(title, subtitle)
    for line in lines:
        app.log(line)
    if pause:
        app.wait()


def human_size(size):
    value = float(size)
    for unit in ("B", "KB", "MB", "GB"):
        if value < 1024 or unit == "GB":
            return f"{value:.0f} {unit}" if unit == "B" else f"{value:.1f} {unit}"
        value /= 1024
    return f"{value:.1f} GB"


def log_tail(path, limit=6):
    try:
        lines = Path(path).read_text(encoding="utf-8").splitlines()
    except OSError:
        return
    tail = [line for line in lines[-limit:] if line.strip()]
    if not tail:
        return
    app.log("")
    app.log(app.dim("최근 로그"))
    for line in tail:
        app.log(app.clip(line, app.WIDTH + 16))


def _read_secret_windows(label):
    """Windows 콘솔에서 가려진 입력을 한 글자씩 읽습니다."""
    buffer = []
    mask = "•" if app.unicode_ok() else "*"
    sys.stdout.write(f"  {app.accent(app.pointer())} {app.pad(label, 15)} ")
    sys.stdout.flush()
    try:
        while True:
            char = app.msvcrt.getwch()
            if char in ("\r", "\n"):
                break
            if char in ("\x00", "\xe0"):
                if app.msvcrt.kbhit():
                    app.msvcrt.getwch()
                continue
            if char == "\x1b":
                if not app.msvcrt.kbhit():
                    return None
                while app.msvcrt.kbhit():
                    app.msvcrt.getwch()
                continue
            if char == "\x03":
                raise KeyboardInterrupt
            if char == "\x04":
                break
            if char in ("\x7f", "\b"):
                if buffer:
                    buffer.pop()
                    sys.stdout.write("\b \b")
                    sys.stdout.flush()
                continue
            if char and char.isprintable() and char != "\t":
                buffer.append(char)
                sys.stdout.write(mask)
                sys.stdout.flush()
    finally:
        print()
    text = "".join(buffer).strip()
    return text or None


def read_secret(label):
    """가려진 입력 한 줄. Enter 확정, Esc 취소(None), 백스페이스 지원."""
    if os.name == "nt" and app.can_raw_input():
        return app._read_secret_windows(label)
    if not (app.POSIX_TERMINAL and sys.stdin.isatty()):
        try:
            return getpass.getpass(f"  {label}: ").strip() or None
        except (EOFError, KeyboardInterrupt):
            return None
    fd = sys.stdin.fileno()
    saved = app.termios.tcgetattr(fd)
    buffer = []
    sys.stdout.write(f"  {app.accent(app.pointer())} {app.pad(label, 15)} ")
    sys.stdout.flush()
    try:
        app.tty.setraw(fd)
        while True:
            char = os.read(fd, 1).decode("utf-8", "ignore")
            if char in ("\r", "\n"):
                break
            if char == "\x1b":
                if not select.select([fd], [], [], 0.05)[0]:
                    return None
                while select.select([fd], [], [], 0.01)[0]:
                    os.read(fd, 1)
                continue
            if char == "\x03":
                raise KeyboardInterrupt
            if char == "\x04":
                break
            if char in ("\x7f", "\b"):
                if buffer:
                    buffer.pop()
                    sys.stdout.write("\b \b")
                    sys.stdout.flush()
                continue
            if char and char.isprintable() and char != "\t":
                buffer.append(char)
                sys.stdout.write("•")
                sys.stdout.flush()
    finally:
        app.termios.tcsetattr(fd, app.termios.TCSADRAIN, saved)
        print()
    text = "".join(buffer).strip()
    return text or None


def _copy_windows_clipboard(text):
    """ctypes로 유니코드 클립보드에 넣습니다. clip.exe의 코드페이지 문제를 피합니다."""
    try:
        import ctypes

        cf_unicode = 13
        gmem_moveable = 0x0002
        user32 = ctypes.windll.user32
        kernel32 = ctypes.windll.kernel32
        kernel32.GlobalAlloc.restype = ctypes.c_void_p
        kernel32.GlobalAlloc.argtypes = [ctypes.c_uint, ctypes.c_size_t]
        kernel32.GlobalLock.restype = ctypes.c_void_p
        kernel32.GlobalLock.argtypes = [ctypes.c_void_p]
        kernel32.GlobalUnlock.argtypes = [ctypes.c_void_p]
        kernel32.GlobalFree.argtypes = [ctypes.c_void_p]
        user32.SetClipboardData.argtypes = [ctypes.c_uint, ctypes.c_void_p]
        user32.SetClipboardData.restype = ctypes.c_void_p
        if not user32.OpenClipboard(None):
            return False
        try:
            user32.EmptyClipboard()
            data = text.encode("utf-16-le") + b"\x00\x00"
            handle = kernel32.GlobalAlloc(gmem_moveable, len(data))
            if not handle:
                return False
            pointer = kernel32.GlobalLock(handle)
            if not pointer:
                kernel32.GlobalFree(handle)
                return False
            try:
                ctypes.memmove(pointer, data, len(data))
            finally:
                kernel32.GlobalUnlock(handle)
            if not user32.SetClipboardData(cf_unicode, handle):
                kernel32.GlobalFree(handle)
                return False
            return True
        finally:
            user32.CloseClipboard()
    except (AttributeError, OSError, ValueError):
        return False


def copy_to_clipboard(text):
    if os.name == "nt":
        return app._copy_windows_clipboard(text)
    if sys.platform == "darwin":
        commands = [["pbcopy"]]
    else:
        commands = []
        if shutil.which("wl-copy"):
            commands.append(["wl-copy"])
        if shutil.which("xclip"):
            commands.append(["xclip", "-selection", "clipboard"])
        if shutil.which("xsel"):
            commands.append(["xsel", "--clipboard", "--input"])
    for command in commands:
        try:
            subprocess.run(command, input=text, text=True, check=True,
                           encoding="utf-8", errors="replace",
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            return True
        except (OSError, subprocess.CalledProcessError):
            continue
    return False


def open_result(folder):
    path = app.output_path(folder)
    if not path.exists():
        app.fail(f"결과 파일이 없습니다: {path}")
    editor = os.getenv("VISUAL") or os.getenv("EDITOR")
    if editor:
        parts = shlex.split(editor, posix=os.name != "nt")
        if os.name == "nt":
            parts = [part.strip('"') for part in parts]
        subprocess.call(parts + [str(path)])
    elif sys.platform == "darwin":
        subprocess.run(["open", str(path)], check=False)
    elif os.name == "nt":
        os.startfile(str(path))
    else:
        subprocess.run(["xdg-open", str(path)], check=False)

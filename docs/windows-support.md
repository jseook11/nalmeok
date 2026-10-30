# Windows 지원 및 배포 계획

> **상태:** 승인됨 (2026-10-30)
> **관련 이슈:** [#1 Windows TUI 실행 문제 해결 및 비개발자를 위한 Easy Setup 제공](https://github.com/jseook11/nalmeok/issues/1)
> **이 문서가 단일 기준입니다.** 코드와 이 문서가 어긋나면, 작업을 멈추고 이 문서를 먼저 갱신하세요.

---

## 0. 이 문서를 먼저 읽는 사람에게

컨텍스트가 요약되거나 새 세션에서 이어받더라도, 아래 순서만 지키면 같은 방향으로 진행할 수 있습니다.

1. 이 문서의 **결정 로그(2장)** 와 **불변 조건(0.3)** 을 확인합니다.
2. **상태 표(0.1)** 에서 마지막으로 끝난 작업과 다음 작업을 확인합니다.
3. 해당 PR의 **작업 범위(5장)** 만 구현합니다. 범위 밖 개선은 하지 않습니다.
4. 작업을 마치면 **상태 표와 변경 이력(10장)** 을 갱신합니다.

### 0.1 상태 표

작업이 끝날 때마다 이 표를 갱신합니다. `PR` 열에는 실제 PR 번호/링크를 적습니다.

| 작업 | 상태 | PR | 검증 |
|---|---|---|---|
| PR 1 · Windows 입력/터미널 계층 | 완료 | - | Windows 11 SSH/ConPTY에서 TUI 렌더링·방향키·Esc·Ctrl+C 확인. 테스트 104 pass (Win 3 skip / macOS 8 skip) |
| PR 2 · Windows 정합성(인코딩·도구·잠금) | 완료 | - | Windows 11에서 108 tests pass. 한글 클립보드 왕복, 프로세스 간 job_lock 차단, doctor·debug 로그 확인 |
| PR 3 · 런타임 ZIP + `nalmeok.cmd`/`installer.cmd` | 완료 | - | Windows 11에서 빌드·압축 해제·런처·번들 테스트 108 pass·ffmpeg 변환·설치·바로가기·doctor 확인. ZIP 164.7MiB |
| 후속 · Windows 타이틀/폴더 스캔 제한 | 완료 | - | 타이틀/바로가기 ASCII `nalmeok`. Windows 111 tests pass. 홈 대시보드 0.013s, 기본 스캔 0.111s |
| 후속 · 메뉴/홈 깜빡임 제거 | 완료 | - | Windows 115 tests pass. select_menu/home clear_screen 1회, 40개 폴더 탐색 11→1회 확인 |
| 수동 검증 · Windows 10 22H2 conhost | 보류 | - | 2026-10-30 사용자 결정으로 실기 검증 보류. best-effort로 문서화 (D13) |
| 수동 검증 · Windows 10 22H2 Windows Terminal | 보류 | - | 위와 동일 |
| 수동 검증 · Windows 11 conhost | 진행 중 | - | VT/ANSI 가능, attached=True, stdin/stdout isatty=True 확인(2026-10-30). TUI 체크리스트·스크린샷 대기 |
| 수동 검증 · Windows 11 Windows Terminal | 진행 중 | - | WT_SESSION/WT_PROFILE_ID, VT/ANSI 가능, attached/isatty 확인(2026-10-30 20:27). TUI 체크리스트·스크린샷 대기 |
| README Windows 설치 안내 | 완료 | - | Windows 11 1차 검증 기준으로 작성 |

상태 값: `미착수` / `진행 중` / `완료` / `보류`

### 0.2 기준선

- 계획 수립 시점 커밋: `103fe69`
- 테스트: `python -m unittest discover -s tests -q` → **99개 통과, 8개 skip** (macOS, Python 3.12.13)
- 앱 버전: `0.4.0` (Windows 지원 릴리스)
- 배포 런타임: Python 3.12.x (python-build-standalone, x86_64)
- **2026-10-30 Windows 1차 검증**
  - 환경: Windows 11 build 26200.9457, OpenSSH/ConPTY, Python 3.12.16 standalone
  - 테스트: **111개 통과, 3개 skip** (Windows)
  - TUI: 키 화면 렌더링, ↑↓ 이동, Esc 복귀, Ctrl+C 종료, 한글 출력 확인
  - 키 등록 흐름: 이름 입력, 가려진 API 키 입력(11자 마스킹), 400 응답 후 저장·홈 복귀 확인
  - PR2: 한글 클립보드 왕복, 프로세스 간 `job_lock` 차단, `doctor`, `NALMEOK_DEBUG` 로그 확인
  - PR3: Windows에서 ZIP 빌드(164.7MiB, SHA-256 `248db552…`), 압축 해제 실행, 번들 테스트 108 pass,
    번들 ffmpeg `convert`/`estimate`, `installer.cmd` 복사·바탕화면/시작 메뉴 바로가기, 설치 후 `doctor` 확인
  - 남은 검증: Windows 11 Windows Terminal(사용자 직접 진행 예정), 무료 live/유료 Batch 실제 전사
  - 후속: Windows 타이틀/바로가기를 ASCII `nalmeok`으로 변경, 폴더 스캔 제한(D14).
    Windows 111 tests pass, 홈 대시보드 0.013s, 기본 스캔 0.111s, `scan_descendant_media` 0.022s.
  - Windows 11 conhost(2026-10-30 20:23): `VT/ANSI 가능`, `attached=True`, `stdin/stdout isatty=True` 확인.
  - Windows 11 Windows Terminal(2026-10-30 20:27): `WT_SESSION`/`WT_PROFILE_ID` 확인, `VT/ANSI 가능`, `stdin/stdout isatty=True`. TUI 체크리스트·스크린샷은 대기.
  - 후속(diff 렌더링): select_menu/home 화면에서 `clear_screen` 1회, 이후 변경된 줄만 갱신됨을 Windows 프로브로 확인.
    긴 메뉴는 커서 주변 뷰포트만 렌더링(40개 폴더 탐색 clear 11→1). Windows 115 tests pass.
  - **Windows 10 22H2 실기 검증은 2026-10-30 사용자 결정으로 보류.** README와 이 문서에서 best-effort로 표기한다 (D13).

### 0.3 불변 조건 (바꾸려면 이 문서를 먼저 고친다)

1. **TUI 화면을 재작성하지 않는다.** 화면/레이아웃/문구는 그대로 두고 입력·터미널 계층만 바꾼다.
2. **PyInstaller/frozen exe를 만들지 않는다.** 배포물은 "런타임 포함 ZIP + cmd 런처"다.
3. **코드서명하지 않는다.** (현 단계. 비용 대비 효과 없음, 2장 D4)
4. **새 런타임 의존성을 추가하지 않는다.** 표준 라이브러리와 기존 `google-genai`만 쓴다.
5. **macOS/Linux 동작과 기존 테스트를 깨지 않는다.** 99개 통과 기준선을 유지한다.
6. **Windows 지원을 실기 검증 없이 "지원됨"으로 표기하지 않는다.**
7. **지원 하한은 Windows 10 22H2(빌드 19045) / Windows 11, x64.** 그 이하는 best-effort.

### 0.4 하지 말 것 (Do-not list)

- `os.path` 사용 금지. 경로는 항상 `pathlib.Path`.
- `subprocess(..., text=True)` 단독 사용 금지. 항상 `encoding="utf-8", errors="replace"`를 함께 준다.
- Windows 클립보드에 `clip.exe` 사용 금지. ctypes `CF_UNICODETEXT`를 쓴다.
- 화면 코드에서 `termios`/`tty`를 직접 참조하지 않는다. 플랫폼 계층 밖으로 새지 않게 한다.
- Windows 경로에 POSIX 규칙의 `shlex.split()`을 쓰지 않는다.
- VT 지원을 확인하지 않고 ANSI 시퀀스를 출력하지 않는다.
- `os.chmod(0o600)`이 Windows에서 권한을 보호한다고 가정하지 않는다.
- `os.execv` 재시작 경로를 Windows 배포물에서 타게 하지 않는다. (런타임에 SDK가 이미 있어 `ensure_sdk`가 조기 반환해야 정상)
- 전체 line-mode TUI를 새로 만들지 않는다. (2장 D9)

---

## 1. 목표와 범위

### 1.1 목표

- Windows 10/11에서 기존 TUI가 **첫 화면부터 정상 동작**한다.
- Python·uv·Git이 없는 PC에서 **압축 해제 → `installer.cmd` → 바탕화면 바로가기 → 실행**으로 첫 전사까지 도달한다.
- 실패했을 때 사용자가 이슈에 붙일 수 있는 **진단 정보(`nalmeok doctor`)** 를 제공한다.

### 1.2 비목표 (이번 작업에서 하지 않음)

- GUI/웹 UI, Electron/Tauri 래퍼
- macOS 설치 프로그램
- 자동 업데이트
- 코드서명, Microsoft Store/winget 배포
- 32비트 Windows, Windows ARM64 공식 지원
- 전체 line-mode TUI 대체 화면
- `fcntl` 수준의 완전한 POSIX 호환 파일 잠금 semantics

---

## 2. 결정 로그

| ID | 결정 | 이유 | 결과 |
|---|---|---|---|
| D1 | TUI를 유지하고 입력/터미널 계층만 수정 | 화면 재작성은 범위가 크고 오픈소스 유지보수 부담이 큼 | PR1이 300~500줄 규모로 제한됨 |
| D2 | PyInstaller 대신 python-build-standalone 런타임 + 소스 + cmd | hidden imports/onefile 재압축/frozen 분기/AV 오탐 회피. 디버깅 쉬움 | `sys.frozen` 분기 불필요, `sys.executable`·`__file__` 기존 코드 그대로 동작 |
| D3 | 배포 파일명 `nalmeok.cmd`, `installer.cmd` | ASCII 파일명은 경로/AV/압축 도구 호환성이 좋음 | 타이틀/바로가기는 `nalmeok`(ASCII) |
| D4 | 코드서명 안 함 | Azure Artifact Signing은 한국 개인 불가, OV는 연 $150~300, EV는 2024년부터 SmartScreen 즉시 통과 특례 없음 | ZIP "차단 해제" 안내 + SHA256 공개 |
| D5 | ffmpeg/ffprobe를 ZIP에 번들 | 학생 파일 대부분이 MP4. PATH/winget 안내는 비개발자 이탈 지점 | LGPL 정적 빌드 + 고지. 실측 ZIP 164.7MiB, v1은 번들 유지 |
| D6 | 지원 하한 Windows 10 22H2 / 11, x64 | 남은 Win10 사용자의 사실상 표준. conhost VT 동작을 보장하기 쉬움 | 1809~21H2는 best-effort로 문서화 |
| D7 | conhost와 Windows Terminal 모두 지원 | Win10은 Windows Terminal이 기본이 아님 | VT 활성화 + conhost 심볼 폴백 |
| D8 | ZIP은 최상위 폴더 없이 루트에 파일 배치 | Explorer "압축 풀기"가 ZIP 이름 폴더를 만들므로 이중 중첩 방지 | `installer.cmd`가 `%~dp0` 기준으로 복사 |
| D9 | 전체 line-mode TUI는 범위 밖 | VT 실패는 지원 하한에서 드묾. 화면별 대체 구현 비용이 큼 | VT 실패 시 명확한 오류 + Windows Terminal 안내. 필요 시 후속 이슈 |
| D10 | `nalmeok.cmd`는 인자가 없을 때만 `cd /d "%USERPROFILE%"` | CLI에서 상대 경로 인자를 깨지 않기 위함 | 더블클릭은 홈에서 시작, `nalmeok.cmd estimate .\x`는 cwd 유지 |
| D11 | ffmpeg 탐색은 `find_tool()`로 통일 | 번들 파일은 PATH에 없음. `shutil.which` 3곳이 흩어져 있음 | `NALMEOK_FFMPEG` 환경변수 → 앱 폴더 → PATH 순 |
| D12 | `doctor` 서브커맨드 추가 | Windows 실기 없이 원격 진단하기 위한 최소 장치 | 키/SDK 없이 동작 |
| D13 | Windows 10 22H2 실기 검증 보류 | Hyper-V 활성화·재부팅·Win10 설치 비용 대비, 사용 API는 22H2에서 안정적. 2026-10-30 사용자 결정 | README/문서에 best-effort로 표기. 필요 시 `nalmeok-verify` 키트로 재개 |
| D14 | Windows 폴더 스캔에 깊이·개수 제한 | `%USERPROFILE%` 아래 AppData/OneDrive 전체 재귀로 메인 화면이 수 분 걸림 | `job_folders` depth 2/dirs 2000(대시보드), `scan_descendant_media` 0.03s/depth 3/dirs 150, 탐색 상세 100/하위 20. 무거운 폴더 이름은 건너뜀 |
| D15 | 메뉴·홈 화면을 diff 렌더링 | 방향키마다 전체 메뉴를 다시 출력해 Windows Terminal에서 깜빡임 | 첫 프레임만 전체 출력. 이후 변경된 줄만 `\033[K`로 갱신하고 나머지는 커서만 이동. 긴 메뉴는 커서 주변 뷰포트만 그려 `(시작-끝/전체)` 표시. 줄바꿈·리사이즈·작은 화면은 전체 재렌더로 폴백 |

---

## 3. 현재 코드 진단 (요약)

전체 진단은 이슈 #1과 PR1 설명에 남긴다. 이후 세션이 같은 조사를 반복하지 않도록 핵심만 기록한다.

### 3.1 Windows에서 TUI가 죽는 경로

```text
import termios 실패 → RAW_KEYS = False
interactive() → fullscreen() → key_screen() → add_key_flow()
  → select_menu() → if not RAW_KEYS: fail("방향키 메뉴는 macOS·Linux 에서만...")
  → cli()가 RuntimeError를 잡고 종료
```

키가 이미 등록되어 있어도 `home_screen()`이 같은 가드를 갖고 있어 동일하게 종료한다.

### 3.2 원인 목록

| 증상 | 원인 | 위치(함수) |
|---|---|---|
| 첫 화면 즉시 종료 | `termios` ImportError → `RAW_KEYS=False` → `fail()` | 모듈 import, `select_menu`, `select_many`, `home_screen` |
| 키 입력 불가 | `os.read(stdin)`, `select.select([stdin])`, `termios.tcsetattr`, `tty.setraw` | `read_key`, `read_secret`, `cbreak_keys`, `_wait_for_key` |
| 화면 깨짐/이스케이프 노출 | VT 처리 미활성 | `fullscreen`, `clear_screen`, `_color_enabled` |
| 리다이렉트/로그에서 크래시 | 로캘 인코딩 + `⠋ ✓ ❯ ● ─` | `live_status`, `log`, 백그라운드 로그 |
| 동시 작업 보호 없음 | `fcntl` 없음 → 잠금 no-op | `job_lock`, `config_lock` |
| 한글 경로 복사 깨짐 | `clip.exe` 코드페이지 | `copy_to_clipboard` |
| ffmpeg 못 찾음 | 번들 파일이 PATH에 없음 | `probe_duration`, `convert`, `split_audio` |
| 오류 창 즉시 닫힘 | 더블클릭 실행 | 새 런처(`nalmeok.cmd`) |

### 3.3 이미 Windows 대응이 된 부분 (재사용)

- `process_alive()`: Windows는 `OpenProcess` 사용
- 백그라운드 감시: `DETACHED_PROCESS`, `stdin=DEVNULL`
- `open_result()`: `os.startfile`
- 설정 경로: `%APPDATA%\nalmeok\config.json`
- `.venv` 탐색에 `Scripts/python.exe` 포함 (배포물에서는 사용하지 않음)
- `__main__` 가드: `app.py`, `nalmeok.py` 모두 존재
- `asyncio`/`multiprocessing` 사용 없음 → spawn 문제 없음

---

## 4. 아키텍처

### 4.1 배포물 구조

ZIP 루트에 파일을 둔다. 압축을 풀면 ZIP 이름의 폴더가 생긴다.

```text
nalmeok-0.4.0-win64.zip
├─ nalmeok.cmd
├─ installer.cmd
├─ nalmeok.py
├─ app.py
├─ pyproject.toml
├─ runtime/                  # python-build-standalone + site-packages
│  ├─ python.exe
│  └─ Lib/site-packages/google/...
├─ ffmpeg.exe
├─ ffprobe.exe
├─ nalmeok.ico            # 선택. 없으면 installer.cmd가 기본 아이콘 사용
├─ README.md
├─ LICENSE
└─ licenses/
   ├─ Python-LICENSE.txt
   ├─ ffmpeg-LGPL.txt
   └─ THIRD-PARTY.md
```

설치 위치(권장): `%LOCALAPPDATA%\Programs\nalmeok`
바로가기: 바탕화면 + 시작 메뉴 `nalmeok`

### 4.2 실행 흐름

```text
바로가기 → nalmeok.cmd
  → PYTHONUTF8=1, PYTHONIOENCODING=utf-8
  → (인자 없음) cd /d %USERPROFILE%
  → runtime\python.exe nalmeok.py %*
  → cli()
     → bootstrap_platform()       # Windows: VT 활성화, stdio 재설정, 도구 탐색, 모드 결정
     → main() → interactive() → 기존 TUI
  → 종료 코드 != 0 이면 pause
```

### 4.3 플랫폼 계층 (PR1에서 `nalmeok.py` 안에 섹션으로 추가)

별도 모듈로 분리하지 않는다. 단일 파일 배포(`py-modules = ["nalmeok"]`)를 유지하기 위함이다. 400줄을 넘으면 그때 분리를 검토한다.

필수 함수/개념:

- `can_raw_input()`: POSIX TTY(termios) 또는 Windows TTY(msvcrt)
- `can_render_full()`: TTY + VT/ANSI 사용 가능
- `enable_vt()`: Windows `SetConsoleMode(+ENABLE_VIRTUAL_TERMINAL_PROCESSING)`
- `read_key()`: POSIX 분기 / Windows 분기
- `read_secret()`: POSIX raw / Windows getwch / 비-TTY getpass
- `key_ready()`: POSIX `select` / Windows `msvcrt.kbhit()`
- `cbreak_keys()`: POSIX termios / Windows no-op
- `wait_key()`: 화면 대기용. raw 불가면 `input()`
- `configure_stdio()`: `reconfigure(encoding="utf-8", errors="replace")`
- `find_tool(name)`: ffmpeg/ffprobe 탐색
- `file_lock(handle, blocking)`: POSIX fcntl / Windows msvcrt

### 4.4 모드 결정과 폴백

| 조건 | 동작 |
|---|---|
| TTY + 전체 렌더 가능 | 기존 TUI 그대로 |
| TTY + VT 실패 | 명확한 오류: 지원 OS/터미널 확인, Windows Terminal 사용 안내. `nalmeok --help` 안내 |
| 비-TTY(파이프/IDE) | 기존과 동일하게 오류 + 서브커맨드 안내 |
| conhost (`WT_SESSION` 없음) | 전체 TUI + **ASCII 심볼 폴백** |
| `NALMEOK_ASCII=1` | 강제 ASCII 심볼 |
| `NALMEOK_DEBUG=1` | `%APPDATA%\nalmeok\nalmeok.log`에 진단 로그 |

ASCII 폴백 대상(최소): 스피너 `⠋...` → `|/-\`, 커서 표시 `❯` → `>`. 상자/불릿/체크는 KS X 1001/TrueType 폴백으로 렌더되는지 수동 확인 후 필요 시 추가한다.

### 4.5 입력 계약 (Windows)

`msvcrt.getwch()` 기준 매핑:

| 키 | POSIX | Windows |
|---|---|---|
| ↑ | `ESC [ A` / `ESC O A` | `\x00 H` / `\xe0 H` |
| ↓ | `ESC [ B` / `ESC O B` | `\x00 P` / `\xe0 P` |
| ← | `ESC [ D` / `ESC O D` | `\x00 K` / `\xe0 K` |
| → | `ESC [ C` / `ESC O C` | `\x00 M` / `\xe0 M` |
| Enter | `\r` / `\n` | `\r` |
| Esc | `\x1b` | `\x1b` |
| Ctrl+C | `\x03` | `\x03` 또는 `KeyboardInterrupt` |
| Backspace | `\x7f` / `\b` | `\b` |

- `getwch()`는 에코가 없으므로 Windows에서 termios raw 모드가 필요 없다.
- Ctrl+C는 처리된 입력이 켜져 있으면 `KeyboardInterrupt`, 꺼져 있으면 `\x03`으로 온다. 둘 다 처리한다.
- 붙여넣기는 Windows Terminal/conhost가 키 이벤트로 전달한다. API 키 입력은 ASCII이므로 `read_secret`에서 문제없다.
- 화면 코드의 `read_key()` 호출은 그대로 둔다. 플랫폼 분기는 `read_key()` 내부에서만 한다.

### 4.6 인코딩 계약

1. 모든 파일 I/O는 `encoding="utf-8"` 명시. JSON은 `ensure_ascii=False`.
2. 모든 `subprocess` 텍스트 모드는 `encoding="utf-8", errors="replace"`.
   - 대상: `probe_duration`, ffmpeg 진행 파이프, 오디오 분할, 클립보드(교체 후), 새로 추가하는 호출 전부.
3. 자식 프로세스 환경에 `PYTHONUTF8=1`, `PYTHONIOENCODING=utf-8` 전달. 특히 백그라운드 `_wait`.
4. 시작 시 `sys.stdout`/`sys.stderr`를 UTF-8 + `errors="replace"`로 재설정. 콘솔에서는 Python의 Unicode console API를 그대로 쓴다. `chcp`를 호출하지 않는다.
5. ANSI 출력은 `can_render_full()`일 때만. `live_status`, 스피너, progress bar도 동일.
6. Windows 클립보드는 ctypes `CF_UNICODETEXT`. 64비트에서 `GlobalAlloc`/`GlobalLock`의 `restype`을 `c_void_p`로 지정한다.

### 4.7 도구 탐색 계약

`find_tool("ffmpeg")` / `find_tool("ffprobe")` 우선순위:

1. `NALMEOK_FFMPEG` 환경변수(파일 경로 또는 디렉터리)
2. 앱 폴더: `Path(__file__).resolve().parent / "ffmpeg.exe"` (Windows), `ffmpeg` (POSIX)
3. `shutil.which("ffmpeg")`

없을 때 오류 메시지에 다음을 포함한다: "Windows는 ZIP에 포함된 ffmpeg를 사용합니다. 설치 폴더의 ffmpeg.exe를 지우지 마세요." / "macOS: brew install ffmpeg" / "Ubuntu: sudo apt install ffmpeg".

`probe_duration`, `convert`, 오디오 분할, `capabilities()`의 모든 `shutil.which` 호출을 `find_tool`로 교체한다.

### 4.8 잠금과 원자적 쓰기

- `job_lock`, `config_lock`은 `file_lock(handle, blocking)`을 쓴다.
  - POSIX: `fcntl.flock(LOCK_EX [| LOCK_NB])`
  - Windows: `msvcrt.locking(fd, LK_LOCK | LK_NBLCK, 1)` (offset 0, `seek(0)` 필요)
  - 실패 시 기존과 같은 사용자 메시지로 `fail()`.
- `atomic_write_text`의 `os.replace`는 `PermissionError`에 한해 5회, 50ms 간격 재시도. AV/인덱서가 파일을 잡는 Windows 특성 대응.
- `chmod(0600)`은 POSIX에서만 의미. Windows는 `%APPDATA%` 기본 ACL에 의존한다고 문서화.

### 4.9 경로 계약

- 파일 경로는 항상 `Path`. 문자열 결합·`os.path` 금지.
- `clean_path`:
  - 공통: 앞뒤 공백 제거, 양끝 따옴표 제거, `expanduser()`
  - POSIX에서만 `"\\ "` → `" "` 치환
  - Windows에서는 역슬래시를 보존
- `shlex.split(editor)`는 `posix=(os.name != "nt")`. Windows에서 남는 따옴표는 제거.
- `short_path`는 `Path.home()` 기준 상대 경로를 `os.sep`로 표시. (`~/Documents\...` 혼합 방지)
- 드라이브 루트는 `current.parent == current`로 판정 (기존 로직 유지).
- MAX_PATH 초과 경로는 이번 범위 밖. 필요 시 별도 이슈.

### 4.10 백그라운드 감시

- 명령: `[sys.executable, str(Path(__file__).resolve()), "_wait", str(folder), ...]`
  - 배포물에서 `sys.executable`은 `runtime\python.exe`, `__file__`은 실제 소스 경로이므로 기존 코드 그대로 동작한다.
- `creationflags=DETACHED_PROCESS` (기존), `stdin=DEVNULL` (기존).
- env에 `PYTHONUTF8=1`, `PYTHONIOENCODING=utf-8` 추가.
- 로그 파일은 기존대로 UTF-8.
- 중단은 `os.kill(pid, SIGTERM)` → Windows에서는 `TerminateProcess`. 정상 종료 협상은 하지 않는다고 문서화.

### 4.11 오류 UX와 `doctor`

- `nalmeok doctor`:
  - OS/빌드, 터미널(`WT_SESSION`, `TERM_PROGRAM`, conhost), VT 활성화 여부, 콘솔 attached/stdin isatty, stdout encoding, isatty
  - Python/실행 파일/앱 폴더, ffmpeg/ffprobe 경로와 버전, SDK 설치 여부
  - 설정 경로, cwd, 마지막 오류 요약
  - 키/SDK 없이 동작해야 한다 (`ensure_sdk` 호출 전 경로)
- `NALMEOK_DEBUG=1`이면 `%APPDATA%\nalmeok\nalmeok.log`에 `log()` 출력을 tee.
- 처리되지 않은 예외 시 화면에 "진단 정보: `nalmeok doctor`"와 로그 경로를 표시.
- `nalmeok.cmd`는 종료 코드가 0이 아니면 `pause`로 창을 유지.

### 4.12 Windows 폴더 스캔 제한

Windows는 `%USERPROFILE%` 아래 AppData/OneDrive가 커서 전체 재귀가 분 단위로 걸릴 수 있습니다. 다음 제한을 둡니다 (D14).

- `job_folders`: 대시보드는 depth 2, 방문 2000개. CLI `stats` 기본은 방문 20000개.
- `scan_descendant_media`: Windows 0.03초, depth 3, 방문 150개. POSIX는 0.1초, depth 8, 1000개.
- 폴더 탐색: 상세 계산 100개, 하위 재귀 20개까지만. 나머지는 스캔 없이 "폴더"로 표시.
- `AppData`, `OneDrive`, `node_modules`, `.git`, `.venv`, `Program Files` 등은 하위 스캔에서 건너뜀.
- 깊은 곳의 작업은 "최근 작업" 목록이나 해당 폴더에서 직접 실행해 찾습니다.

---

## 5. 작업 계획

각 PR은 앞의 PR이 병합된 뒤 시작한다. 범위를 넘는 수정은 별도 이슈로 분리한다.

### PR 1 · Windows 입력/터미널 계층 (P0)

**목표:** Windows에서 TUI가 첫 화면부터 동작한다.

**변경 파일:** `nalmeok.py`, `tests/test_nalmeok.py`

**작업**

- [x] `_enable_windows_vt()`, `configure_stdio()`, `can_raw_input()`, `vt_supported()` 추가
- [x] `read_key()` Windows 분기 (`msvcrt.getwch`, `\x00`/`\xe0` 매핑)
- [x] `read_secret()` Windows 분기, 비-TTY는 `getpass` 유지
- [x] `_key_ready()` 추가, `_wait_for_key()`를 플랫폼 중립으로 변경
- [x] `cbreak_keys()` Windows no-op 분기
- [x] `RAW_KEYS` 가드를 `can_raw_input()`/`require_tui()`로 교체
- [x] `select_menu`, `select_many`, `home_screen`의 "macOS·Linux 전용" `fail()` 제거
- [x] `message_screen` 등 `read_key()` 직접 호출부가 비-TTY에서 NameError 없이 동작하도록 `wait_key()`로 교체
- [x] `_color_enabled`, `clear_screen`, `fullscreen`, `live_status`에 ANSI 가능 여부 반영
- [x] conhost ASCII 심볼 폴백 (스피너, 커서 표시, 진행 막대)
- [x] `bootstrap_platform()`을 `cli()` 시작에 연결
- [x] 테스트: Windows 키 매핑 순수 함수, 비-TTY 경로, ASCII 폴백, 가드 회귀

**수용 기준**

- Windows 10 22H2 conhost와 Windows Terminal에서: 첫 실행 키 등록, ↑↓, Enter, Esc, Backspace, Ctrl+C, 한글 경로 붙여넣기, 다중 선택이 동작.
- macOS/Linux 기존 테스트 99개 통과 유지. (현재 104개)
- Windows에서 `nalmeok` 실행 시 "macOS·Linux 전용" 오류가 나오지 않음. (Windows 11 SSH/ConPTY에서 확인)

**범위 밖:** 런타임 ZIP, cmd 런처, line-mode TUI, ffmpeg 번들.

### PR 2 · Windows 정합성 (P1)

**목표:** 파일·프로세스·도구 계층이 Windows에서 안전하게 동작한다.

**변경 파일:** `nalmeok.py`, `tests/test_nalmeok.py`

**작업**

- [x] `subprocess` 텍스트 모드 4곳 + 신규 호출에 `encoding="utf-8", errors="replace"`
- [x] `find_tool()` 도입, `probe_duration`/`convert`/분할/`capabilities` 교체
- [x] `file_lock()` 도입, `job_lock`/`config_lock` 교체 (Windows `msvcrt.locking`)
- [x] `atomic_write_text`의 `os.replace` 재시도
- [x] `copy_to_clipboard` Windows를 ctypes `CF_UNICODETEXT`로 교체
- [x] `clean_path` Windows 분기, `shlex.split(posix=...)`, `short_path` 표시 수정
- [x] 백그라운드 `_wait` env에 UTF-8 설정 추가
- [x] `doctor` 서브커맨드 + `NALMEOK_DEBUG` 로그
- [x] 테스트: `find_tool` 우선순위, `clean_path` Windows/ POSIX 케이스, 원자적 쓰기 재시도, doctor 출력 형식

**수용 기준**

- 한글 경로의 MP4 변환/분할/전사가 동작.
- 결과 경로 복사가 한글을 보존.
- 같은 폴더 동시 작업 시 잠금 메시지가 Windows에서도 나옴.
- `nalmeok doctor`가 키/SDK 없이 실행.

**범위 밖:** Windows 알림, line-mode TUI.

### PR 3 · 배포 (P1)

**목표:** Python 없는 Windows PC에서 ZIP만으로 첫 전사까지.

**변경/추가 파일**

- `scripts/build_windows.py` (또는 `.ps1`)
- `.github/workflows/windows.yml`
- `nalmeok.cmd`, `installer.cmd` (템플릿은 `scripts/`에 두고 빌드 시 ZIP 루트로 복사)
- `README.md` Windows 절
- `.gitattributes` (`*.cmd text eol=crlf`)
- `licenses/` 생성 로직

**작업**

- [ ] python-build-standalone 3.12.x x86_64 다운로드·압축 해제 → `runtime/`
- [ ] `uv pip install --python runtime/python.exe`로 `google-genai`와 전이 의존성 설치 (버전 고정)
- [ ] LGPL ffmpeg/ffprobe 고정 버전 다운로드 + SHA256 검증 + 라이선스 복사
- [ ] `nalmeok.py`, `README.md`, `LICENSE`, 아이콘 복사
- [ ] ZIP 루트에 파일 배치, `SHA256SUMS.txt` 생성
- [ ] `nalmeok.cmd` / `installer.cmd` 작성 (CRLF)
- [ ] GitHub Actions: Windows 빌드 + `runtime\python.exe -m unittest` + ZIP 아티팩트
- [ ] README: 차단 해제 → 압축 풀기 → `installer.cmd` → 바로가기 → 첫 실행, 문제 해결
- [ ] 수동 검증 결과를 이슈 #1에 기록

**`nalmeok.cmd` 계약**

```bat
@echo off
title nalmeok
setlocal
set "HERE=%~dp0"
set "PYTHONUTF8=1"
set "PYTHONIOENCODING=utf-8"
if "%~1"=="" cd /d "%USERPROFILE%"
"%HERE%runtime\python.exe" "%HERE%nalmeok.py" %*
if errorlevel 1 pause
```

**`installer.cmd` 계약**

- `%~dp0` → `%LOCALAPPDATA%\Programs\nalmeok`로 `robocopy /E` (삭제 없이 덮어쓰기)
- 바탕화면 + 시작 메뉴에 `nalmeok.lnk`
  - Target: `%DEST%\nalmeok.cmd`
  - WorkingDirectory: `%USERPROFILE%`
  - Icon: `%DEST%\nalmeok.ico`
- 관리자 권한, 레지스트리, PATH 변경 없음
- 제거 안내: 폴더와 바로가기 삭제
- 오류 시 `pause`, 성공 시 "바탕화면의 nalmeok 바로가기로 실행하세요"

**수용 기준**

- Python·uv·Git이 없는 Windows 10 22H2에서 ZIP만으로 설치·첫 실행.
- SmartScreen 차단 해제 절차가 README에 있고 실제로 경고 없이 실행됨.
- 바로가기 더블클릭 → 콘솔 → TUI → 키 등록 → 폴더 선택 → 전사 시작.
- 오류 시 창이 닫히지 않고 진단 안내가 보임.

**범위 밖:** 코드서명, 자동 업데이트, 제거 프로그램(선택 사항).

---

## 6. 테스트와 검증

### 6.1 단위 테스트

- Windows 키 매핑: `\x00H`, `\xe0P`, `\r`, `\x1b`, `\x03`, `\b`
- `can_raw_input`/`can_render_full` 조합별 동작
- 비-TTY에서 `wait_key()`가 `input()`으로 대체
- `find_tool` 우선순위 (`NALMEOK_FFMPEG` → 앱 폴더 → PATH)
- `clean_path` 케이스: `"C:\Users\홍길동\강의"`, `'C:\path with space'`, POSIX escape
- `atomic_write_text`의 `PermissionError` 재시도 (monkeypatch)
- `doctor` 출력에 필수 필드 포함
- 기존 99개 유지

### 6.2 CI

- `ubuntu-latest`, `macos-latest`, `windows-latest` × Python 3.10/3.12: `unittest`
- `windows-latest`: ZIP 빌드, `runtime\python.exe -m unittest`, `nalmeok.cmd --version` smoke
- CI는 대화형 콘솔을 검증하지 못한다. **수동 검증이 최종 게이트**다.

### 6.3 수동 검증 매트릭스

> 2026-10-30: **Windows 10 22H2 검증은 사용자 결정으로 보류** (D13, best-effort). Windows 11 Windows Terminal은 환경(WT_SESSION/VT/isatty) 확인 완료, TUI 체크리스트만 남았습니다.
> Windows 10 열은 재개 시 사용한다.

각 환경에서 이슈 #1에 결과를 기록한다.

| 항목 | Win10 22H2 conhost | Win10 22H2 WT | Win11 WT |
|---|---|---|---|
| ZIP 차단 해제/압축 풀기 | | | |
| `installer.cmd` + 바로가기 | | | |
| 첫 실행 키 등록 | | | |
| ↑↓/Enter/Esc/Backspace/Ctrl+C | | | |
| 한글 경로 붙여넣기 | | | |
| 다중 선택(폴더) | | | |
| 창 크기 변경 | | | |
| 화면 클릭 후 복귀(QuickEdit) | | | |
| MP4 → M4A 변환 | | | |
| 무료 live 전사 | | | |
| 백그라운드 대기/복귀 | | | |
| 결과 경로 복사(한글) | | | |
| `nalmeok doctor` | | | |

기록 항목: Windows 빌드(`winver`), 터미널/버전, 실행 경로, 결과, 스크린샷, 오류 로그.

### 6.4 이슈 재현 정보 템플릿

- Windows 버전/빌드
- 터미널: Windows Terminal / conhost / 기타 + 버전
- 실행 방법: 바로가기 / `nalmeok.cmd` / `runtime\python.exe nalmeok.py`
- `nalmeok doctor` 출력
- `%APPDATA%\nalmeok\nalmeok.log` (있는 경우)
- 스크린샷

---

## 7. 릴리스 절차

1. `__version__`과 `pyproject.toml` 버전을 올린다.
2. Windows CI 빌드를 실행한다.
3. ZIP을 풀어 **깨끗한 Windows 10 22H2**에서 수동 매트릭스를 통과시킨다.
4. `SHA256SUMS.txt`를 생성한다.
5. GitHub Release에 ZIP, SHA256, 변경 사항, "차단 해제" 안내를 올린다.
6. README의 Windows 절과 다운로드 링크를 갱신한다.
7. 이 문서의 상태 표와 변경 이력을 갱신한다.

---

## 8. 리스크와 대안

### 8.1 왜 PyInstaller가 아닌가

| 항목 | PyInstaller onedir | 런타임 포함 ZIP |
|---|---|---|
| hidden imports | google-genai/pydantic 대응 필요 | 불필요 |
| onefile | 실행마다 재압축 해제, `_wait` 재실행 비용 | 해당 없음 |
| frozen 분기 | `ensure_sdk`, `restart`, `_wait` 경로 수정 필요 | 불필요 |
| 디버깅 | 스택트레이스/소스 접근이 어려움 | 소스 그대로 실행 가능 |
| AV 오탐 | 상대적으로 높음 | 낮음 |
| 크기 | 비슷 | 비슷 |

### 8.2 왜 코드서명하지 않는가

- Azure Artifact Signing(구 Trusted Signing)은 개인 기준 미국/캐나다만 가능.
- OV는 연 $150~300, EV는 연 $400~600. 2024년부터 EV도 SmartScreen 즉시 통과 특례가 없음.
- 따라서 지금은 "차단 해제 + SHA256 + 문서"로 대응하고, 배포 규모가 커지면 OV/winget/Store를 재검토한다.

### 8.3 왜 시스템 Python을 쓰지 않는가

- 이슈 완료 기준이 "Python·uv·Git이 없는 깨끗한 Windows"다.
- Windows의 `python.exe` Store 별칭은 Microsoft Store를 열 수 있어 신뢰할 수 없다.

### 8.4 왜 웹 UI가 아닌가

- TUI 자산을 버리고 UI/폴더 선택/서버 수명 관리를 새로 만들어야 한다.
- Windows TUI 복구와 설치 경로가 먼저다. 수요가 검증되면 별도 v2로 검토한다.

### 8.5 리스크 표

| 리스크 | 영향 | 완화 |
|---|---|---|
| conhost VT/대체 화면 quirks | 화면 깨짐 | 지원 하한 22H2, 수동 게이트, Windows Terminal 안내 |
| conhost 폰트 두부 | 심볼 안 보임 | 스피너/커서 ASCII 폴백, `NALMEOK_ASCII=1` |
| SmartScreen 경고 | 설치 이탈 | ZIP 차단 해제 안내, SHA256, 스크린샷 |
| AV가 python.exe 오탐 | 실행 실패 | 공식 standalone 빌드, 체크섬, PyInstaller 미사용 |
| ffmpeg LGPL 고지 누락 | 라이선스 위반 | `licenses/`에 고지/출처, 별도 프로세스 호출 |
| ZIP 용량 | 다운로드 부담 | 실측 164.7MiB(runtime+ffmpeg+의존성). v1은 번들 유지, 계속 문제면 온디맨드 전환 |
| Python 보안 업데이트 | 취약점 | 분기별 또는 CVE 시 런타임 재빌드 |
| `cd`로 CLI 상대 경로 깨짐 | CLI 회귀 | 인자 없을 때만 `cd` (D10) |
| QuickEdit로 화면 정지 | 앱 멈춘 것처럼 보임 | README 안내, Windows Terminal 권장 |
| MAX_PATH 초과 | 파일 작업 실패 | 범위 밖, 별도 이슈 |
| 컨텍스트 유실 | 방향 이탈 | 이 문서 + 상태 표 + `CLAUDE.md` 포인터 |

---

## 9. 열린 질문

| # | 질문 | 기본값 | 상태 |
|---|---|---|---|
| Q1 | 바로가기 표시 이름 | `nalmeok` (ASCII) | 확정 |
| Q2 | 제거 프로그램(`uninstaller.cmd`) 포함 | 미포함, 수동 삭제 안내 | 보류 |
| Q3 | ffmpeg 번들 vs 온디맨드 | 번들 유지 (D5). 실측 ZIP 164.7MiB | 확정, 용량 민감 시 재검토 |
| Q4 | Windows 10 최소 빌드 | 22H2 (D6) | 확정. 실기 검증은 보류 (D13) |
| Q5 | Windows 알림 | 후순위, 터미널 벨 유지 | 보류 |
| Q6 | line-mode TUI | 범위 밖 (D9) | 보류 |
| Q7 | 실기 검증 담당 | Windows 11은 사용자 직접, Windows 10은 보류 | 확정 |
| Q8 | 한 줄 `install.ps1`을 주 경로로 할지 | ZIP + `installer.cmd` 유지, `install.ps1`은 보조 후보 | 논의 |

---

## 10. 변경 이력

| 날짜 | 변경 | 작성 |
|---|---|---|
| 2026-10-30 | 최초 작성. 이슈 #1 분석, 배포/호환성 결정, PR 분해 | Claude Code |
| 2026-10-30 | PR1 입력/터미널 계층 구현. Windows 11 SSH/ConPTY에서 TUI 렌더링·키 입력 검증 | Claude Code |
| 2026-10-30 | PR2 인코딩·도구·잠금·클립보드·doctor 구현. Windows 11에서 108 tests pass | Claude Code |
| 2026-10-30 | PR3 런타임 ZIP·cmd 설치 스크립트·CI·README 추가. Windows 11에서 빌드/설치/번들 실행 E2E 검증 (ZIP 164.7MiB) | Claude Code |
| 2026-10-30 | Windows 10 22H2 실기 검증 보류(D13). Windows 11 Windows Terminal은 사용자 직접 진행으로 상태 정리 | Claude Code |
| 2026-10-30 | 후속: Windows 타이틀/바로가기 ASCII `nalmeok`, 폴더 스캔 제한(D14). Windows 111 tests pass, 대시보드 0.013s | Claude Code |
| 2026-10-30 | Windows 11 conhost 환경 검증(VT/ANSI·isatty). Windows Terminal은 WT_SESSION 부재로 재실행 필요 | Claude Code |
| 2026-10-30 | Windows 11 Windows Terminal 환경 검증(WT_SESSION/WT_PROFILE_ID·VT/ANSI). TUI 체크리스트 대기 | Claude Code |
| 2026-10-30 | 후속: 메뉴/홈 화면 diff 렌더링으로 방향키 깜빡임 제거(D15). Windows 115 tests pass | Claude Code |
| 2026-10-30 | 후속: 긴 메뉴 뷰포트 추가. 40개 폴더 탐색 clear_screen 11→1, Windows 115 tests pass | Claude Code |

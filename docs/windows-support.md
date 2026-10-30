# Windows 지원 및 배포

> **상태:** 운영 중 (v0.6.2 기준) · **관련 이슈:** [#1](https://github.com/jseook11/nalmeok/issues/1)
> 이 문서가 Windows 지원·배포의 단일 기준입니다. 코드와 어긋나면 코드를 고치기 전에 이 문서를 먼저 갱신하세요.

---

## 0. 이 문서를 먼저 읽는 사람에게

1. **상태(0.1)** 와 **불변 조건(0.3)**, **하지 말 것(0.4)** 을 확인합니다.
2. 동작을 바꾸려면 **결정 로그(2장)** 에 해당 항목이 있는지 봅니다. 없으면 결정을 먼저 추가합니다.
3. 작업을 마치면 **상태 표와 변경 이력(10장)** 을 갱신합니다.

### 0.1 상태

| 작업 | 상태 | 검증 |
|---|---|---|
| Windows 입력/터미널 계층, 정합성, 배포 (PR1–3) | 완료 | Windows 11 SSH/ConPTY에서 TUI·한글 클립보드·잠금·doctor 확인 |
| thin ZIP 배포 (v0.6.1) | 완료 | 첫 실행 PBS·SDK, MP4 첫 사용 ffmpeg, `update --check`/apply/restore |
| 설치 오류·ZIP 구조 정리 (v0.6.2) | 완료 | ko-KR Windows 11에서 설치·바로가기·드롭 안내·`--version` 확인, 오류 없음 |
| Windows 11 Windows Terminal TUI 체크리스트 | 진행 중 | 환경(WT_SESSION·VT·isatty) 확인 완료. 화면 체크리스트·스크린샷 대기 |
| Windows 10 22H2 실기 검증 | 보류 | 2026-10-30 사용자 결정(D13). best-effort로 표기 |
| v0.6.2 릴리스(패키지 분리) | 완료 | [v0.6.2](https://github.com/jseook11/nalmeok/releases/tag/v0.6.2) thin ZIP·`nalmeok-app-win64.zip`·manifest |

상태 값: `미착수` / `진행 중` / `완료` / `보류`

### 0.2 기준선

- 앱 버전: **0.6.2** (`nalmeok/constants.py`, `pyproject.toml`)
- 테스트: `python -m unittest discover -s tests -q` → **158개 통과, 8개 skip** (macOS, Python 3.12)
- 배포물: thin `nalmeok-<v>-win64.zip` (**약 195KB**) + `windows-manifest.json` + `SHA256SUMS.txt`
- 런타임: python-build-standalone 3.12.16 (x86_64, 내려받기 약 45MB), google-genai 2.31.0, ffmpeg 8.1.3 온디맨드(약 160MB)
- 실기 검증: Windows 11 ko-KR(build 26200), conhost·Windows Terminal 환경 확인
- 지원 하한: Windows 10 22H2(19045) / Windows 11, x64. 그 이하·ARM64·32비트는 best-effort 또는 미지원

### 0.3 불변 조건

1. **TUI 화면 구조를 새로 만들지 않는다.** 화면/문구는 그대로 두고 입력·터미널 계층을 바꾼다. 깜빡임 방지용 diff 렌더링(D15)은 화면 구조 변경이 아니다.
2. **PyInstaller/frozen exe를 만들지 않는다.** 배포물은 "얇은 ZIP + 첫 실행 부트스트랩"이다.
3. **`.cmd` 파일은 순수 ASCII다.** 한국어 문구는 UTF-8 BOM이 있는 `.ps1`이 소유한다(D19). 빌드와 테스트가 위반을 거부한다.
4. **Windows 지원을 실기 검증 없이 "지원됨"으로 표기하지 않는다.** README의 지원 범위와 검증 상태가 일치해야 한다.
5. **지원 하한은 Windows 10 22H2 / Windows 11, x64.** 22H2의 실기 검증은 보류(D13)이므로 문서·README에는 best-effort로 적는다. 그 이하는 best-effort.
6. **macOS/Linux 동작과 기존 테스트를 깨지 않는다.**

> 2026-10-30 폐기(Windows 한정): "Windows 배포물을 코드서명한다"(D4는 배포 편의 결정일 뿐 개발 제약이 아님), "새 런타임 의존성을 추가하지 않는다", "앱 코드는 단일 `nalmeok.py`". **macOS는 서명·공증이 필수**다(`docs/macos-app.md`).

### 0.4 하지 말 것

- `subprocess(..., text=True)` 단독 사용. 항상 `encoding="utf-8", errors="replace"`를 함께 준다.
- Windows 클립보드에 `clip.exe` 사용. ctypes `CF_UNICODETEXT`를 쓴다.
- 화면 코드에서 `termios`/`tty` 직접 참조. 플랫폼 계층 밖으로 새지 않게 한다.
- Windows 경로에 POSIX 규칙의 `shlex.split()` 사용.
- VT 지원을 확인하지 않고 ANSI 시퀀스 출력. 화면 코드는 `require_tui()`를 통과한 뒤에만 그린다.
- `os.chmod(0o600)`이 Windows에서 권한을 보호한다고 가정.
- `.cmd`(배치)에 한국어·비ASCII 문자. cmd.exe가 UTF-8 줄을 중간부터 실행해 `'더의' is not recognized` 같은 오류가 난다(D19).
- 경로를 문자열 결합으로 다루기. `pathlib.Path`를 쓴다. `winreg` 조회값의 `os.path.expandvars`처럼 문자열 API 경계는 예외.

---

## 1. 목표와 범위

### 1.1 목표

- Windows 10/11에서 기존 TUI가 **첫 화면부터 정상 동작**한다.
- Python·uv·Git이 없는 PC에서 **압축 해제 → `installer.cmd` → 바로가기 → 첫 전사**로 도달한다.
- 실패 시 사용자가 이슈에 붙일 수 있는 **진단 정보(`nalmeok doctor`)** 를 제공한다.

### 1.2 비목표

- GUI/웹 UI, Electron/Tauri 래퍼, 32비트/ARM64 공식 지원
- Microsoft Store/winget 배포, 코드서명(D4 — SmartScreen 확인창 한 번은 감수)
- 전체 line-mode TUI 대체 화면(D9), `fcntl` 수준의 완전한 POSIX 파일 잠금 semantics

---

## 2. 결정 로그

| ID | 결정 | 이유 | 결과 |
|---|---|---|---|
| D1 | TUI 유지, 입력/터미널 계층만 수정 | 화면 재작성은 범위·유지보수 부담이 큼 | PR1이 300~500줄로 제한됨 |
| D2 | PyInstaller 대신 PBS 런타임 + 소스 + cmd | hidden imports/재압축/frozen/AV 오탐 회피, 디버깅 쉬움 | `sys.frozen` 분기 불필요 |
| D3 | 배포 파일명 `nalmeok.cmd`, `installer.cmd` | ASCII 파일명은 경로/AV/압축 도구 호환성이 좋음 | 타이틀·바로가기도 ASCII `nalmeok` |
| D4 | 코드서명 안 함 | Azure Artifact Signing은 한국 개인 불가, OV/EV 비용 대비 이득 없음 | **개발 제약 아님.** SmartScreen 확인창 1회는 감수 |
| D5 | ffmpeg/ffprobe 번들 | 학생 파일 대부분이 MP4, PATH/winget 안내는 이탈 지점 | v0.6.0까지 유지 → D16/D17로 대체 |
| D6 | 지원 하한 Win10 22H2 / 11, x64 | 남은 Win10의 사실상 표준, conhost VT 보장이 쉬움 | 1809~21H2는 best-effort |
| D7 | conhost와 Windows Terminal 모두 지원 | Win10은 WT가 기본이 아님 | VT 활성화 + conhost 심볼 폴백 |
| D8 | ZIP 최상위 폴더 없이 루트에 파일 배치 | Explorer 이중 중첩 방지 | **D18로 대체** |
| D9 | 전체 line-mode TUI는 범위 밖 | VT 실패는 지원 하한에서 드묾 | VT 실패 시 명확한 오류 + WT 안내 |
| D10 | `nalmeok.cmd`는 인자가 없을 때만 `cd /d "%USERPROFILE%"` | CLI 상대 경로를 깨지 않기 위함 | 더블클릭은 홈에서, `estimate .\x`는 cwd 유지 |
| D11 | ffmpeg 탐색은 `find_tool()`로 통일 | 번들 파일은 PATH에 없음 | `NALMEOK_FFMPEG` → 앱 폴더 → PATH |
| D12 | `doctor` 서브커맨드 | Windows 실기 없이 원격 진단 | 키/SDK 없이 동작 |
| D13 | Win10 22H2 실기 검증 보류 | Hyper-V·Win10 설치 비용 대비 API 안정성. 2026-10-30 사용자 결정 | README/문서에 best-effort 표기 |
| D14 | Windows 폴더 스캔에 깊이·개수 제한 | `%USERPROFILE%` 전체 재귀가 분 단위로 걸림 | 4.12의 수치 |
| D15 | 메뉴·홈 화면 diff 렌더링 | 방향키마다 전체 출력하면 WT에서 깜빡임 | 첫 프레임만 전체, 이후 변경 줄만 갱신 |
| **D16** | **thin ZIP이 표준 배포** | 165MB ZIP은 업데이트마다 전체 재다운로드 유발 | `nalmeok-<v>-win64.zip`(~194KB) + 첫 실행 다운로드 |
| **D17** | **오프라인 번들은 선택 확장판** | 인터넷 없거나 단일 파일로 고정해야 하는 환경 | `-offline.zip`(~165MB). 릴리스 기본은 thin |
| **D18** | **ZIP 루트에는 `installer.cmd`만, 나머지는 `app\`** | 파일이 많으면 일반 사용자가 installer를 못 찾음 | 설치 결과는 평평한 `%LOCALAPPDATA%\Programs\nalmeok` |
| **D19** | **`.cmd`는 순수 ASCII, 한국어는 UTF-8 BOM PowerShell** | cmd.exe가 `chcp 65001` 뒤 UTF-8 배치 줄을 중간부터 실행(`'더의' is not recognized`) | `install.ps1`·`messages.ps1`이 문구 소유, 빌드가 비ASCII `.cmd` 거부 |

---

## 3. 구조 요약

앱 코드는 **`nalmeok/` 패키지**다(2026-10-31 분리). 배포 경로는 `python -m nalmeok` 또는 얇은 런처를 쓴다. 모듈 경계는 `docs/development.md` 참고.

역사적 진단(v0.4.0 이전, 이슈 #1에 전체 기록):

```text
import termios 실패 → RAW_KEYS = False → select_menu/home_screen의 fail()
  → cli()가 RuntimeError를 잡고 종료 (키가 이미 등록돼 있어도 동일)
```

| 증상 | 원인 | 현재 |
|---|---|---|
| 첫 화면 즉시 종료 | `termios` ImportError → 가드 | `can_raw_input()`/`require_tui()` |
| 키 입력 불가 | `termios` 직접 사용 | `read_key()` 내부 플랫폼 분기 |
| 화면 깨짐 | VT 미활성 | `_enable_windows_vt()`, conhost ASCII 폴백 |
| 리다이렉트 크래시 | 로캘 인코딩 | `configure_stdio()`, `errors="replace"` |
| 잠금 no-op | `fcntl` 없음 | `file_lock()` (msvcrt) |
| 한글 경로 복사 깨짐 | `clip.exe` 코드페이지 | ctypes `CF_UNICODETEXT` |
| ffmpeg 못 찾음 | 번들 파일이 PATH에 없음 | `find_tool()` |
| 오류 창 즉시 닫힘 | 더블클릭 실행 | 런처의 `pause` |

---

## 4. 아키텍처

### 4.1 배포물 구조

```text
nalmeok-<version>-win64.zip   (~194KB)
├─ installer.cmd            # ZIP 루트에서 유일하게 보이는 파일 (순수 ASCII)
└─ app/
   ├─ nalmeok.cmd           # 실행 런처 (ASCII)
   ├─ nalmeok-drop.cmd      # 드래그앤드롭 런처 (ASCII)
   ├─ install.ps1           # 설치 로직·한국어 안내 (UTF-8 BOM)
   ├─ messages.ps1          # 런처 안내 문구 (UTF-8 BOM)
   ├─ bootstrap.ps1         # PBS 다운로드·SHA-256·압축 해제
   ├─ bootstrap.env         # 고정 URL/SHA
   ├─ shortcuts.ps1         # 바로가기 2개
   ├─ nalmeok/               # 앱 패키지 (constants, runtime, keys, update,
   │                         #  term, media, engine, screens, cli)
   ├─ app.py  pyproject.toml
   ├─ nalmeok.ico  README.md  LICENSE
   └─ licenses/THIRD-PARTY.md
```

설치 후:

```text
%LOCALAPPDATA%\Programs\nalmeok\     # 앱 (평평한 구조, ~200KB)
%LOCALAPPDATA%\nalmeok\
  app\nalmeok\                       # 실행·업데이트 대상 패키지
  app\.venv\                         # google-genai
  runtime\<version>\python.exe       # PBS
  runtime\current.txt                # 현재 버전
  bin\ffmpeg.exe, ffprobe.exe        # MP4 첫 사용 시
```

**오프라인 확장판**(D17)은 `runtime/`과 ffmpeg를 포함한다. `nalmeok.cmd`가 `%HERE%runtime\python.exe`를 찾으면 offline 모드로 동작한다.

### 4.2 실행 흐름

```text
바로가기 → nalmeok.cmd
  → offline: %HERE%runtime\python.exe 로 바로 실행
  → thin:  %DATA%\app\nalmeok\ 로 복사(seed)
           runtime\current.txt 확인 → 없으면 bootstrap.ps1 (PBS, SHA-256)
  → NALMEOK_HOME, NALMEOK_PACKAGED=1, NALMEOK_YES=1, NALMEOK_FFMPEG=...
  → PYTHONPATH=%DATA%\app, python -m nalmeok %*
  → cli() → bootstrap_platform() → configure_stdio() + VT 준비
  → TUI. 첫 전사 때 get_client() → ensure_sdk()가 app\.venv에 SDK 설치
     → 설치 직후 .venv 인터프리터로 restart()
  → MP4 변환이 필요하면 ensure_ffmpeg()가 manifest로 ffmpeg 설치
  → 종료 코드 != 0 이면 pause
```

`ensure_sdk()`는 `main()`이 아니라 **첫 API 클라이언트 생성 시점**에 호출된다. 그래서 `--version`, `doctor`, `convert`는 SDK 없이 동작한다.

### 4.3 플랫폼 계층

플랫폼 계층은 `nalmeok/term.py`와 `nalmeok/runtime.py`에 있다. 필수 함수:

- `can_raw_input()`: POSIX TTY(termios) 또는 Windows TTY(msvcrt)
- `vt_supported()` / `_enable_windows_vt()`: Windows `SetConsoleMode(+ENABLE_VIRTUAL_TERMINAL_PROCESSING)`
- `read_key()`: POSIX 분기 / Windows 분기 (`msvcrt.getwch`), `_key_ready()`
- `read_secret()`: POSIX raw / Windows getwch / 비-TTY getpass
- `cbreak_keys()`, `wait_key()`, `unicode_ok()` (심볼 폴백 판정)
- `configure_stdio()`: `reconfigure(encoding="utf-8", errors="replace")`
- `find_tool(name)`, `_lock_file(handle, blocking)`

### 4.4 모드 결정과 폴백

| 조건 | 동작 |
|---|---|
| TTY + 전체 렌더 가능 | 기존 TUI 그대로 |
| TTY + VT 실패 | 명확한 오류: 지원 OS/터미널 확인, Windows Terminal 안내 |
| 비-TTY(파이프/IDE) | 기존과 동일하게 오류 + 서브커맨드 안내 |
| `WT_SESSION` 없음(conhost, ConPTY/SSH 포함) | 전체 TUI + **ASCII 심볼 폴백**(`❯`→`>`, 스피너 `|/-\`) |
| `NALMEOK_ASCII=1` / `NALMEOK_UNICODE=1` | 심볼 폴백 강제 / 유니코드 강제 |
| `NALMEOK_DEBUG=1` | `%APPDATA%\nalmeok\nalmeok.log`에 진단 로그 |

심볼 폴백은 `WT_SESSION` 유무로만 판단한다. ConPTY·SSH 세션은 VT가 동작해도 `WT_SESSION`이 없어 ASCII 심볼을 쓴다(기능 손실 없음).

### 4.5 입력 계약 (Windows)

`msvcrt.getwch()` 기준:

| 키 | POSIX | Windows |
|---|---|---|
| ↑/↓/←/→ | `ESC [ A/B/D/C` 또는 `ESC O …` | `\x00`/`\xe0` + `H/P/K/M` |
| Enter | `\r` / `\n` | `\r` |
| Esc | `\x1b` | `\x1b` |
| Ctrl+C | `\x03` | `\x03` 또는 `KeyboardInterrupt` |
| Backspace | `\x7f` / `\b` | `\b` |

- `getwch()`는 에코가 없어 raw 모드가 필요 없다.
- Ctrl+C는 처리된 입력 여부에 따라 `KeyboardInterrupt` 또는 `\x03`으로 온다. 둘 다 처리한다.
- 화면 코드는 `read_key()`만 호출하고, 플랫폼 분기는 그 안에서만 한다.

### 4.6 인코딩 계약

1. 모든 파일 I/O는 `encoding="utf-8"`, JSON은 `ensure_ascii=False`.
2. 모든 `subprocess` 텍스트 모드는 `encoding="utf-8", errors="replace"`.
3. 자식 프로세스 env에 `PYTHONUTF8=1`, `PYTHONIOENCODING=utf-8`.
4. 시작 시 `sys.stdout`/`sys.stderr`를 UTF-8 + `errors="replace"`로 재설정. `chcp`를 호출하지 않는다.
5. ANSI 출력은 `vt_supported()`일 때만.
6. Windows 클립보드는 ctypes `CF_UNICODETEXT`(64비트에서 `restype = c_void_p`).

### 4.7 도구 탐색

`find_tool("ffmpeg")` 우선순위: `NALMEOK_FFMPEG`(파일 또는 디렉터리) → 앱 폴더 → `shutil.which`.
없을 때: `ensure_ffmpeg()`가 `NALMEOK_PACKAGED=1`에서 manifest의 고정 URL·SHA로 `NALMEOK_HOME/bin`에 받는다(첫 MP4 변환 시). 그래도 없으면 오류 메시지가 설치 폴더의 `ffmpeg.exe`와 `NALMEOK_FFMPEG`, `brew install ffmpeg`를 안내한다.

### 4.8 잠금과 원자적 쓰기

- `job_lock`, `config_lock` → `_lock_file()`: POSIX `fcntl.flock`, Windows `msvcrt.locking`(offset 0).
- `atomic_write_text`의 `os.replace`는 `PermissionError`에 5회 재시도(대기 50ms×시도 횟수, AV/인덱서 대응).
- `chmod(0600)`은 POSIX에서만 의미. Windows는 `%APPDATA%` ACL에 의존.

### 4.9 경로 계약

- 경로는 항상 `Path`.
- `clean_path`: 앞뒤 공백·따옴표 제거, `expanduser()`. POSIX에서만 `"\ "` → `" "`. Windows는 역슬래시 보존.
- `shlex.split(editor)`는 `posix=(os.name != "nt")`.
- 드라이브 루트는 `current.parent == current`로 판정. MAX_PATH 초과는 범위 밖.

### 4.10 백그라운드 감시

- `[sys.executable, __file__, "_wait", folder, ...]`, `DETACHED_PROCESS`, `stdin=DEVNULL`.
- env에 UTF-8 설정 추가. 로그는 UTF-8.
- 중단은 `TerminateProcess`. 정상 종료 협상은 하지 않는다.

### 4.11 오류 UX와 `doctor`

- `doctor`는 버전, OS, 터미널 이름(`WT_SESSION`/`TERM_PROGRAM`/conhost), VT, 콘솔 attached·isatty, stdout isatty·encoding,
  Python, 실행 파일, 앱 폴더, ffmpeg/ffprobe 경로, SDK 설치 여부, API 키 요약, 설정 파일, 로그 경로, 작업 폴더를 출력한다. 키/SDK 없이 동작.
- `NALMEOK_DEBUG=1`이면 `log()`가 `%APPDATA%\nalmeok\nalmeok.log`에도 기록된다.
- `nalmeok.cmd`는 종료 코드가 0이 아니면 `pause`. 처리되지 않은 예외는 `cli()`가 `오류:`로 출력하고 종료 코드를 남긴다.

### 4.12 폴더 스캔 제한 (D14)

- `job_folders`: 대시보드·CLI `stats`·사용량 화면 모두 depth 2/2000개(Windows), depth 5/20000개(POSIX).
  v0.6.2까지 `stats`와 사용량 화면은 제한 없이(최대 200,000개) 훑어 `$HOME`에서 15초 넘게 걸렸다.
- `scan_descendant_media`: Windows 0.03초·depth 3·150개, POSIX 0.1초·depth 8·1000개.
- 폴더 탐색: 상세 100개, 하위 20개. 나머지는 스캔 없이 "폴더"로 표시.
- `AppData`, `OneDrive`, `node_modules`, `.git`, `.venv`, `Program Files` 등은 건너뜀.

### 4.13 업데이트와 manifest

- thin 런처는 `NALMEOK_PACKAGED=1`, `NALMEOK_HOME=%LOCALAPPDATA%\nalmeok`, `NALMEOK_FFMPEG=%NALMEOK_HOME%\bin`을 설정한다.
- `nalmeok update`는 `releases/latest/download/manifest.json`을 읽는다. manifest는 하나이고 플랫폼별 핀은 `platforms.windows` / `platforms.macos` 섹션에 있다.
  - `nalmeok-app.zip`(패키지, 플랫폼 공통) SHA-256 검증 → `.nalmeok-next`에 풀어 컴파일 검증 → 디렉터리 교체(다음 실행부터 적용, 실패 시 기존 유지)
  - SDK 버전이 manifest보다 낮을 때만 `.venv` pip 갱신
  - `NALMEOK_MANIFEST_URL`로 다른 manifest를 지정할 수 있다(테스트·미러용)
- ffmpeg/ffprobe가 없고 MP4 변환이 필요하면 `platforms.windows.ffmpeg`의 고정 URL·SHA로 `%NALMEOK_HOME%\bin`에 받는다. `NALMEOK_FFMPEG_YES=1`은 자동화용.
- `scripts/build_windows.py`가 thin ZIP을 만들고, `scripts/release_common.py`로 공통 payload와 manifest의 windows 섹션을 갱신한다.
- offline 확장판은 `%HERE%runtime`을 쓰고, update는 설치 폴더의 `nalmeok/` 패키지를 교체한다.
- 릴리스 자산: `nalmeok-<v>-win64.zip`, (선택) `-offline.zip`, `nalmeok-app.zip`, `manifest.json`, `SHA256SUMS.txt`.

---

## 5. 런처·설치 계약

### 5.1 `nalmeok.cmd` (순수 ASCII)

- offline이면 `%HERE%runtime\python.exe`.
- thin이면 `%DATA%\app\nalmeok\` 패키지로 seed하고 `runtime\current.txt`를 읽는다. 없으면 `bootstrap.ps1` 실행.
- `PYTHONPATH=%DATA%\app`(offline은 `%HERE%`)로 두고 `python -m nalmeok`을 실행한다.
- 인자가 없을 때만 `cd /d "%USERPROFILE%"`(D10).
- 런타임 오류 안내는 `messages.ps1 -Id install-failed|python-missing`.
- 종료 코드가 0이 아니면 `pause`.

### 5.2 `installer.cmd` / `install.ps1`

- ZIP 루트의 유일한 파일. 순수 ASCII 런처가 `app\install.ps1`(UTF-8 BOM)을 호출한다.
- `app\*` → `%LOCALAPPDATA%\Programs\nalmeok`로 복사(삭제 없이 덮어쓰기). 설치 폴더는 평평하다.
- 바탕화면 + 시작 메뉴에 `nalmeok.lnk`, `nalmeok에 끌어놓기.lnk`
  - Target: `%DEST%\nalmeok.cmd`, `%DEST%\nalmeok-drop.cmd`
  - WorkingDirectory: `%USERPROFILE%`, Icon: `%DEST%\nalmeok.ico`
- 바로가기 파일명은 ASCII 임시 이름으로 저장한 뒤 `Move-Item`으로 변경한다. WScript.Shell은 시스템 ANSI 코드페이지 밖의 파일명(영문 Windows의 한글)으로 `.lnk`를 저장하지 못한다.
- 관리자 권한, 레지스트리, PATH 변경 없음. 제거는 폴더·바로가기 삭제.

---

## 6. 테스트와 검증

### 6.1 단위 테스트

- Windows 키 매핑, `can_raw_input`/`vt_supported` 조합, 비-TTY `wait_key()` 폴백
- `find_tool` 우선순위, `clean_path` 케이스, `atomic_write_text` 재시도, `doctor` 출력
- `.cmd` 순수 ASCII, `.ps1` UTF-8 BOM, `installer.cmd` → `app\install.ps1` 연결
- 현재: **157 pass, 8 skip** (macOS 기준. Windows CI는 3.10/3.12에서 같은 스위트 실행)

### 6.2 CI

- `windows-latest` / `ubuntu-latest` / `macos-latest` × Python 3.10/3.12: `unittest`
- `windows-latest` 빌드 잡:
  - thin ZIP 빌드 → 압축 해제
  - 루트에 `installer.cmd` + `app\`만 있는지, `app\` 필수 파일이 모두 있는지 확인
  - `python -m nalmeok --version`(PYTHONPATH=app), `app\nalmeok.cmd --version`(첫 실행 PBS 부트스트랩), `app\nalmeok-drop.cmd --help`
  - `installer.cmd` 실행 → `Programs\nalmeok\nalmeok.cmd` 생성 확인, 출력에 `is not recognized`가 없어야 함
  - ZIP·`nalmeok-app.zip`·`manifest.json`·`SHA256SUMS.txt` 아티팩트 업로드
- offline 확장판은 기본 CI에서 빌드하지 않는다: `python scripts/build_windows.py --offline`

### 6.3 수동 검증 매트릭스

> Windows 10 22H2는 보류(D13). Windows 11 Windows Terminal은 환경 확인 완료, TUI 체크리스트만 남음.

| 항목 | Win10 22H2 | Win11 conhost | Win11 WT |
|---|---|---|---|
| ZIP 차단 해제/압축 풀기 | | | |
| `installer.cmd` + 바로가기 | | | |
| 설치 콘솔에 `is not recognized` 없음 | | | ✓ |
| 첫 실행 PBS(~45MB)/SDK(~20MB) | | | ✓ |
| 첫 MP4 변환 시 ffmpeg(~160MB) | | | |
| `nalmeok update --check` | | | ✓ |
| 첫 실행 키 등록 · ↑↓/Enter/Esc/Ctrl+C | | | |
| 한글 경로 붙여넣기 · 다중 선택 | | | |
| 창 크기 변경 · QuickEdit 후 복귀 | | | |
| MP4 변환 · live 전사 · 백그라운드 대기 | | | |
| 결과 경로 복사(한글) · `doctor` | | | ✓ |

기록 항목: 빌드(`winver`), 터미널/버전, 실행 경로, 결과, 스크린샷, 오류 로그.

### 6.4 이슈 재현 정보 템플릿

Windows 버전/빌드 · 터미널(WT/conhost + 버전) · 실행 방법 · `nalmeok doctor` 출력 ·
`%APPDATA%\nalmeok\nalmeok.log` · 스크린샷.

---

## 7. 릴리스 절차

1. `__version__`과 `pyproject.toml` 버전을 올린다.
2. 테스트를 통과시킨다.
3. `python scripts/build_windows.py`로 thin ZIP을 만든다(필요 시 `--offline`).
4. ZIP을 풀어 **깨끗한 Windows 11**에서 첫 실행(PBS/SDK/ffmpeg)·업데이트를 확인한다.
5. `SHA256SUMS.txt`와 `windows-manifest.json`을 확인한다.
6. GitHub Release에 thin ZIP, (선택) offline ZIP, `nalmeok-app.zip`, `manifest.json`, `SHA256SUMS.txt`를 올린다.
7. README의 Windows 절과 다운로드 링크를 갱신한다.
8. 이 문서의 상태 표와 변경 이력을 갱신한다.

---

## 8. 리스크

| 리스크 | 영향 | 완화 |
|---|---|---|
| conhost VT/대체 화면 quirks | 화면 깨짐 | 지원 하한 22H2, 수동 게이트, WT 안내 |
| conhost 폰트 두부 | 심볼 안 보임 | ASCII 폴백, `NALMEOK_ASCII=1` |
| SmartScreen 경고 | 설치 이탈 | ZIP 차단 해제 안내, SHA256 |
| AV가 python.exe 오탐 | 실행 실패 | 공식 standalone 빌드, 체크섬, PyInstaller 미사용 |
| ffmpeg LGPL 고지 누락 | 라이선스 위반 | `licenses/`에 고지·출처, 별도 프로세스 호출 |
| 첫 실행 다운로드 실패 | 사용 불가 | 재시도, 오프라인 확장판(D17) |
| Python 보안 업데이트 | 취약점 | 분기별 또는 CVE 시 런타임 재빌드 |
| `cd`로 CLI 상대 경로 깨짐 | CLI 회귀 | 인자 없을 때만 `cd`(D10) |
| QuickEdit로 화면 정지 | 멈춘 것처럼 보임 | README 안내, WT 권장 |
| MAX_PATH 초과 | 파일 작업 실패 | 범위 밖, 별도 이슈 |
| 한글 `.lnk` 저장 실패 | 바로가기 없음 | ASCII 임시 이름 + `Move-Item` |

---

## 9. 열린 질문

| # | 질문 | 기본값 | 상태 |
|---|---|---|---|
| Q1 | 제거 프로그램(`uninstaller.cmd`) | 미포함, 수동 삭제 안내 | 보류 |
| Q2 | Windows 알림 | 후순위, 터미널 벨 유지 | 보류 |
| Q3 | Windows Terminal TUI 체크리스트 | 사용자 직접 확인 | 진행 중 |
| Q4 | 코드서명 도입 | 하지 않음(D4). 필요하면 배포 편의로 재검토 | 확정 |

---

## 10. 변경 이력

| 날짜 | 변경 | 작성 |
|---|---|---|
| 2026-10-30 | 최초 작성. 이슈 #1 분석, 배포/호환성 결정, PR 분해 | Claude Code |
| 2026-10-30 | PR1 입력/터미널 계층, PR2 정합성, PR3 런타임 ZIP·설치 스크립트 구현 | Claude Code |
| 2026-10-30 | Windows 폴더 스캔 제한(D14), 메뉴/홈 diff 렌더링(D15), 드롭 기능 | Claude Code |
| 2026-10-30 | v0.6.0 릴리스(macOS notarized DMG + Windows ZIP) | Claude Code |
| 2026-10-30 | thin 배포 결정(D16/D17). v0.6.1 릴리스(thin ZIP 193KB, bootstrap·update 검증) | Claude Code |
| 2026-10-30 | **v0.6.2**: `'더의' is not recognized` 수정. `.cmd` 순수 ASCII(D19), ZIP 루트 `installer.cmd`만(D18). Windows 11 실기 검증 | Claude Code |
| 2026-10-30 | 문서 정리: 오래된 기준선·중복 제거, 기준선 0.6.2/157 tests로 갱신, 코드서명·의존성 금지·단일 파일 제약 폐기 | Claude Code |
| 2026-10-31 | **패키지 분리**: 단일 `nalmeok.py` → `nalmeok/` 패키지. 런처는 `-m nalmeok`, 업데이트는 공통 `nalmeok-app.zip` 디렉터리 교체 | Claude Code |
| 2026-10-31 | **v0.6.2 릴리스**: thin ZIP 199KB + 공통 `nalmeok-app.zip` + `manifest.json`. Windows 11 실기·CI 통과 | Claude Code |
| 2026-10-31 | 사용량 화면·CLI `stats`의 `job_folders` 무제한 스캔(최대 200,000개)을 D14 제한으로 통일. `$HOME` 기준 15초 → 1초 미만 | Claude Code |

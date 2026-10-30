# macOS 앱 패키징 계획

> **상태:** 계획 (2026-10-30)
> **기준:** `master` `0d6c07a`, v0.5.0
> **목표:** macOS 비개발자가 DMG 하나로 설치하고, Python·brew·git 없이 TUI를 쓰며, 업데이트 때 전체를 다시 받지 않도록 합니다.
> 이 문서가 단일 기준입니다.

## 0. 이 문서를 먼저 읽는 사람에게

1. **결정 로그(2장)** 와 **불변 조건(0.3)** 을 확인합니다.
2. **상태 표(0.1)** 에서 마지막으로 끝난 작업과 다음 작업을 확인합니다.
3. 해당 마일스톤의 **작업 범위(5장)** 만 구현합니다. 범위 밖 개선은 하지 않습니다.
4. 작업을 마치면 **상태 표와 변경 이력(10장)** 을 갱신합니다.

### 0.1 상태 표

| 작업 | 상태 | PR | 검증 |
|---|---|---|---|
| M0 · 계획 문서 | 완료 | - | 이 문서 |
| M1 · 앱 번들·런처·부트스트랩 | 미착수 | - | - |
| M2 · DMG·서명·공증·staple | 미착수 | - | - |
| M3 · 컴포넌트 업데이트·ffmpeg 온디맨드 | 미착수 | - | - |
| M4 · CI·테스트·문서 | 미착수 | - | - |
| 후속 · Finder 아이콘 드롭 | 보류 (v1.1) | - | - |
| 후속 · Intel x86_64 | 보류 | - | - |
| 후속 · 앱 자동 업데이트(Sparkle 등) | 보류 | - | - |

상태 값: `미착수` / `진행 중` / `완료` / `보류`

### 0.2 기준선

- 커밋: `0d6c07a` (v0.5.0, 드래그앤드롭 포함)
- 테스트: **134개 통과, 8개 skip** (macOS), Windows 134개 통과 (4 skip)
- 앱 버전: `0.5.0` → 목표 `0.6.0`
- 대상: macOS 12 Monterey 이상, **Apple Silicon(arm64) 우선**
- 빌드 도구: Xcode Command Line Tools(`swiftc`), `hdiutil`, `codesign`, `notarytool`
- 배포 계정: Apple Developer Program(Developer ID Application)

### 0.3 불변 조건 (바꾸려면 이 문서를 먼저 고친다)

1. **Homebrew를 설치하지 않는다.** 이미 있으면 감지만 하고, 설치·업그레이드는 하지 않는다.
2. **시스템 Python을 실행하거나 수정하지 않는다.** 특히 `/usr/bin/python3` 스텁을 실행하면 CLT 설치 GUI가 뜨므로 건드리지 않는다.
3. 런타임에 **`sudo`를 요구하지 않는다.**
4. 사용자 상태는 모두 `~/Library/Application Support/nalmeok/` 아래에 둔다. 앱 번들과 `/Applications`에는 쓰지 않는다.
5. 다운로드한 산출물은 **SHA-256 검증 전에는 실행하지 않는다.**
6. 업데이트는 같은 경로에서 **원자적으로** 교체하고, 이전 버전 하나만 남긴다. 앱·폴더를 복제하지 않는다.
7. 릴리스는 **서명 + 공증 + staple**된 DMG로만 배포한다.
8. 앱 코드는 계속 단일 `nalmeok.py`를 쓴다. TUI를 Swift/네이티브로 재작성하지 않는다.
9. 사용자 인터페이스는 터미널(TUI)이다. 별도 GUI를 만들지 않는다.

### 0.4 하지 말 것 (Do-not list)

- Python·google-genai·ffmpeg를 **DMG 앱 안에 번들하지 않는다.** (v1은 얇은 앱)
- Homebrew를 자동 설치하지 않는다.
- PyInstaller/Nuitka로 재작성하지 않는다.
- 맨 `.sh`/`.command`를 배포하지 않는다. (공증 불가, Gatekeeper 경고)
- AppleScript로 Terminal을 제어하지 않는다. (Automation 권한 프롬프트)
- `/usr/bin/python3`를 실행해 버전을 확인하지 않는다.
- 업데이트를 검증 없이 적용하지 않는다.
- 앱 번들 안에 `.venv`나 로그를 만들지 않는다.

## 1. 목표와 범위

### 1.1 목표

- 비개발자가 **DMG 다운로드 → Applications로 드래그 → 더블클릭**으로 시작한다.
- Python, brew, git, CLT를 요구하지 않는다.
- 첫 실행에서 필요한 런타임만 내려받고, 이후 업데이트는 **바뀐 컴포넌트만** 받는다.
- 터미널에 폴더/MP4/M4A를 끌어다 놓으면 바로 전사한다. (v0.5.0 기능 재사용)
- 서명·공증으로 Gatekeeper 경고 없이 실행된다.
- Apple Silicon에서 동작을 검증한다.

### 1.2 비목표 (이번 작업에서 하지 않음)

- Intel(x86_64) DMG
- Mac App Store 배포
- Finder 아이콘에 직접 드롭 (v1.1)
- 앱 번들 자동 업데이트 (Sparkle 등)
- 네이티브 GUI 재작성
- Windows 배포 방식 변경
- 백그라운드 데몬/메뉴바 앱

## 2. 결정 로그

| ID | 결정 | 이유 | 결과 |
|---|---|---|---|
| MD1 | **얇은 `.app` + DMG** 배포 | 설치 경험은 Mac 표준, PKG는 관리자 권한·마법사가 불필요하게 추가됨 | `nalmeok.app` + DMG |
| MD2 | 페이로드는 **첫 실행에 내려받음** | 업데이트마다 100MB 재다운로드·폴더 복제를 피함 | 앱 1~2MB, 첫 실행 ~50MB |
| MD3 | **Homebrew 설치 금지** | sudo·CLT GUI·시스템 변경·버전 드리프트 | 이미 있으면 ffmpeg만 감지 |
| MD4 | Python은 **PBS 기본**, 기존 3.10+는 검증 후 선택 | 재현성과 사용자 환경 존중의 절충 | `runtime/`에 PBS, 실패 시 폴백 |
| MD5 | 앱 코드는 **Application Support/app**, venv는 그 옆 | 번들 쓰기 권한 문제 회피, 기존 `ensure_sdk` 재사용 | `app/nalmeok.py`, `app/.venv` |
| MD6 | Swift 런처가 **Terminal을 열어 실행** | TUI에 TTY 필요, Apple Events 권한 회피 | `open -a Terminal` + `.command` |
| MD7 | **컴포넌트 단위 업데이트** + 원자적 교체 | 코드 변경은 수십 KB, 의존성은 드묾 | manifest 기반 |
| MD8 | 무결성은 **HTTPS + SHA-256** | pip와 동등한 신뢰 모델, 구현 단순 | manifest에 해시 고정 |
| MD9 | ffmpeg/ffprobe는 **온디맨드 정적 빌드** | M4A만 쓰는 사용자에게 80MB를 강요하지 않음 | `bin/ffmpeg`, `bin/ffprobe` |
| MD10 | v1은 **arm64만** | 사용자 대부분, 빌드·검증 단순화 | `nalmeok-<v>-arm64.dmg` |
| MD11 | 최소 **macOS 12 Monterey** | Swift 런타임 내장, PBS 지원 | `LSMinimumSystemVersion 12.0` |
| MD12 | **Developer ID + notarytool + staple** | Gatekeeper 경고 제거 | DMG 공증 |
| MD13 | 앱 껍데기 업데이트는 **수동 DMG** | 껍데기는 거의 안 바뀜, Sparkle은 후속 | v1.1 검토 |
| MD14 | 파일 입력은 **터미널 드롭** | v0.5.0에서 검증, Finder 드롭은 후속 | 브래킷 붙여넣기 |
| MD15 | 목표 버전 **0.6.0** | 기능 추가 | 머지 후 범프 |
| MD16 | 번들 ID `com.jseook11.nalmeok` | 역DNS, 서명·공증 식별자 | Info.plist |

## 3. 현재 상태 진단

### 3.1 macOS 온보딩 현황

- 실행: `git clone` → `uv run app.py` 또는 `./app.py`
- Python 3.10+ 직접 준비
- ffmpeg: `brew install ffmpeg`
- `google-genai`는 첫 실행 시 `.venv` 생성 후 자동 설치 (`ensure_sdk`)
- API 키는 TUI에서 등록
- 터미널 드롭은 v0.5.0에서 동작
- `.app`/DMG/서명 없음

### 3.2 macOS 내장 Python의 현실

| macOS | 상태 |
|---|---|
| 10.15 ~ 12.2 | Python 2.7 |
| 12.3+ | 2.7 제거 |
| 12.3+ | `/usr/bin/python3`는 **CLT 설치를 안내하는 스텁** |
| CLT 설치 후 | Python 3.9.6 (요구사항 3.10 미만) |

결론: 내장 Python은 사용하지 않는다.

### 3.3 재사용할 것

- `ensure_sdk()`: SDK가 없으면 `.venv`를 만들고 설치한 뒤 그 인터프리터로 재실행
- `find_tool()`: `NALMEOK_FFMPEG` → 앱 폴더 → PATH 순서
- 터미널 드롭(브래킷 붙여넣기), `nalmeok drop`, 문서 폴더 복사
- `nalmeok doctor`, 설정·키 관리, TUI 전체

### 3.4 새로 만들 것

- Swift 런처와 `.app` 번들
- 부트스트랩/업데이트 스크립트
- 빌드·서명·공증 스크립트와 CI

## 4. 아키텍처

### 4.1 배포물 구조

```text
nalmeok.app/
  Contents/
    Info.plist
    MacOS/nalmeok              # Swift 런처 (arm64, < 1MB)
    Resources/
      nalmeok.py               # 최초 실행 시 seed (이후 Application Support 사용)
      run.command              # TUI 실행
      bootstrap.command        # Python 다운로드·설치 후 run.command로 인계
      nalmeok.icns
```

DMG 구성:

```text
nalmeok.app
Applications -> /Applications   # 드래그 설치용 심볼릭 링크
```

### 4.2 사용자 데이터 구조

```text
~/Library/Application Support/nalmeok/
  app/
    nalmeok.py                 # 실제 실행 코드 (업데이트 대상)
    .venv/                     # google-genai (ensure_sdk가 생성)
  runtime/
    3.12.16/                   # PBS 설치본
    current -> 3.12.16          # 원자적 전환
  bin/
    ffmpeg                     # 온디맨드
  manifest.json                # 마지막으로 확인한 버전
  logs/
```

### 4.3 첫 실행 흐름

1. 사용자가 앱을 더블클릭한다.
2. Swift 런처가 `~/Library/Application Support/nalmeok/`를 만든다.
3. `app/nalmeok.py`가 없으면 번들의 seed를 복사한다.
4. 사용 가능한 Python 3.10+를 찾는다.
   - `runtime/current/bin/python3` → 검증
   - `python.org`, pyenv, `/opt/homebrew`, `/usr/local` 경로만 확인
   - `/usr/bin/python3`는 **확인하지 않는다**
5. 없으면 Terminal을 열어 `bootstrap.command`를 실행한다.
   - PBS tarball 다운로드 (~30MB), SHA-256 검증
   - 임시 디렉터리에 압축 해제 후 `runtime/<version>`으로 원자적 이동
   - `current` 심볼릭 링크 전환
   - 완료되면 `run.command`로 인계
6. Terminal에서 `run.command`가 `python app/nalmeok.py`를 실행한다.
7. `ensure_sdk()`가 `.venv`를 만들고 `google-genai`를 설치한다. (기존 흐름)
8. TUI가 열리고 API 키를 등록한다.

첫 실행 화면 예:

```text
nalmeok을 처음 실행합니다.
Python 3.12 준비 중...      30MB
google-genai 설치 중...     20MB
(ffmpeg는 MP4 변환을 처음 할 때 설치합니다)
```

### 4.4 일반 실행 흐름

1. Swift 런처가 데이터 디렉터리와 seed를 확인한다.
2. Python 경로를 확인한다. (없으면 bootstrap으로)
3. `open -a Terminal run.command`로 TUI를 연다.
4. 사용자가 폴더/MP4/M4A를 Terminal 창에 끌어다 놓는다.
5. v0.5.0 드롭 흐름으로 전사한다.

### 4.5 업데이트 흐름

manifest 예:

```json
{
  "app_version": "0.6.1",
  "nalmeok": {"version": "0.6.1", "url": "...", "sha256": "..."},
  "sdk": {"name": "google-genai", "version": "2.31.0"},
  "python": {"version": "3.12.16", "url": "...", "sha256": "..."},
  "ffmpeg": {"version": "8.1.3", "url": "...", "sha256": "..."}
}
```

1. `nalmeok update`(또는 홈 메뉴)가 manifest를 받는다.
2. `nalmeok.py` 해시가 다르면 **그 파일만** 받아 `app/nalmeok.py.tmp` → `os.replace`.
3. `sdk` 버전이 다르면 `.venv`에서 `pip install -U google-genai==<version>`.
4. `python`/`ffmpeg` 핀이 다르면 새 버전을 받아 검증 후 `current`를 전환하고, 이전 버전은 하나만 남긴다.
5. 실행 중인 프로세스는 이전 코드로 계속 동작하고, 다음 실행부터 새 코드가 적용된다.

원칙:

- 부분 실패 시 기존 버전을 유지한다.
- 다운로드 파일은 `.tmp`에 받고 검증 후 교체한다.
- 앱·폴더를 복제하지 않는다.

### 4.6 Python 선택 전략

우선순위:

1. `runtime/current/bin/python3` (PBS)
2. python.org 프레임워크 설치
3. pyenv shim
4. `/opt/homebrew/bin/python3`, `/usr/local/bin/python3`

검증:

- `sys.version_info >= (3, 10)`
- `-m venv` 생성 가능
- arm64 (Rosetta x86_64는 제외)
- 실행 시간 2초 제한

검증 실패 또는 없음 → PBS 다운로드.

Homebrew Python을 쓰더라도 venv가 Homebrew 업그레이드로 깨질 수 있으므로, `ensure_sdk`가 venv 재생성으로 복구하도록 보강한다.

### 4.7 SDK 설치 재사용

`ensure_sdk()`는 이미 다음을 한다.

- SDK import 확인
- `app/.venv`의 인터프리터 확인
- 없으면 `python -m venv .venv` 후 `pip install google-genai`
- 설치 후 `.venv` 인터프리터로 재실행

패키징에서 필요한 변경:

- **venv 경로를 cwd가 아니라 `Path(__file__).parent / ".venv"`로 명시**한다. 현재 `python -m venv .venv`는 cwd 기준이라 패키징 환경에서 `$HOME/.venv`를 만들 수 있다.
- `NALMEOK_PACKAGED=1`이면 `uv sync`를 쓰지 않고 해당 venv의 pip로 설치한다. 앱 데이터 디렉터리에는 `pyproject.toml`이 없기 때문이다.
- 패키징 모드에서는 `NALMEOK_YES=1`로 자동 설치하고, 소스 실행에서는 기존 대화형 확인을 유지한다.
- Homebrew Python을 쓰는 경우 업그레이드로 venv가 깨질 수 있으므로, venv 인터프리터 검증에 실패하면 `.venv`를 재생성한다.

### 4.8 ffmpeg/ffprobe 온디맨드

- `run.command`가 `NALMEOK_FFMPEG="$DATA/bin"`을 설정한다. `find_tool()`은 이 디렉터리에서 `ffmpeg`/`ffprobe`를 찾고, 없으면 PATH로 폴백한다.
- MP4 변환이 처음 필요할 때:
  1. "ffmpeg가 필요합니다. 지금 받을까요? (약 40~80MB)"
  2. 고정 URL에서 다운로드, SHA-256 검증, `chmod +x`
  3. `bin/ffmpeg`, `bin/ffprobe`에 배치하고 계속 진행
- 실패하면 기존 brew 안내를 보여주되, 비개발자에게는 재시도 버튼 역할의 Enter 안내를 제공한다.

### 4.9 Terminal 실행

- Swift 런처는 `NSWorkspace.shared.open(URL(fileURLWithPath: commandPath))`로 `.command`를 연다.
- LaunchServices가 Terminal을 선택한다. Apple Events/Automation 권한이 필요 없다.
- `Info.plist`에 `LSUIElement=true`를 두어 Dock 아이콘 없이 실행한다.
- 창 제목은 `nalmeok`으로 고정한다.
- `run.command`는 `NALMEOK_HOME`, `NALMEOK_PACKAGED=1`, `NALMEOK_FFMPEG="$DATA/bin"`을 설정하고, `cd "$HOME"` 후 Python을 exec한다. (현재 폴더 하위 스캔이 `/`에서 시작하지 않도록)
- 종료 코드가 0이 아니면 창을 유지하고 안내를 출력한다.

### 4.10 Finder 아이콘 드롭 (v1.1)

- Swift 런처에 `application(_:open:)`을 추가한다.
- 드롭된 URL을 임시 `.command`에 안전하게 인용해 넣고 `drop` 인자로 전달한다.
- v1에서는 터미널 드롭으로 충분하므로 보류한다.

### 4.11 서명·공증

빌드 순서:

1. `swiftc`로 런처 컴파일
2. 앱 번들 구성, `assets/nalmeok.ico` → `.icns` 변환
3. `codesign --force --options runtime --timestamp --sign "$IDENTITY" nalmeok.app`
4. `hdiutil create`로 DMG 생성
5. `codesign --sign "$IDENTITY" nalmeok-<v>-arm64.dmg`
6. `xcrun notarytool submit --wait`
7. `xcrun stapler staple`
8. `spctl -a -vv`, `stapler validate`로 확인

CI에서는 서명 인증서와 공증 자격이 있을 때만 3~8을 수행하고, 없으면 unsigned 앱만 빌드한다.

### 4.12 보안 모델

- 앱 번들: Developer ID 서명 + 공증.
- 다운로드 산출물: HTTPS + SHA-256 고정. 검증 실패 시 실행하지 않는다.
- manifest: GitHub Releases의 HTTPS URL. v1.1에서 Ed25519 서명을 추가해 앱에 내장한 공개키로 검증한다.
- 앱이 직접 파일을 쓰므로 quarantine은 붙지 않는다. 그래도 해시 검증을 생략하지 않는다.
- 업데이트 중에는 잠금 파일을 사용하고, 전사 중에는 업데이트를 미룬다.

## 5. 작업 계획

### M1 · 앱 번들·런처·부트스트랩

범위:

- `macos/launcher.swift`, `Info.plist`, `run.command`, `bootstrap.command`
- 데이터 디렉터리/seed/ Python 탐색
- PBS 다운로드·검증·설치
- `open -a Terminal` 실행
- `ensure_sdk`의 venv 경로·패키징 모드 수정

완료 기준:

- Python이 있는 Mac에서 더블클릭 → TUI
- Python이 없는 Mac에서 더블클릭 → PBS 설치 → TUI
- `sudo` 요구 없음, `~/Library/Application Support/nalmeok`만 사용
- `/usr/bin/python3`를 실행하지 않음

### M2 · DMG·서명·공증

범위:

- `scripts/build_macos.py` 또는 `scripts/build_macos.sh`
- `.icns` 생성, DMG 레이아웃
- 서명·공증·staple, `SHA256SUMS.txt`
- Developer ID/공증 자격 환경변수 문서화

완료 기준:

- 깨끗한 Mac에서 DMG 열기 → 앱 실행 → Gatekeeper 경고 없음
- `spctl -a -vv` accepted, `stapler validate` 통과
- 앱 번들 1~2MB (Resources 포함)

### M3 · 업데이트·ffmpeg

범위:

- manifest 포맷과 `nalmeok update`
- `nalmeok.py` 원자적 교체, SDK 버전 비교, 이전 버전 정리
- ffmpeg 온디맨드 다운로드
- 실패 시 롤백

완료 기준:

- 코드만 바뀐 릴리스에서 수십 KB만 다운로드
- 손상된 해시 → 기존 버전 유지
- ffmpeg 없는 상태에서 MP4 변환 시 안내·다운로드·변환 성공

### M4 · CI·테스트·문서

범위:

- macOS 러너에서 테스트 + unsigned 앱 빌드
- 서명·공증은 secrets가 있을 때만
- README macOS 설치 안내
- `docs/macos-app.md` 갱신

완료 기준:

- CI green
- 새 Mac에서 수동 체크리스트 통과

## 6. 테스트와 검증

### 6.1 단위 테스트

- manifest 파싱·버전 비교
- SHA-256 검증 성공/실패
- Python 후보 탐색 (`/usr/bin/python3` 제외)
- 앱 코드 원자적 교체
- ffmpeg 경로 탐색

### 6.2 빌드 검증

```bash
codesign --verify --deep --strict --verbose=2 nalmeok.app
spctl -a -vv --type execute nalmeok.app
xcrun stapler validate nalmeok-<v>-arm64.dmg
```

### 6.3 수동 매트릭스

| 환경 | 확인 |
|---|---|
| macOS 12/13/14/15 arm64, CLT·brew·Python 없음 | 첫 실행, PBS 다운로드, TUI |
| python.org 3.12 설치됨 | PBS 다운로드 생략 |
| pyenv/Homebrew Python 3.10+ | 검증 후 사용 또는 폴백 |
| 네트워크 없음 | 명확한 오류, 재시도 |
| 한글·공백 경로 드롭 | 전사 시작 |
| 코드만 바뀐 업데이트 | `nalmeok.py`만 교체 |
| 해시 불일치 | 롤백, 기존 버전 유지 |
| 공증된 DMG, 새 Mac | Gatekeeper 경고 없음 |

### 6.4 실패 시나리오

- 다운로드 중단 → `.tmp` 남기고 재시도
- 디스크 부족 → 명확한 메시지
- Terminal이 기본 핸들러가 아님 → 안내
- 앱 이전(translocation) → Application Support만 사용하므로 영향 없음

## 7. 릴리스 절차

1. `nalmeok.py` 버전 범프
2. 테스트 통과
3. `scripts/build_macos.py`로 앱·DMG 빌드
4. 서명·공증·staple
5. `SHA256SUMS.txt` 생성
6. GitHub Release에 DMG·manifest·SHA256SUMS 업로드
7. manifest의 URL·해시 갱신
8. 새 Mac에서 설치·업데이트 확인

## 8. 리스크와 대안

| 리스크 | 영향 | 대응 |
|---|---|---|
| 공증 거부 | 배포 불가 | 중첩 코드 서명, script 권한, `--timestamp` 확인 |
| 다운로드 실패 | 첫 실행 불가 | 재시도, resume, 오프라인 안내 |
| Homebrew Python venv 깨짐 | 실행 실패 | venv 재생성, PBS 폴백 |
| PBS 아키텍처 불일치 | 실행 실패 | arm64 고정, `file` 검증 |
| Gatekeeper가 다운로드 바이너리 차단 | 실행 실패 | 해시 검증, 필요 시 Developer ID 서명 |
| 업데이트 중 전사 | 데이터 손상 | 잠금, 다음 실행에 적용 |
| 앱 번들 크기 증가 | 배포 부담 | seed만 포함, 페이로드 분리 |

### 8.1 전체 번들 vs 얇은 앱

- 전체 번들: 오프라인·단순, 그러나 업데이트마다 100MB 재다운로드와 폴더 복제 위험.
- 얇은 앱: 채택. 첫 실행 다운로드가 필요하지만 업데이트가 작다.

### 8.2 Sparkle

- 앱 껍데기 자동 업데이트에 적합. 페이로드 분리 구조와는 별개.
- v1.1 이후 검토. v1은 수동 DMG.

### 8.3 PKG

- 관리자 권한·설치 마법사·postinstall root 실행이 추가됨.
- 소비자용 DMG보다 이점이 없어 채택하지 않는다.

### 8.4 Homebrew

- 자동 설치는 sudo·CLT GUI·시스템 변경을 유발.
- 이미 설치된 ffmpeg만 감지하고, 없으면 정적 빌드를 받는다.

### 8.5 네이티브 재작성

- 앱 크기는 줄지만 TUI·Batch·live·병합 로직을 다시 만들어야 함.
- 오픈소스 유지보수 비용이 커 채택하지 않는다.

### 8.6 시스템 Python

- 스텁/3.9 문제로 사용 불가.
- `/usr/bin/python3`는 탐색·실행 대상에서 제외한다.

## 9. 확정된 결정

2026-10-30 승인:

| 항목 | 결정 |
|---|---|
| Finder 아이콘 드롭 | **v1.1** (v1은 터미널 드롭) |
| Intel x86_64 | **보류** (arm64만) |
| 업데이트 확인 | **v1 수동** (`nalmeok update`/홈 메뉴), 자동 확인은 v1.1 |
| ffmpeg/ffprobe 출처 | **LGPL 정적 빌드**, SHA-256 고정 |
| manifest Ed25519 서명 | **v1.1** (v1은 HTTPS + SHA-256) |
| 최소 macOS | **12 Monterey** |
| 앱 이름/번들 ID | `nalmeok`, `com.jseook11.nalmeok` |

열린 질문은 없습니다. 새 결정이 필요하면 이 표를 먼저 갱신합니다.

## 10. 변경 이력

| 날짜 | 변경 | 작성 |
|---|---|---|
| 2026-10-30 | 최초 작성. 얇은 앱 + PBS + 기존 SDK 흐름 + 공증 DMG 결정 | Claude Code |
| 2026-10-30 | 계획 승인. Intel 보류, 업데이트 v1 수동, 나머지 추천안 확정 | Claude Code |

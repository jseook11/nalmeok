# macOS 앱 패키징

> **상태:** 운영 중 · 최근 릴리스 v0.6.1 (0.6.2는 미배포)
> **목표:** macOS 비개발자가 DMG 하나로 설치하고, Python·brew·git 없이 TUI를 쓰며, 업데이트 때 전체를 다시 받지 않도록 합니다.
> 이 문서가 macOS 배포의 단일 기준입니다. 사용자용 설치 절차는 `README.md`가 소유합니다.

## 0. 이 문서를 먼저 읽는 사람에게

1. **상태(0.1)** 와 **불변 조건(0.3)** 을 확인합니다.
2. 런처·부트스트랩·서명·업데이트를 바꾸면 이 문서를 먼저 고칩니다.
3. 작업을 마치면 상태 표와 변경 이력(10장)을 갱신합니다.

### 0.1 상태

| 작업 | 상태 | 검증 |
|---|---|---|
| M1 · 앱 번들·런처·부트스트랩 | 완료 | 앱 2.1MB. PBS 3.12.16 다운로드·SHA 검증·venv/SDK 설치 확인 |
| M2 · DMG·서명·공증·staple | 완료 | Developer ID 서명, notarytool **Accepted**(`2c48e95d`, v0.6.0 빌드), staple·spctl 통과. DMG 2.45MB |
| M3 · 업데이트 | 완료 | `nalmeok update`·manifest·앱 코드/SDK 갱신, E2E 확인 |
| M3 · ffmpeg 온디맨드 | 미착수 | macOS manifest에 `ffmpeg` 항목 없음. 현재 `brew install ffmpeg` 안내 |
| M4 · CI·테스트·문서 | 완료 | unsigned 앱 CI 빌드, README 설치 안내, v0.6.0/0.6.1 릴리스 |
| 후속 · Finder 아이콘 드롭 | 보류 (v1.1) | - |
| 후속 · Intel x86_64 | 보류 | - |
| 후속 · 앱 자동 업데이트(Sparkle 등) | 보류 | - |

상태 값: `미착수` / `진행 중` / `완료` / `보류`

### 0.2 기준선

- 앱 버전: **0.6.2** (`nalmeok/constants.py`, `pyproject.toml`)
- 테스트: **158개 통과, 8개 skip** (macOS)
- 릴리스: https://github.com/jseook11/nalmeok/releases/tag/v0.6.1
- 대상: macOS 12 Monterey 이상, **Apple Silicon(arm64)**. Intel은 미지원
- 번들 ID `com.jseook11.nalmeok`, 최소 버전은 `Info.plist`의 `LSMinimumSystemVersion`
- 빌드 도구: `swiftc`, `sips`/`iconutil`, `hdiutil`, `codesign`, `notarytool`, `file`
- 런타임 부트스트랩: `curl`, `tar`, `shasum`
- CI는 `macos-latest`에서 **unsigned** 앱만 빌드한다. 서명·공증은 로컬 자격이 있을 때만

### 0.3 불변 조건

1. **Homebrew를 설치하지 않는다.** 이미 있으면 ffmpeg 감지만 한다.
2. **시스템 Python을 실행하거나 수정하지 않는다.** `/usr/bin/python3`는 CLT 설치 GUI를 띄우는 스텁이다.
3. 런타임에 **`sudo`를 요구하지 않는다.**
4. **앱 번들과 `/Applications`에 쓰지 않는다.** 앱 코드·런타임은 `~/Library/Application Support/nalmeok/`, 설정·키는 `~/.config/nalmeok/`, 작업 상태는 각 강의 폴더에 둔다. (예외: 드롭한 단일 파일 작업 폴더는 `~/.local/share/nalmeok/jobs`.)
5. 다운로드한 산출물은 **SHA-256 검증 전에는 실행하지 않는다.**
6. 릴리스에 서명·공증을 **요구하지 않는다.** 자격이 있으면 서명·공증하고, 없으면 unsigned DMG로 배포하되 README에 첫 실행 방법(Gatekeeper 우회)을 적는다. 2026-10-30 사용자 결정.
7. 사용자 인터페이스는 터미널(TUI)이다. 별도 GUI를 만들지 않는다. 런처의 오류 `NSAlert`는 예외.

> 2026-10-30 폐기: "앱 코드는 계속 단일 `nalmeok.py`". 2026-10-31 `nalmeok/` 패키지로 분리했다(`CLAUDE.md` 제약 조정).

### 0.4 하지 말 것

- Python·google-genai·ffmpeg를 **DMG 앱 안에 번들하지 않는다.** (얇은 앱)
- Homebrew를 자동 설치하지 않는다.
- PyInstaller/Nuitka로 재작성하지 않는다.
- 맨 `.sh`/`.command`를 **배포물로 내놓지 않는다.** (공증 불가, Gatekeeper 경고) 서명된 앱 번들 안의 리소스는 해당 없음.
- AppleScript로 Terminal을 제어하지 않는다. (Automation 권한 프롬프트)
- `/usr/bin/python3`를 실행해 버전을 확인하지 않는다.
- 업데이트를 검증 없이 적용하지 않는다.
- 앱 번들 안에 `.venv`나 로그를 만들지 않는다.

## 1. 목표와 범위

### 1.1 목표

- 비개발자가 **DMG 다운로드 → Applications로 드래그 → 더블클릭**으로 시작한다.
- Python, brew, git, CLT를 요구하지 않는다.
- 첫 실행에서 필요한 런타임만 내려받고, 이후 코드 업데이트는 앱 코드 파일 하나만 받는다.
- 터미널에 폴더/MP4/M4A를 끌어다 놓으면 바로 전사한다(`docs/drop-target.md`).
- 자격이 있으면 서명·공증으로 Gatekeeper 경고 없이 실행된다.
- Apple Silicon에서 동작을 검증한다.

### 1.2 비목표

- Intel(x86_64) DMG, Mac App Store 배포
- Finder 아이콘에 직접 드롭 (v1.1), Sparkle 기반 앱 껍데기 자동 업데이트
- 네이티브 GUI 재작성, 백그라운드 데몬/메뉴바 앱
- Windows 배포 방식 변경

## 2. 결정 로그

| ID | 결정 | 이유 | 결과 |
|---|---|---|---|
| MD1 | **얇은 `.app` + DMG** | Mac 표준 설치 경험, PKG의 관리자 권한·마법사 불필요 | `nalmeok.app` + DMG |
| MD2 | 페이로드는 **첫 실행에 내려받음** | 업데이트마다 큰 재다운로드·폴더 복제 회피 | 앱 2.1MB, 첫 실행 PBS 약 25MB + SDK 약 20MB |
| MD3 | **Homebrew 설치 금지** | sudo·CLT GUI·시스템 변경 | 이미 있으면 ffmpeg만 감지 |
| MD4 | Python은 **PBS 기본**, 기존 3.10+는 검증 후 사용 | 재현성과 사용자 환경 존중의 절충 | `runtime/`, 실패 시 PBS |
| MD5 | 앱 코드는 **Application Support/app**, venv는 그 옆 | 번들 쓰기 권한 회피, `ensure_sdk` 재사용 | `app/nalmeok.py`, `app/.venv` |
| MD6 | Swift 런처가 **`.command`를 열어** Terminal 실행 | TUI에 TTY 필요, Apple Events 회피 | `NSWorkspace.open` |
| MD7 | **컴포넌트 단위 업데이트** | 코드 변경은 앱 파일 하나, 의존성은 드묾 | manifest 기반 |
| MD8 | 무결성은 **HTTPS + SHA-256** | pip와 동등한 신뢰 모델 | manifest에 해시 고정 |
| MD9 | ffmpeg/ffprobe는 **온디맨드 정적 빌드** (계획) | M4A만 쓰는 사용자에게 큰 다운로드를 강요하지 않음 | **미구현.** v1.1 후보 |
| MD10 | v1은 **arm64만** | 사용자 대부분, 빌드·검증 단순화 | `nalmeok-<v>-macos-arm64.dmg` |
| MD11 | 최소 **macOS 12 Monterey** | Swift 런타임 내장, PBS 지원 | `LSMinimumSystemVersion 12.0` |
| MD12 | **Developer ID + notarytool + staple은 선택** | Gatekeeper 경고를 없애 주지만, 개인 배포에서 필수는 아니다 | 자격이 있으면 서명·공증, 없으면 unsigned DMG + README 안내 |
| MD13 | 앱 껍데기 업데이트는 **수동 DMG** | 껍데기는 거의 안 바뀜 | Sparkle은 후속 |
| MD14 | 파일 입력은 **터미널 드롭** | v0.5.0에서 검증 | 브래킷 붙여넣기 |
| MD16 | 번들 ID `com.jseook11.nalmeok` | 서명·공증 식별자 | `Info.plist` |

## 3. 현재 구현

### 3.1 구성 요소

| 파일 | 역할 |
|---|---|
| `macos/launcher.swift` | `Contents/MacOS/nalmeok`. 데이터 폴더·seed 준비 후 `bootstrap.command`를 `NSWorkspace`로 연다 |
| `macos/bootstrap.command` | Python 탐색 → 없으면 PBS 다운로드·검증·설치 → `run.command`로 인계 |
| `macos/run.command` | `NALMEOK_HOME`/`PACKAGED`/`YES`/`FFMPEG` 설정, `$HOME`에서 TUI 실행, 오류 시 창 유지 |
| `scripts/build_macos.py` | 앱 번들·DMG·manifest·SHA256SUMS 생성, 선택적 서명·공증 |
| `.github/workflows/macos.yml` | 단위 테스트 + unsigned 앱 빌드 |

### 3.2 재사용하는 앱 기능

`ensure_sdk()`(앱 폴더 옆 `.venv`), `find_tool()`, 터미널 드롭, `nalmeok drop`, 문서 폴더 복사, `doctor`, TUI 전체.

## 4. 아키텍처

### 4.1 배포물 구조

```text
nalmeok.app/
  Contents/
    Info.plist
    MacOS/nalmeok              # Swift 런처 (arm64, ~104KB)
    Resources/
      nalmeok/                 # seed 패키지 (app/nalmeok/가 없을 때만 복사)
      run.command
      bootstrap.command
      bootstrap.env            # 빌드가 생성하는 PBS 버전/URL/SHA
      LICENSE
      nalmeok.icns
```

DMG에는 `nalmeok.app`과 `/Applications` 심볼릭 링크가 들어간다.

### 4.2 사용자 데이터 구조

```text
~/Library/Application Support/nalmeok/
  app/nalmeok/                 # 실제 실행·업데이트 대상 패키지
  app/.venv/                   # google-genai
  runtime/3.12.16/             # PBS 설치본
  runtime/current -> 3.12.16
  bin/                         # ffmpeg/ffprobe (현재 비어 있음)
~/.config/nalmeok/config.json  # 키·최근 작업 (0600)
~/.config/nalmeok/nalmeok.log  # NALMEOK_DEBUG 로그
~/.local/share/nalmeok/jobs/   # 드롭한 단일 파일 작업 폴더
~/Documents/nalmeok/           # 드롭 결과 .md
```

### 4.3 첫 실행 흐름

1. 사용자가 앱을 더블클릭한다.
2. Swift 런처가 `~/Library/Application Support/nalmeok/app/`를 만들고, `nalmeok/__init__.py`가 **없을 때만** 번들 seed 패키지를 복사한다.
3. 런처가 `bootstrap.command`를 `NSWorkspace`로 연다(LaunchServices가 Terminal 선택).
4. `bootstrap.command`가 Python을 찾는다.
   - `runtime/current/bin/python3` → 버전·`-m venv` 가능 여부 확인
   - python.org 프레임워크, pyenv, `/opt/homebrew`, `/usr/local`, `PATH` 순
   - `/usr/bin/python3`는 확인하지 않는다
5. 없으면 PBS를 내려받아 `runtime/<version>`에 설치하고 `current`를 전환한다.
6. `run.command`가 `PYTHONPATH=$DATA/app`으로 `python -m nalmeok`을 실행한다.
7. 첫 전사에서 `get_client()` → `ensure_sdk()`가 `app/.venv`를 만들고 `google-genai`를 설치한 뒤 `.venv` 인터프리터로 재시작한다.
8. TUI가 열리고 API 키를 등록한다.

첫 실행 화면(PBS 약 25MB + SDK 약 20MB):

```text
Python 3.12 준비 중...
google-genai 설치 중...
(ffmpeg는 MP4 변환을 처음 할 때 설치합니다 — 현재는 brew 안내)
```

### 4.4 일반 실행 흐름

1. 런처가 데이터 폴더와 seed를 확인한다.
2. `bootstrap.command`가 `run.command`로 인계한다.
3. 사용자가 폴더/MP4/M4A를 Terminal 창에 끌어다 놓으면 드롭 흐름으로 전사한다.

### 4.5 업데이트

macOS manifest(`dist/macos-manifest.json` → 릴리스에서 `macos-manifest.json`)는 Windows보다 작다.

```json
{
  "app_version": "0.6.2",
  "nalmeok": {"version": "0.6.2", "url": "...", "sha256": "..."},
  "sdk": {"name": "google-genai", "version": "2.31.0"},
  "python": {"version": "3.12.16"}
}
```

`ffmpeg` 키는 없다. Windows manifest에는 `python.url/sha256`과 `ffmpeg`가 추가로 들어간다(`docs/windows-support.md` 4.13).

동작:

1. `update`/홈 화면 `v`가 manifest를 받는다. `NALMEOK_MANIFEST_URL`로 대체 가능.
2. manifest 버전이 현재 `__version__`보다 **클 때만** 앱 패키지 ZIP(`nalmeok-app-macos-arm64.zip`)을 내려받는다. 같은 버전의 수정 빌드는 받지 않는다.
3. SHA-256 → ZIP 확인 → `.nalmeok-next`에 풀고 `nalmeok/__init__.py`의 버전 문자열과 모든 `.py`의 `compile()`을 검증한 뒤 디렉터리를 교체한다. 실패하면 기존 패키지가 그대로 남는다(교체 전에 실패). 백업은 교체 직후 삭제한다.
4. SDK는 **이미 설치돼 있고** manifest 버전보다 낮을 때만 `.venv` pip로 갱신한다.
5. Python·ffmpeg는 `update`가 교체하지 않는다. Python은 첫 실행 부트스트랩이, ffmpeg는 온디맨드가 맡는다(후자는 미구현).

제약:

- `NALMEOK_PACKAGED=1`이 아니면(소스 실행) 업데이트를 거부한다: "git pull로 업데이트하세요".
- DMG 사용자에게는 `nalmeok` CLI가 PATH에 없다. **홈 화면 `v`** 가 업데이트 입구다.
- 실행 중인 프로세스는 이전 코드로 계속 동작하고, 다음 실행부터 새 코드가 적용된다.
- **DMG를 다시 설치해도 앱 코드는 갱신되지 않는다.** 런처가 seed를 `app/nalmeok/`이 없을 때만 복사하기 때문이다. 새 버전은 업데이트로 받거나 `app/nalmeok/`을 지운 뒤 앱을 다시 실행한다.

### 4.6 Python 선택

1. `runtime/current/bin/python3` (PBS)
2. python.org 프레임워크, pyenv shim, `/opt/homebrew/bin/python3`, `/usr/local/bin/python3`
3. `PATH`의 `python3`

확인: `>= 3.10`, `-m venv` 생성 가능. `/usr/bin/python3`는 제외.
없거나 검증 실패 → PBS 다운로드(약 25MB, SHA-256 검증).

### 4.7 SDK 설치

`ensure_sdk()`가 이미 처리한다. venv 경로는 cwd와 무관하게 `Path(__file__).resolve().parent / ".venv"`이고, `NALMEOK_PACKAGED=1`이면 `uv sync` 없이 venv pip로 설치하며, `NALMEOK_YES=1`이면 확인 없이 진행한다. venv 인터프리터가 깨졌으면 재생성한다.

### 4.8 ffmpeg (미구현)

현재 macOS는 ffmpeg를 자동으로 받지 않는다. `find_tool()`이 `NALMEOK_FFMPEG`(`$DATA/bin`) → 앱 폴더 → PATH 순으로 찾고, 없으면 `brew install ffmpeg`를 안내한다.

온디맨드로 만들려면: macOS manifest에 `ffmpeg {version,url,sha256}`를 추가하고 `ensure_ffmpeg()`가 이를 쓰게 한다. LGPL 정적 빌드 확보가 선행 조건이다.

### 4.9 Terminal 실행

- 런처는 `NSWorkspace.shared.open(bootstrap)`을 쓴다. Apple Events/Automation 권한이 필요 없다.
- `Info.plist`의 `LSUIElement=true`로 Dock 아이콘 없이 실행한다.
- `run.command`는 `cd "$HOME"` 후 실행하고, 종료 코드가 0이 아니면 창을 유지한다.
- 런처 오류는 `NSAlert`로 표시한다(터미널이 없을 수 있으므로).

### 4.10 Finder 아이콘 드롭 (v1.1)

- Swift 런처에 `application(_:open:)`을 추가하고, 드롭된 URL을 `drop` 인자로 전달한다.
- v1은 터미널 드롭으로 충분하므로 보류한다.

### 4.11 서명·공증 (선택)

**릴리스 요건이 아니다.** 자격이 있을 때만 수행한다. `NALMEOK_SIGN_IDENTITY`/`NALMEOK_NOTARY_PROFILE`이 없으면 빌드는 unsigned DMG를 만든다.

빌드 순서(`scripts/build_macos.py`):

1. `swiftc`로 런처 컴파일 → 앱 번들 구성, 아이콘 변환(`sips`/`iconutil`)
2. `codesign --force --options runtime --timestamp --sign "$IDENTITY" nalmeok.app`
3. `hdiutil create`로 DMG 생성
4. `codesign --sign "$IDENTITY" nalmeok-<v>-macos-arm64.dmg`
5. `xcrun notarytool submit --wait`
6. `xcrun stapler staple`
7. `spctl -a -vv`(마운트한 DMG), `stapler validate`

자격은 이 Mac의 키체인(`nalmeok-notary`)에 있다. 설정 방법은 `CLAUDE.md` 참고. **CI에는 서명 단계가 없다.** CI는 unsigned 앱을 `nalmeok-macos-unsigned-<sha>` 아티팩트로 올린다.

### 4.12 보안 모델

- 앱 번들: Developer ID 서명 + 공증.
- 다운로드 산출물: HTTPS + SHA-256 고정. 검증 실패 시 실행하지 않는다.
- manifest: GitHub Releases의 HTTPS URL. Ed25519 서명은 v1.1 후보(현재 미구현).
- 업데이트 중 잠금은 없다. 전사 중 업데이트는 다음 실행에 적용된다.

## 5. 작업 기록

| 마일스톤 | 내용 |
|---|---|
| M1 | `macos/launcher.swift`, `bootstrap.command`, `run.command`, 데이터 폴더·seed, PBS 설치, `ensure_sdk` 패키징 경로 |
| M2 | `scripts/build_macos.py`, `.icns`, DMG, 서명·공증·staple, `SHA256SUMS.txt` |
| M3 | manifest 포맷, `nalmeok update`, 앱 코드 원자적 교체, SDK 버전 비교 |
| M4 | macOS CI(테스트 + unsigned 빌드), README 설치 안내 |

## 6. 테스트와 검증

### 6.1 단위 테스트

- manifest 파싱·버전 비교, SHA-256 검증 성공/실패
- 앱 코드 원자적 교체, `ensure_ffmpeg` 경로, ffmpeg 탐색
- (미비) Python 후보 탐색은 `bootstrap.command`의 셸 로직이라 단위 테스트가 없다

### 6.2 빌드 검증

```bash
codesign --verify --deep --strict --verbose=2 build/macos/app/nalmeok.app
xcrun stapler validate dist/nalmeok-<v>-macos-arm64.dmg
spctl -a -vv --type execute /Volumes/nalmeok/nalmeok.app   # 마운트한 DMG 기준
```

### 6.3 수동 매트릭스

| 환경 | 확인 |
|---|---|
| macOS 12/13/14/15 arm64, CLT·brew·Python 없음 | 첫 실행, PBS 다운로드, TUI |
| python.org 3.12 설치됨 | PBS 다운로드 생략 |
| pyenv/Homebrew Python 3.10+ | 검증 후 사용 또는 폴백 |
| 네트워크 없음 | 명확한 오류, 재시도 |
| 한글·공백 경로 드롭 | 전사 시작 |
| 코드만 바뀐 업데이트 | `nalmeok-app-macos-arm64.zip`으로 패키지만 교체 |
| 해시 불일치 | 교체 없이 기존 파일 유지 |
| 공증된 DMG, 새 Mac | Gatekeeper 경고 없음 |

### 6.4 실패 시나리오

- 다운로드 중단 → 임시 파일 삭제 후 재시도
- 디스크 부족 → 명확한 메시지
- Terminal이 기본 핸들러가 아님 → 안내
- 앱 이전(translocation) → Application Support만 쓰므로 영향 없음

## 7. 릴리스 절차

1. `__version__`과 `pyproject.toml` 버전을 올린다(둘이 같아야 한다. 테스트가 검사).
2. 테스트를 통과시킨다.
3. `NALMEOK_SIGN_IDENTITY`, `NALMEOK_NOTARY_PROFILE`을 지정하면 서명·공증까지 수행한다(선택). 없으면 unsigned DMG가 나온다.
4. `spctl`, `stapler validate`, 마운트한 DMG 실행을 확인한다.
5. GitHub Release에 **DMG, `nalmeok-app-macos-arm64.zip`, `macos-manifest.json`, `SHA256SUMS.txt`** 를 올린다. 태그는 `v<version>`.
6. 새 Mac에서 설치·업데이트를 확인한다.
7. 이 문서의 상태 표와 변경 이력을 갱신한다.

> `dist/SHA256SUMS.txt`는 Windows 빌드와 **같은 파일명**을 쓴다. 한 체크아웃에서 두 빌드를 연속 실행하면 마지막 것이 앞의 해시를 덮어쓴다. 릴리스 자산은 워크플로별 아티팩트에서 가져온다.

## 8. 리스크

| 리스크 | 영향 | 대응 |
|---|---|---|
| 공증 거부 | 배포 불가 | 중첩 서명, 스크립트 권한, `--timestamp` 확인 |
| 다운로드 실패 | 첫 실행 불가 | 재시도, 오프라인 안내 |
| Homebrew Python venv 깨짐 | 실행 실패 | venv 재생성, PBS 폴백 |
| PBS 아키텍처 불일치 | 실행 실패 | arm64 고정 |
| Gatekeeper가 다운로드 바이너리 차단 | 실행 실패 | 해시 검증 |
| DMG 재설치로 코드 미갱신 | 구버전 실행 | 4.5의 안내, 업데이트 경로 사용 |
| 업데이트 백업 없음 | 손상 시 복구 불가 | 교체 전 검증(해시·버전·compile). 필요 시 백업 추가 검토 |
| 앱 번들 크기 증가 | 배포 부담 | seed만 포함, 페이로드 분리 |

## 9. 확정된 결정

| 항목 | 결정 |
|---|---|
| Finder 아이콘 드롭 | **v1.1** (v1은 터미널 드롭) |
| Intel x86_64 | **보류** (arm64만) |
| 업데이트 확인 | **수동** (홈 `v`, 소스는 `nalmeok update`). 자동 확인은 v1.1 후보 |
| ffmpeg/ffprobe | **LGPL 정적 빌드, 온디맨드** — 미구현 |
| manifest Ed25519 서명 | **v1.1 후보** (현재 HTTPS + SHA-256) |
| 최소 macOS | **12 Monterey** |
| 앱 이름/번들 ID | `nalmeok`, `com.jseook11.nalmeok` |
| 릴리스 서명·공증 | **선택.** 자격이 있으면 수행, 없으면 unsigned + README 안내 |
| CI 서명 | **없음.** CI는 unsigned 아티팩트만 만든다 |

## 10. 변경 이력

| 날짜 | 변경 | 작성 |
|---|---|---|
| 2026-10-30 | 최초 작성. 얇은 앱 + PBS + 공증 DMG 결정 | Claude Code |
| 2026-10-30 | M1 구현. Swift 런처·bootstrap/run.command·PBS 3.12.16·`ensure_sdk` 패키징 경로 | Claude Code |
| 2026-10-30 | M2 완료. Developer ID 서명·공증 Accepted(`2c48e95d`)·staple·spctl 통과 | Claude Code |
| 2026-10-30 | M3. `nalmeok update`로 앱 코드·SDK 갱신, manifest 생성 | Claude Code |
| 2026-10-30 | v0.6.0 → v0.6.1 릴리스, 파일명 `macos-arm64`로 통일 | Claude Code |
| 2026-10-30 | 문서 정리: 코드와 어긋난 설명 수정(런처가 bootstrap을 연다, macOS manifest에는 ffmpeg/python URL이 없다, update는 앱 코드만 교체·백업 없음, ffmpeg 온디맨드 미구현), 폐기된 단일 파일 제약 제거, **서명·공증 필수 요건 폐기**(선택 사항으로) | Claude Code |
| 2026-10-31 | 패키지 분리 반영: 번들 Resources/`nalmeok/`, seed는 패키지 복사, 실행은 `-m nalmeok`, 업데이트는 `nalmeok-app-macos-arm64.zip` 교체 | Claude Code |

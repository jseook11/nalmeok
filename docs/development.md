# 개발 가이드

> 사용자용 문서는 [`README.md`](../README.md), 상세 사용법은 [`usage.md`](usage.md)에 있습니다.

## 문서 지도

| 문서 | 소유하는 내용 |
|---|---|
| `README.md` | 설치, 처음 사용, 다운로드 |
| `docs/usage.md` | 화면 사용법, 비용, API 키 발급, FAQ |
| `docs/prompts.md` | 전사본을 AI에 올려 물어볼 질문 예시(한·영). 도움말 **?**의 링크 대상(`help.promptsUrl`) |
| 로컬 `CLAUDE.md` (저장소 밖) | 작업 지침, 폐기된 제약, macOS 자격 |

## Go 앱

Go + Wails 데스크톱 앱이다.

```text
cmd/nalmeok/        진입점: 인자 없으면 GUI, 인자가 있으면 CLI
internal/config     설정 폴더, config.json(키·gui.mode·signals)
internal/store      items.json(처리 목록), 검색
internal/media      입력 스캔·길이, 다시 인코딩 없이 조각 만들기 (표준 라이브러리만, 아래 "미디어 형식")
internal/plan       조각 계획(요청당 최대 65분, 균등 분할), 예상 비용
internal/gemini     genai SDK 래퍼: 업로드, live, Batch
internal/runner     실행기: Gemini 무료 키 순환·Batch, 한도 소진 전환, 재개
internal/output     결과 병합, .md 쓰기
internal/app        Wails 바인딩
internal/telemetry  무료 키 상태 신호: 요청 결과를 모아 사이트로 보내고, 전체 상태를 읽는다 (아래 "상태 신호")
internal/notify     완료 알림: macOS 앱 번들은 UNUserNotificationCenter, 번들 밖은 osascript, Windows는 토스트
internal/i18n       문구 카탈로그 읽기: T(키), Error(키)
frontend/dist       화면 (HTML/CSS/JS, 빌드 단계 없음)
frontend/dist/i18n  문구 카탈로그 (en.json 기준, ko.json 한국어)
```

전사 결과는 원본 옆 `.md`와 함께 설정 폴더의 `transcripts/<항목 ID>.md`에도 보관한다(`output.ArchivePath`). 화면은 원래 위치의 `.md`를 먼저 읽고, 그 파일이 옮겨지거나 지워졌을 때만 보관본을 보여 주며 "다시 찾기…"(`LocateOutput`)·"다시 저장"(`RestoreOutput`)을 낸다. 보관본이 생기기 전 항목은 처음 읽을 때와 앱을 켤 때 옮겨 두고, 목록에서 지우면 보관본도 지운다.

### 미디어 형식

받는 확장자는 `internal/media`의 `formats` 표 하나가 정한다. 파일 선택 창 필터는 `media.Extensions()`로 만들고, `app.js`의 `AUDIO_EXT`는 손으로 맞추되 `internal/app/formats_test.go`가 어긋나면 실패한다. 조각은 다시 인코딩하지 않는다.

| 입력 | 조각 |
|---|---|
| ISO-BMFF(`.m4a` `.mp4` `.mov` `.m4v` `.m4b` `.3gp`), 일반·조각난(moof) MP4 | AAC → 오디오만 담은 M4A로 리먹스. MP3 오디오 → MP3 프레임 그대로 |
| `.aac`(ADTS) | M4A로 리먹스. Gemini가 ADTS 업로드를 막는 경우가 있어서다 |
| `.mp3` | 프레임 경계에서 자름 |
| `.wav`(RIFF·RF64), `.aif`·`.aiff`·`.aifc` | 샘플 경계에서 자르고 헤더를 새로 씀. RF64는 짧아도 RIFF로 다시 씀 |
| `.flac` | 프레임 경계에서 자르고 STREAMINFO를 새로 씀 |
| `.ogg`·`.opus`(Opus·Vorbis) | 페이지 경계에서 자르고 코덱 헤더 페이지를 조각마다 반복, granule·순번·CRC를 다시 씀 |

요청 하나는 오디오 최대 65분이다. 답과 thinking이 출력 한도 65,536토큰을 함께 쓰는데, 110분을 한 요청에 담았더니 넘쳤다(2026-11-05). 긴 파일은 필요한 최소 개수로 똑같이 나눈다(`plan.EvenSeconds`). 110분은 55분 둘, 140분은 약 47분 셋이다. 프레임 경계 때문에 남는 몇 초는 따로 요청하지 않고 마지막 조각에 붙인다(`plan.Units`, 60초 여유). 그래도 출력 한도에 걸린 조각은 받은 텍스트를 버리지 않고 끝에 잘렸다는 줄(`transcript.truncated`)을 붙여 저장하고, 토큰 사용량을 로그에 남긴다.

65분 안에 드는 WAV·AIFF·MP3·FLAC·Ogg는 원본을 그대로 올린다(하드 링크 또는 복사). 조각난 MP4는 moov에 `mvex`가 있을 때만 moof의 샘플을 읽는다. OBS Hybrid MP4처럼 완전한 moov와 moof가 같이 있으면 moof는 무시한다. WebM/MKV, MP4 안의 ALAC·Opus·AC-3은 받지 않는다.

`go test ./internal/media`의 원본 소리는 테스트가 직접 만든 WAV이고, 나머지 형식은 `afconvert`로 만든다(macOS가 아니면 건너뜀). ffmpeg로 만든 실제 파일(조각난 MP4, Ogg)과 ffmpeg 디코딩 확인은 ffmpeg가 있을 때만 돈다. CI에는 ffmpeg가 없으므로 같은 경우를 Go로 만든 바이트로도 검사한다.

### 모델과 자동 재시도

전사 모델은 기본 `gemini-3.8-flash`(`gemini.Model`)이고, 거부되면 `gemini-3.6-flash`(`gemini.FallbackModel`)로 한 번 더 보낸다. 3.8이 오디오 입력 단계에서 간헐적으로 `promptFeedback.blockReason`을 내며 후보를 비우는 오탐이 있었고(2026-11-08), 같은 요청이 3.6에서는 통과했다. 모델은 `gemini.Request.Model`로 요청마다 지정하며, Batch job 하나는 한 모델로 고정이므로 제출할 때 모든 `BatchRequest.Model`이 같아야 한다.

- live: 한 키에서 unit의 거부(`BLOCKED_*`) 시 3.6으로 1회, 빈 `MALFORMED_RESPONSE`면 같은 모델로 1회. 두 재시도는 각각 1회라 그 키에서 최대 3요청이다. 3.6이 429면 기존 키 순환으로 다음 키에서 같은 규칙을 다시 적용한다(무료 요청이라 비용은 없다). 같은 키와 업로드를 재사용하고, 3.6 요청도 키 간격·일일 요청 수·상태 신호에 포함된다.
- Batch: 결과 수집에서 거부가 있으면 성공한 unit의 텍스트는 두고 남은 unit만 3.6 job으로 1회 자동 재제출한다. 거부 없이 빈 `MALFORMED_RESPONSE`만 있으면 같은 모델로 1회. 재시도 여부는 `BatchRef.Attempt`/`Fallback`에 저장해 재시작·재폴링에서 반복되지 않는다. 수동 **다시 시도**는 자동 재시도를 더 하지 않는다. 사용자가 취소한 항목은 끝난 job의 결과만 수집하고 새 job을 만들지 않는다.
- 비용: 3.6 구간도 `plan.CostUSD`의 3.8 요금표로 계산한다(근사). 거부된 요청은 청구될 수 있지만 사용량을 저장하지 않으므로 표시 비용은 실제보다 조금 낮을 수 있다. 거부된 unit은 재제출로 한 번 더 청구된다.
- 상태 신호: fallback 요청도 `Generate` 한 번으로 세므로 무료 신호가 한 건 더 생기고, Batch는 새 job이 끝날 때 한 건 더 생긴다.

### 빌드

```bash
go test ./...
go build -tags desktop,production -o build/nalmeok ./cmd/nalmeok
scripts/build_mac_app.sh      # build/Nalmeok.app (arm64·amd64 범용, 아이콘 포함). 서명·공증 자격이 있으면 dist/Nalmeok-<version>-macos-universal.zip까지
scripts/build_windows_app.sh  # build/windows/Nalmeok.exe(amd64)·Nalmeok-arm64.exe
```

`build/nalmeok`은 개발용 네이티브 바이너리다. `.app`에는 들어가지 않는다. `scripts/build_mac_app.sh`는 arm64와 amd64를 따로 빌드해 `lipo -create`로 하나로 합친다. Wails의 macOS 백엔드가 cgo를 쓰므로 각 조각을 `CC="clang -arch ..."`와 `CGO_CFLAGS`·`CGO_LDFLAGS="-arch ..."`로 빌드한다. 확인은 `lipo -archs build/Nalmeok.app/Contents/MacOS/nalmeok`(→ `x86_64 arm64`).

아이콘 원본은 꽉 찬 정사각형 그림 `assets/nalmeok-art.png`(1024px, 모서리 없음)다. 여기서 두 단계로 만든다. 결과물은 모두 커밋해 두고, 그림이 바뀔 때만 다시 만든다.

1. `uv run --no-project --with pillow python -I scripts/make_icon_master.py assets/nalmeok-art.png assets/nalmeok.png assets/nalmeok.ico` — macOS 격자(1024 안에 824 몸체, 둥근 모서리, 옅은 밝은 테두리, 그림자)로 자른 `assets/nalmeok.png`와 Windows용 여러 크기 `assets/nalmeok.ico`.
2. `scripts/make_icns.sh` — `assets/nalmeok.png`에서 `assets/nalmeok.icns`(sips로 크기별 PNG, iconutil로 묶음).

빌드 스크립트는 이 파일을 `Contents/Resources`에 복사하고 `CFBundleIconFile`로 가리킨다.

Windows용 실행 파일은 macOS·Linux에서 크로스 빌드한다. Wails의 Windows 백엔드는 순수 Go라 cgo가 필요 없다. exe의 아이콘과 애플리케이션 매니페스트는 `cmd/nalmeok/rsrc_windows_amd64.syso`·`rsrc_windows_arm64.syso`에 들어 있다. `GOOS=windows`로 빌드하면 Go 링커가 이 파일을 알아서 넣고, 다른 OS 빌드는 무시한다. `assets/nalmeok.ico`나 `scripts/winres/nalmeok.exe.manifest`를 바꾸면 저장소 루트에서 `go run ./scripts/winres`로 다시 만들어 커밋한다. 표준 라이브러리만 쓰는 작은 생성기라 따로 설치할 도구는 없다. 아이콘 그룹 ID는 3이다. Wails가 창 아이콘을 이 번호로 읽는다. 매니페스트는 asInvoker, Common Controls v6, PerMonitorV2 DPI, Windows 10·11, UTF-8 코드 페이지를 지정한다. `go test ./scripts/winres`는 커밋된 `.syso`가 지금 아이콘·매니페스트와 다르면 실패한다.

CI는 `.github/workflows/`에서 돈다. macOS·Windows에서 `gofmt`·`go vet`·`go test ./...`를 돌리고, macOS는 `.app`, Windows는 amd64·arm64 exe를 빌드해 아티팩트로 올린다. CI는 서명·공증을 하지 않는다.

서명·공증은 환경변수가 있을 때만 한다. `NALMEOK_SIGN_IDENTITY="Developer ID Application: ..."`, `NALMEOK_NOTARY_PROFILE=nalmeok-notary`. 둘 다 있으면 공증·staple 뒤 `ditto -c -k --keepParent`로 `dist/Nalmeok-<version>-macos-universal.zip`을 만들고, `dist/SHA256SUMS.txt`의 macOS ZIP 줄도 갱신한다(Windows 실행 파일 줄은 유지). 없으면 임시 서명(ad-hoc)한 `.app`만 만들고 `dist/`에는 쓰지 않는다.

### Store판 (MSIX)

Microsoft Store 제출물은 x64·arm64를 묶은 `Nalmeok-<version>-windows.msixbundle`이다. exe는 macOS·Linux에서 `scripts/build_windows_app.sh`로 만들거나 CI 아티팩트를 받아 `build/windows/`에 둔다. 그다음 Windows(SDK 설치됨)에서 아래를 돌린다. 결과는 `dist/`에 남는다.

```powershell
scripts/package_windows_msix.ps1 [-Version x.y.z] [-Register]
```

- 매니페스트 템플릿은 `packaging/windows/AppxManifest.xml`, 로고는 `packaging/windows/Assets/`다. 로고는 `scripts/make_msix_assets.py`로 만들고 결과물을 커밋한다(위 아이콘 절차와 같다).
- 패키지 버전은 네 자리이고 마지막은 0이다(`<version>.0`). `-Version`을 생략하면 `scripts/build_windows_app.sh`의 기본 버전을 읽는다.
- identity 값은 Partner Center의 **Product identity**와 같다(Name `Seook.Nalmeok`, Publisher `CN=9C1988C4-DDED-41A0-88F0-2005D33927CF`, 표시 이름 `Seook`). 비밀 값이 아니라 스크립트 기본값으로 둔다. 바꿀 때만 `-IdentityName`·`-Publisher`나 `NALMEOK_MSIX_NAME`·`NALMEOK_MSIX_PUBLISHER`를 쓴다. 표시 이름은 매니페스트에 있고 대소문자까지 Partner Center와 같아야 한다.
- 서명은 Store가 한다. 스크립트는 서명하지 않고, CI는 unsigned `.msixbundle`만 아티팩트로 올린다.
- `-Register`는 패키지를 만들지 않고 현재 PC 아키텍처의 레이아웃(`build/msix/<arch>/`)을 `Add-AppxPackage -Register`로 등록한다. 개발자 모드가 필요하고 서명·Windows SDK는 필요 없다. VM에서 Store 설치본과 같은 동작을 시험할 때 쓴다.
- MSIX 앱은 파일·레지스트리 쓰기가 패키지 전용 공간으로 가상화된다. 기존 `%APPDATA%\nalmeok`의 설정은 읽지만, 새로 쓰는 키·목록은 `%LOCALAPPDATA%\Packages\<PackageFamilyName>\LocalCache\` 아래로 간다. 그래서 포터블 exe판과 설정이 갈라지고, Store판을 제거하면 그 설정도 함께 지워진다. 가상화를 끄려면 `unvirtualizedResources` 제한 권한과 심사 사유가 필요하므로 켜 둔다.
- 최소 버전은 Windows 10 1903(`10.0.18362.0`)이다. 위 가상화 동작의 기준 버전이다.

### 플랫폼별 동작

- 완료 알림(`internal/notify`): 실행기는 `Options.Notify`로 알림 함수를 받는다(테스트는 가짜를 넣고, 기본은 알리지 않음). macOS는 `.app` 안에서 실행될 때만 `UNUserNotificationCenter`를 쓴다. 번들 식별자가 없으면 이 API가 예외를 던지므로 `build/nalmeok`·CLI는 osascript로 보낸다(스크립트 편집기 이름으로 뜸). 첫 알림 때 권한을 묻고, 사용자가 거부하면(또는 나중에 끄면) 알리지 않는다. 앱 안에서는 osascript로 대신하지 않는다 — macOS는 거부도 오류(`UNErrorDomain` 1)로 돌려주므로, 대신 보내면 거부한 사용자에게 스크립트 편집기 이름으로 알림이 간다. 알림을 누르면 앱을 활성화하고 창을 앞으로(두 번째 실행과 같은 `showWindow`). 해당 행 강조는 하지 않는다. 확인은 Finder나 `open build/Nalmeok.app`으로 연 앱에서 한다(터미널에서 실행 파일을 바로 띄우면 거부될 수 있다).
- Windows 알림: PowerShell의 WinRT 연결로 토스트를 띄운다. 스크립트는 `-EncodedCommand`, 프로세스는 `CREATE_NO_WINDOW`. 앱 ID `kr.nalmeok.app`을 `HKCU\Software\Classes\AppUserModelId`에 이름·아이콘(`%LOCALAPPDATA%\Nalmeok\notify-icon.png`, `assets/nalmeok.ico`의 256px)과 함께 등록한다. Store판(MSIX)은 `GetCurrentApplicationUserModelId`로 패키지가 준 앱 ID(`<PackageFamilyName>!App`)를 받아 그대로 쓰고 레지스트리에 아무것도 쓰지 않는다. 패키지 앱의 HKCU 쓰기는 가상화되어 알림 시스템이 보지 못하기 때문이다. 누르면 아무 일도 없다.
- Windows 폴더 열기·파일 열기: `explorer.exe /select,"<경로>"`·`explorer.exe "<경로>"`를 `SysProcAttr.CmdLine`으로 그대로 넘긴다. explorer는 성공해도 종료 코드 1이라 시작만 확인한다.
- Windows CLI 출력: exe가 `-H windowsgui`라 콘솔이 없다. 인자가 있으면 `AttachConsole(ATTACH_PARENT_PROCESS)`로 부모 콘솔에 붙어 출력한다. cmd·PowerShell은 GUI 프로그램을 기다리지 않으므로 출력이 프롬프트 뒤에 찍히고 종료 코드도 바로 안 나온다. 기다리려면 `start /wait Nalmeok.exe doctor`(cmd) 또는 `Start-Process -Wait -NoNewWindow .\Nalmeok.exe doctor`(PowerShell).
- 무료 전사 중 종료 확인: OS 대화상자가 아니라 앱 안의 카드다. `BeforeClose`가 창을 앞으로 가져오고 `quit:confirm` 이벤트를 보낸 뒤 닫기를 막는다. 카드에서 종료를 고르면 `ConfirmQuit`가 표시를 남기고 `runtime.Quit`를 부른다(Quit가 `BeforeClose`를 다시 부르므로 표시가 필요하다). Wails v2의 Windows `MessageBox`는 앱 아이콘과 버튼 이름을 받지 못해 바꿨다.
- `scripts/build_mac_app.sh`는 서명 정보가 없으면 `.app`을 임시 서명(ad-hoc)하고, 공증 자격이 없으면 `dist/`에 배포용 ZIP을 만들지 않는다. Info.plist와 번들 식별자를 묶어 알림이 앱 이름으로 붙게 한다.
- Codex 전사(실험)는 화면에 숨은 기능이다. 설정 시트에서 **Shift+E**를 누르면 `config.json`의 `experiments.codex`가 켜지거나 꺼지고, 토스트가 방향을 알려 준다. 켜져 있는 동안 설정 시트 맨 위에 **전사 방식** 스위치가 나타나 Gemini와 Codex를 고른다. `NALMEOK_EXPERIMENTS=codex`는 파일을 건드리지 않고 같은 스위치를 보여 준다.
- macOS 최소 버전은 13.0(Go가 지원하는 가장 오래된 macOS)이다. `scripts/build_mac_app.sh`의 `MIN_MACOS`가 cgo 빌드의 배포 대상(`MACOSX_DEPLOYMENT_TARGET`·`-mmacosx-version-min`)과 Info.plist의 `LSMinimumSystemVersion`을 함께 정한다. 배포 대상을 빼면 clang이 빌드한 Mac의 macOS 버전을 실행 파일에 박아서, 그보다 오래된 Mac은 "소프트웨어 업데이트"를 요구하며 열지 않는다(0.10.1까지 이 문제로 minos 27.0이었다). 확인: `vtool -show-build build/Nalmeok.app/Contents/MacOS/nalmeok`.

### CLI: 테스트·AI 에이전트용

CLI는 **사용자용 기능이 아니다.** 실제 API 회귀 테스트, AI 에이전트의 자동화, 문제 보고(`doctor`)에만 쓴다. README와 `usage.md`에는 싣지 않는다. 전사 로직은 GUI와 같은 실행기·저장소를 쓴다.

```bash
build/nalmeok [--free|--batch] <파일 또는 폴더>...   # 끝날 때까지 상태를 한 줄씩 출력. 모두 완료면 0, 아니면 1
build/nalmeok doctor                                  # 버전·OS·설정 폴더·등급별 키 개수 (키 값은 출력하지 않음)
build/nalmeok --version
```

실제 API 테스트는 `XDG_CONFIG_HOME`을 임시 폴더로 바꾸고 `config.json`만 복사해서 돌린다. 설정 폴더마다 프로세스 하나만 돈다: 앱과 CLI는 `<설정 폴더>/nalmeok.lock`을 잡고, 이미 잡혀 있으면 CLI는 종료 코드 1로 끝나고 앱은 안내 후 닫힌다. 앱을 두 번 열면 Wails 단일 인스턴스 잠금으로 기존 창이 앞으로 온다. `config.json`은 바꾸기 직전에 다시 읽어, 다른 프로세스가 바꾼 내용을 지우지 않는다. `NALMEOK_CONFIG_DIR`을 주면 `config.Dir`이 가장 먼저 그 폴더를 쓴다(실험·테스트를 실제 기록과 섞지 않을 때).

### 상태 신호

Gemini 요청 결과를 익명으로 모아 `https://seook.dev/nalmeok`의 `POST /api/signal`로 보낸다. 사이트가 모든 앱의 신호로 무료·Batch가 지금 잘 되는지 판단해 `/nalmeok/status/` 페이지와 `GET /api/status`로 내놓고, 앱은 `App.FreeStatus`로 그것을 읽는다. 사용자 설명은 `usage.md`의 "앱이 보내는 정보"에 있다.

- 기록 위치(`internal/runner`, `Options.Signal`, nil이면 기록하지 않음): 무료는 `freeAttempt`의 `Generate` 시도마다 한 건(`attempt`는 같은 요청의 1부터 센 재시도 번호)과 업로드 실패 한 건. 거부 후 3.6으로 다시 보낸 요청이나 빈 결과 재시도도 별도 `Generate` 한 건이다. Batch는 업로드 실패, 작업 제출(`SubmitBatch`) 한 건, 끝난 작업 한 건(성공 `ok`, 실패 `other`, 만료 `timeout`, 차단 `blocked`; 사용자가 취소한 작업은 빼고, 상태 확인 실패는 다시 확인하므로 기록하지 않는다). 자동 재제출한 job은 끝날 때 한 건 더 기록한다.
- 결과 분류는 `telemetry.Classify`. 취소된 요청은 기록하지 않는다. 실행기의 `blockedError`는 `blocked`, 응답은 왔지만 쓸 수 없는 경우(텍스트 없이 `MAX_TOKENS`, 다른 중단 이유)는 `other`. 텍스트가 있는 `MAX_TOKENS`는 잘린 채 저장하므로 `ok`.
- 키, 경로, 전사 내용, 오류 메시지는 보내지 않는다. 설치 ID는 UTC 날짜마다 새로 만들어 `<설정 폴더>/signal-id.json`에 둔다.
- `under_quota`(무료 429에만): 실행기는 키마다 RPM·TPM 아래로 간격을 둔다(`gap()`). 그러니 429는 하루 한도이거나 Google이 부하를 덜어 내는 경우다. 실행기가 키마다 이번 할당 날(미국 태평양 자정 기준, tzdata 없이 UTC-8 고정)에 보낸 `Generate` 수를 메모리에 세고, 이번 요청 전 수가 `freeDailyRequestsFloor`(20, 무료 등급 하루 요청 수의 보수적 하한)보다 적으면 true다. 앱 자신의 수라서 같은 키를 쓰는 다른 앱은 보이지 않고, 다시 켜면 0부터 센다.
- 상태 조회(`telemetry.Status`, 60초 캐시): 신호가 켜져 있으면 `GET /api/status?install=<오늘 ID>`로 묻는다. 서버가 그 ID를 뺀 최근 1시간 무료 실패 설치 수를 `free.others_failing`으로 더해 준다(ID는 저장하지 않음). 무료 항목이 실패했을 때 "다른 사용자도 실패 중"을 보여 주는 근거다. 꺼져 있으면 ID 없이 묻고 그 필드는 없다. 서버의 판정 기준(60분·8건·2곳, 모자라면 360분)은 seookdev README에 있다.
- 끄기: 설정의 "무료 키 상태 신호 보내기"(`config.json`의 `"signals": false`, 없으면 켜짐). `telemetry.Client`는 이벤트·전송마다 `config.json`을 다시 읽어 바로 따른다. 환경변수 `NALMEOK_SIGNALS=0`이면 아예 만들지 않는다.
- 개발 빌드(버전이 `-dev`로 끝남)는 보내지 않는다. `NALMEOK_SIGNAL_URL=<base URL>`을 주면 개발 빌드도 그 주소로 보낸다(로컬 Worker 시험용). 미리보기(`NALMEOK_UI_PREVIEW=1`)는 상태를 읽지 않는다.
- 전송은 30초마다 또는 50건이 모이면 한 번. 실패하면 그 묶음은 버리고 다시 보내지 않는다. 종료 때 남은 것을 최대 2초 기다려 보낸다. CLI 실행도 같은 규칙으로 보낸다.

### 문구 (문자열 카탈로그)

사용자에게 보이는 문구는 `frontend/dist/i18n/` 아래 언어별 JSON 파일에 있다. `en.json`이 기준 언어이고 `ko.json`이 같은 키를 한국어로 담는다. 화면, 오류 메시지, 항목 상태, 파일 선택 창, 알림, CLI 출력이 다 여기서 나온다. 평평한 JSON 객체이고 키는 `영역.이름`(`error.emptyResult`, `status.transcribing`, `cli.done` 등)이다. 값 안의 `{name}`, `{minutes}` 같은 자리는 부르는 쪽이 채운다. 복수형 처리는 없다(영어 문구도 단수·복수를 구분하지 않게 쓴다).

- Go: `i18n.T("cli.done", "name", name, "output", path)`는 문구를, `i18n.Error(키, ...)`는 그 문구를 메시지로 하는 오류를 돌려준다. `i18n.Wrap(키, err, ...)`는 원래 오류를 감싸고 문구의 `{error}` 자리에 넣는다. 오류 문구는 `Error()`를 부를 때 찾으므로 패키지 변수로 만든 오류도 언어를 따라간다. 파일은 `frontend` 패키지의 embed로 실행 파일에 들어간다.
- 화면: `app.js`가 시작할 때 기준 언어(`i18n/en.json`)와 OS 설정에서 고른 언어의 파일을 `fetch`로 읽는다(앱 안의 파일이라 오프라인에서도 된다). 코드에서는 `t("row.waitingMinutes", { minutes })`를 쓰고, `index.html`의 고정 문구는 비워 두고 `data-i18n="키"`로 채운다. 속성은 `data-i18n-aria-label`, `data-i18n-title`, `data-i18n-placeholder`를 쓴다.
- 없는 키는 기준 언어 문구로 대신하고, 기준 언어에도 없으면 키 이름이 그대로 보인다. `go test ./internal/i18n`이 이것을 막는다. 코드(Go·JS·HTML)가 쓰는 키가 모두 en.json에 있는지, en.json에 안 쓰는 키가 없는지, 모든 언어 파일이 en.json에 있는 키만 쓰고 `{자리}`가 같은지, Go 문자열과 JS·HTML·CSS에 카탈로그 밖 한글이 없는지 검사한다. 주석, `_test.go`, 전사 프롬프트(`internal/gemini/prompt.go`)는 예외다. 키는 문자열 그대로 써야 한다(`t(변수)`는 검사가 못 보므로 실패한다).
- 로그(`nalmeok.log`)와 개발자용 메시지는 카탈로그에 넣지 않는다.

문구를 추가할 때는 en.json에 키를 넣고 코드에서 그 키를 부른다. 다른 언어를 추가할 때는 `frontend/dist/i18n/<lang>.json`을 같은 키로 만들고, 테스트가 그 언어를 읽을 수 있는지 확인한다. 기본 언어는 en이고 언어는 실행할 때 OS 설정에서 고른다. CLI는 `LC_ALL`·`LC_MESSAGES`·`LANGUAGE`·`LANG`을 보고(`i18n.Detect`), 한국어면 ko, 그 밖의 로케일이나 설정 없음·`C`·`POSIX`면 기준 언어 en이다. GUI는 Wails 페이지의 `navigator.languages[0]`로 같은 규칙을 적용하고 `App.UseLanguage`로 Go 쪽 언어를 맞춘다. Finder에서 실행한 앱은 환경변수에 로케일이 없어 이 동기화가 필요하다.

### 화면 미리보기 (스크린샷용)

`NALMEOK_UI_PREVIEW=1`이면 GUI가 실제 실행기 대신 `StubRunner`를 쓴다. 저장된 `items.json` 상태가 그대로 남고 네트워크를 쓰지 않는다. 키 확인도 건너뛰므로 가짜 키를 넣은 임시 설정으로 화면을 볼 수 있다.

```bash
XDG_CONFIG_HOME=/tmp/uiseed NALMEOK_UI_PREVIEW=1 build/nalmeok
#   NALMEOK_UI_APPEARANCE=light|dark   창 모양을 고정한다
#   NALMEOK_UI_PREVIEW_JS='...'        DOM이 준비되고 1초 뒤 실행할 JS (시트 열기, 검색어 넣기 등)
```

창만 찍는다. 창 번호는 `CGWindowListCopyWindowInfo`를 PID로 걸러 얻고 `screencapture -x -o -l<창 번호>`로 저장한다. 화면 전체는 찍지 않는다.

## 작업 규칙 요약

- 동작·배포·구조를 바꾸면 관련 문서를 먼저 고친다. 문서가 코드와 어긋나 있으면 문서를 먼저 바로잡는다.
- 폐기된 제약(코드서명 필수, 런타임 의존성 금지, 단일 `nalmeok.py`)은 `CLAUDE.md`의 제약 조정 절을 따른다.

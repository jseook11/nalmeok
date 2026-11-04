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
internal/config     설정 폴더, config.json(키·gui.mode)
internal/store      items.json(처리 목록), 검색
internal/media      입력 스캔, MP4 AAC→M4A 리먹스·분할, MP3·WAV 분할 (표준 라이브러리만)
internal/plan       조각 계획(130분), 예상 비용
internal/gemini     genai SDK 래퍼: 업로드, live, Batch
internal/runner     실행기: 무료 키 순환, Batch, 한도 소진 전환, 재개
internal/output     결과 병합, .md 쓰기
internal/app        Wails 바인딩
internal/notify     완료 알림: macOS 앱 번들은 UNUserNotificationCenter, 번들 밖은 osascript, Windows는 토스트
internal/i18n       문구 카탈로그 읽기: T(키), Error(키)
frontend/dist       화면 (HTML/CSS/JS, 빌드 단계 없음)
frontend/dist/i18n  문구 카탈로그 (en.json 기준, ko.json 한국어)
```

전사 결과는 원본 옆 `.md`와 함께 설정 폴더의 `transcripts/<항목 ID>.md`에도 보관한다(`output.ArchivePath`). 화면은 원래 위치의 `.md`를 먼저 읽고, 그 파일이 옮겨지거나 지워졌을 때만 보관본을 보여 주며 "다시 찾기…"(`LocateOutput`)·"다시 저장"(`RestoreOutput`)을 낸다. 보관본이 생기기 전 항목은 처음 읽을 때와 앱을 켤 때 옮겨 두고, 목록에서 지우면 보관본도 지운다.

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

### 플랫폼별 동작

- 완료 알림(`internal/notify`): 실행기는 `Options.Notify`로 알림 함수를 받는다(테스트는 가짜를 넣고, 기본은 알리지 않음). macOS는 `.app` 안에서 실행될 때만 `UNUserNotificationCenter`를 쓴다. 번들 식별자가 없으면 이 API가 예외를 던지므로 `build/nalmeok`·CLI는 osascript로 보낸다(스크립트 편집기 이름으로 뜸). 첫 알림 때 권한을 묻고, 사용자가 거부하면(또는 나중에 끄면) 알리지 않는다. 앱 안에서는 osascript로 대신하지 않는다 — macOS는 거부도 오류(`UNErrorDomain` 1)로 돌려주므로, 대신 보내면 거부한 사용자에게 스크립트 편집기 이름으로 알림이 간다. 알림을 누르면 앱을 활성화하고 창을 앞으로(두 번째 실행과 같은 `showWindow`). 해당 행 강조는 하지 않는다. 확인은 Finder나 `open build/Nalmeok.app`으로 연 앱에서 한다(터미널에서 실행 파일을 바로 띄우면 거부될 수 있다).
- Windows 알림: PowerShell의 WinRT 연결로 토스트를 띄운다. 스크립트는 `-EncodedCommand`, 프로세스는 `CREATE_NO_WINDOW`. 앱 ID `kr.nalmeok.app`을 `HKCU\Software\Classes\AppUserModelId`에 이름·아이콘(`%LOCALAPPDATA%\Nalmeok\notify-icon.png`, `assets/nalmeok.ico`의 256px)과 함께 등록한다. 누르면 아무 일도 없다.
- Windows 폴더 열기·파일 열기: `explorer.exe /select,"<경로>"`·`explorer.exe "<경로>"`를 `SysProcAttr.CmdLine`으로 그대로 넘긴다. explorer는 성공해도 종료 코드 1이라 시작만 확인한다.
- Windows CLI 출력: exe가 `-H windowsgui`라 콘솔이 없다. 인자가 있으면 `AttachConsole(ATTACH_PARENT_PROCESS)`로 부모 콘솔에 붙어 출력한다. cmd·PowerShell은 GUI 프로그램을 기다리지 않으므로 출력이 프롬프트 뒤에 찍히고 종료 코드도 바로 안 나온다. 기다리려면 `start /wait Nalmeok.exe doctor`(cmd) 또는 `Start-Process -Wait -NoNewWindow .\Nalmeok.exe doctor`(PowerShell).
- 무료 전사 중 종료 확인: OS 대화상자가 아니라 앱 안의 카드다. `BeforeClose`가 창을 앞으로 가져오고 `quit:confirm` 이벤트를 보낸 뒤 닫기를 막는다. 카드에서 종료를 고르면 `ConfirmQuit`가 표시를 남기고 `runtime.Quit`를 부른다(Quit가 `BeforeClose`를 다시 부르므로 표시가 필요하다). Wails v2의 Windows `MessageBox`는 앱 아이콘과 버튼 이름을 받지 못해 바꿨다.
- `scripts/build_mac_app.sh`는 서명 정보가 없으면 `.app`을 임시 서명(ad-hoc)하고, 공증 자격이 없으면 `dist/`에 배포용 ZIP을 만들지 않는다. Info.plist와 번들 식별자를 묶어 알림이 앱 이름으로 붙게 한다.

### CLI: 테스트·AI 에이전트용

CLI는 **사용자용 기능이 아니다.** 실제 API 회귀 테스트, AI 에이전트의 자동화, 문제 보고(`doctor`)에만 쓴다. README와 `usage.md`에는 싣지 않는다. 전사 로직은 GUI와 같은 실행기·저장소를 쓴다.

```bash
build/nalmeok [--free|--batch] <파일 또는 폴더>...   # 끝날 때까지 상태를 한 줄씩 출력. 모두 완료면 0, 아니면 1
build/nalmeok doctor                                  # 버전·OS·설정 폴더·등급별 키 개수 (키 값은 출력하지 않음)
build/nalmeok --version
```

실제 API 테스트는 `XDG_CONFIG_HOME`을 임시 폴더로 바꾸고 `config.json`만 복사해서 돌린다. 설정 폴더마다 프로세스 하나만 돈다: 앱과 CLI는 `<설정 폴더>/nalmeok.lock`을 잡고, 이미 잡혀 있으면 CLI는 종료 코드 1로 끝나고 앱은 안내 후 닫힌다. 앱을 두 번 열면 Wails 단일 인스턴스 잠금으로 기존 창이 앞으로 온다. `config.json`은 바꾸기 직전에 다시 읽어, 다른 프로세스가 바꾼 내용을 지우지 않는다.

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

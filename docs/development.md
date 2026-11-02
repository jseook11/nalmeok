# 개발 가이드

> 사용자용 문서는 [`README.md`](../README.md), 상세 사용법은 [`usage.md`](usage.md)에 있습니다.

## 문서 지도

| 문서 | 소유하는 내용 |
|---|---|
| `README.md` | 설치, 처음 사용, 다운로드 |
| `docs/usage.md` | 화면 사용법, 비용, API 키 발급, FAQ |
| `old/` | 기존 Python 앱과 그 문서 (보존용) |
| 로컬 `CLAUDE.md` (저장소 밖) | 작업 지침, 폐기된 제약, macOS 자격 |

## Go 앱 (재설계, 진행 중)

[`redesign-2026-11.md`](redesign-2026-11.md)에 따라 Go + Wails로 다시 만들고 있다. 기존 Python 앱은 `old/`에 보존한다.

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
frontend/dist       화면 (HTML/CSS/JS, 빌드 단계 없음)
```

### 빌드

```bash
go test ./...
go build -tags desktop,production -o build/nalmeok ./cmd/nalmeok   # -o 필수: 루트의 Python nalmeok/ 폴더와 이름이 겹친다
scripts/build_mac_app.sh      # build/Nalmeok.app (arm64·amd64 범용, 아이콘 포함)
scripts/build_windows_app.sh  # build/windows/Nalmeok.exe(amd64)·Nalmeok-arm64.exe
```

`build/nalmeok`은 개발용 네이티브 바이너리다. `.app`에는 들어가지 않는다. `scripts/build_mac_app.sh`는 arm64와 amd64를 따로 빌드해 `lipo -create`로 하나로 합친다. Wails의 macOS 백엔드가 cgo를 쓰므로 각 조각을 `CC="clang -arch ..."`와 `CGO_CFLAGS`·`CGO_LDFLAGS="-arch ..."`로 빌드한다. 확인은 `lipo -archs build/Nalmeok.app/Contents/MacOS/nalmeok`(→ `x86_64 arm64`).

아이콘 원본은 꽉 찬 정사각형 그림 `assets/nalmeok-art.png`(1024px, 모서리 없음)다. 여기서 두 단계로 만든다. 결과물은 모두 커밋해 두고, 그림이 바뀔 때만 다시 만든다.

1. `uv run --no-project --with pillow python -I scripts/make_icon_master.py assets/nalmeok-art.png assets/nalmeok.png assets/nalmeok.ico` — macOS 격자(1024 안에 824 몸체, 둥근 모서리, 옅은 밝은 테두리, 그림자)로 자른 `assets/nalmeok.png`와 Windows용 여러 크기 `assets/nalmeok.ico`.
2. `scripts/make_icns.sh` — `assets/nalmeok.png`에서 `assets/nalmeok.icns`(sips로 크기별 PNG, iconutil로 묶음).

빌드 스크립트는 이 파일을 `Contents/Resources`에 복사하고 `CFBundleIconFile`로 가리킨다.

Windows용 실행 파일은 macOS·Linux에서 크로스 빌드한다. Wails의 Windows 백엔드는 순수 Go라 cgo가 필요 없다. exe의 아이콘과 애플리케이션 매니페스트는 `cmd/nalmeok/rsrc_windows_amd64.syso`·`rsrc_windows_arm64.syso`에 들어 있다. `GOOS=windows`로 빌드하면 Go 링커가 이 파일을 알아서 넣고, 다른 OS 빌드는 무시한다. `assets/nalmeok.ico`나 `scripts/winres/nalmeok.exe.manifest`를 바꾸면 저장소 루트에서 `go run ./scripts/winres`로 다시 만들어 커밋한다. 표준 라이브러리만 쓰는 작은 생성기라 따로 설치할 도구는 없다. 아이콘 그룹 ID는 3이다. Wails가 창 아이콘을 이 번호로 읽는다. 매니페스트는 asInvoker, Common Controls v6, PerMonitorV2 DPI, Windows 10·11, UTF-8 코드 페이지를 지정한다. `go test ./scripts/winres`는 커밋된 `.syso`가 지금 아이콘·매니페스트와 다르면 실패한다.

서명·공증은 환경변수가 있을 때만 한다. `NALMEOK_SIGN_IDENTITY="Developer ID Application: ..."`, `NALMEOK_NOTARY_PROFILE=nalmeok-notary`. 없으면 서명하지 않은 `.app`만 만든다.

### CLI: 테스트·AI 에이전트용

CLI는 **사용자용 기능이 아니다.** 실제 API 회귀 테스트, AI 에이전트의 자동화, 문제 보고(`doctor`)에만 쓴다. README와 `usage.md`에는 싣지 않는다. 전사 로직은 GUI와 같은 실행기·저장소를 쓴다.

```bash
build/nalmeok [--free|--batch] <파일 또는 폴더>...   # 끝날 때까지 상태를 한 줄씩 출력. 모두 완료면 0, 아니면 1
build/nalmeok doctor                                  # 버전·OS·설정 폴더·등급별 키 개수 (키 값은 출력하지 않음)
build/nalmeok --version
```

실제 API 테스트는 `XDG_CONFIG_HOME`을 임시 폴더로 바꾸고 `config.json`만 복사해서 돌린다. 프로세스 여러 개가 같은 `items.json`을 동시에 쓰지 않게 하기 위해서다.

### 화면 미리보기 (스크린샷용)

`NALMEOK_UI_PREVIEW=1`이면 GUI가 실제 실행기 대신 `StubRunner`를 쓴다. 저장된 `items.json` 상태가 그대로 남고 네트워크를 쓰지 않는다. 키 확인도 건너뛰므로 가짜 키를 넣은 임시 설정으로 화면을 볼 수 있다.

```bash
XDG_CONFIG_HOME=/tmp/uiseed NALMEOK_UI_PREVIEW=1 build/nalmeok
#   NALMEOK_UI_APPEARANCE=light|dark   창 모양을 고정한다
#   NALMEOK_UI_PREVIEW_JS='...'        DOM이 준비되고 1초 뒤 실행할 JS (시트 열기, 검색어 넣기 등)
```

창만 찍는다. 창 번호는 `CGWindowListCopyWindowInfo`를 PID로 걸러 얻고 `screencapture -x -o -l<창 번호>`로 저장한다. 화면 전체는 찍지 않는다.

## 기존 Python 앱 (`old/`)

기존 TUI 앱은 `old/`에 그대로 옮겨 보존한다. 구조·테스트·빌드는 [`old/docs/development.md`](../old/docs/development.md)를 따르고, 명령은 `old/`에서 실행한다(`cd old && python -m unittest discover -s tests -v`). CI(`.github/workflows/`)도 지금은 `old/`에서 Python 테스트·빌드만 돈다. Go 앱용 CI는 PR이 머지된 뒤 새로 만든다.

`internal/gemini/prompt_python_test.go`는 Go 프롬프트가 `old/nalmeok/constants.py`와 같은지 확인한다.

## 작업 규칙 요약

- 동작·배포·구조를 바꾸면 관련 문서를 먼저 고친다. 문서가 코드와 어긋나 있으면 문서를 먼저 바로잡는다.
- 폐기된 제약(코드서명 필수, 런타임 의존성 금지, 단일 `nalmeok.py`)은 `CLAUDE.md`의 제약 조정 절을 따른다.

# 개발 가이드

> 사용자용 문서는 [`README.md`](../README.md), 상세 사용법은 [`usage.md`](usage.md)에 있습니다.

## 문서 지도

| 문서 | 소유하는 내용 |
|---|---|
| `README.md` | 설치, 처음 사용, 다운로드 |
| `docs/usage.md` | 대시보드, CLI, 무료 live, 비용, API 키 발급, FAQ |
| `docs/drop-target.md` | 드래그앤드롭 입력 정규화·작업 폴더·결과 복사 결정 |
| `docs/windows-support.md` | Windows 지원 범위, 배포물 구조, 런처 계약, CI |
| `docs/macos-app.md` | 앱 번들, 부트스트랩, DMG, 업데이트, 서명(선택) |
| 로컬 `CLAUDE.md` (저장소 밖) | 작업 지침, 폐기된 제약, macOS 자격 |

## Go 앱 (재설계, 진행 중)

[`redesign-2026-11.md`](redesign-2026-11.md)에 따라 Go + Wails로 다시 만들고 있다. 기능을 다 갖추면 아래 Python 구조를 대체한다.

```text
cmd/nalmeok/        진입점: 인자 없으면 GUI, 인자가 있으면 CLI
internal/config     설정 폴더, config.json(키·gui.mode)
internal/store      items.json(처리 목록), 검색
internal/media      입력 스캔, MP4 AAC→ADTS 추출·분할, MP3·WAV 분할 (표준 라이브러리만)
internal/plan       조각 계획(130분), 예상 비용
internal/gemini     genai SDK 래퍼: 업로드, live, Batch
internal/runner     실행기: 무료 키 순환, Batch, 한도 소진 전환, 재개
internal/output     결과 병합, .md 쓰기
internal/app        Wails 바인딩
frontend/dist       화면 (HTML/CSS/JS, 빌드 단계 없음)
```

```bash
go test ./...
go build -tags desktop,production -o build/nalmeok ./cmd/nalmeok   # -o 필수: 루트의 Python nalmeok/ 폴더와 이름이 겹친다
scripts/build_mac_app.sh                                            # build/날먹.app (자격 환경변수가 있으면 서명·공증)
```

### CLI: 테스트·AI 에이전트용

CLI는 **사용자용 기능이 아니다.** 실제 API 회귀 테스트, AI 에이전트의 자동화, 문제 보고(`doctor`)에만 쓴다. README와 `usage.md`에는 싣지 않는다. 전사 로직은 GUI와 같은 실행기·저장소를 쓴다.

```bash
build/nalmeok [--free|--batch] <파일 또는 폴더>...   # 끝날 때까지 상태를 한 줄씩 출력. 모두 완료면 0, 아니면 1
build/nalmeok doctor                                  # 버전·OS·설정 폴더·등급별 키 개수 (키 값은 출력하지 않음)
build/nalmeok --version
```

실제 API 테스트는 `XDG_CONFIG_HOME`을 임시 폴더로 바꾸고 `config.json`만 복사해서 돌린다. 프로세스 여러 개가 같은 `items.json`을 동시에 쓰지 않게 하기 위해서다.

## 프로젝트 구조 (기존 Python 앱)

```text
app.py                     # 실행 진입점 (./app.py)
nalmeok/                   # 앱 패키지
  constants.py             # 버전·상수·공통 설정값
  runtime.py               # 로깅, 경로·설정 파일, 잠금, 원자적 쓰기
  keys.py                  # API 키 레지스트리
  update.py                # venv/SDK, manifest, 앱 패키지 업데이트, ffmpeg 설치
  term.py                  # 터미널 입력·렌더링·메뉴 (프롬프트 포함)
  media.py                 # ffmpeg/변환, 길이·비용 추정, 사용량·상태 표시
  engine.py                # Batch·live·혼합 파이프라인, 감시 프로세스
  screens.py               # 대시보드·폴더·세션 화면
  cli.py                   # 인자 파싱, 진입점
tests/test_nalmeok.py      # 순수 로직·상태 전이 테스트
tests/test_free_key_rotation.py  # 무료 키 429 순환 테스트
scripts/                   # 배포 빌드
  build_windows.py  build_macos.py  verify-windows.cmd
  installer.cmd  install.ps1  shortcuts.ps1  messages.ps1
  nalmeok.cmd  nalmeok-drop.cmd  bootstrap.ps1  bootstrap.env
macos/                     # Swift 런처, Info.plist, run.command, bootstrap.command
docs/                      # 플랫폼·사용·개발 문서
pyproject.toml             # 패키지 정의와 nalmeok 콘솔 스크립트
```

기존 모듈 사이의 호출은 `import nalmeok as app` 후 `app.<name>`으로 합니다. 상수도 `app.<NAME>`으로 씁니다. `nalmeok/__init__.py`의 `_export`가 각 모듈이 정의한 이름을 `app`에 자동으로 올리므로, 함수를 추가해도 따로 등록할 필요가 없습니다. 테스트의 `mock.patch.object(app, ...)`는 이 공용 이름을 바꿉니다.

구조 정리(`docs/review-2026-11.md` §4)에서 새로 떼어 내는 모듈은 일반 import를 쓰고, 테스트는 그 모듈 경로를 패치합니다.

## 정적 검사

```bash
uv run ruff check .
```

규칙은 `pyproject.toml`의 `[tool.ruff.lint]`에 고정했고, macOS CI가 같은 버전으로 검사합니다.

## 테스트

표준 라이브러리 `unittest`만 씁니다. SDK나 API 키 없이 실행됩니다.

```bash
uv run python -m unittest discover -s tests -v
# 또는
python -m unittest discover -s tests -v
```

`tests/__init__.py`가 있어 저장소 루트에서 `python -m unittest discover`만 실행해도 테스트를 찾습니다.

테스트 범위: Batch 가격 계산, 토큰 집계, 상태 판정, live 그룹핑(7×20분 → 2요청, 130분 상한), 무료/유료 키 레지스트리와 라벨·마이그레이션, 무료 키 429 순환, 혼합 배분(130/130/40), 혼합 결과 병합, live 상태·아카이브·CLI 인자, 설정 파일 권한, 원자적 쓰기, 경로 파싱, 재시도 대상 선택, 드롭 입력 정규화, 패키징(버전 동기화, `.cmd` ASCII, `.ps1` BOM), 업데이트 매니페스트.

## 빌드

| 대상 | 명령 | 문서 |
|---|---|---|
| Windows thin ZIP | `python scripts/build_windows.py` | [`scripts/README.md`](../scripts/README.md) |
| Windows offline ZIP | `python scripts/build_windows.py --offline` (Windows에서만) | [`docs/windows-support.md`](windows-support.md) |
| macOS DMG | `python3 scripts/build_macos.py` | [`docs/macos-app.md`](macos-app.md) |

## 작업 규칙 요약

- 동작·배포·구조를 바꾸면 관련 문서를 먼저 고친다. 문서가 코드와 어긋나 있으면 문서를 먼저 바로잡는다.
- `.cmd`는 순수 ASCII, 한국어는 UTF-8 BOM `.ps1`이 소유한다(`docs/windows-support.md` D19).
- 폐기된 제약(코드서명 필수, 런타임 의존성 금지, 단일 `nalmeok.py`)은 `CLAUDE.md`의 제약 조정 절을 따른다.

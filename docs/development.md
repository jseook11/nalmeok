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
| `CLAUDE.md` | 작업 지침, 폐기된 제약, macOS 자격 |

## 프로젝트 구조

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

모듈 사이의 호출은 `import nalmeok as app` 후 `app.<name>`으로 합니다. 이렇게 하면 테스트의 `mock.patch.object(app, ...)`가 그대로 동작하고, 모듈을 나눠도 패치 지점이 바뀌지 않습니다. 상수는 `from .constants import *`로 직접 가져옵니다.

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

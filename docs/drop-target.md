# 드래그앤드롭 전사 계획

> **상태:** 구현 완료 (2026-10-30, `feature/drop-target`, v0.5.0)
> **선행:** `feature/windows-support`(PR #3)를 `master`에 머지한 뒤 시작
> **목표:** 폴더/MP4/M4A를 바탕화면 바로가기에 끌어다 놓으면 전사하고, 결과를 `Documents\nalmeok`에 모읍니다.
> 이 문서가 단일 기준입니다.

## 구현 위치

| 기능 | 위치 |
|---|---|
| 입력 정규화·파일 작업 폴더 | `nalmeok.py` `normalize_drop_paths`, `file_job_folder`, `drop_jobs_dir` |
| 문서 폴더 복사 | `nalmeok.py` `copy_result_to_documents`, `result_copy_path`, `documents_dir` |
| 드롭 화면 흐름 | `nalmeok.py` `drop_flow`, `show_drop_summary` |
| CLI | `nalmeok.py` `parse_args`의 `drop`, `main` 분기 |
| 런처 | `scripts/nalmeok-drop.cmd` |
| 바로가기 | `scripts/shortcuts.ps1`, `scripts/installer.cmd` |
| 테스트 | `tests/test_nalmeok.py` `DropTests` |


## 1. 확정 결정

| ID | 결정 | 이유 | 결과 |
|---|---|---|---|
| DD1 | **바탕화면/시작 메뉴 바로가기 드롭**을 주 경로로 | 드롭 경로가 `%*` → `sys.argv`로 그대로 전달돼 raw TUI 붙여넣기 문제가 없음 | `nalmeok-drop.cmd` + 두 번째 `.lnk` |
| DD2 | 결과는 **`Documents\nalmeok`에 모음** | 비개발자가 결과를 찾기 쉬움. 소스 폴더의 상태 파일은 유지 | 완료된 `.md`만 복사, 이름 충돌은 ` (2)` |
| DD3 | **폴더는 그 자리에서**, **파일은 별도 작업 폴더**에서 | 같은 폴더의 다른 강의를 섞지 않음 | `%LOCALAPPDATA%\nalmeok\jobs\<stem>`에 하드링크, 실패 시 복사 |
| DD4 | 기존 `session_screen`/전사 흐름 재사용, **무료 우선** | 이미 검증된 키·비용·재개 흐름을 그대로 사용 | 새 파이프라인 없음 |
| DD5 | `nalmeok drop <paths...>` CLI + `nalmeok-drop.cmd` | 바로가기와 CLI 양쪽에서 같은 진입점 | `drop` 서브커맨드 |
| DD6 | 터미널 창 직접 드롭은 **후속** | Windows Terminal이 경로를 입력창에 붙여넣는 방식이라 raw TUI에서 불안정 | 필요 시 홈 화면 `d`(경로 붙여넣기) 폴백 |
| DD7 | 새 런타임 의존성 없음 | 배포 ZIP 크기/유지보수 | 표준 라이브러리만 |
| DD8 | 버전 `0.5.0` 목표 | Windows 0.4.0과 분리 | 머지 후 범프 |

## 2. 사용자 흐름

1. 사용자가 폴더/MP4/M4A를 **`nalmeok에 끌어놓기`** 바로가기 위에 떨어뜨린다.
2. Windows가 경로를 인자로 넘기고 `nalmeok-drop.cmd`가 콘솔을 연다.
3. `nalmeok drop <paths...>`가 입력을 정리한다.
   - 폴더: 그대로 작업 폴더로 사용.
   - 파일: `%LOCALAPPDATA%\nalmeok\jobs\<stem>`에 하드링크(불가 시 복사).
   - PDF 단독, 지원하지 않는 확장자는 오류 안내.
4. 예상 계획(파일 수/길이/무료·유료)과 결과 위치를 보여준다.
5. 키가 없으면 기존 키 등록 화면으로 이동한 뒤, 드롭한 작업을 이어간다.
6. 기존 무료 우선/혼합/유료 흐름으로 폴더를 순서대로 전사한다.
7. 완료된 `.md`를 `Documents\nalmeok`에 복사하고, 결과 폴더를 연다.
8. 여러 항목이면 마지막에 성공/실패 요약을 보여준다.

## 3. 출력 규칙

- `documents_dir()` = `Path.home() / "Documents" / "nalmeok"` (없으면 생성).
- 소스 결과: 기존 `output_path(folder)` = `<작업폴더>/<작업폴더명>.md`.
- 복사 대상: `documents_dir() / f"{folder.name}.md"`.
- 같은 이름이 있으면 `name (2).md`, `name (3).md` ...
- `shutil.copy2`로 복사. 복사 실패는 원본 결과를 유지하고 경고.
- 상태 파일·세그먼트·Batch 메타데이터는 소스/작업 폴더에 그대로 둔다.

## 4. 파일 작업 폴더

- Windows: `%LOCALAPPDATA%\nalmeok\jobs\<stem>`.
- POSIX: `~/.local/share/nalmeok/jobs/<stem>`.
- 같은 stem이 있으면 `stem-2`, `stem-3`.
- `os.link(source, job / source.name)` 시도 → `OSError`면 `shutil.copy2`.
- 완료 후에도 작업 폴더는 재개/감사를 위해 유지한다. 정리 정책은 후속.

## 5. CLI / 바로가기

- `COMMANDS`에 `drop` 추가. `paths`는 `nargs="+"`.
- `nalmeok drop ./lecture ./week1.mp4` 형태.
- `scripts/nalmeok-drop.cmd`:
  ```bat
  @echo off
  title nalmeok drop
  setlocal
  set "HERE=%~dp0"
  set "PYTHONUTF8=1"
  set "PYTHONIOENCODING=utf-8"
  if "%~1"=="" (
    echo 폴더나 MP4/M4A 파일을 이 파일 위에 끌어다 놓으세요.
    pause
    exit /b 1
  )
  "%HERE%runtime\python.exe" "%HERE%nalmeok.py" drop %*
  if errorlevel 1 pause
  ```
- `installer.cmd`가 두 번째 바로가기 `nalmeok에 끌어놓기.lnk`를 만든다.
  - Target: `%DEST%\nalmeok-drop.cmd`
  - WorkingDirectory: `%USERPROFILE%`
  - Icon: `%DEST%\nalmeok.ico`
- Explorer가 `.cmd` 바로가기에 드롭 경로를 인자로 넘기는지 Windows 실기에서 먼저 확인한다. 안 되면 데스크톱에 `.cmd` 파일을 두거나 `cmd.exe /c` 래퍼 바로가기로 바꾼다.

## 6. 테스트

- 단위:
  - 입력 정규화(폴더/파일/지원하지 않는 확장자/중복).
  - 작업 폴더 이름 충돌 suffix.
  - 하드링크 실패 시 복사 폴백.
  - 결과 복사 이름 충돌 suffix.
  - `drop` 인자 파싱.
- TUI:
  - 키가 있을 때/없을 때 흐름.
  - 여러 항목 순차 처리 요약.
- Windows 수동:
  - 바로가기에 폴더 1개/파일 1개/여러 개 드롭.
  - 공백·한글 경로.
  - 실패 시 창 유지(`pause`), 결과 폴더 열림.
  - 키 없음 → 등록 → 이어서 전사.

## 7. 열린 질문

- 완료 후 파일 작업 폴더 자동 삭제 여부 — **기본: 유지**. 재개·감사에 필요하므로 v1에서는 지우지 않습니다.
- 터미널 창 드롭(`d` 경로 붙여넣기) — **v1 제외**, 후속.
- `Documents`가 OneDrive로 리디렉트된 환경 — **해결**: Windows에서는 셸의
  `User Shell Folders\Personal` 값을 읽어 실제 문서 폴더를 찾고, 실패할 때만
  `Path.home()/Documents`로 폴백합니다.
- 여러 파일을 하나의 Batch/live 그룹으로 묶을지 — **파일별 처리**로 확정.

## 8. 검증 메모

- 단위 테스트: `DropTests` 11개 (정규화, 하드링크 폴백, 작업 폴더 suffix, 문서 복사 suffix,
  Windows 문서 폴더 해석·폴백, CLI 파싱).
- macOS: 126 tests pass (8 skip).
- Windows 11 (2026-10-30, SSH/ConPTY, 설치본 0.5.0):
  - 124 tests pass (3 skip).
  - `nalmeok-drop.cmd --help` → `drop --help` 인자 전달 확인.
  - `.lnk` → `.cmd` 경유 `--help` 전달, 종료 코드 0 확인.
  - `shortcuts.ps1`로 바탕화면·시작 메뉴에 `nalmeok`, `nalmeok에 끌어놓기` 2개 생성,
    한글 이름·대상 경로 확인.
  - 공백·한글 경로(`Drop Test\강의 1.txt`)가 인용을 거쳐 파이썬까지 전달되고
    "지원하지 않는 형식" 진단에 그대로 표시됨.
  - 실제 `week1.m4a` 드롭: `%LOCALAPPDATA%\nalmeok\jobs\week1` 생성, 원본과
    하드링크(`fsutil hardlink list`로 두 경로 확인), Esc 후 "드롭 결과" 요약·정상 종료.
- 남은 수동 확인: 탐색기에서 마우스로 실제 드래그(폴더 1개, 여러 파일) 1회.
  셸 경유 인자 전달은 위에서 확인했으므로 실패 시 `.cmd`를 바탕화면에 직접 두는 폴백만 검토.



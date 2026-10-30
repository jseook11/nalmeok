![날먹 — 강의 파일을 먹고 전사 노트를 만드는 캐릭터. 강의는 길고, 내 집중력은 짧다.](assets/readme-banner.png)

# 날먹 (nalmeok)

<div align="center">

[![Python](https://img.shields.io/badge/Python-3.10%2B-3776AB?logo=python&logoColor=white)](https://www.python.org/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Gemini Batch API](https://img.shields.io/badge/Gemini-Batch%20API-8E75B2?logo=googlegemini&logoColor=white)](https://ai.google.dev/gemini-api/docs/batch-api)
[![Free live](https://img.shields.io/badge/free%20tier-live%20supported-brightgreen)](docs/usage.md#%EB%AC%B4%EB%A3%8C-live-%EB%AA%A8%EB%93%9C)
[![uv](https://img.shields.io/badge/uv-ready-DE5FE9?logo=uv&logoColor=white)](https://docs.astral.sh/uv/)
[![ffmpeg](https://img.shields.io/badge/ffmpeg-required-007808?logo=ffmpeg&logoColor=white)](https://ffmpeg.org/)
[![Cost](https://img.shields.io/badge/1%EC%8B%9C%EA%B0%84-%EC%95%BD%2074%EC%9B%90-brightgreen)](docs/usage.md#%EB%B9%84%EC%9A%A9-%EB%B9%84%EA%B5%90)
![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Windows%20%7C%20Linux-lightgrey)

</div>

무료 등급에서도 쓸 수 있는 **live 모드**를 기본으로, 큰 작업은 **유료 Batch API**로 처리하는 강의 음성 전사(STT) CLI 도구입니다.

유료 Batch는 `gemini-3.8-flash` 기준 시간당 약 $0.053(약 74원)이고, 무료 등급은 live로 같은 모델을 한도 내에서 $0로 사용할 수 있습니다. 무료 live는 130분 단위로 자동 분할하고, RPM 5·TPM 250k를 넘지 않도록 요청 간격을 자동 조정합니다(최소 12초, 130분은 약 60초).

무료 키와 유료 키를 여러 개 등록하고 `free`/`paid` 라벨을 붙일 수 있습니다. **기본은 무료 키 live 전사**이고, 유료 Batch는 큰 작업을 위한 선택 사항입니다. 준비 화면에서 `p`(유료 Batch 포함)를 누르거나 CLI `auto`를 쓰면 무료 키를 먼저 소진하고, 무료 한도를 넘는 분량만 유료 Batch로 보냅니다. 예를 들어 300분짜리 파일 하나와 무료 키 2개(각 130분)라면 `무료 130분 + 무료 130분 + 유료 Batch 40분`이 됩니다.

## 빠른 시작

Python 3.10 이상이 필요합니다. 클론한 뒤 인자 없이 실행하면 최근 작업 대시보드가 열립니다.

### uv 사용 시 (권장)

```bash
git clone https://github.com/jseook11/nalmeok
cd nalmeok
uv run app.py
```

`uv sync`를 한 번 실행하면 콘솔 스크립트도 설치됩니다.

```bash
uv sync
uv run nalmeok
```

### uv 없이 실행

```bash
git clone https://github.com/jseook11/nalmeok
cd nalmeok
./app.py
```

> `google-genai` 라이브러리가 없으면 최초 실행 시 설치 여부를 물어보고, 저장소 안에 `.venv`를 만들어 그 안에서 다시 실행합니다. 시스템 파이썬은 건드리지 않습니다. `convert`·`estimate`·`stats`는 SDK 없이도 동작합니다.

무료 live는 **무료 등급 키**로 바로 사용할 수 있고, 유료 Batch를 쓰려면 **결제가 연결된 Gemini API 키**가 필요합니다. [API 키 발급 안내](docs/usage.md#gemini-api-키-발급)를 참고하세요. MP4 변환과 길이 측정에는 [ffmpeg 설치](docs/usage.md#사전-요구사항)도 필요합니다.

---

## Windows 설치 (비개발자용)

> **지원 범위:** Windows 11 x64(설치·실행·드롭 실기 검증 완료. Windows Terminal 화면 체크리스트 일부 대기). Windows 10 22H2(빌드 19045)는 best-effort이며, 2026-10-30 기준 실기 검증은 보류했습니다. 그 이전 Windows 10 빌드도 best-effort이고, Windows ARM64와 32비트는 지원하지 않습니다.

ZIP은 약 200KB입니다. 실행에 필요한 Python과 google-genai는 **첫 실행 때**, ffmpeg/ffprobe는 **처음 필요할 때**(MP4 변환, 130분 초과 live 분할) 자동으로 내려받습니다. 설치와 실행은 관리자 권한 없이 사용자 폴더에서만 이루어집니다.

### 1. ZIP 내려받기

[Releases](https://github.com/jseook11/nalmeok/releases)에서 최신 `nalmeok-<버전>-win64.zip`과 `SHA256SUMS.txt`를 내려받습니다.

### 2. 압축 풀기 전에 차단 해제

1. 내려받은 ZIP을 마우스 오른쪽 버튼으로 클릭합니다.
2. **속성** → **일반** 탭 아래의 **차단 해제(Unblock)** 를 체크합니다.
3. **확인**을 누릅니다.
4. 그다음 **모두 압축 풀기**로 풉니다.

> 차단 해제를 먼저 하지 않으면 SmartScreen이 `installer.cmd` 실행을 막을 수 있습니다. 압축을 푼 뒤에는 차단 해제가 파일에 적용되지 않으므로 순서를 지켜 주세요.

### 3. 설치

1. 압축을 푼 폴더에서 `installer.cmd`를 더블클릭합니다. 압축을 풀면 `installer.cmd`와 `app` 폴더만 보입니다. `app` 폴더는 열지 않아도 됩니다.
2. SmartScreen 경고가 나오면 **추가 정보** → **실행**을 누릅니다. 코드서명을 하지 않아 경고가 정상적으로 표시됩니다.
3. "설치가 끝났습니다"가 나오면 창을 닫습니다.

설치 위치는 `%LOCALAPPDATA%\Programs\nalmeok`이고, 바탕화면과 시작 메뉴에 **nalmeok**와 **nalmeok에 끌어놓기** 바로가기가 생깁니다. 레지스트리와 PATH는 건드리지 않습니다.

### 4. 첫 실행

1. 바탕화면의 **nalmeok** 바로가기를 더블클릭합니다.
2. 첫 실행이 **Python 3.12(약 45MB)** 와 **google-genai(약 20MB)** 를 내려받습니다. 콘솔에 진행 표시가 나오며, 한 번만 받으면 다음부터는 바로 시작합니다.
3. 첫 화면에서 **무료 키** 또는 **유료 키**를 고르고 API 키를 등록합니다. ([Gemini API 키 발급](docs/usage.md#gemini-api-키-발급))
4. 전사할 강의 폴더를 고르고 Enter로 시작합니다. (대시보드에서는 **새 전사**를 먼저 고릅니다.)
5. MP4를 처음 변환하거나 130분이 넘는 오디오를 live로 나눌 때 **ffmpeg/ffprobe(약 160MB)** 를 받을지 물어봅니다. 그런 작업이 없다면 받지 않습니다.

### 5. 드래그앤드롭으로 전사하기

터미널 메뉴를 거치지 않고 바로 전사할 수 있습니다.

1. 탐색기에서 전사할 폴더나 MP4/M4A 파일을 선택합니다.
2. 바탕화면의 **nalmeok에 끌어놓기** 바로가기 위에 끌어다 놓습니다.
3. 예상 분량과 비용을 확인하고 Enter를 누릅니다.
4. 끝나면 결과 `.md`가 **문서 폴더의 `nalmeok`**(보통 `%USERPROFILE%\Documents\nalmeok`, OneDrive로 리디렉트된 PC는 그 문서 폴더)에 복사됩니다.

- **폴더**는 그 자리에서 전사하고 결과만 문서 폴더로 복사합니다. 폴더 안의 상태 파일과 원본은 그대로 둡니다.
- **MP4/M4A 파일 하나**는 `%LOCALAPPDATA%\nalmeok\jobs\<확장자를 뺀 파일명>`에 별도 작업 폴더를 만들어 처리합니다(이름이 겹치면 `-2`, `-3`). 같은 폴더의 다른 강의와 섞이지 않으며, 원본 파일은 하드링크로 연결하고 안 되면 복사합니다.
- 여러 항목을 한 번에 떨어뜨려도 순서대로 처리하고 마지막에 성공·실패를 요약합니다.
- 한 강의가 `part1.mp4`, `part2.mp4`처럼 여러 파일이면 **폴더째** 떨어뜨리세요. 파일을 따로 떨어뜨리면 파일마다 결과가 하나씩 생깁니다.
- 이름이 겹치면 `강의 (2).md`처럼 번호를 붙여 저장합니다.

### 6. 오프라인 설치 (선택)

인터넷 없이 설치해야 한다면 `nalmeok-<버전>-win64-offline.zip`(약 165MB)을 사용하세요. Python·google-genai·ffmpeg가 들어 있어 첫 실행에 다운로드가 필요 없습니다. 설치와 사용법은 동일합니다.

### Windows 문제 해결

- **SmartScreen이 실행을 막습니다.** ZIP을 풀기 전에 속성에서 **차단 해제**를 했는지 확인하세요. 그래도 막히면 **추가 정보 → 실행**을 선택합니다.
- **Windows Terminal 사용을 권장합니다.** Windows 10 기본 콘솔(conhost)보다 색상·키 입력·창 크기 변경이 안정적입니다. Microsoft Store에서 "Windows Terminal"을 설치한 뒤 바로가기를 Windows Terminal로 열어도 됩니다.
- **화면이 멈춘 것처럼 보입니다.** 콘솔에서 글자를 드래그하면 QuickEdit 때문에 앱이 잠시 멈춥니다. Enter나 Esc를 누르면 돌아옵니다. 자주 겪으면 콘솔 속성 → 옵션에서 **빠른 편집(QuickEdit) 모드**를 끄세요.
- **오류 창이 금방 닫힙니다.** `nalmeok.cmd`는 종료 코드가 0이 아니면 `pause`로 창을 유지합니다. 그래도 닫히면 설치 폴더에서 `nalmeok.cmd`를 직접 실행하세요.
- **Ctrl+C로 종료하면 "일괄 작업을 끝내시겠습니까?(Y/N)"가 뜹니다.** 배치 런처 특성입니다. TUI의 `q` 또는 종료 메뉴를 사용하면 나오지 않습니다. 프롬프트가 뜨면 `Y`를 누르면 됩니다.
- **첫 실행이 오래 걸립니다.** Python(약 45MB)과 google-genai(약 20MB)를 내려받는 중입니다. 진행 표시가 멈추면 네트워크·프록시를 확인하고 바로가기를 다시 실행하세요.
- **ffmpeg 다운로드가 실패합니다.** MP4 변환을 처음 할 때 약 160MB를 받습니다. 네트워크를 확인한 뒤 다시 시도하거나, 이미 설치된 ffmpeg가 있으면 `NALMEOK_FFMPEG` 환경변수로 그 폴더를 지정하세요.
- **진단 정보가 필요합니다.** 설치 폴더에서 `nalmeok.cmd doctor`(예: `%LOCALAPPDATA%\Programs\nalmeok\nalmeok.cmd doctor`)를 실행하면 Windows 버전, 터미널, VT 지원, 인코딩, ffmpeg/ffprobe, SDK, 설정 경로를 한 번에 확인할 수 있습니다. 이슈에는 이 출력과 `winver`, 실행 방법, 스크린샷을 붙여 주세요.
- **제거하려면** `%LOCALAPPDATA%\Programs\nalmeok`(앱), `%LOCALAPPDATA%\nalmeok`(Python·SDK·ffmpeg·작업 폴더), 바탕화면·시작 메뉴의 `nalmeok`, `nalmeok에 끌어놓기` 바로가기를 삭제합니다. `%APPDATA%\nalmeok`의 설정과 키는 별도이므로 필요하면 함께 삭제하세요.
- **다운로드 무결성 확인(선택).** PowerShell에서 `Get-FileHash .\nalmeok-<버전>-win64.zip -Algorithm SHA256`을 실행해 `SHA256SUMS.txt`의 값과 비교합니다.

---

## macOS 설치 (비개발자용)

> **지원 범위:** macOS 12 Monterey 이상, **Apple Silicon(arm64)**. Intel Mac은 아직 지원하지 않습니다.

Python·brew·git을 설치할 필요가 없습니다. 앱은 Developer ID로 서명·공증되어 Gatekeeper 경고 없이 실행됩니다. 단, **MP4 변환과 130분 초과 live 분할에는 아직 ffmpeg가 필요합니다**(3번 사용 절 참고).

### 1. 설치

1. [Releases](https://github.com/jseook11/nalmeok/releases)에서 최신 `nalmeok-<버전>-macos-arm64.dmg`를 내려받습니다.
2. DMG를 열고 **nalmeok.app**을 **Applications** 폴더로 드래그합니다.
3. Applications에서 **nalmeok**을 더블클릭합니다. Terminal 창이 열리고 첫 실행이 시작됩니다.

### 2. 첫 실행

첫 실행에서 필요한 것만 자동으로 준비합니다.

```text
Python 3.12 준비 중...       약 25MB
google-genai 설치 중...      약 20MB
```

- 관리자 비밀번호를 요구하지 않습니다.
- 앱 코드·Python·SDK는 `~/Library/Application Support/nalmeok/`에, 설정과 API 키는 `~/.config/nalmeok/`에 저장됩니다.
- API 키를 등록하면 바로 전사할 수 있습니다.

### 3. 사용

- Terminal 창에 **폴더/MP4/M4A를 끌어다 놓으면** 바로 전사합니다.
- 결과는 `~/Documents/nalmeok/`에 복사됩니다.
- 업데이트는 홈 화면에서 `v`로 확인합니다(DMG로 설치하면 `nalmeok` 명령이 PATH에 없습니다). 앱 코드만 바뀐 경우 앱 파일 하나(약 260KB)만 내려받습니다.
- **MP4 변환에는 아직 ffmpeg가 필요합니다.** 현재는 `brew install ffmpeg`로 설치하거나, 130분 이하의 M4A 파일을 사용하세요. macOS용 자동 설치(온디맨드)는 준비 중입니다.

### 4. 문제 해결

- **"확인되지 않은 개발자" 경고가 뜹니다.** Releases의 DMG인지 확인하세요. GitHub Actions의 `unsigned` 아티팩트는 테스트용이며 배포용이 아닙니다.
- **아무 반응이 없습니다.** 앱은 Dock 아이콘 없이 Terminal 창을 엽니다. Terminal이 뒤에 열려 있지 않은지 확인하세요.
- **Intel Mac입니다.** 현재 arm64 전용이라 실행되지 않습니다.
- **제거하려면** `nalmeok.app`, `~/Library/Application Support/nalmeok`, 필요하면 `~/.config/nalmeok`과 `~/Documents/nalmeok`을 삭제합니다.
- **무결성 확인(선택).** `shasum -a 256 nalmeok-<버전>-macos-arm64.dmg`를 `SHA256SUMS.txt`와 비교합니다.

---

## 처음 사용

인자 없이 실행하면 **키 → 작업 → 진행** 3화면이 이어집니다.

1. 키가 없으면 첫 화면에서 **무료 키** 또는 **유료 키**를 고르고 API 키를 등록합니다. ([키 발급 안내](docs/usage.md#gemini-api-키-발급))
2. **새 전사**에서 폴더를 고르고 Enter를 누릅니다. 무료 키가 있으면 무료 live로 처리하고, `p`를 누르면 무료 키로 처리하고 남는 분량만 유료 Batch로 보냅니다.
3. 끝나면 `<폴더>/<폴더명>.md`가 생깁니다.

| 키 | 동작 |
| --- | --- |
| `↑`/`↓`, `Enter`, `Esc` | 이동 · 선택 · 뒤로 |
| `d` | 드롭 · 경로 입력 |
| `k` | API 키 관리 |
| `u` / `s` | 사용량 / 설정 |
| `q` | 종료 |

폴더나 MP4/M4A를 터미널 창에 끌어다 놓아도 바로 전사합니다.

## 더 알아보기

- **[사용 설명서](docs/usage.md)** — 대시보드 전체 화면, CLI, 무료 live 모드, 비용 비교, API 키 발급, 작업 폴더, FAQ
- [드래그앤드롭 사양](docs/drop-target.md)
- [Windows 배포·지원](docs/windows-support.md)
- [macOS 앱 패키징](docs/macos-app.md)
- [개발 가이드](docs/development.md) — 프로젝트 구조, 테스트, 빌드

---

## 라이선스

MIT License

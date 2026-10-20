![날먹 — 강의 파일을 먹고 전사 노트를 만드는 캐릭터. 강의는 길고, 내 집중력은 짧다.](assets/readme-banner.png)

# 날먹 (nalmeok)

<div align="center">

[![Python](https://img.shields.io/badge/Python-3.10%2B-3776AB?logo=python&logoColor=white)](https://www.python.org/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Gemini Batch API](https://img.shields.io/badge/Gemini-Batch%20API-8E75B2?logo=googlegemini&logoColor=white)](https://ai.google.dev/gemini-api/docs/batch-api)
[![uv](https://img.shields.io/badge/uv-ready-DE5FE9?logo=uv&logoColor=white)](https://docs.astral.sh/uv/)
[![ffmpeg](https://img.shields.io/badge/ffmpeg-required-007808?logo=ffmpeg&logoColor=white)](https://ffmpeg.org/)
[![Cost](https://img.shields.io/badge/1%EC%8B%9C%EA%B0%84-%EC%95%BD%2074%EC%9B%90-brightgreen)](#%EB%B9%84%EC%9A%A9-%EB%B9%84%EA%B5%90)
![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)

</div>

Gemini Flash 모델과 **Batch API**를 사용해 강의 음성을 저비용으로 텍스트로 변환(STT)하는 CLI 도구입니다.

기본 모델(`gemini-3.8-flash`) 기준 시간당 약 $0.053 (약 74원)이 소요되며, 실시간 STT API 대비 비용을 대폭 낮췄습니다.

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

전사를 제출하려면 **결제가 연결된 Gemini API 키**가 필요합니다. [API 키 발급 안내](#gemini-api-키-발급)를 참고하세요. MP4 변환과 길이 측정에는 [ffmpeg 설치](#사전-요구사항)도 필요합니다.

---

## 터미널 대시보드

인자 없이 실행하면 **키 → 작업 → 진행** 3화면으로 이어집니다. 메뉴를 찾아 헤맬 필요 없이, 폴더를 고르고 Enter만 누르면 됩니다.

### 1. API 키 (처음 한 번만)

키가 없으면 첫 화면에서 붙여넣습니다. Enter를 누르면 과금 없이 유효성을 확인하고 `~/.config/nalmeok/config.json`(0600)에 저장한 뒤 바로 작업 화면으로 넘어갑니다. `GEMINI_API_KEY` 환경변수가 있으면 이 화면은 건너뜁니다. 화면에 키가 남지 않으며, 입력은 `•`로만 표시됩니다.

```text
  API 키   Gemini 결제 프로젝트 키 · ~/.config/nalmeok/config.json
  ───────────────────────────────────────────

  붙여넣고 Enter. 입력은 화면에 남지 않습니다.

  ❯ GEMINI_API_KEY   ••••••••••••••••••••

  API ✓ · ffmpeg ✓ · SDK ✓
```

### 2. 작업

```text
  날먹   강의 음성을 Batch로 전사   v0.2.0
  ───────────────────────────────────────────

  작업

  ❯ ch09        전사 중 · 12:03 경과
    ch07_08     완료 · 오디오 2개
  ───────────────────────────────────────────
    새 전사      폴더를 고르거나 경로를 붙여넣기
    여러 폴더 제출  2개 준비됨
    원격 상태 새로고침
    사용량
    설정
    종료

  API ✓ · ffmpeg ✓ · SDK ✓
  ↑↓ 이동 · Enter 선택 · k 키 · u 사용량 · s 설정 · m 일괄 · q 종료
```

### 3. 진행

폴더를 고르면 상태에 맞는 작업이 한 화면에서 이어집니다.

| 폴더 상태 | 화면에서 하는 일 |
| --- | --- |
| 준비됨 / 제출 전 | 예상 비용 확인 → MP4 변환 → 업로드 → 완료 대기 → 결과 병합 |
| 제출됨 | 완료 대기. `Esc`는 백그라운드 전환 |
| 전사 중 | 백그라운드 상태와 로그 |
| 결과 수신 | 결과 병합 |
| 완료 | 결과 열기 · 비용 보기 · 새 작업 시작 |
| 파일 없음 / 변경됨 / 손상 | 안내 후 고급 메뉴로 위임 |

```text
  전사   ~/lectures/ch10
  ───────────────────────────────────────────

  오디오 2개 · PDF 참고자료

  파일         2개 · 총 1시간 12분
  입력 토큰    109,535 (오디오 109,535 + 프롬프트 0)
  출력 토큰    9,000 ~ 10,800
  예상 비용    $0.0572 (약 80원)

  API ✓ · ffmpeg ✓ · SDK ✓
  Enter 전사 시작 · e 비용 · b 파일 · a 고급 · Esc 뒤로
```

* 대기 화면은 스피너와 흐르는 막대로 경과 시간을 보여줍니다. `Esc` 또는 `b`로 **백그라운드 대기**로 전환하면 완료 시 알림이 오고 결과가 자동으로 병합됩니다.
* 대시보드는 네트워크를 호출하지 않습니다. 제출한 Batch의 원격 상태는 `r`(원격 상태 새로고침)을 눌렀을 때 한 번에 조회합니다.
* **여러 폴더 한꺼번에 제출**(`m`)에서 Space로 폴더를 고르면 예상 비용을 한 번만 확인하고 일괄 제출합니다.
* 개별 변환·비용 상세·재시도·보관 같은 세밀한 제어는 폴더 화면에서 `a`를 누르면 나오는 **고급 메뉴**에 그대로 있습니다.
* **결과 열기**는 `$VISUAL`/`$EDITOR`가 있으면 그 에디터로, 없으면 OS 기본 앱으로 엽니다.
* 설정·폴더 열기·여러 폴더 제출·진행 상황 화면도 같은 목록 스타일과 하단 키 안내를 씁니다.

| 키 | 동작 |
| --- | --- |
| `↑` / `↓` | 이동 |
| `Enter` / `→` | 선택·시작 |
| `←` / `Esc` | 뒤로 (대기 중이면 백그라운드 전환) |
| `b` | 대기 중 백그라운드 전환 |
| `k` | API 키 변경 |
| `u` | 사용량 |
| `s` | 설정 |
| `m` | 여러 폴더 한꺼번에 제출 |
| `r` | 원격 상태 새로고침 |
| `a` | (전사·완료 화면) 고급 메뉴 |
| `?` | 도움말 |
| `Space` | 여러 폴더 선택에서 선택/해제 |
| `q` / `Ctrl+C` | 종료 |

---

## 비용 비교

2026년 10월 기준 공식 요율 및 실제 전사 테스트 기준입니다. (`gemini-3.8-flash` Batch 요금: 1M 토큰당 입력 $0.375 / 출력 $1.875)

| 방식 | 1시간 비용 | 원화 (환율 1,400원) | 비고 |
| --- | --- | --- | --- |
| **본 도구 (Gemini 3.8 Flash Batch)** | **$0.053** | **약 74원** | 기본 설정 |
| Gemini 3.5 Flash-Lite Batch | $0.026 | 약 36원 | 초저비용 모드: `--model gemini-3.5-flash-lite` |
| Gemini 3.8 Flash (실시간 API) | $0.106 | 약 148원 |  |
| OpenAI gpt-4o-mini-transcribe | $0.180 | 약 250원 |  |
| Gemini 3.5 Transcribe (음성 전용) | $0.300 | 약 420원 |  |
| OpenAI Whisper / gpt-4o-transcribe | $0.360 | 약 500원 | 약 7배 차이 |

> **단가 산정 기준**
> * **Batch API 50% 할인**: 즉시 응답 대신 비동기 큐(최대 24시간 이내 완료)로 처리하여 절반 요금 적용.
> * **고정 오디오 토큰**: 오디오 입력은 초당 25토큰(1시간 90,000 토큰, 입력비 약 $0.034)으로 환산.
> * **Thinking 최소화**: `thinking_level`을 `low`로 두어 불필요한 출력 토큰 청구를 줄입니다. (thinking도 출력으로 청구됩니다.)

### 실제 실행 데이터

| 작업 폴더 | 실제 오디오 길이 | 입력 토큰 | 출력 토큰 | 청구액 (Google Cloud 기준) |
| --- | --- | --- | --- | --- |
| `ch07_08` | 1시간 12분 | 109,535 | 11,048 | $0.0618 (약 87원) |
| `ch09` | 55분 | 82,703 | 9,871 | $0.0495 (약 69원) |
| **합계** | **2시간 7분** | **192,238** | **20,919** | **$0.1113 (약 156원)** |

---

## 사전 요구사항

| 항목 | 필요한 기능 | 없어도 되는 기능 |
| --- | --- | --- |
| **ffmpeg** | `convert`(MP4 → M4A), `estimate`(길이 측정), MP4만 있는 폴더의 `upload`(자동 변환) | — |
| **Gemini API 키** | `upload` · `status` · `wait` · `download` | `convert` · `estimate` · `stats` |

ffmpeg 설치:

```bash
# macOS
brew install ffmpeg

# Ubuntu / Debian
sudo apt install ffmpeg
```

설치 확인:

```bash
ffmpeg -version
```

> **API 키는 무료 등급으로 사용할 수 없습니다.** Batch API는 결제 프로필이 연결된 종량제(Pay-as-you-go) 프로젝트에서만 동작합니다. 발급 절차는 아래 [Gemini API 키 발급](#gemini-api-키-발급)을 참고하세요.

---

## Gemini API 키 발급

Batch API는 무료 등급에서 사용할 수 없습니다. **결제가 연결된 Google Cloud 프로젝트**에서 키를 발급해야 합니다. 아래 순서대로 따라 하면 됩니다.

### 1단계 · 결제 프로필 만들기 (최초 1회)

1. [Google Cloud Console → 결제](https://console.cloud.google.com/billing)에 접속합니다.
2. **결제 계정 만들기**를 눌러 국가와 카드 정보를 입력합니다. (신규 가입 시 크레딧이 제공되기도 합니다.)
3. 방금 만든 결제 계정을 사용할 **프로젝트에 연결**합니다.

> 이미 결제가 연결된 프로젝트가 있다면 이 단계는 건너뛰세요.

### 2단계 · API 키 발급

1. [Google AI Studio → API 키](https://aistudio.google.com/apikey)에 접속해 Google 계정으로 로그인합니다.
2. 오른쪽 위 **Create API key**(API 키 만들기)를 누릅니다.
3. 위에서 결제를 연결한 **프로젝트를 선택**합니다.
4. 생성된 `AIza...`로 시작하는 키를 복사합니다.

> 키 전체는 생성 직후에만 표시됩니다. 반드시 안전한 곳에 보관하세요.

### 3단계 · 키 등록

환경변수로 넘기거나 `--key` 옵션으로 직접 전달합니다.

```bash
# 현재 셸에만 적용
export GEMINI_API_KEY="AIza..."

# 영구 등록 (zsh)
echo 'export GEMINI_API_KEY="AIza..."' >> ~/.zshrc && source ~/.zshrc

# bash라면
echo 'export GEMINI_API_KEY="AIza..."' >> ~/.bashrc && source ~/.bashrc
```

```bash
# 또는 실행할 때마다 직접 지정
./app.py upload ./lecture_folder --key "AIza..."
```

키를 찾는 순서는 `--key` → `GEMINI_API_KEY` 환경변수 → 저장해 둔 키 순서입니다.

### 3-1단계 · 키 저장 (선택, 권장)

터미널 메뉴(`./app.py`)로 실행하면 키가 없을 때 **최초 1회만** 물어보고, 이어서 "저장할까요?"를 묻습니다. **저장**을 고르면 `~/.config/nalmeok/config.json`에 **소유자만 읽을 수 있는 권한(0600)** 으로 남겨 두고 다음 실행부터는 묻지 않습니다. (Windows는 `%APPDATA%\nalmeok\config.json`)

```bash
# 저장 확인
cat ~/.config/nalmeok/config.json

# 저장된 키 지우기 (파일 전체 삭제도 됩니다)
rm ~/.config/nalmeok/config.json
```

> 키를 저장하지 않으려면 메뉴에서 **이번만 사용**을 고르면 됩니다. 저장한 키가 유효하지 않으면(401 오류) 자동으로 지워져 다음 실행 때 다시 물어봅니다.

### 4단계 · 동작 확인

`convert`·`estimate`·`stats`는 키 없이 동작하므로, 키 검증은 첫 업로드로 합니다.

```bash
./app.py estimate ./lecture_folder   # 키 불필요
./app.py upload ./lecture_folder     # 키 필요 — 여기까지 통과하면 정상
```

### 자주 겪는 문제

| 증상 | 원인 / 해결 |
| --- | --- |
| `429 RESOURCE_EXHAUSTED` | 무료 등급 키입니다. 프로젝트에 결제 프로필을 연결하세요. |
| `403 PERMISSION_DENIED` | 키가 다른 프로젝트에 속해 있습니다. 해당 프로젝트에 결제가 연결됐는지 확인하세요. |
| `API key not valid` | 복사한 키 앞뒤의 공백·줄바꿈을 확인하세요. |
| `API 키가 필요합니다` | `GEMINI_API_KEY`가 export 되지 않았습니다. 새 터미널에서 export 후 다시 실행하거나, 터미널 메뉴에서 한 번 입력하고 **저장**을 선택하세요. |

---

## 작업 폴더 구조

하나의 폴더가 하나의 전사 작업 단위가 됩니다.

```
lecture_folder/
├── 01.mp4                    # convert 또는 upload 시 M4A로 추출 (원본 유지)
├── 02.m4a
├── 03.m4a
├── 참고자료.pdf               # (선택) 전문 용어 매칭용 문맥 파일 (최대 1개)
├── lecture_folder.md          # 전사 완료 후 생성되는 최종 결과물
├── .gemini_batch.*            # 상태·요청·결과 체크포인트 (자동 생성)
├── .gemini_batch.wait.log     # 백그라운드 대기 로그 (자동 생성)
└── .nalmeok-archive/<시각>/    # 새 작업 시작 시 상태·결과·Markdown 보관
```

* **입력 제한**: 폴더당 `.m4a` 오디오 파일 1~10개, 참고 PDF 최대 1개.
* **PDF 참고자료**: 전사 시 누락되거나 왜곡되기 쉬운 도메인 용어 매칭 참고용으로만 프롬프트에 주입됩니다.
* **보관 기록**: `새 작업 시작`을 고르면 상태·결과 파일만 `.nalmeok-archive/<시각>/`으로 이동합니다. 미디어 원본은 그대로 남습니다.

---

## CLI 사용법

인자 없이 `./app.py`를 실행하면 최근 작업 대시보드가 열립니다. 자동화할 때는 아래 서브커맨드를 쓰세요. 백그라운드 대기는 `wait --background`로도 시작할 수 있습니다. 원샷 파이프라인(변환·비용 확인·제출·대기·병합)은 대시보드 전용입니다.

```bash
export GEMINI_API_KEY="your-api-key"

# 1. MP4에서 음성 추출 (M4A 생성, 원본 보존)
./app.py convert ./lecture_folder

# 2. 오디오 길이 기준 예상 토큰 및 청구 금액 확인
./app.py estimate ./lecture_folder

# 3. 오디오 업로드 및 Batch 전사 작업 제출 (M4A가 없으면 MP4에서 자동 추출, 제출 전 예상 비용 확인)
./app.py upload ./lecture_folder

# 4. Batch 작업 처리 상태 확인
./app.py status ./lecture_folder

# 5. 완료까지 대기 후 자동 다운로드 (상태와 경과 시간을 실시간으로 표시)
./app.py wait ./lecture_folder --download

# 5-1. 기다리지 않고 백그라운드 감시 시작 (완료되면 자동 병합)
./app.py wait ./lecture_folder --background

# 6. 전사 결과 다운로드, 취합 및 단일 마크다운(.md) 생성
./app.py download ./lecture_folder

# 누적 사용 비용 통계 확인
./app.py stats
```

> `upload`는 길이·토큰·예상 금액을 보여주고 확인을 받은 뒤 제출합니다. 사람 없이 돌리는 자동화에서는 `--yes`로 확인을 건너뜁니다. 폴더에 M4A가 없고 MP4만 있으면 M4A를 먼저 만들어 준비하므로 `convert`를 따로 실행하지 않아도 됩니다.
>
> `wait`는 Batch가 끝날 때까지 폴링하며 완료되면 터미널 벨과 (가능하면) macOS 알림으로 알려줍니다. `--download`를 붙이면 곧바로 결과 병합까지 이어서 수행합니다. `--background`는 분리된 프로세스로 감시를 시작하고 PID만 출력합니다. 진행 상태는 폴더의 `.gemini_batch.wait.json`, 로그는 `.gemini_batch.wait.log`에 남으며, 일부 파일이 실패하면 자동 병합을 멈추고 오류를 기록합니다. 그때는 `download --retry live` 또는 `--retry batch`로 재시도하세요.

---

## 처리 파이프라인 및 안전장치

* **상태 기반 대시보드**: 최근 작업과 현재 폴더 아래의 작업을 모아 `폴더 없음 · 파일 없음 · 입력 확인 필요 · 준비됨 · 제출 전 · 제출됨 · 전사 중 · 결과 수신 · 완료 · 입력 변경됨 · 상태 손상`으로 구분합니다. 메뉴는 이 상태에서 파생되므로 이미 끝난 작업에 업로드를 다시 요청하는 일이 없습니다.
* **원샷 전사**: `전사 시작`은 변환·비용 확인·제출·대기·병합을 한 흐름으로 실행합니다. 중간에 실패하면 성공한 단계의 체크포인트를 남기고 멈춥니다.
* **백그라운드 대기**: 대기 화면에서 `Esc`/`b`를 누르면 별도 프로세스가 완료를 감시합니다. 진행 상태는 폴더 안 `.gemini_batch.wait.json`에 기록되어 대시보드가 네트워크 없이 읽고, 완료되면 알림과 함께 결과를 자동 병합합니다. 로그는 `.gemini_batch.wait.log`에 남습니다.
* **다중 폴더 제출**: 여러 폴더를 골라 예상 비용을 한 번에 확인하고 일괄 제출합니다. 제출 후 모두 백그라운드 감시로 넘길 수 있습니다.
* **제출 전 비용 확인**: `upload`와 대시보드 파이프라인은 길이·토큰·예상 금액을 먼저 보여주고 확인을 받은 뒤 제출합니다. 실수로 과금되는 일을 막고, 자동화에서는 `--yes`로 건너뜁니다. ffprobe가 없어 길이를 읽지 못하면 예상 비용 없이 한 번 더 확인합니다.
* **MP4 자동 변환**: `upload`는 M4A가 없고 MP4만 있으면 M4A를 먼저 추출합니다. 변환 중에는 파일별 진행률과 경과 시간을 보여줍니다.
* **API 키 기억**: 최초 1회 입력한 키를 저장하면 다음 실행부터는 묻지 않습니다. 저장 위치는 `~/.config/nalmeok/config.json`(0600)입니다. 대시보드 `설정`에서 변경·삭제할 수 있습니다.
* **사전 점검**: 대시보드와 설정 화면에 ffmpeg·ffprobe·SDK·키 상태를 표시합니다. MP4 변환에 ffmpeg가 없으면 제출 전에 안내합니다.
* **한글 파일명 지원**: 오디오·PDF 파일 이름이 한글이어도 업로드됩니다. (SDK가 파일명을 HTTP 헤더에 넣어 깨지는 문제를 파일 객체 업로드로 우회합니다.)
* **체크포인트 보존**: Batch 결과는 로컬 `.gemini_batch.result.jsonl`에 먼저 저장됩니다. 다운로드 도중 연결이 끊겨도 원격 재요청 없이 복구됩니다.
* **동시 작업 잠금**: macOS·Linux에서 같은 폴더에 업로드와 다운로드가 동시에 실행되지 않도록 잠급니다. 대시보드의 백그라운드 대기와 수동 병합이 겹쳐도 안전합니다.
* **원자적 파일 쓰기**: 상태·결과·Markdown은 고유한 임시 파일에 쓴 뒤 교체합니다. 중간에 프로세스가 죽어도 반쯤 쓰인 파일이 남지 않습니다.
* **파일 무결성 검증**: 업로드 후 로컬 파일의 수정 시각이나 크기가 변경되면 제출 작업을 즉시 중단하여 누락/중복 전사를 방지합니다. 대시보드는 이 상태를 `입력 변경됨`으로 표시합니다.
* **선택적 재시도**:
  * `download --retry batch`: 실패한 파일만 추려 새 Batch로 재등록합니다.
  * `download --retry live`: 실패한 파일만 실시간 일반 API로 즉시 요청합니다. (비용은 일반 API 단가 적용)
* **덮어쓰기 방지**: 타깃 마크다운 파일이 이미 존재하면 작업을 덮어쓰지 않고 에러를 반환합니다. 같은 내용이면 조용히 넘어갑니다.

---

## FAQ

**Q. 전사 품질과 프롬프트 정책은 어떻게 되나요?**

인위적인 문장 윤문이나 요약을 배제하고, 발화자의 구어체와 전문 용어를 그대로 전사하도록 프롬프팅되어 있습니다. 음질 불량 구간은 임의로 환각(Hallucination)을 생성하지 않고 `[불명확]` 태그로 처리합니다.

**Q. Batch 결과 수신까지 얼마나 걸리나요?**

구글 배치 큐 상태에 따라 다르지만 보통 10분~1시간 내외로 완료됩니다. (API SLA 최대 24시간) 대시보드에서 `완료까지 기다리기`를 고르거나, `./app.py wait ./lecture_folder --download`로 완료 시점을 자동으로 잡을 수 있습니다.

**Q. 기다리는 동안 다른 작업을 할 수 있나요?**

대기 화면에서 `Esc` 또는 `b`를 누르면 백그라운드 대기로 전환됩니다. 별도 프로세스가 완료를 감시하므로 메뉴로 돌아와 다른 폴더를 제출하거나 사용량을 확인할 수 있고, 완료되면 알림과 함께 결과가 자동 병합됩니다. 터미널을 닫아도 감시 프로세스는 계속 실행됩니다. 진행 상황은 폴더의 `.gemini_batch.wait.json`, 로그는 `.gemini_batch.wait.log`에서 확인할 수 있습니다.

**Q. 여러 강의 폴더를 한 번에 제출할 수 있나요?**

됩니다. 대시보드에서 `여러 폴더 한꺼번에 제출`을 고르고 Space로 폴더를 선택하세요. 예상 비용을 한 번만 확인한 뒤 폴더마다 Batch를 제출하고, 이어서 모두 백그라운드 감시로 넘길 수 있습니다. 이미 제출된 폴더는 건너뛰거나 이어서 제출합니다.

**Q. 오디오 파일 이름이 한글이어도 되나요?**

됩니다. SDK가 파일명을 HTTP 헤더에 넣어 깨지는 문제를 파일 객체 업로드로 우회하고, 표시 이름은 원래 한글 이름 그대로 보존합니다.

**Q. OpenRouter API Key로 사용 가능한가요?**

불가능합니다. OpenRouter Batch 엔드포인트는 멀티모달 오디오 바이너리 입력을 지원하지 않으므로 Google AI Studio 직접 발급 키만 사용해야 합니다.

**Q. API 키는 어디에 저장되나요?**

터미널 메뉴에서 **저장**을 고른 경우에만 `~/.config/nalmeok/config.json`에 소유자 전용 권한(0600)으로 저장합니다. `--key`나 `GEMINI_API_KEY` 환경변수가 있으면 그쪽이 우선하므로 저장된 키는 쓰이지 않습니다. 대시보드 `설정`에서 키를 변경하거나 삭제할 수 있고, 파일을 직접 지워도 됩니다. 같은 파일에 최근 작업 폴더 목록(`recent`)도 함께 들어갑니다.

**Q. MP4만 있어도 전사할 수 있나요?**

됩니다. `./app.py upload ./lecture_folder` 또는 대시보드의 `전사 시작`을 실행하면 M4A가 없다는 것을 감지해 ffmpeg로 먼저 추출한 뒤 이어서 업로드합니다. MP4 변환과 업로드가 한 번에 처리되므로 `convert`를 먼저 실행할 필요가 없습니다.

**Q. 완료된 폴더에서 다시 전사하려면?**

대시보드에서 폴더를 열고 `새 작업 시작`을 고르세요. 기존 상태·결과는 `.nalmeok-archive/<시각>/`으로 옮겨 보존하고, 같은 MP4/M4A로 새 전사를 시작할 수 있습니다. 미디어 원본은 건드리지 않습니다.

**Q. 전사 결과를 바로 열 수 있나요?**

완료된 폴더의 `결과 열기`를 고르면 `$VISUAL`/`$EDITOR`가 있을 때 그 에디터로, 없으면 OS 기본 앱으로 엽니다. `결과 경로 복사`로 Markdown 경로를 클립보드에 복사할 수도 있습니다.

**Q. 터미널 메뉴가 열리지 않습니다.**

대시보드는 실제 터미널(TTY)에서만 동작합니다. 파이프나 IDE 출력 창에서는 `./app.py --help`로 서브커맨드를 사용하세요.

---

## 테스트

핵심 로직은 표준 라이브러리 `unittest`로 검증합니다. SDK나 API 키 없이 실행됩니다.

```bash
uv run python -m unittest discover -s tests -v
# 또는
python -m unittest discover -s tests -v
```

`tests/__init__.py`가 있어 저장소 루트에서 `python -m unittest discover`만 실행해도 테스트를 찾습니다.

테스트 범위: Batch 가격 계산, 토큰 집계, 상태 판정(준비·제출 전·제출됨·전사 중·결과 수신·완료·입력 변경·손상), 결과 병합과 실패 검증, 설정 파일 권한, 원자적 쓰기, 경로 파싱, 재시도 대상 선택.

## 프로젝트 구조

```text
app.py                 # 실행 진입점 (./app.py)
nalmeok.py             # CLI 본체 (TUI, Batch 파이프라인, 설정)
tests/test_nalmeok.py  # 순수 로직·상태 전이 테스트
pyproject.toml         # 패키지 정의와 nalmeok 콘솔 스크립트
```

---

## 라이선스

MIT License

---

![날먹 — 강의 파일을 먹고 전사 노트를 만드는 캐릭터. 강의는 길고, 내 집중력은 짧다.](assets/readme-banner.png)

# 날먹 (nalmeok)

<div align="center">

[![Python](https://img.shields.io/badge/Python-3.10%2B-3776AB?logo=python&logoColor=white)](https://www.python.org/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Gemini Batch API](https://img.shields.io/badge/Gemini-Batch%20API-8E75B2?logo=googlegemini&logoColor=white)](https://ai.google.dev/gemini-api/docs/batch-api)
[![Free live](https://img.shields.io/badge/free%20tier-live%20supported-brightgreen)](#%EB%AC%B4%EB%A3%8C-live-%EB%AA%A8%EB%93%9C)
[![uv](https://img.shields.io/badge/uv-ready-DE5FE9?logo=uv&logoColor=white)](https://docs.astral.sh/uv/)
[![ffmpeg](https://img.shields.io/badge/ffmpeg-required-007808?logo=ffmpeg&logoColor=white)](https://ffmpeg.org/)
[![Cost](https://img.shields.io/badge/1%EC%8B%9C%EA%B0%84-%EC%95%BD%2074%EC%9B%90-brightgreen)](#%EB%B9%84%EC%9A%A9-%EB%B9%84%EA%B5%90)
![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)

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

무료 live는 **무료 등급 키**로 바로 사용할 수 있고, 유료 Batch를 쓰려면 **결제가 연결된 Gemini API 키**가 필요합니다. [API 키 발급 안내](#gemini-api-키-발급)를 참고하세요. MP4 변환과 길이 측정에는 [ffmpeg 설치](#사전-요구사항)도 필요합니다.

---

## 터미널 대시보드

인자 없이 실행하면 **키 → 작업 → 진행** 3화면으로 이어집니다. 메뉴를 찾아 헤맬 필요 없이, 폴더를 고르고 Enter만 누르면 됩니다.

### 1. API 키 (처음 한 번만)

키가 없으면 첫 화면에서 **무료 키**와 **유료 키** 중 하나를 고르고, 구분용 이름(`free1`, `paid1` 등)과 키를 입력합니다. Enter를 누르면 과금 없이 유효성을 확인하고 `~/.config/nalmeok/config.json`(0600)에 저장한 뒤 바로 작업 화면으로 넘어갑니다. 이후 `k` 또는 설정에서 키를 추가·삭제할 수 있고, 무료/유료 라벨을 바꿀 수 있습니다.

```text
  키 추가   free / paid
  ───────────────────────────────────────────

  API 키   GEMINI_API_KEY 환경변수도 사용할 수 있습니다

  ❯ 무료 키   live 전용 · 기본 130분/키
    유료 키   Batch 전용 · 남는 오디오 처리
    ← 뒤로

  API ✓ 무료 2 · 유료 1 · ffmpeg ✓ · SDK ✓
  ↑↓ 이동 · Enter 선택 · Esc 뒤로
```

* **무료 키**는 live 전용입니다. Batch API는 무료 등급에서 지원되지 않으므로 무료 키를 Batch에 쓰지 않습니다.
* **유료 키**는 Batch 전용입니다. 기본 무료 전사에는 쓰이지 않고, `p`(유료 Batch 포함)나 `auto`에서 무료 키로 처리하고 남은 분량만 맡습니다.
* `GEMINI_API_KEY` 환경변수도 키로 인식합니다. 등급은 `GEMINI_API_KEY_TIER=free` 또는 `paid`로 정하고, 생략하면 `paid`입니다.
* 저장 파일에는 키가 배열로 들어가며 화면에는 `••••1111`처럼 뒤 4자리만 보입니다.

### 2. 작업

```text
  날먹   강의 음성을 Batch·live로 전사   v0.3.0
  ───────────────────────────────────────────

  작업

  ❯ ch09        전사 중 · 12:03 경과
    ch07_08     완료 · 오디오 2개
  ───────────────────────────────────────────
    새 전사      폴더를 고르거나 경로를 붙여넣기
    여러 폴더 유료 Batch  2개 준비됨
    원격 상태 새로고침
    사용량
    설정
    종료

  API ✓ 무료 2 · 유료 1 · ffmpeg ✓ · SDK ✓
  ↑↓ 이동 · Enter 선택 · k 키 · u 사용량 · s 설정 · m 유료 일괄 · q 종료
```

### 3. 진행

폴더를 고르면 상태에 맞는 작업이 한 화면에서 이어집니다.

| 폴더 상태 | 화면에서 하는 일 |
| --- | --- |
| 준비됨 / 제출 전 | 무료 키가 있으면 Enter: 무료 live(등록된 무료 키 모두, Batch 없음) / `p`: 유료 Batch 포함(무료 먼저, 남는 분량만 Batch) / `l`: 첫 무료 키로 live만 |
| 제출됨 | 완료 대기. `Esc`는 백그라운드 전환 |
| 전사 중 | 백그라운드 상태와 로그 |
| live 전사 | 남은 그룹부터 live 이어서 |
| 혼합 전사 | 무료 live 그룹과 유료 Batch를 이어서 진행 (기본 Enter는 혼합으로 가지 않습니다) |
| 결과 수신 | 결과 병합 (혼합 작업 포함) |
| 완료 | 결과 열기 · 비용 보기 · 새 작업 시작 |
| 파일 없음 / 변경됨 / 손상 | 안내 후 고급 메뉴로 위임 |

```text
  전사   ~/lectures/ch10
  ───────────────────────────────────────────

  오디오 2개 · PDF 참고자료

  파일         2개 · 총 2시간 10분

  무료 free1 2시간 10분
  전체를 무료 키로 처리합니다 · Batch 없음

  API ✓ 무료 2 · 유료 1 · ffmpeg ✓ · SDK ✓
  Enter 무료 전사 · p 유료 Batch 포함 · l 무료 live · e 비용 · b 파일 · a 고급 · Esc 뒤로
```

* 대기 화면은 스피너와 흐르는 막대로 경과 시간을 보여줍니다. `Esc` 또는 `b`로 **백그라운드 대기**로 전환하면 완료 시 알림이 오고 결과가 자동으로 병합됩니다.
* 대시보드는 네트워크를 호출하지 않습니다. 제출한 Batch의 원격 상태는 `r`(원격 상태 새로고침)을 눌렀을 때 한 번에 조회합니다.
* **여러 폴더 유료 Batch**(`m`)에서 Space로 폴더를 고르면 예상 비용을 한 번만 확인하고 일괄 제출합니다.
* 개별 변환·비용 상세·재시도·보관 같은 세밀한 제어는 폴더 화면에서 `a`를 누르면 나오는 **고급 메뉴**에 그대로 있습니다.
* **결과 열기**는 `$VISUAL`/`$EDITOR`가 있으면 그 에디터로, 없으면 OS 기본 앱으로 엽니다.
* 설정·폴더 열기·여러 폴더 유료 Batch·진행 상황 화면도 같은 목록 스타일과 하단 키 안내를 씁니다.

| 키 | 동작 |
| --- | --- |
| `↑` / `↓` | 이동 |
| `Enter` / `→` | 선택·시작 |
| `←` / `Esc` | 뒤로 (대기 중이면 백그라운드 전환) |
| `b` | 대기 중 백그라운드 전환 |
| `k` | API 키 관리 (추가·삭제) |
| `u` | 사용량 |
| `s` | 설정 |
| `m` | 여러 폴더 유료 Batch 한꺼번에 제출 |
| `r` | 원격 상태 새로고침 |
| `a` | (전사·완료 화면) 고급 메뉴 |
| `p` | (준비 화면) 유료 Batch 포함 실행 (무료 키가 있으면 무료 먼저) |
| `l` | (전사 화면) 첫 무료 키로 live만 실행 |
| `?` | 도움말 |
| `Space` | 여러 폴더 선택에서 선택/해제 |
| `q` / `Ctrl+C` | 종료 |

---

## 비용 비교

2026년 10월 기준 공식 요율 및 실제 전사 테스트 기준입니다. (`gemini-3.8-flash` Batch 요금: 1M 토큰당 입력 $0.375 / 출력 $1.875)

| 방식 | 1시간 비용 | 원화 (환율 1,400원) | 비고 |
| --- | --- | --- | --- |
| **본 도구 (Gemini 3.8 Flash Batch)** | **$0.053** | **약 74원** | 유료 Batch (헤비 유저용 선택) |
| **본 도구 (Gemini 3.8 Flash live, 무료 등급)** | **$0** | **0원** | 5 RPM · 250k TPM · 20 RPD 예시, 모델/계정별 상이 |
| Gemini 3.5 Flash-Lite Batch | $0.026 | 약 36원 | 초저비용 모드: `--model gemini-3.5-flash-lite` |
| Gemini 3.8 Flash (유료 실시간 API) | $0.106 | 약 148원 |  |
| OpenAI gpt-4o-mini-transcribe | $0.180 | 약 250원 |  |
| Gemini 3.5 Transcribe (음성 전용) | $0.300 | 약 420원 |  |
| OpenAI Whisper / gpt-4o-transcribe | $0.360 | 약 500원 | 약 7배 차이 |

> **단가 산정 기준**
> * **Batch API 50% 할인**: 즉시 응답 대신 비동기 큐(최대 24시간 이내 완료)로 처리하여 절반 요금 적용.
> * **무료 live**: 3.8 Flash의 일반(live) 호출은 무료 등급에서 $0입니다. 대신 RPM/TPM/RPD 한도가 있고, 3.8 Flash Batch는 무료 등급에서 지원되지 않습니다.
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
| **ffmpeg** | `convert`(MP4 → M4A), `estimate`(길이 측정), `live`(긴 파일 130분 분할), MP4만 있는 폴더의 `upload`(자동 변환) | — |
| **Gemini API 키** | `live`(무료 등급 가능) · `upload` · `status` · `wait` · `download`(유료 Batch) | `convert` · `estimate` · `stats` |

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

> **무료 등급은 live 모드로 사용할 수 있습니다.** 3.8 Flash의 일반(live) 호출은 무료 등급에서 $0이고, 한도(예: 5 RPM · 250k TPM · 20 RPD)가 적용됩니다. 반면 **3.8 Flash Batch는 무료 등급에서 지원되지 않습니다.** 유료 Batch를 쓰려면 결제가 연결된 프로젝트가 필요합니다. 발급 절차는 아래 [Gemini API 키 발급](#gemini-api-키-발급)을 참고하세요.

---

## Gemini API 키 발급

무료 등급은 **live 모드**로 바로 사용할 수 있습니다. **Batch API(3.8 Flash)는 무료 등급에서 사용할 수 없으므로**, Batch를 쓰려면 **결제가 연결된 Google Cloud 프로젝트**에서 키를 발급해야 합니다. 아래 순서는 유료 Batch 기준입니다.

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

환경변수로 넘기거나 `--key` 옵션으로 직접 전달할 수 있습니다. 대시보드에서는 키마다 **무료/유료 등급**과 **라벨**을 붙여 여러 개를 등록합니다.

```bash
# 무료 등급 키 (live 전용)
export GEMINI_API_KEY="AIza..."
export GEMINI_API_KEY_TIER=free

# 유료 등급 키 (Batch 전용, 기본값)
export GEMINI_API_KEY="AIza..."
export GEMINI_API_KEY_TIER=paid

# 영구 등록 (zsh)
echo 'export GEMINI_API_KEY="AIza..."' >> ~/.zshrc
echo 'export GEMINI_API_KEY_TIER=paid' >> ~/.zshrc
source ~/.zshrc
```

```bash
# 또는 실행할 때마다 직접 지정
./app.py upload ./lecture_folder --key "AIza..."
./app.py live ./lecture_folder --key "AIza..."
```

> `live`는 무료 키 전용입니다. 등록된 유료 라벨 키를 `--key`로 넘기면 거부합니다. 등록되지 않은 키를 직접 넘길 때만 그대로 사용합니다.

키를 찾는 순서는 모드마다 다릅니다.

| 모드 | 키 선택 |
| --- | --- |
| `live` / 무료 전사 | `--key` → 무료 라벨이 붙은 `GEMINI_API_KEY` → 등록된 무료 키 |
| `upload`·`status`·`wait`·`download` / 유료 Batch 구간 | `--key`(무료 라벨 키는 거부) → 유료 라벨이 붙은 `GEMINI_API_KEY` → 등록된 유료 키 |
| 대시보드 Enter (무료 전사) | 등록된 무료 키를 순서대로 모두 사용 · Batch 없음 |
| 대시보드 `p` / CLI `auto` (유료 포함) | 무료 키를 먼저 사용 → 남는 분량은 등록된 유료 키로 Batch |

> 무료 키는 Batch API에서 지원되지 않으므로 무료 라벨이 붙은 키를 Batch에 쓰지 않습니다. 무료 키만 있는데 `p`를 누르면 유료 키를 추가하라는 안내가 나옵니다. 유료 키만 있으면 Enter는 무료 키 추가로 안내하고, `p`를 눌렀을 때만 유료 Batch를 시작합니다.

### 3-1단계 · 키 저장 (선택, 권장)

터미널 메뉴(`./app.py`)로 실행하면 첫 화면에서 **무료 키** 또는 **유료 키**를 고르고, 라벨과 키를 입력합니다. Enter를 누르면 키 유효성을 확인한 뒤 `~/.config/nalmeok/config.json`에 **소유자만 읽을 수 있는 권한(0600)** 으로 저장합니다. (Windows는 `%APPDATA%\nalmeok\config.json`)

설정 파일은 키 배열과 라벨을 가집니다.

```json
{
  "keys": [
    {"id": "f1", "label": "free1", "tier": "free", "api_key": "AIza...", "limit_minutes": 130},
    {"id": "f2", "label": "free2", "tier": "free", "api_key": "AIza...", "limit_minutes": 130},
    {"id": "p1", "label": "paid1", "tier": "paid", "api_key": "AIza...", "limit_minutes": 130}
  ]
}
```

```bash
# 저장 확인 (키가 그대로 보이므로 주의)
cat ~/.config/nalmeok/config.json

# 저장된 키 전체 지우기
rm ~/.config/nalmeok/config.json
```

대시보드에서 `k`(API 키)를 누르면 등록된 키 목록, 무료/유료 등급, 라벨, 마스킹된 키를 보고 추가·삭제할 수 있습니다. 예전 단일 `api_key` 설정은 자동으로 `기본`이라는 유료 키로 읽힙니다.

> 저장한 키가 유효하지 않으면(401 오류) 해당 키를 지우고 다시 입력을 요청합니다.

### 4단계 · 동작 확인

`convert`·`estimate`·`stats`는 키 없이 동작합니다. 무료 키는 live로, 유료 키는 Batch 업로드로 검증하세요.

```bash
./app.py estimate ./lecture_folder   # 키 불필요
./app.py live ./lecture_folder       # 무료 키 검증
./app.py upload ./lecture_folder     # 유료 키 검증
```

### 자주 겪는 문제

| 증상 | 원인 / 해결 |
| --- | --- |
| `429 RESOURCE_EXHAUSTED` | 무료 등급 live 한도(RPM/TPM/RPD)이거나 Batch 미지원일 수 있습니다. live는 잠시 후/다음 날 재시도하고, Batch는 프로젝트에 결제를 연결하세요. |
| `403 PERMISSION_DENIED` | 키가 다른 프로젝트에 속해 있습니다. 해당 프로젝트에 결제가 연결됐는지 확인하세요. |
| `API key not valid` | 복사한 키 앞뒤의 공백·줄바꿈을 확인하세요. |
| `API 키가 필요합니다` | `GEMINI_API_KEY`가 export 되지 않았습니다. 새 터미널에서 export 후 다시 실행하거나, 대시보드 첫 화면에서 무료/유료 키를 등록하세요. |
| `유료 API 키가 필요합니다` | 무료 키만 등록된 상태에서 유료 Batch(`p`, `upload`)를 실행했습니다. 유료 키를 추가하거나 `live`/`auto`를 사용하세요. |

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
├── .gemini_batch.*            # 유료 Batch 상태·요청·결과 체크포인트 (자동 생성)
├── .gemini_live.*             # 무료 live 상태·결과 체크포인트 (자동 생성)
├── .gemini_mixed.*            # 혼합 작업의 유료 Batch 요청·결과 (자동 생성)
├── .gemini_batch.wait.log     # 백그라운드 대기 로그 (자동 생성)
└── .nalmeok-archive/<시각>/    # 새 작업 시작 시 상태·결과·Markdown 보관
```

* **입력 제한**: 폴더당 `.m4a` 오디오 파일 1~10개, 참고 PDF 최대 1개.
* **PDF 참고자료**: 전사 시 누락되거나 왜곡되기 쉬운 도메인 용어 매칭 참고용으로만 프롬프트에 주입됩니다.
* **보관 기록**: `새 작업 시작`을 고르면 상태·결과 파일만 `.nalmeok-archive/<시각>/`으로 이동합니다. 미디어 원본은 그대로 남습니다.

---

## CLI 사용법

인자 없이 `./app.py`를 실행하면 최근 작업 대시보드가 열립니다. 자동화할 때는 아래 서브커맨드를 쓰세요. 백그라운드 대기는 `wait --background`로도 시작할 수 있습니다. 무료 live와 유료 Batch를 한 번에 처리하는 `auto`도 CLI에서 실행할 수 있습니다.

```bash
# 유료 Batch용 키 (기본값 paid)
export GEMINI_API_KEY="your-api-key"

# 무료 live용 키라면 등급을 명시
# export GEMINI_API_KEY_TIER=free

# 1. MP4에서 음성 추출 (M4A 생성, 원본 보존)
./app.py convert ./lecture_folder

# 2. 오디오 길이 기준 예상 토큰 및 청구 금액 확인
./app.py estimate ./lecture_folder

# 3. 오디오 업로드 및 Batch 전사 작업 제출 (M4A가 없으면 MP4에서 자동 추출, 제출 전 예상 비용 확인)
./app.py upload ./lecture_folder

# 4. 무료 키를 먼저 live로 쓰고, 남는 분량만 유료 Batch로 자동 처리
./app.py auto ./lecture_folder

# 비용 확인 없이 자동 실행
./app.py auto ./lecture_folder --yes

# 5. Batch 작업 처리 상태 확인
./app.py status ./lecture_folder

# 6. 완료까지 대기 후 자동 다운로드 (상태와 경과 시간을 실시간으로 표시)
./app.py wait ./lecture_folder --download

# 6-1. 기다리지 않고 백그라운드 감시 시작 (완료되면 자동 병합)
./app.py wait ./lecture_folder --background

# 7. 전사 결과 다운로드, 취합 및 단일 마크다운(.md) 생성
./app.py download ./lecture_folder

# 8. 무료 등급 live 전사 (Batch 없이, 130분 그룹 · 자동 간격 · 이어서 가능)
./app.py live ./lecture_folder

# 그룹 길이/간격/모델 조정
./app.py live ./lecture_folder --chunk-minutes 120 --gap 12 \
  --model gemini-3.8-flash

# 누적 사용 비용 통계 확인
./app.py stats
```

> `upload`는 유료 키가 필요하고, 길이·토큰·예상 금액을 보여주고 확인을 받은 뒤 제출합니다. `auto`는 등록된 무료 키를 먼저 live로 소진한 뒤 남는 분량만 유료 Batch로 보내며, 유료 분량이 있으면 예상 금액을 확인합니다. 사람 없이 돌리는 자동화에서는 `--yes`로 확인을 건너뜁니다. 폴더에 M4A가 없고 MP4만 있으면 M4A를 먼저 만들어 준비하므로 `convert`를 따로 실행하지 않아도 됩니다. `auto`는 `--key` 대신 설정에 등록했거나 환경변수로 지정한 무료/유료 키를 사용합니다.
>
> `wait`는 Batch가 끝날 때까지 폴링하며 완료되면 터미널 벨과 (가능하면) macOS 알림으로 알려줍니다. `--download`를 붙이면 곧바로 결과 병합까지 이어서 수행합니다. `--background`는 분리된 프로세스로 감시를 시작하고 PID만 출력합니다. 진행 상태는 폴더의 `.gemini_batch.wait.json`, 로그는 `.gemini_batch.wait.log`에 남으며, 일부 파일이 실패하면 자동 병합을 멈추고 오류를 기록합니다. 그때는 `download --retry live` 또는 `--retry batch`로 재시도하세요.

---

## 무료 live 모드

무료 등급 키로 Batch 없이 바로 전사합니다. 대시보드의 전사 화면에서 `l`을 누르거나 CLI에서 실행하세요.

```bash
./app.py live ./lecture_folder
```

* **130분 고정 그룹**: 한 요청의 오디오가 130분을 넘지 않게 자동으로 나눕니다.
  - 6시간 파일 → `130 + 130 + 100분` = **3요청**
  - 20분 파일 7개 → `4개(80분) + 3개(60분)` = **2요청**
* **작은 파일은 한 요청에 묶음**: 파일을 각각 업로드한 뒤 한 요청에 여러 audio part로 넣습니다. 로컬 concat은 하지 않습니다.
* **순차 + 자동 간격**: 요청 시작 간 최소 12초(RPM 5)를 두고, 오디오 토큰이 250k TPM을 넘지 않도록 요청 길이에 따라 간격을 늘립니다. 130분(195k tokens)은 약 60초, 20분 그룹은 12초입니다. 이 간격은 요청 타임아웃이 아니라 **시작 간격**입니다.
* **그룹별 체크포인트**: 성공한 그룹은 `.gemini_live.result.jsonl`에 즉시 저장됩니다. 중단하거나 한도에 걸리면 다시 실행할 때 완료된 그룹을 건너뜁니다.
* **이어서 하기**: 대시보드에서 `live 전사 · n/m 그룹`으로 표시되고, 같은 폴더에서 `l`을 다시 누르면 남은 그룹부터 이어갑니다.
* **PDF**: 있으면 각 그룹 요청에 그대로 포함합니다. PDF 때문에 TPM이 빠듯하면 `--chunk-minutes`를 낮추세요.
* **Batch 미지원**: 3.8 Flash Batch는 무료 등급에서 지원되지 않습니다. 무료 키로는 live만 사용하세요.
* **여러 무료 키**: Enter(무료 전사)는 등록된 무료 키를 라벨 순서대로 모두 사용합니다. 무료 키 하나가 130분을 처리하고, 다음 무료 키가 다음 130분을 처리합니다. `l`은 첫 번째 무료 키로만 실행하고, 남는 분량을 유료 Batch로 보내려면 `p`(유료 포함)를 누르세요.
* **한도 단위**: 5 RPM은 파일 5개가 아니라 **분당 요청 5개**입니다. 여러 파일을 한 요청에 묶으면 파일 수와 무관합니다. 20 RPD는 하루 요청 수이고, 코드가 따로 세지 않으므로 넘으면 API가 429를 돌려줍니다. 완료된 그룹은 저장되므로 다음 날 이어서 하면 됩니다.
* **한도**: 예시 계정 기준 5 RPM · 250k TPM · 20 RPD입니다. 모델·계정별로 다르며 AI Studio에서 확인하세요. 429가 나면 잠시 후 재시도하거나 다음 날 이어갑니다.
* **과금 fallback**: 기본 Enter(무료 전사)와 `live`는 유료 Batch로 자동 전환하지 않습니다. `p`(대시보드, 유료 포함) 또는 CLI `auto`를 명시적으로 실행해야 남는 분량이 유료 Batch로 갑니다.
* **무료 키 429 순환**: 기본 Enter의 여러 무료 키 전사와 CLI `auto`에서는 요청이나 파일 업로드가 429로 제한되면 다음 무료 키로 같은 그룹을 시도합니다. 등록된 무료 키는 서로 다른 프로젝트의 키를 사용하세요. 제한된 키는 이번 실행에서 건너뛰며, 모든 무료 키가 제한되면 터미널 벨과 macOS 알림(지원 시)을 보냅니다. 완료 그룹은 보존되고, 다시 실행하면 무료 키를 다시 시도합니다. 단일 키 `l`/CLI `live`는 기존 재시도 동작을 유지합니다.
* **무료 키 모두 제한 시**: 대시보드에서 예상 비용과 함께 `유료 새 작업` 또는 `중단`을 선택합니다. 유료 새 작업을 선택하면 기존 기록·결과를 `.nalmeok-archive/`에 보관하고 **폴더 전체를 유료 Batch로 다시 전사**합니다. 유료 키가 없으면 등록 화면으로 이동하며, 취소하면 무료 작업 기록을 유지합니다. 비대화형 CLI는 유료로 전환하지 않고 오류로 종료합니다. 유료 자동전환 설정은 없습니다.

* **데이터 사용**: 무료 등급 입력/출력은 Google 제품 개선에 사용될 수 있습니다. 민감한 강의는 유료 프로젝트를 사용하세요.

---

## 처리 파이프라인 및 안전장치

* **상태 기반 대시보드**: 최근 작업과 현재 폴더 아래의 작업을 모아 `폴더 없음 · 파일 없음 · 입력 확인 필요 · 준비됨 · 제출 전 · 제출됨 · 전사 중 · live 전사 · 혼합 전사 · 결과 수신 · 완료 · 입력 변경됨 · 상태 손상`으로 구분합니다. 메뉴는 이 상태에서 파생되므로 이미 끝난 작업에 업로드를 다시 요청하는 일이 없습니다.
* **원샷 전사**: `전사` 흐름은 변환·비용 확인·제출·대기·병합을 한 번에 실행합니다. 중간에 실패하면 성공한 단계의 체크포인트를 남기고 멈춥니다.
* **자동(무료 → 유료, 선택)**: `p` 또는 CLI `auto`에서 무료 키 용량 안의 파일은 live로, 남는 파일은 유료 Batch로 나눕니다. 무료 키를 먼저 소진하고, 유료 Batch가 끝나면 결과를 파일 순서대로 합쳐 하나의 Markdown으로 만듭니다. 중간에 멈춰도 `.gemini_live.*`와 `.gemini_mixed.*` 상태로 이어서 합니다.
* **작업별 배분**: 무료 키의 `limit_minutes`는 작업마다 0에서 새로 계산합니다. 다른 폴더에서 방금 쓴 분량은 차감하지 않습니다. 실제 한도는 API가 강제하며, 같은 프로젝트에서 연속 요청하면 429가 날 수 있습니다.
* **무료 live**: 130분 고정 그룹으로 나누고, 작은 파일은 한 요청에 묶어 RPD를 아낍니다. 순차 요청 + 자동 시작 간격, 그룹별 체크포인트로 중단 후 이어서 합니다. 3.8 Flash Batch는 무료 등급에서 지원되지 않습니다.
* **live 그룹 계획**: 총 길이를 130분으로 나눈 최소 요청 수를 계산하고, 파일 순서를 유지한 채 그룹을 균형 있게 배분합니다. 7×20분은 4+3(80/60분) 두 요청이 됩니다.
* **백그라운드 대기**: 대기 화면에서 `Esc`/`b`를 누르면 별도 프로세스가 완료를 감시합니다. 진행 상태는 폴더 안 `.gemini_batch.wait.json`에 기록되어 대시보드가 네트워크 없이 읽고, 완료되면 알림과 함께 결과를 자동 병합합니다. 로그는 `.gemini_batch.wait.log`에 남습니다.
* **여러 폴더 유료 Batch**: 여러 폴더를 골라 예상 비용을 한 번에 확인하고 일괄 제출합니다. 제출 후 모두 백그라운드 감시로 넘길 수 있습니다.
* **제출 전 비용 확인**: `upload`와 `p`(유료 Batch 포함)는 길이·토큰·예상 금액을 먼저 보여주고 확인을 받은 뒤 제출합니다. 실수로 과금되는 일을 막고, 자동화에서는 `--yes`로 건너뜁니다. 기본 무료 전사는 Batch 비용을 만들지 않습니다. ffprobe가 없어 길이를 읽지 못하면 예상 비용 없이 한 번 더 확인합니다.
* **MP4 자동 변환**: `upload`는 M4A가 없고 MP4만 있으면 M4A를 먼저 추출합니다. 변환 중에는 파일별 진행률과 경과 시간을 보여줍니다.
* **API 키 기억**: 등록한 키는 라벨·등급과 함께 `~/.config/nalmeok/config.json`(0600)에 저장됩니다. 무료 키는 live, 유료 키는 Batch에만 쓰며, 대시보드 `k`에서 추가·삭제할 수 있습니다. 예전 단일 `api_key`는 `기본` 유료 키로 자동 이전됩니다.
* **사전 점검**: 대시보드와 설정 화면에 ffmpeg·ffprobe·SDK·키 상태를 표시합니다. MP4 변환에 ffmpeg가 없으면 제출 전에 안내합니다.
* **한글 파일명 지원**: 오디오·PDF 파일 이름이 한글이어도 업로드됩니다. (SDK가 파일명을 HTTP 헤더에 넣어 깨지는 문제를 파일 객체 업로드로 우회합니다.)
* **체크포인트 보존**: Batch 결과는 `.gemini_batch.result.jsonl`, live 결과는 `.gemini_live.result.jsonl`, 혼합 작업의 유료 결과는 `.gemini_mixed.result*`에 먼저 저장됩니다. 중단되어도 완료된 그룹/파일을 다시 요청하지 않습니다.
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

**Q. 무료 API 키로도 사용할 수 있나요?**

됩니다. `./app.py live ./lecture_folder` 또는 전사 화면에서 `l`을 누르세요. 3.8 Flash의 live 호출은 무료 등급에서 $0이고, 예시 계정 기준 5 RPM · 250k TPM · 20 RPD 한도가 있습니다. 반면 **3.8 Flash Batch는 무료 등급에서 지원되지 않습니다.** 정확한 한도는 AI Studio의 Rate Limit 화면에서 확인하세요.

무료 키와 유료 키를 함께 등록했다면 `./app.py auto ./lecture_folder` 또는 대시보드 `p`(유료 Batch 포함)로 무료 live를 먼저 쓰고 남는 분량만 유료 Batch로 보낼 수 있습니다.

**Q. live는 6시간짜리 파일을 어떻게 처리하나요?**

130분을 넘지 않게 자동으로 나눕니다. 6시간이면 `130 + 130 + 100분`으로 **3요청**입니다. 20분 파일 7개처럼 작은 파일은 순서를 유지한 채 `4개(80분) + 3개(60분)`으로 묶어 **2요청**으로 보냅니다. 요청은 순차 실행하고, RPM 5·TPM 250k 기준으로 시작 간격을 자동 조정합니다(20분 그룹 12초, 130분 그룹 약 60초).

**Q. 무료 한도를 다 쓰면 유료 Batch로 자동 전환되나요?**

`auto` 또는 대시보드 `p`는 **처음부터** 무료 키 용량을 계산해 무료 live를 먼저 쓰고, 남는 분량만 유료 Batch로 보냅니다. 기본 Enter는 무료 키만 쓰고 유료 Batch로 자동 전환하지 않습니다. 실행 중 무료 한도(429)에 걸리면 완료된 그룹을 저장하고 멈추며, 다시 실행하면 이어서 진행합니다. `live`만 실행하면 유료 Batch로 자동 전환하지 않습니다.

**Q. PDF가 있으면 live에서 어떻게 되나요?**

있으면 각 그룹 요청에 그대로 포함합니다. PDF 토큰 때문에 TPM이 빠듯하면 `--chunk-minutes`를 120 이하로 낮추세요.

**Q. Batch 결과 수신까지 얼마나 걸리나요?**

구글 배치 큐 상태에 따라 다르지만 보통 10분~1시간 내외로 완료됩니다. (API SLA 최대 24시간) 대시보드에서 `완료까지 기다리기`를 고르거나, `./app.py wait ./lecture_folder --download`로 완료 시점을 자동으로 잡을 수 있습니다.

**Q. 기다리는 동안 다른 작업을 할 수 있나요?**

대기 화면에서 `Esc` 또는 `b`를 누르면 백그라운드 대기로 전환됩니다. 별도 프로세스가 완료를 감시하므로 메뉴로 돌아와 다른 폴더를 제출하거나 사용량을 확인할 수 있고, 완료되면 알림과 함께 결과가 자동 병합됩니다. 터미널을 닫아도 감시 프로세스는 계속 실행됩니다. 진행 상황은 폴더의 `.gemini_batch.wait.json`, 로그는 `.gemini_batch.wait.log`에서 확인할 수 있습니다.

**Q. 여러 강의 폴더를 한 번에 제출할 수 있나요?**

됩니다. 대시보드에서 `여러 폴더 한꺼번에 제출`을 고르고 Space로 폴더를 선택하세요. 목록에는 제출 준비가 끝난(준비됨·제출 전) 폴더만 나옵니다. 예상 비용을 한 번만 확인한 뒤 폴더마다 Batch를 제출하고, 이어서 모두 백그라운드 감시로 넘길 수 있습니다. 무료 live는 폴더별로 실행하세요.

**Q. 오디오 파일 이름이 한글이어도 되나요?**

됩니다. SDK가 파일명을 HTTP 헤더에 넣어 깨지는 문제를 파일 객체 업로드로 우회하고, 표시 이름은 원래 한글 이름 그대로 보존합니다.

**Q. OpenRouter API Key로 사용 가능한가요?**

불가능합니다. OpenRouter Batch 엔드포인트는 멀티모달 오디오 바이너리 입력을 지원하지 않으므로 Google AI Studio 직접 발급 키만 사용해야 합니다.

**Q. API 키는 어디에 저장되나요?**

대시보드에서 등록한 키는 `~/.config/nalmeok/config.json`에 소유자 전용 권한(0600)으로 저장합니다. 키마다 `id`, `label`, `tier`(free/paid), `api_key`, `limit_minutes`가 들어가며, 화면에는 뒤 4자리만 보입니다. `--key`나 `GEMINI_API_KEY` 환경변수는 저장된 키보다 우선합니다. 대시보드 `k`(API 키)에서 추가·삭제할 수 있고, 파일을 직접 지워도 됩니다. 같은 파일에 최근 작업 폴더 목록(`recent`)도 함께 들어갑니다.

**Q. 키를 여러 개 등록할 수 있나요?**

됩니다. 무료 키와 유료 키를 각각 여러 개 등록하고 `free1`, `free2`, `paid1`처럼 라벨을 붙이세요. `auto`/`p`는 무료 키를 먼저 쓰고, 유료 분량은 유료 키 여러 개에 나눠 Batch로 제출합니다. 무료 키는 live에만, 유료 키는 Batch에만 쓰입니다.

**Q. MP4만 있어도 전사할 수 있나요?**

됩니다. `./app.py upload ./lecture_folder`, `./app.py live ./lecture_folder`, `./app.py auto ./lecture_folder`, 또는 대시보드의 `무료 전사`/`유료 Batch`/`무료 live`를 실행하면 M4A가 없다는 것을 감지해 ffmpeg로 먼저 추출합니다. MP4 변환과 전사가 한 번에 처리되므로 `convert`를 먼저 실행할 필요가 없습니다.

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

테스트 범위: Batch 가격 계산, 토큰 집계, 상태 판정(준비·제출 전·제출됨·전사 중·live·혼합·결과 수신·완료·입력 변경·손상), live 그룹핑(7×20분 → 2요청, 130분 상한), 무료/유료 키 레지스트리와 라벨·마이그레이션, 혼합 배분(130/130/40)·긴 파일 분할·무료 키 우선·유료 키 분산, 혼합 결과 병합, live 상태·아카이브·CLI 인자, 설정 파일 권한, 원자적 쓰기, 경로 파싱, 재시도 대상 선택.

## 프로젝트 구조

```text
app.py                 # 실행 진입점 (./app.py)
nalmeok.py             # CLI 본체 (TUI, Batch·live 파이프라인, 설정)
tests/test_nalmeok.py  # 순수 로직·상태 전이 테스트
pyproject.toml         # 패키지 정의와 nalmeok 콘솔 스크립트
```

---

## 라이선스

MIT License

---

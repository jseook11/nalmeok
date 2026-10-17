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

Python 3.10 이상이 필요합니다. 클론한 뒤 실행하면 폴더 선택부터 시작하는 터미널 메뉴가 열립니다.

### uv 사용 시 (권장)

```bash
git clone https://github.com/jseook11/nalmeok
cd nalmeok
uv run app.py
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
| **ffmpeg** | `convert`(MP4 → M4A), `estimate`(길이 측정) | — |
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
| `API 키가 필요합니다` | `GEMINI_API_KEY`가 export 되지 않았습니다. 새 터미널에서 export 후 다시 실행하세요. |

---

## 작업 폴더 구조

하나의 폴더가 하나의 전사 작업 단위가 됩니다.

```
lecture_folder/
├── 01.mp4          # convert 명령어 실행 시 M4A로 추출 (원본 유지)
├── 02.m4a
├── 03.m4a
├── 참고자료.pdf     # (선택) 전문 용어 매칭용 문맥 파일 (최대 1개 권장)
└── lecture_folder.md # 전사 완료 후 자동 생성되는 최종 결과물
```

* **입력 제한**: 폴더당 `.m4a` 오디오 파일 1~10개.
* **PDF 참고자료**: 전사 시 누락되거나 왜곡되기 쉬운 도메인 용어 매칭 참고용으로만 프롬프트에 주입됩니다.

---

## CLI 사용법

인자 없이 `./app.py`를 실행하면 폴더 선택부터 시작하는 터미널 메뉴가 열립니다. 자동화할 때는 아래 서브커맨드를 쓰세요.

```bash
export GEMINI_API_KEY="your-api-key"

# 1. MP4에서 음성 추출 (M4A 생성, 원본 보존)
./app.py convert ./lecture_folder

# 2. 오디오 길이 기준 예상 토큰 및 청구 금액 확인
./app.py estimate ./lecture_folder

# 3. 오디오 업로드 및 Batch 전사 작업 제출 (제출 전 예상 비용을 확인합니다)
./app.py upload ./lecture_folder

# 4. Batch 작업 처리 상태 확인
./app.py status ./lecture_folder

# 5. 완료까지 대기 후 자동 다운로드 (상태와 경과 시간을 실시간으로 표시)
./app.py wait ./lecture_folder --download

# 6. 전사 결과 다운로드, 취합 및 단일 마크다운(.md) 생성
./app.py download ./lecture_folder

# 누적 사용 비용 통계 확인
./app.py stats
```

> `upload`는 길이·토큰·예상 금액을 보여주고 확인을 받은 뒤 제출합니다. 사람 없이 돌리는 자동화에서는 `--yes`로 확인을 건너뜁니다.
>
> `wait`는 Batch가 끝날 때까지 폴링하며 완료되면 터미널 벨과 (가능하면) macOS 알림으로 알려줍니다. `--download`를 붙이면 곧바로 결과 병합까지 이어서 수행합니다.

---

## 처리 파이프라인 및 안전장치

* **제출 전 비용 확인**: `upload`는 길이·토큰·예상 금액을 먼저 보여주고 확인을 받은 뒤 제출합니다. 실수로 과금되는 일을 막고, 자동화에서는 `--yes`로 건너뜁니다.
* **완료 대기와 알림**: `wait`가 Batch가 끝날 때까지 폴링하며 상태와 경과 시간을 보여주고, 완료되면 알림을 보냅니다. `--download`를 붙이면 결과 병합까지 자동으로 이어집니다. 터미널 메뉴에서는 "완료까지 대기 후 다운로드" 항목이 같은 일을 합니다.
* **폴더 상태 표시**: 터미널 메뉴의 폴더 목록이 각 작업의 단계(제출 전 · 전사 중 · 완료 등)를 보여줍니다. API 키가 있으면 원격 Batch 상태까지 조회해 표시합니다.
* **한글 파일명 지원**: 오디오·PDF 파일 이름이 한글이어도 업로드됩니다. (SDK가 파일명을 HTTP 헤더에 넣어 깨지는 문제를 파일 객체 업로드로 우회합니다.)
* **체크포인트 보존**: Batch 결과는 로컬 `.gemini_batch.result.jsonl`에 먼저 저장됩니다. 다운로드 도중 연결이 끊겨도 원격 재요청 없이 복구됩니다.
* **파일 무결성 검증**: 업로드 후 로컬 파일의 수정 시각이나 크기가 변경되면 제출 작업을 즉시 중단하여 누락/중복 전사를 방지합니다.
* **선택적 재시도**:
  * `download --retry batch`: 실패한 파일만 추려 새 Batch로 재등록합니다.
  * `download --retry live`: 실패한 파일만 실시간 일반 API로 즉시 요청합니다. (비용은 일반 API 단가 적용)
* **덮어쓰기 방지**: 타깃 마크다운 파일이 이미 존재하면 작업을 덮어쓰지 않고 에러를 반환합니다.

---

## FAQ

**Q. 전사 품질과 프롬프트 정책은 어떻게 되나요?**

인위적인 문장 윤문이나 요약을 배제하고, 발화자의 구어체와 전문 용어를 그대로 전사하도록 프롬프팅되어 있습니다. 음질 불량 구간은 임의로 환각(Hallucination)을 생성하지 않고 `[불명확]` 태그로 처리합니다.

**Q. Batch 결과 수신까지 얼마나 걸리나요?**

구글 배치 큐 상태에 따라 다르지만 보통 10분~1시간 내외로 완료됩니다. (API SLA 최대 24시간) 기다리기 번거로우면 `./app.py wait ./lecture_folder --download`로 완료 시점을 자동으로 잡을 수 있습니다.

**Q. 오디오 파일 이름이 한글이어도 되나요?**

됩니다. SDK가 파일명을 HTTP 헤더에 넣어 깨지는 문제를 파일 객체 업로드로 우회하고, 표시 이름은 원래 한글 이름 그대로 보존합니다.

**Q. OpenRouter API Key로 사용 가능한가요?**

불가능합니다. OpenRouter Batch 엔드포인트는 멀티모달 오디오 바이너리 입력을 지원하지 않으므로 Google AI Studio 직접 발급 키만 사용해야 합니다.

---

## 라이선스

MIT License

---

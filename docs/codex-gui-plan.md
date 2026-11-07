# Codex 전사 GUI 통합 — 설계

> 2026-11-07. 기준 구현은 `worktree-codex-asr-experiment`의 `1b1c28b`
> (Codex 백엔드 정리 완료). Windows 연결성은 다른 작업자가 맡으므로 이 문서는
> **macOS GUI/UX**만 다룬다.
>
> **구현 상태 (2026-11-08):** `worktree-codex-gui`에 1~4단계를 넣었다.
> 설정의 전사 방식 섹션, 실험 플래그, 항목별 `Backend`, 러너 두 개와 라우터,
> 모드별 화면 반영, 테스트까지 들어갔다. AIFF 추가 시점 필터(9.7 B)도
> 들어갔다. 남은 것은 실제 Codex 엔드포인트로의 라이브 QA, Windows 연결이다.

## 확정한 방향

- **무료만 Codex**: Codex를 켜면 무료 레인만 Codex가 되고, 유료(Batch)는
  Gemini를 유지한다. 무료 Gemini 키는 지우지 않고 보관만 한다.
- **`Free` → `Codex`**: Codex가 켜지면 상단 무료 세그먼트 라벨과 항목 상세의
  티어 표기를 `Codex`로 바꾼다. 유료는 `Paid` 그대로.
- **새 항목부터 즉시**: `Runner`를 gemini·codex 두 개 만들어 두고, 새 항목을
  현재 선택된 러너로 보낸다. 앱을 재시작하지 않아도 새 파일부터 적용된다.
- **실험 플래그 뒤**: 첫 배포는 설정에 바로 노출하지 않는다. 플래그를 켠
  사용자에게만 `전사 방식` 섹션이 보인다.
- **macOS 우선**: Windows 경로·MSIX·Codex Desktop 버전 감지는 이번 범위가
  아니다. UI가 플랫폼별 분기를 만들지 않게만 해 둔다.

## UI 원칙

- 설정에는 **결정에 필요한 것만** 둔다. 전사 방식 선택, Codex 로그인 상태,
  `다시 확인`. 그 외 차이(8분 조각, PDF 없음, AIFF, 평문, 신호 없음,
  비공식·OpenAI 전송)는 `?` 도움말로 보낸다.
- 사용자를 과잉 설명하지 않는다. `Codex / Paid`는 그대로 쓴다.
- 캡션은 오해가 생기는 곳에만 짧게. 예: "Codex를 쓰는 동안 무료 키는
  사용되지 않아요."
- 토스트는 한 줄. 설정을 열어야 하는 경우에만 액션을 붙인다.
- 새 모달을 만들지 않는다.

## 왜 Runner 두 개인가

Codex 백엔드 작업자가 남긴 메모대로 `Runner`의 백엔드는 `runner.New` 시점에
고정되고, 백엔드의 `defaults`가 조각 길이·키 간격을 바꾼다. 그래서 러너 하나로
토글을 즉시 반영하려면 `Runner` 내부를 항목별 백엔드로 고쳐야 한다.

대신 **백엔드별 러너 두 개**를 두면 내부를 건드리지 않고 같은 효과를 낸다.

- 각 러너는 자기 `Options.Backend`와 그 백엔드의 기본값(65분/8분, 키 간격,
  EarlyEstimate)을 그대로 쓴다.
- `Runner.Start`는 자기 백엔드의 항목만 큐에 넣는다.
- 새 항목은 현재 설정의 러너로 보낸다.
- 예전 항목은 자기 백엔드 러너가 이어서 처리한다. 65분 조각이 Codex로 가는
  사고가 없다.
- 두 러너는 같은 `store`, `workDir`, `transcriptsDir`, `logPath`, `signals`,
  `notify`를 공유한다.

`Runner`에 필요한 최소 변경:

1. `newItem`이 `store.Item.Backend`를 기록한다.
2. `Start`가 자기 백엔드가 아닌 항목을 건너뛴다(빈 `Backend`는 gemini).
3. `PollOnce`가 `r.back.batches() == false`면 아무것도 하지 않는다. Codex
   러너가 Gemini의 `batch_waiting` 항목을 건드리면 `BatchStatus`가
   `error.codexNoBatch`로 실패해 항목을 망가뜨린다.
4. `Retry`가 다른 백엔드 항목을 거절한다(라우터가 잘못 보냈을 때의 안전망).

그 외 `Candididates`·`parts`·`free`·`client`는 이미 백엔드 인터페이스 안에
있어서 손대지 않는다.

## 1. 지금 구조 (확인한 사실)

| 사실 | 근거 |
|---|---|
| GUI는 Wails + `frontend/dist` 정적 HTML/CSS/JS. 빌드 단계 없음 | `frontend/dist` |
| 설정은 톱니바퀴가 여는 **API 키 시트 하나뿐** | `index.html` `#keys-sheet`, `app.js` `openKeys()` |
| 상단 Free/Paid 토글은 **키 유무**로 결정 | `config.Tiers()`, `App.ModeState()` |
| 백엔드는 `runner.New`에서 고정. GUI는 `NALMEOK_BACKEND`, CLI는 `--backend` | `cmd/nalmeok/main.go` `newRuntime()` |
| `Options.Backend`가 `""`/`gemini`/`codex`를 고르고, 백엔드가 기본값을 채움 | `internal/runner/backend.go` |
| 조각 길이: Gemini 65분, Codex 8분. 추가할 때 확정됨 | `plan.UnitMaxSeconds`, `codexasr.UnitMaxSeconds` |
| `store.Item`에는 `Mode`만 있고 **`Backend`가 없다** | `internal/store/store.go` |
| `Runner.Start`는 `StateRunning`인 모든 항목을 큐에 넣음 | `runner.go` `Start()` |
| `PollOnce`는 `batch_waiting`인 모든 항목을 폴링 | `runner.go` `PollOnce()` |
| `Retry`는 저장된 조각을 그대로 씀 | `runner.go` `Retry()` |
| Batch는 Gemini 전용. Codex에서 `SendToBatch`는 `error.codexNoBatch` | `runner.go` `SendToBatch()` |
| 무료 키 상태 신호·health는 Gemini 전용. Codex는 `signals()=false` | `telemetry`, `codex.go` |
| Codex 로그인 상태 문구는 `codexState()`가 만든다 | `cmd/nalmeok/main.go` |
| 설정 파일은 모르는 필드를 보존 | `config.Config.raw` |

## 2. 데이터·설정

### 2.1 `config.json`

- `"backend": "gemini" | "codex"` — 선택 값. `SetPane` 패턴으로 즉시 저장.
  필드가 없거나 모르는 값이면 gemini.
- `"experiments": {"codex": true}` — 실험 노출 플래그. UI가 쓰지 않고 개발자가
  직접 넣거나 환경변수로 켠다. `backend`를 껐다 켜도 섹션이 사라지지 않게
  선택 값과 분리한다.
- 우선순위: `NALMEOK_BACKEND` > `config.backend` > 기본 gemini.
  환경변수가 있으면 설정의 토글을 잠그고 "환경 변수로 고정됨"을 보여 준다.
- 노출 조건: `experiments.codex` 또는 `NALMEOK_BACKEND=codex` 또는
  `config.backend=codex`.

```go
func (c *Config) Backend() string              // "gemini" | "codex"
func (c *Config) SetBackend(name string) error // SetPane 모양
func (c *Config) Experiment(name string) bool  // "experiments" map
```

`SetBackend`는 이전 값을 잡아 두고 `Save` 실패 시 되돌린다.

### 2.2 `store.Item`

- `Backend string` 추가. 빈 값은 기존 항목 호환을 위해 gemini로 읽는다.
- 항목을 추가할 때 현재 백엔드를 기록한다.
- 재시작·재개·`Retry`가 이 값을 쓴다.
- `SendToBatch`는 Codex 항목에 대해 UI에서 막는다(아래 3.4). 굳이 전환하지
  않는다.

### 2.3 러너 라우터

`newRuntime`을 다음 모양으로 나눈다.

```text
shared := cfg, store, workDir, transcriptsDir, logPath, signals, notify
gemini := runner.New(Options{Backend: "", ...shared})
codex  := runner.New(Options{Backend: "codex", ...shared})
router := app.NewRouter(gemini, codex, store, currentBackend)
```

`currentBackend()`는 `NALMEOK_BACKEND`가 있으면 그 값, 없으면 `cfg.Backend()`.

`app.Runner` 구현(라우터)의 규칙:

| 메서드 | 보낼 곳 |
|---|---|
| `Start` | 둘 다 |
| `Add` | `currentBackend()`의 러너 |
| `AddWithPDFs` | gemini만. Codex면 `error.codexNoPDF` |
| `Cancel` / `Remove` | 항목의 `Backend` 러너 |
| `Retry` | 항목의 `Backend` 러너 |
| `SendToBatch` | 항목이 gemini면 gemini 러너; Codex면 `error.codexNoBatch` |
| `Close` | 둘 다 |

CLI는 지금처럼 `newRuntime(backend)` 하나만 쓴다. GUI만 두 러너를 만든다.
두 러너가 같은 로그 파일을 각자 열어도 된다(각자 핸들, append). 문제가 되면
공유 로거 옵션을 나중에 넣는다.

## 3. 화면별 변경

### 3.1 상단 모드 토글
- Codex가 켜지면 무료 세그먼트 라벨을 `Codex`로 바꾼다. 세그먼트 폭이
  바뀌어도 레이아웃이 흔들리지 않게 한다.
- Codex 로그인 없음/만료면 무료 세그먼트를 `aria-disabled`로 두고, 클릭 시
  "Codex 로그인이 필요해요" 토스트 + 설정 열기.
- 유료 세그먼트는 지금처럼 유료 키 유무로 결정.
- Codex 사용 중에는 무료 키 health 점·행을 숨긴다.
- 토글 표시 조건: `무료 레인 사용 가능 || 유료 키 있음 || backend==codex`.
  로그인이 없어도 Codex 세그먼트를 보여 줘야 이유를 설명할 수 있다.

### 3.2 설정 시트
- 제목을 `API 키`에서 `설정`으로 넓히고, 키 섹션 제목은 유지한다.
- 맨 위에 **전사 방식** 섹션(실험 플래그가 켜졌을 때만):
  - `Gemini API 키` / `Codex (ChatGPT 로그인)` 선택.
  - Codex 상태 줄: `로그인 있음 · <경로>` / `만료됨` / `없음`.
    `codexState()`의 문구를 재사용한다.
  - `다시 확인` 버튼(앱 밖에서 로그인했을 때).
  - 비공식·데이터 전송 고지와 차이 설명은 `?` 도움말로 보낸다. 설정에는
    넣지 않는다.
  - 적용 안내는 전환할 때 토스트 한 줄로만.
- Codex 사용 중 무료 키 목록:
  - 지우지 않고 보여 주되 "Codex를 쓰는 동안에는 사용되지 않아요" 캡션.
  - 또는 접어서 숨긴다. 숨겨도 `+ 무료 키 추가`는 남길지 정한다.
- 신호 설정은 그대로. Codex에서는 신호가 전송되지 않는다는 설명을 추가.
- 환경변수로 백엔드가 고정되면 선택을 비활성화하고 이유를 보여 준다.

### 3.3 빈 화면·파일 추가
- `hasKeys()`·`emptyAsksForKey()`를 **현재 무료 레인 사용 가능 여부**로 바꾼다.
  - Codex 정상: "파일을 떨어뜨리세요".
  - Codex 로그인 없음: "Codex에 로그인하세요" + 설정 열기.
  - Gemini: 지금 문구.
- `items:addFailed`의 `noKeys` 플래그도 같은 기준으로 계산한다.
- Codex 모드에서는 PDF 선택 시트를 띄우지 않는다. `holdPDFChoices`가
  `hasKeys()`가 아니라 현재 백엔드를 본다. 단 Paid는 Gemini Batch로 가서
  PDF를 쓰므로, Codex 모드라도 Paid일 때는 시트를 띄운다.
- 키 피커는 Codex일 때 숨긴다(새 파일이 키를 쓰지 않으므로).
- AIFF는 추가할 때 걸러 내고 나머지만 추가한 뒤 토스트로 알린다
  (`error.codexAIFFSkipped`). AIFF가 든 폴더도 통째로 건너뛴다.

### 3.4 목록 행·상세 패널
- 같은 목록에 Gemini 무료 항목과 Codex 항목이 섞일 수 있다.
- 상세 패널 `paneMeta()`: Codex 항목이면 `Codex`, 아니면 지금처럼
  `Paid`/`Free`.
- Codex 항목의 비용·예상 비용은 표시하지 않는다.
- 무료 health 안내(`freeTrouble`)는 Codex 항목에 붙이지 않는다.
- Codex 항목의 "유료로 이어서"(`batch`) 액션은 숨긴다. `SendToBatch`가
  `error.codexNoBatch`를 내는 경로를 UI에서 만들지 않는다.
- `free_exhausted` 안내 문구는 Codex 항목에 나오지 않게 한다.

### 3.5 도움말
- `Free` 설명을 백엔드에 따라 바꾼다.
  - Gemini: 지금 문구.
  - Codex: "ChatGPT 로그인으로 바로 전사 · 한도는 ChatGPT 정책 · 조각 8분 ·
    PDF 없음 · 평문 결과 · 비공식 기능".
- 지원 형식에서 Codex일 때 AIFF를 뺀다.
- 데이터 전송처(OpenAI)를 한 줄 넣는다.

### 3.6 토스트·오류
- 로그인 없음: 설정 열기 액션이 있는 토스트.
- 로그인 만료: "Codex를 실행해 다시 로그인" + `다시 확인`.
- AIFF: "Codex는 AIFF를 받지 않아요 · WAV/MP3로 변환해 주세요".
- 429/한도: "Codex가 잠시 요청을 제한했어요 · 자동으로 다시 시도 중"과 최종
  실패 문구.
- PDF: Codex 모드에서는 시트 자체가 뜨지 않는다.

### 3.7 CLI·doctor
- `--backend codex`와 `NALMEOK_BACKEND`는 그대로 둔다.
- `doctor`의 Codex 로그인 줄은 이미 있다.
- GUI 설정과 환경변수가 충돌하면 환경변수가 이긴다.

## 4. 앱 바인딩·상태

`ModeState`를 확장한다.

```go
type ModeState struct {
    Mode        string // "free" | "batch"
    Free        bool   // 현재 백엔드의 무료 레인 사용 가능
    Paid        bool
    Backend     string // "gemini" | "codex"
    Codex       string // "ok" | "expired" | "missing" | ""
    CodexPath   string
    CodexForced bool   // NALMEOK_BACKEND
    Experiment  bool   // experiments.codex
}
```

- `Free`: Codex면 로그인 유효, Gemini면 무료 키 존재.
- `Mode`: Codex면 항상 `free`, Gemini면 `EffectiveMode()`.
- `GetMode()`도 같은 규칙.
- `hasKeys()`는 `freeAvailable || paid`로 바꾼다.
- 새 바인딩:
  - `Backend() string`
  - `SetBackend(name string) error`
  - `CodexStatus() (state, path string)` — `codexasr.Find()` + `Expired()`
  - `CheckCodex()` — 다시 읽기(캐시 없이 같은 값 반환)
- 로그인 상태는 앱 시작 때와 `CheckCodex`에서만 읽는다. 실행 중 만료되면
  요청이 실패하고, 재로그인 후 `Retry`로 이어 간다.

### 상태 표

| Codex 로그인 | 실험 플래그 | 무료 레인 | 화면 |
|---|---|---|---|
| 유효 | 켜짐 | Codex | `Codex` 세그먼트 활성 |
| 만료 | 켜짐 | 사용 불가 | `Codex` 비활성 + 만료 안내 |
| 없음 | 켜짐 | 사용 불가 | `Codex` 비활성 + 로그인 안내 |
| — | 꺼짐 | Gemini 무료 키 | 지금과 같음 |

## 5. Codex와 Gemini 무료의 차이

| 항목 | Gemini 무료 | Codex |
|---|---|---|
| 준비 | API 키 | 이 컴퓨터의 ChatGPT 로그인 |
| 비용 | 무료(키 한도) | 앱은 무료, 실제로는 ChatGPT 구독 필요 |
| 요청 조각 | 최대 65분 | 최대 8분 |
| PDF 참고문서 | 사용 | 불가 |
| Batch | 불가 | 불가 |
| 형식 | AIFF 포함 | AIFF 불가 |
| 결과 | 프롬프트 기반 정돈된 평문 | 끝점이 준 평문 |
| 사용량·비용 표시 | 무료(비용 없음) | 없음 |
| 상태 신호 | 전송 | 전송 안 함 |
| 실패 안내 | 무료 한도·서버 혼잡 | 로그인·429·형식 |

## 6. 위험

- **약관·비공개 엔드포인트**: Codex가 예고 없이 바뀌거나 막을 수 있다.
  실험 플래그로 노출 범위를 줄인다.
- **자격증명**: 다른 앱의 토큰 파일을 읽는다. 읽기만 하고 로그·텔레메트리에
  남기지 않는다. 설정에 고지한다.
- **배포**: README·Store에 실리는 순간 숨은 실험이 아니다. 첫 배포는 플래그
  뒤에 둔다.
- **AIFF**: macOS 사용자에게 흔하다. MVP는 실패 사유 안내, 다음 단계에서
  사전 차단·변환을 검토한다.
- **품질**: 긴 침묵·전문용어·화자 구분은 Gemini 프롬프트만큼 다듬어지지 않는다.
- **지원 부담**: "Codex 로그인이 안 돼요" 문의가 늘 수 있다.

## 7. 단계

0. **기준점**: `worktree-codex-asr-experiment`의 `1b1c28b`에서 GUI 워크트리를
   분기. `go test ./...` 통과 확인.
1. **상태 기반**: `config.Backend`/`SetBackend`/`Experiment`, `store.Item.Backend`,
   러너 `Start` 필터·`PollOnce` 가드·`Retry` 가드, `ModeState` 확장,
   두 러너 라우터. GUI 없이 Go 테스트로 고정.
2. **설정 UI**: 전사 방식 섹션, 로그인 상태, 다시 확인, 저장, 적용 안내.
3. **화면 반영**: 상단 라벨, 빈 화면, 키 피커 숨김, 상세 패널, health 숨김,
   PDF 시트 차단, Batch 액션 숨김.
4. **예외 UX**: AIFF 사전 차단, 로그인 만료·429 안내.
5. **문서**: `development.md`에 실험 플래그를 켜는 법. `usage.md`/README는
   노출 수준이 정해진 뒤.
6. **QA**: 아래 매트릭스.

## 8. QA 매트릭스 (macOS)

- 로그인: 없음 / 만료 / 정상 / 앱 실행 중 만료.
- 키: 없음 / 무료만 / 유료만 / 둘 다.
- 전환: 유휴 / 대기 항목 있음 / 실행 중 항목 있음 / 앱 재시작 후 재개.
- 입력: 짧은 파일 / 8분 초과 / 2시간 / 폴더 + PDF 여러 개 / AIFF / WAV·MP3·M4A.
- 실패: 429 / 401 / 네트워크 끊김 / 빈 응답.
- 화면: 한국어·영어, 라이트·다크, 키보드만, 스크린리더, 좁은 창.
- 회귀: Codex를 끈 상태에서 기존 Gemini 무료·유료 흐름이 그대로인지.
- Windows는 이번 범위가 아니다. UI가 macOS 전용 분기를 만들지 않는지만 확인.

## 9. 작업 전에 정한 것 (디자인/UX)

아래는 2026-11-08에 추천안으로 확정했다. 1·3·4는 특히 구현 방향을 갈랐다.

### 9.1 설정 화면의 형태 — 구조가 갈림

- **A. 기존 API 키 시트를 `설정`으로 넓히고 맨 위에 전사 방식 섹션 (권장)**
  새 시트·포커스 트랩·메뉴를 안 만들어도 되고 가장 빠르다.
- **B. 별도 설정 시트/탭** — 정보 구조는 깔끔하지만 시트가 하나 더 늘고,
  사용자가 "API 키"와 "설정" 중 어디를 열어야 하는지 배워야 한다.
- **C. 상단 바에 제공자 선택** — 눈에 잘 띄지만 상단이 붐비고, 모드 토글과
  의미가 겹친다.

### 9.2 Codex 사용 중 무료 Gemini 키 표시 — 신뢰 문제

- **A. 키 목록은 그대로 두고 "Codex를 쓰는 동안에는 사용되지 않아요" 캡션
  (권장)** — 키가 사라진 것처럼 보이지 않는다.
- **B. 접어서 숨기기** — 깔끔하지만 "내 키 어디 갔지"가 될 수 있다.
- **C. 무료 키만 비활성 스타일** — 명확하지만 목록이 복잡해진다.

### 9.3 로그인 없음/만료일 때 Codex 선택 — 막다른 길 방지

- **A. Codex 선택을 비활성하고 `다시 확인`만 제공 (권장)**
  로그인 전에 선택해서 "무료 전사가 안 되는 상태"에 들어가는 일을 막는다.
- **B. 선택은 허용하되 상단 Codex 세그먼트를 비활성** — 설정을 미리 해 두는
  것은 가능하지만, 사용자가 왜 안 되는지 상단만 보고는 모를 수 있다.
- **C. 자동으로 Gemini 무료 키로 폴백 (비권장)** — 조용히 다른 서비스로
  보내는 것은 비용·개인정보 기대를 깨뜨린다.

예외: 이미 `backend=codex`인데 로그인이 만료되면 선택은 유지하고, 상단
세그먼트를 비활성 + 설정에 만료 안내를 띄운다. Gemini로 돌아가는 것은 항상
허용한다.

### 9.4 앱 시작 시 Codex 로그인 없음 + 미완료 항목 — 실패 처리 방식

- **A. 항목을 실패 처리하고 "로그인 후 Retry" 안내 (v1 권장)**
  구현이 작고, 조각이 이미 저장돼 있어 Retry로 이어진다. 대신 시작하자마자
  실패 알림이 여러 개 뜰 수 있다.
- **B. Codex 러너를 시작하지 않고 "로그인 후 이어서" 안내**
  UX는 좋지만 `Runner`에 "일시 정지/다시 시작" 개념이 필요해 작업이 늘어난다.
- **C. 자동 재시도** — 로그인이 풀린 동안 계속 실패할 수 있어 권하지 않는다.

### 9.5 전환 안내 — 대화상자 여부

- **A. 토스트만 (권장)**: "새 파일부터 Codex로 전사해요. 진행 중인 항목은
  이전 방식으로 계속돼요."
- **B. 확인 대화상자**: 실수로 바꾸는 것을 막지만, 되돌리기 쉬운 설정이라
  과하다.

### 9.6 고지·동의 — 약관/개인정보

- **A. `?` 도움말에만 (실험 단계 권장)** — 설정 화면에는 고지 문단을 두지
  않는다.
- **B. 처음 켤 때 한 번 확인 모달**: "비공식 기능이며 오디오가 OpenAI로
  전송됩니다."
- **C. 체크박스 동의**: 정식 배포에 가까워지면 필요할 수 있다.

### 9.7 AIFF 처리 — macOS에서 흔함

- **A. 항목 실패 사유로 안내 (v1)** — 구현은 작지만 긴 파일을 계획한 뒤
  실패해서 답답하다.
- **B. 추가 시 AIFF만 걸러 내고 나머지는 추가 + 토스트 (권장)**
  "AIFF 2개는 Codex에서 지원하지 않아 건너뛰었어요."
- **C. 변환 지원** — 이번 범위 밖.

### 9.8 목록에서 백엔드 표시

- **A. 상세 패널에만 `Codex` (권장)** — 행은 지금처럼 깔끔하게.
- **B. 행에도 작은 `Codex` 배지** — Gemini 무료 항목과 섞였을 때 한눈에
  보이지만 행이 복잡해진다.

### 9.9 상단 라벨 쌍

- **`Codex / Paid` (권장)** — 그대로 쓴다. 별도 설명을 붙이지 않는다.
- `Codex / Batch` — 정확하지만 사용자에게 낯선 내부 용어.
- `Codex / Paid (Gemini)` — 가장 정확하지만 세그먼트가 길어진다.

### 9.10 설정 시트 제목

- **`설정` / `Settings` (권장)** — 전사 방식이 들어가므로.
- `API 키` 유지 — 변화는 적지만 제목이 내용과 어긋난다.

### 9.11 실험 플래그를 켜는 법

- **config.json의 `experiments.codex` 또는 `NALMEOK_EXPERIMENTS=codex`만
  (권장)** — UI에 숨은 제스처를 만들지 않는다. `development.md`에 적는다.
- 빌드 태그 — 릴리스 산출물에서 코드를 아예 빼지만, 테스트·스크린샷 경로가
  갈라진다.

## 10. 내가 기본값으로 정하고 진행해도 되는 것

이견 있으면 말해 줘. 없으면 이대로 간다.

- 로고/브랜드 마크는 쓰지 않는다(OpenAI 제휴로 오해될 수 있음).
- 상태 점은 기존 health/status 점 스타일을 재사용한다.
- Codex 사용 중 무료 health 점·행은 숨긴다.
- 무료+Codex일 때 키 피커는 숨기고, Paid 모드에서는 유료 키 피커를 보여 준다.
- Codex 항목의 "유료로 이어서" 액션은 숨긴다.
- Codex 로그인 실패 시 Gemini 무료 키로 자동 폴백하지 않는다.
- 도움말은 현재 백엔드에 맞춰 동적으로 바꾼다.
- 새 모달은 만들지 않고 기존 시트 안에서 해결한다.
- 문구 초안은 내가 쓰고, 너가 한국어·영어 톤만 검수한다.
- 두 러너가 같은 로그 파일을 각자 핸들로 연다.
- CLI는 `--backend` > `NALMEOK_BACKEND` > `config.backend` > gemini 순으로
  읽어 GUI 설정과 어긋나지 않게 한다.
- 기존 `items.json` 항목은 `Backend`가 없으면 gemini로 읽는다.

## 11. 우려되는 것 (결정은 아니지만 알고 있어야 함)

- **무료 키가 "안 쓰이는" 상태**가 데이터 손실처럼 보일 수 있다. 캡션·상태
  표시가 중요하다.
- **키 피커가 사라지는 것**은 기존 사용자에게 기능이 줄어든 것처럼 보일 수
  있다. Paid에서는 유지되는 것을 명확히.
- **로그인 상태가 설정에만 있으면** 상단 Codex가 왜 비활성인지 모를 수 있다.
  클릭 시 토스트로 설정을 열어 주는 흐름이 필요하다.
- **혼합 목록**: Codex 항목과 Gemini 무료 항목이 섞이면 Retry·Batch 가능
  여부가 다르다. 상세 패널의 백엔드 표기가 유일한 단서가 될 수 있다.
- **두 러너 동시 실행**: Codex와 Gemini 무료 작업이 동시에 돌 수 있다. 네트워크
  사용량이 늘고, 사용자는 두 작업이 같이 도는 이유를 모를 수 있다.
- **긴 Codex 항목**: 8분 조각이 많아져 진행률이 자주 움직인다. 요청 수가
  많다는 것은 `?` 도움말에 적는다.
- **토큰 만료**: 실행 중 만료되면 항목이 실패하고 사용자가 직접 Retry해야
  한다. 자동 복구는 없다.
- **실험 플래그 제거**: 나중에 플래그를 없앨 때 `backend=codex` 사용자가
  설정을 잃지 않도록 노출 조건을 `backend=codex`에도 걸어 둔다.
- **배포 책임**: `?` 도움말 고지만으로 공개 배포의 약관·자격증명 리스크가
  충분한지는 별도 판단이 필요하다.

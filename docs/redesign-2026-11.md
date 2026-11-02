# Nalmeok 재설계: Go + Wails 데스크톱 앱 (2026-11-02)

> 상태: **확정, 구현 중**. 이 문서가 `docs/review-2026-11.md`의 작업 모델·감시자·대시보드 계획(C6·C7·C12, §3-3, D3·D4)과 런타임 계획(§3-1 uv)을 대체한다.
> 기존 Python 앱(`nalmeok/`)은 Go 앱이 기능을 갖출 때까지 남겨 두고, 이후 지운다.

## 0. 한 줄 요약

**음성 파일이나 폴더를 창에 놓으면 원본 옆에 `.md`가 생긴다.** 처리 방식은 무료(바로)와 유료 Batch(저렴, 대기) 둘이다. 지나간 전사는 목록에 남고 검색할 수 있다.

## 1. 결정 사항

| # | 결정 |
|---|---|
| R1 | 언어 **Go**, GUI **Wails v2**(OS 내장 웹뷰). 실행 파일 하나로 배포한다. Python, SDK, ffmpeg 준비 과정을 없앤다 |
| R2 | **유료 Batch는 핵심이라 유지한다.** 무료 live와 함께 토글 하나로 고른다 |
| R3 | 폴더를 놓으면 그 안 오디오를 이름 순서로 이어 **`.md` 하나**로 만든다. 위치는 폴더 안 `<폴더명>.md` |
| R4 | 무료 한도가 바닥나면 **버튼을 눌러** 남은 분량을 유료 Batch로 보낸다. 자동 혼합은 하지 않는다 |
| R5 | 처리 목록은 **지나간 항목도 계속 쌓이고, 검색창**으로 이름·경로를 찾는다 |
| R6 | 원본 폴더에는 결과 `.md` 말고 아무것도 만들지 않는다. 상태는 앱 데이터 폴더에 둔다 |
| R7 | 키는 기존 Python 설정 파일(`config.json`의 `keys`)을 그대로 읽고 쓴다. 다시 입력할 필요가 없다 |
| R8 | macOS 서명·공증은 기존 절차(CLAUDE.md)를 따른다. Windows 코드서명은 선택이다 |

## 2. 비슷한 앱 조사 요약

MacWhisper와 Aiko는 창에 놓으면 확인 없이 바로 전사한다. Buzz는 작업 목록·뷰어 중심이라 무겁다. TurboScribe는 클라우드 비동기 처리를 "끝나면 알림" 하나로 보여 준다. Nalmeok은 **놓으면 바로 시작, 결과는 파일, 비동기는 대기 표시와 알림**으로 간다.

## 3. 기능

### 3-1. 입력
- 창 어디에든 **파일이나 폴더**를 놓거나, 위쪽 `+`(목록이 비었으면 가운데 안내)를 눌러 파일 선택 창을 연다. 여러 개를 한 번에 놓을 수 있다.
- 받는 형식: `.m4a`, `.mp4`(AAC 오디오), `.mp3`, `.wav`. 그 밖의 형식이나 AAC가 아닌 MP4는 "지원하지 않는 형식"으로 실패 처리한다.
- 폴더: 바로 아래의 오디오(하위 폴더 제외)를 **자연 정렬**(`2강` < `10강`)해서 쓴다. 1~10개. PDF가 1개 있으면 용어 참고자료로 첨부한다(2개 이상이면 실패).
- 놓자마자 시작한다. 확인 단계는 없다.

### 3-2. 처리 방식
- 토글: **무료**(등록한 무료 키를 돌려 가며 live 호출) / **유료**(Batch, 약 74원/시간, 보통 10분~1시간). 화면에는 "무료 | 유료"만 쓰고 설명은 `?` 도움말에 둔다. 선택값은 저장해 두고 다음 드롭부터 적용한다.
- 키가 한 종류뿐이면 그 방식이 선택되고 다른 쪽은 비활성화된다(숨기지 않는다). 이때 선택은 저장된 값을 덮어쓰지 않아, 나중에 두 종류가 되면 사용자의 마지막 선택이 돌아온다. 키가 없으면 키 설정을 먼저 띄운다.
- 무료 처리 중 **모든 무료 키가 한도(429 일일 한도)에 걸리면** 그 항목은 `free_exhausted` 상태가 된다. 유료 키가 있으면 행에 "오늘 무료 사용량을 다 썼어요"와 "유료로 이어서" 버튼을 띄운다. 이미 끝난 조각은 그대로 쓰고 남은 조각만 Batch로 보낸다.

### 3-3. 처리 목록 (R5)
- 행마다 이름과 상태 하나만 보인다: `running`(진행 막대, 진행률을 모르면 흐르는 막대) · `batch_waiting`("기다리는 중 · n분") · `free_exhausted`(버튼) · `done`(끝난 때: "오늘 21:08", "어제 14:02", "11월 2일", "2025년 11월 2일". 완료는 기본 상태라 따로 말하지 않는다) · `failed`("실패 · 사유") · `canceled`("취소됨").
- 보이는 동작은 행마다 하나다. 완료 행은 오른쪽에 회색 폴더 아이콘(**Finder에서 보기**, Windows: 탐색기에서 보기. .md를 ChatGPT 프로젝트에 올리기 위해서)만 두고, 행을 누르면 파일을 연다. 실패 행은 **다시 시도**.
- 나머지(열기·복사·Finder에서 보기·취소·다시 시도·이름 변경·삭제)는 오른쪽 클릭 메뉴에 있다. 트랙패드 두 손가락 가로 스와이프나 마우스 끌기로 행 오른쪽에 **이름 변경**(진행 중이면 **취소**)과 **삭제**가 나오고, 끝까지 밀면 삭제한다.
- 이름은 오디오 확장자(.m4a·.mp4·.mp3·.wav)를 빼고 보여 준다(폴더 이름은 그대로). 이름 변경: 완료 항목은 같은 폴더의 `.md` 파일 이름도 바꾼다(같은 이름이 있으면 번호를 붙인다). 삭제는 목록에서만 지우고 파일은 그대로 둔다. 4초 동안 "실행 취소"할 수 있다.
- 최신 항목이 위로 온다. 상단 **검색창**은 이름과 경로를 부분 일치로 거른다.
- **복사**는 `.md` 전체를 클립보드에 넣는다.

### 3-4. Batch 대기
- 창이 열려 있으면 30초마다 대기 중인 Batch를 조회한다. 끝나면 `.md`를 저장하고 OS 알림을 띄운다.
- 앱을 닫아도 Batch는 서버에서 진행된다. **앱을 켜면 대기 항목부터 조회**해서 마무리한다. 백그라운드 프로세스는 없다.
- 업로드한 파일은 48시간 뒤 만료된다. Batch 생성 전에 만료됐으면 다시 올린다.

### 3-5. 결과 파일
- 파일: `강의09.m4a` → 같은 폴더의 `강의09.md`
- 폴더: `ch07/` → `ch07/ch07.md`. 조각 결과를 순서대로 빈 줄 하나로 잇는다.
- 같은 이름이 있으면 `강의09 (2).md`처럼 번호를 붙인다. 덮어쓰지 않는다.
- 내용은 전사 본문만 쓴다(머리말 없음).

### 3-6. 키와 업데이트
- ⚙ "API 키" 시트: 키 목록(이름, 뒤 4자리), 추가(등급·키만 받고 이름은 "무료 1", "유료 1"처럼 자동으로 붙인다. 저장 전 `models.get` 같은 무과금 호출로 확인), 삭제. 키가 없으면 "키 받는 곳" 링크를 보여 준다.
- 시작할 때 manifest를 확인한다. 새 버전이 있으면 창 아래쪽에 한 줄 배너를 띄운다(1차 범위에서는 링크만).

### 3-7. 없애는 것
TUI 전체, 작업 단계 9종, 여러 폴더 제출, 원격 새로고침, 감시자, 예상 비용 화면, 사용량, 고급 메뉴, 자동 혼합, 폴더 안 상태 파일 15종, CLI 단계 명령, 런처·부트스트랩·Python 런타임 준비.

## 4. 화면

창 하나(약 480×680). 위에서부터: 통합 툴바(macOS는 제목 막대를 투명하게 하고 신호등 단추 줄에 가운데 무료|유료 토글, 오른쪽 `+`를 둔다. 빈 곳을 끌면 창이 움직인다. Windows는 기본 제목 막대 아래에 같은 막대) → 검색창(항상 보임) → 처리 목록(구분선만 있는 Mail·Finder식 목록) → 얇은 아래 막대의 오른쪽 `?`·⚙. 시트는 오른쪽 위 "완료"로 닫는다. 드롭 상자는 따로 없고 창 전체가 드롭을 받는다(끌어 오면 창 테두리가 옅게 강조된다). 목록이 비었으면 가운데에 파형과 "음성 파일이나 폴더를 놓으세요"만 둔다. 키가 한 등급뿐이면 다른 쪽을 비활성화한다(숨기지 않는다). 설명은 모두 `?` 도움말 시트로 보낸다. 강조색은 딥 그린 하나, 라이트·다크 모드를 모두 지원한다. 시안 이미지는 저장소 밖 루트 폴더의 `mock-main.png`, `mock-empty.png`.

## 5. 구현 계약

### 5-1. 디렉터리

```
go.mod                      module github.com/jseook11/nalmeok
cmd/nalmeok/main.go         Wails 앱 진입. frontend/dist를 embed
internal/config/            경로, config.json(키·설정) 읽기·쓰기
internal/media/             입력 스캔, 오디오 길이, AAC→M4A 리먹스, 분할
internal/plan/              입력 → 조각(Unit) 계획, 예상 비용 (순수 함수)
internal/gemini/            업로드, live 호출(키 순환), Batch 제출·조회·결과
internal/store/             items.json(목록·상태), 검색
internal/output/            조각 결과 병합, .md 쓰기, 이름 충돌 처리
internal/runner/            항목 실행기: plan → 업로드 → live/Batch → output, 재개
internal/app/               Wails 바인딩(프런트가 부르는 메서드), 이벤트 발행
frontend/dist/              index.html, app.css, app.js (빌드 단계 없는 순수 파일)
scripts/build_mac_app.sh    go build + .app 번들 + (자격이 있으면) 서명·공증
```

Wails CLI 없이 `go build -tags desktop,production -o build/nalmeok ./cmd/nalmeok`으로 빌드한다. `-o`를 빼면 실행 파일 이름이 `nalmeok`이 되어 저장소 루트의 Python 패키지 폴더 `nalmeok/`와 겹치므로 반드시 붙인다. 의존 방향은 `app → runner → gemini · media · plan · output · store → config` 한쪽이다.

### 5-2. 데이터

- **경로**: 설정 폴더는 Python과 같다. `$XDG_CONFIG_HOME/nalmeok`, Windows는 `%APPDATA%\nalmeok`, 그 밖에는 `~/.config/nalmeok`. 여기에 `config.json`(기존), `items.json`(새로), `work/<item-id>/`(임시 조각과 참고 PDF 사본 `reference.pdf`, 완료 시 삭제), `nalmeok.log`.
- **config.json**: 기존 형식 `{"keys":[{"id","label","tier":"free|paid","api_key","limit_minutes"}], "recent":[...]}`. Go 앱은 `keys`만 쓰고 모르는 필드는 보존한다. 새 설정은 `"gui":{"mode":"free|batch"}`에 둔다. 파일 권한 0600.
- **items.json**: `{"items":[Item...]}`. 쓰기는 임시 파일 → rename.

```go
type Item struct {
    ID        string    // 시간 기반 고유값
    Source    string    // 놓은 파일 또는 폴더의 절대 경로
    Kind      string    // "file" | "folder"
    Name      string    // 표시 이름
    Mode      string    // "free" | "batch"
    State     string    // running | batch_waiting | free_exhausted | done | failed | canceled
    Progress  float64   // 0~1 (running일 때)
    Detail    string    // 한 줄 상태·오류 설명
    Output    string    // 완료된 .md 경로
    AudioSec  float64
    CreatedAt, UpdatedAt time.Time
    Units     []Unit    // 실행 계획과 조각별 진행 (재개에 쓴다)
    Batch     *BatchRef // 제출한 Batch 정보 (batch_waiting)
}
type Unit struct {
    Index   int
    Files   []string  // 이 요청에 들어가는 오디오 조각 경로(work/ 안)
    Seconds float64
    Text    string    // 완료된 전사 결과 (비어 있으면 미완)
}
type BatchRef struct {
    KeyID, Name string    // Batch 리소스 이름
    SubmittedAt time.Time
    Uploads     map[string]Upload // 로컬 경로 → 업로드 정보
}
type Upload struct { URI, MIME string; UploadedAt time.Time }
```

### 5-3. 전사 규칙 (Python 구현을 기준으로 옮긴다)

- 모델 `gemini-3.8-flash`, `thinking_level=low`. 프롬프트는 `nalmeok/constants.py`의 `PROMPT`와 `LIVE_PROMPT`를 글자 그대로 옮긴다(`internal/gemini/prompt.go`, 테스트가 일치를 검사한다). 요청에 오디오 파트가 둘 이상이면 `LIVE_PROMPT`, 하나면 `PROMPT`를 쓴다. 아래 조각 계획 때문에 요청의 오디오 파트는 항상 하나이므로 실제로는 `PROMPT`만 쓰인다.
- 요청 하나의 구성: 프롬프트 텍스트 + (PDF가 있으면 PDF 파일 파트) + 오디오 파일 파트 **하나**. 폴더의 여러 파일을 한 요청에 묶지 않는다(2026-11-02 실측: 두 강의를 한 요청에 넣으면 `blockReason=OTHER`, 파일마다 따로 보내면 통과).
- **조각 계획**: 원본 오디오 파일 하나당 요청 하나. 파일이 130분을 넘으면 130분 단위로 잘라 파일 하나가 여러 요청이 된다. 결과는 파일 순서, 조각 순서대로 이어 붙인다. 오디오 토큰은 초당 32로 계산한다(무료 TPM 250k 기준). 이 규칙은 `plan` 패키지의 순수 함수로 두고 테스트한다.
- **오디오 준비**: m4a/mp4는 AAC 프레임을 꺼내 **M4A(MP4 컨테이너, `audio/m4a`)** 로 리먹스한다(재인코딩 없음). 분할은 AAC 프레임 경계에서 한다. mp3는 `audio/mp3`로 그대로 올리고 분할은 프레임 경계, wav는 `audio/wav`로 그대로 올리고 분할은 샘플 경계에서 한다.
- **업로드는 키별**이다. 파일 URI는 올린 키의 프로젝트에서만 쓸 수 있으므로 키를 바꾸면 다시 올린다.
- **무료 live**: 무료 키를 순서대로 쓴다. 429를 받으면 다음 키로 넘긴다. 요청 시작 간격은 최소 12초(키별). 일시 오류(5xx, 네트워크)는 10초에서 시작해 최대 2분까지 늘어나는 지수 백오프로 5회 다시 보낸다(재시도 중 행에는 "Gemini 서버가 붐벼요 · 다시 시도 중"). 모든 무료 키가 429면 `free_exhausted`.
- **유료 Batch**: 유료 키로 미완 조각마다 요청 하나를 만든다. 요청 키(`key`)는 `unit-<index>`다. JSONL 파일을 올리거나 inline 요청으로 Batch를 만든다. 성공하면 결과를 키로 맞춰 `Unit.Text`에 넣고, 실패한 요청이 있으면 `failed`로 두고 "다시 시도"에서 그 조각만 다시 보낸다.
- 결과 텍스트가 비어 있거나 `MAX_TOKENS`로 끊기면 그 조각을 실패로 본다.
- `promptFeedback.blockReason`이 오면 그 조각은 `BLOCKED_<reason>`으로 실패 처리한다(live·Batch 모두). 화면에는 "Gemini가 이 오디오를 처리하지 않았어요"만 보여 주고, 원인은 로그에 남긴다.
- 무료 키 요청 간격은 키별 최소 12초이고, 조각이 길면 TPM 250k에 맞춰 늘린다(130분 조각이면 60초). Python `live_gap_seconds`와 같다.
- 완료(`done`)되면 `.md`가 기록이므로 `Units`와 `Batch`를 비워 `items.json`을 작게 유지한다.
- 키 추가 시 확인(`models.get`)은 키가 거부될 때(401·403·`API_KEY_INVALID`)만 저장을 막는다. 네트워크 오류면 그대로 저장한다(Python `check_key`와 같다).

### 5-4. 프런트와 백엔드 계약 (Wails 바인딩)

```go
// internal/app
func (a *App) ListItems(query string) []Item       // 검색 포함, 최신순
func (a *App) AddPaths(paths []string) error       // 드롭 또는 선택한 경로
func (a *App) PickFiles() error                    // OS 파일 선택 창 → AddPaths
func (a *App) Cancel(id string) error
func (a *App) Retry(id string) error
func (a *App) SendToBatch(id string) error         // free_exhausted → 유료 Batch
func (a *App) Remove(id string) error              // 목록에서만 지움
func (a *App) OpenOutput(id string) error          // OS 기본 앱
func (a *App) RevealOutput(id string) error        // Finder/탐색기
func (a *App) CopyOutput(id string) error          // 클립보드
func (a *App) GetMode() string; func (a *App) SetMode(m string) error
func (a *App) ModeState() ModeState               // 실제 모드 + 키가 있는 등급
func (a *App) Keys() []KeyView                     // 이름·등급·뒤 4자리
func (a *App) AddKey(label, tier, key string) error  // 확인 후 저장
func (a *App) DeleteKey(id string) error
```

- 항목이 바뀔 때마다 이벤트 `items:changed`를 발행하고, 프런트는 `ListItems`를 다시 부른다.
- 파일 드롭은 Wails 런타임의 `OnFileDrop`으로 받는다.

## 6. 작업 분담

| 단계 | 담당 | 내용 |
|---|---|---|
| 1 | dlaude | 골격: go.mod·의존성, `cmd`, `config`, `store`, `output`, `app` 바인딩 껍데기, 프런트(시안 기준), 빌드 스크립트. 테스트 포함 |
| 2a | Opus 서브에이전트 | `media`(M4A 리먹스·분할·길이)와 `plan`. 테스트 음원은 macOS `afconvert`·`say`로 만든다 |
| 2b | Opus 서브에이전트 | `gemini`와 `runner`(live 키 순환, Batch, `free_exhausted`, 재개). 가짜 클라이언트로 테스트 |
| 3 | 오케스트레이터 | 통합, 짧은 실제 음원으로 무료·Batch 실측, `.app` 빌드, 문서 정리 |

## 7. 남은 확인

- [ ] Windows(`jswd`)에서 빌드·드롭·WebView2 동작
- [ ] Batch 실측 소요 시간과 비용이 문서 수치와 맞는지
- [ ] Go 앱이 기능을 갖추면 Python 앱, `scripts/` 런처, 관련 문서 삭제

# 코드 정리·리뷰 점검

작업을 끝내고 커밋·머지하기 전에, 또는 "정리해/점검해"를 받았을 때 항목마다 적용한다. 범위는 둘 중 하나다.
이번 diff(작업 후 점검), 또는 영역 전체(쌓인 것을 한 번에 치우는 정리). 스킬 `code-cleanup`이 이 문서를 부른다.
`development.md`의 규칙을 다시 적지 않는다. 여기 있는 것은 코드를 읽으면서 놓치기 쉬운 것이다.

## 돌리는 법

### diff 점검

1. 범위를 정한다: 작업 브랜치면 `git diff master...HEAD`, 지정한 커밋 범위가 있으면 그것.
2. diff에서 **새로 생긴 이름**(타입·함수·상수·`App` 메서드·`json` 태그·문구 키·config 키·CSS 클래스)과
   **새로 쓴 조건식**을 뽑는다.
3. 아래 항목을 차례로 본다. grep은 `internal/`·`cmd/`·`frontend/dist/`·`docs/`·`README.md` 전체에 건다. diff
   안만 보면 겹침을 못 찾는다.
4. 고칠 것은 고치고, 보고는 **고친 것 / 문제없던 것 / 그대로 둔 판단(이유)** 세 묶음으로 한다.

### 영역 정리

"전체 정리", "runner 정리"처럼 diff가 아니라 영역을 받았을 때.

1. 시작 전에 `git status`를 본다. 커밋 안 된 변경이 있는 파일은 작업 중인 것이다. 건드리지 않고 보고에만 적는다.
2. 영역의 파일을 전부 읽는다. 항목 하나의 흐름은 `App.AddPaths`(`internal/app`) → `runner.Add` → `worker` →
   `runItem`·`process` → 무료(`runFreeGemini`·`freeAttempt`) 또는 Batch(`submitBatch`, 확인, `retryBatch`) →
   `finish` → `output.WriteNew` 순서로, `development.md`의 관련 절과 같이 읽는다. 화면은 `app.js`의 `init` →
   `bindEvents` → `loadItems`·`renderItems` → `renderPane` 순서로 읽는다.
3. 기계적으로 찾을 수 있는 것부터 찾는다 (아래 G).
4. A–D와 H를 영역 전체에 건다. 정리 커밋은 동작을 바꾸지 않는다. 정리 중에 본 버그는 따로 `fix:` 커밋으로 고친다.
5. E의 검사가 정리 전후 모두 통과해야 한다.

## A. 이름

- **같은 단어가 이미 다른 뜻인가.** 새 이름마다 저장소 전체를 grep 한다. 예: `Sorted`는 "강의별 정리를 했는가"
  (`matched`/`none`/빈 값)이고 목록 정렬과 상관없다. 목록을 묶는 방식은 `group`(`config.ListGroup`)이다. `mode`는
  무료·유료 선택(`config.Mode`)이고 화면 상태가 아니다. `Output`은 사용자 폴더의 `.md`, 보관본은 `ArchivePath`다.
- **같은 일을 하는 것이 이미 있는가.** 있으면 그것을 쓴다.

  | 하려는 일 | 이미 있는 것 |
  |---|---|
  | 사용자 문구, 문구를 메시지로 한 오류, 오류 감싸기 | `i18n.T`, `i18n.Error`, `i18n.Wrap`. 카탈로그 문구 오류인지 보기는 `i18n.IsText` |
  | 정리 실패를 짧은 문장으로 | `internal/app` `errorMessage` |
  | Gemini 오류 가르기 | `gemini.Is429`, `IsAuth`, `IsTransient`, `IsUnavailable`, `IsNotFound`, `IsEmptyResponse`, `StatusCode` |
  | 전사본 위치, 보관본 위치 | `output.TargetPath(source, kind)`, `output.ArchivePath(dir, id)` |
  | 겹치지 않는 이름 (`name (2).md`) | 새로 쓸 때 `output.WriteNew`, 다른 프로세스와 겹칠 수 있는 옮기기는 `output.Reserve`(배타적 생성으로 이름을 잡음) 뒤 rename, 앱 안에서만 옮길 때 `output.UniquePath` |
  | 파일이 있는가 | `internal/app` `isFile` |
  | 끝난 항목 하나 꺼내기, 그 전사본 읽기 | `App.finishedItem`, `App.outputText` |
  | 항목 바꾸기 | `store.Update(id, fn)`. 같은 `.md`를 쓰는 항목 전부면 `App.updateTranscript` |
  | config 한 값 바꾸기 | 키는 `Config.editKey`, `gui` 아래 값은 `Config.setGUI`. 바꾸기 직전에 `reload`를 부른다(직접 고칠 때도) |
  | 녹음 시각, 학기 주차 | `media.RecordedAt`, `course.SemesterStart`, `course.Week`, 이름의 주차 `course.WeekFromName`. 항목의 주차는 `App.weeks.of` |
  | 녹음 이름 (확장자 뺀 파일 이름, 폴더 이름 그대로) | Go `recordingName`, 화면 `displayName` |
  | 자연 순서 정렬 (`2`가 `10` 앞) | `media.SortNatural`, `media.NaturalLess` |
  | 화면에서 Go 부르기 | `app.js` `call("메서드", ...)`. 오류 문장은 `errorText` |
  | HTML에 값 넣기 | `esc` |
  | 시트·메뉴 열고 닫기, 포커스 가두기 | `openSurface`, `closeSurface`, `trapSurface`, `restoreFocus` |
  | 알림 줄, 화면 낭독 | `showToast`, `announce` |
  | 지우고 되돌리기 | `removeWithUndo`(항목), `deleteKeyWithUndo`(키) |
  | 날짜 묶음·완료 시각 | `dayKey`, `dayLabel`, `finishStamp`, `finishedAt` |
- **이웃 이름의 꼴을 따르는가.** 문구 키는 `영역.이름`(`error.noCourses`, `group.week`), config의 화면 값은
  `gui.<이름>`, `App`의 읽기/쓰기 짝은 `ListGroup`/`SetListGroup`처럼 `X`/`SetX`, 플랫폼 파일은
  `_darwin`·`_windows`·`_other`.
- **이름이 뜻을 말하는가.** 값을 박은 이름은 피한다. 시간 상수에는 단위를 붙인다.
- **주석·문서의 용어가 코드 이름과 같은가.** 화면 문구와 문서는 사용자 말("강의별 정리", "목록 묶기")을, 개발 문서는
  코드 이름을 쓴다.

## B. 중복과 땜질

- **새로 쓴 조건식이 다른 곳에도 있는가.** 같은 판정이 Go와 JS에 둘 다 있을 수 있다(정리할 수 있는 항목, 끝난 상태,
  확장자). 한쪽을 바꾸면 다른 쪽을 grep 한다.
- **고친 함수와 같은 일을 손으로 하는 곳.** 원래 있던 중복이어도 이번에 그 함수를 건드렸으면 같이 정리한다.
- **증상을 덮는 땜질을 지운다.** 읽는 쪽이 만드는 쪽의 누락을 덮고 있으면(빈 값이면 여기서 채운다, 실패하면 한 번
  더 부른다) 만드는 쪽을 고친다. 파일을 쪼개는 것은 정리가 아니다. 지금 구조 안에서 원인을 고친다.
- **모양을 바꿨으면 만드는 곳과 읽는 곳을 전부 찾는다.** `store.Item` 필드는 `items.json`에 남으므로 옛 파일도
  읽혀야 한다. `app.Item`의 `json` 태그는 `app.js`가 읽는다.
- 안 쓰게 된 함수·export·문구 키·CSS 클래스·config 값은 지운다. 문구 키는 `go test ./internal/i18n`이 남은 것을
  잡는다.

## C. 경계

- **화면과 Go.** `App`의 공개 메서드는 Wails가 `window.go.app.App`으로 내보낸다. 이름을 바꾸면 `app.js`의
  `call("옛이름"`이 조용히 `error.notReady`가 된다. 검사가 없으니 grep 한다.
- **저장 파일.** `config.json`과 `items.json`은 다른 프로세스(CLI, 다른 버전 앱)도 쓴다. 모르는 값을 지우지 않고,
  형식이 조금 달라도 키를 버리지 않는다(`config/tolerance_test.go`).
- **사용자 폴더.** 사용자 파일을 옮기거나 지우는 코드는 같은 이름이 이미 있을 때(`UniquePath`), 같은 파일을 가리키는
  항목이 둘일 때(`updateTranscript`), 옮기는 중에 실패했을 때 무엇이 남는지 본다.
- **보내는 정보.** 상태 신호에 키·경로·전사 내용·오류 메시지가 들어가지 않는가 (`development.md` "상태 신호").
  로그에 API 키가 찍히지 않는가.
- **Store판.** 권한, 파일 위치, 보이는 기능 설명이 바뀌면 `notes/store-next.md`(저장소 밖)에 한 줄을 더한다.

## D. 주석·문서

- **바뀐 동작을 말하던 기존 주석·문서를 찾는다.** 고친 함수 위만이 아니라 그 값·이름을 말하는 `docs/`, `README.md`,
  도움말 문구, 다른 함수의 주석도 본다.
- **문서 지도.** 새 문서는 `development.md` "문서 지도"에 한 줄. 사용자 문서(`README.md`, `usage.md`)에는 사용자에게
  필요한 것만 둔다.
- 개발 문서에는 코드로 알 수 없는 것만 적는다: 규칙과 이유, 데이터 모양, 하면 안 되는 것과 이유. 언제 어떻게 쟀는지,
  예전에는 어땠는지는 적지 않는다. 사라진 것(옛 Python 앱, Codex 백엔드)을 가리키는 주석은 지운다.
- 문서와 코드가 다르면 코드가 맞다. 문서를 고친다.

## E. 검사

- 커밋 전에 모두 통과해야 한다. 실패하면 기대값이 아니라 코드를 고친다.
  - `test -z "$(gofmt -l $(git ls-files '*.go'))"` (Windows CI에는 없으니 여기서 잡는다)
  - `go vet ./...`, `GOOS=windows go vet ./...` (플랫폼 파일을 건드렸으면 꼭)
  - `go test ./...`
  - `node --check frontend/dist/app.js`
- 화면을 바꿨으면 미리보기(`development.md` "화면 미리보기")로 라이트·다크를 한 번씩 찍어 본다. 사용자 설정을 쓰지
  않고 임시 설정 폴더를 쓴다. 키가 든 설정을 복사하면 권한을 0600으로 둔다.
- CI(`gh pr checks`)는 머지 때 기다리지 않는다. 릴리스 전에 master의 최근 실행이 통과했는지 본다.

## F. 마무리

- 같은 작업의 후속 수정은 amend, 따로 떼는 정리 작업은 새 커밋. push된 커밋은 amend 하지 않는다.
- 브랜치 → PR → 로컬 검사 통과 → 머지. 커밋에 Co-Authored-By를 넣지 않는다.

## G. 기계적으로 찾는 것 (영역 정리)

- **안 쓰는 함수**: 패키지 밖에서 안 부르는 대문자 함수, 테스트에서만 부르는 함수. `go vet`은 이것을 못 잡는다.
  함수 이름마다 grep 한다.
- **화면이 안 부르는 `App` 메서드, 없는 메서드를 부르는 화면**: `grep -o 'call("[A-Za-z]*"' frontend/dist/app.js`와
  `internal/app`의 `func (a \*App) [A-Z]`를 맞춰 본다. CLI(`cmd/nalmeok`)나 메뉴가 부르는 것도 있으니 지우기 전에
  그쪽도 grep.
- **같은 헬퍼의 사본**: `strings.TrimSuffix(filepath.Base(`, `os.Stat(` 뒤 `IsDir`, `fmt.Sprintf("%s (%d)`,
  `innerHTML =` 에 `esc` 없이 들어간 값을 grep 해서 A의 표에 있는 것을 다시 만든 곳을 찾는다.
- **문서의 죽은 이름**: 지우거나 바꾼 이름을 `docs/`·`README.md`에서 grep.

## 화면

- 행·메뉴·시트는 이미 있는 것과 같은 방식으로 만든다(메뉴는 키 선택 메뉴, 시트는 `openSurface`). 새 방식을 만들면
  키보드·낭독·포커스 복귀를 다시 맞춰야 한다.
- 문구는 짧은 명사형, 보조 정보는 **?** 뒤로. 화면에 설명을 늘어놓지 않는다.
- 상태에서 나오는 화면(빈 화면 안내, 버튼 보이기)은 그 상태를 바꾸는 모든 경로에서 다시 그린다 (H).

## 빌드·CI

- `scripts/build_mac_app.sh`는 서명·공증 환경변수가 없으면 ad-hoc 앱만 만든다. 로컬 시험 빌드는
  `env -u NALMEOK_SIGN_IDENTITY -u NALMEOK_NOTARY_PROFILE`로 돌린다.
- 자격(키체인 프로필 이름 밖의 Apple ID, 비밀번호, API 키)을 스크립트·워크플로·문서에 적지 않는다.
- 릴리스 자산 이름을 바꾸면 사이트의 `assetNames()`도 바꿔야 한다 (로컬 `CLAUDE.md`).

## H. 되풀이된 결함

이 저장소에서 두 번 이상 고친 종류다. diff 점검은 diff가 건드린 것만, 영역 정리는 전부 본다.

- **겹치는 두 동작.** 취소와 실행 끝남, 유료로 이어서와 무료 완료, 작업이 시작되는 틈의 취소, Batch 제출이 두 번.
  `runner`의 상태를 바꾸는 새 경로는 다른 경로가 같은 항목을 이미 바꿨을 때 무엇을 하는가. 바꾸기 직전에
  `store.Get`으로 다시 읽고, 끝난 결과를 덮지 않는다. 강의별 정리도 같다: 같은 실행에서 중복 항목이 이미 옮긴
  전사본은 건너뛴다(`sortItems`).
- **같은 이름, 같은 파일.** 같은 녹음을 두 번 넣은 항목, 같은 이름의 결과 파일, 음성 메모 이름 겹침. 파일을 만들거나
  옮기는 새 코드는 `WriteNew`/`Reserve`/`UniquePath`를 쓰고, 같은 `Output`을 가진 다른 항목도 같이 바꾸는가.
- **config를 덮어써 다른 값이 사라짐.** CLI나 다른 프로세스가 쓴 값, 형식이 조금 다른 값. 저장하는 새 코드는
  `reload` 뒤에 바꾸고, 모르는 값을 지우지 않는가.
- **Windows에서만 깨짐.** 콘솔 출력(한글), `explorer` 명령줄, 닫지 않은 파일 때문에 지워지지 않는 임시 폴더, 리소스
  파일 줄바꿈, 한글 경로. 파일을 열면 닫는가, 셸 명령을 직접 만들지 않는가. `GOOS=windows go vet ./...`를 돌리고,
  실기 확인을 못 했으면 보고에 적는다.
- **언어를 따라가지 않는 문구.** 메뉴바·알림처럼 시작할 때 한 번 만든 것, 카탈로그를 읽기 전에 쓰인 문구, 언어를
  박아 넣은 이름(전사본 파일 이름의 주차). 언어가 바뀌면 다시 만드는가, 앱 언어를 쓰는가.
- **상태가 바뀌어도 화면이 그대로.** 키를 넣거나 지웠는데 빈 화면 안내·버튼이 늦게 바뀜, 무료 사용량이 끝난 항목의
  버튼. 새 화면 상태는 그 상태를 바꾸는 모든 경로(`reloadKeys`, `refreshItems`, 모드 변경)에서 다시 그리는가.
- **끌어서 순서 바꾸기의 끝.** 키를 맨 위·맨 아래로 못 옮기던 회귀가 두 번 있었다. 끌기 코드를 건드리면 첫 자리와
  마지막 자리로 옮겨 본다.

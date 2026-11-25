---
name: code-cleanup
description: 이 저장소의 코드 정리·리뷰 점검. "정리해", "점검해", "스파게티 없나 봐", "땜빵 없나", "기존 이름 안 쓰고 새로 만든 거 있나", "자주 나던 버그 다시 났나 봐", 작업 후 커밋·머지 전 점검, 또는 "전체 정리", "runner 정리", "화면 정리"처럼 영역을 통째로 정리해 달라고 받았을 때. 인자로 커밋 범위나 영역(경로, "전체")을 받을 수 있다.
---

점검 항목은 `docs/code-review.md`에만 있다. 여기에 옮겨 적지 않는다. 여기에는 범위를 정하는 법과 영역을 넘는 것만 둔다.

| 바뀐 파일 | 그 문서에서 볼 곳 | 통과시킬 검사 |
|---|---|---|
| `internal/`, `cmd/` | A–D, H | gofmt 검사, `go vet ./...`, `GOOS=windows go vet ./...`, `go test ./...` |
| `frontend/dist/` | A–D, H, "화면" | `go test ./internal/i18n`, `node --check frontend/dist/app.js`, 미리보기로 화면 확인 |
| `scripts/`, `.github/` | "빌드·CI" | `bash -n` (셸), 바꾼 스크립트를 직접 한 번 |
| 문서 (`*.md`) | D | 문서가 가리키는 이름을 grep |

gofmt 검사는 CI와 같은 `test -z "$(gofmt -l $(git ls-files '*.go'))"`.

## 따로 있는 같은 규칙

한쪽만 고쳐서 다시 고치기 쉬운 자리다. 범위가 한쪽을 건드렸으면 다른 쪽도 같은 작업에서 본다.

| 규칙 | 한쪽 | 다른 쪽 | 맞춰 보는 것 |
|---|---|---|---|
| 사용자 문구 | Go `i18n.T/Error/Wrap("키"` | `app.js` `t("키"`, `index.html` `data-i18n*` | `frontend/dist/i18n/en.json`·`ko.json`. `go test ./internal/i18n`이 키·자리·한글을 맞춰 본다 |
| 화면이 부르는 Go 메서드 | `internal/app` `App`의 공개 메서드 | `app.js` `call("메서드"` | 이름을 바꾸거나 지웠으면 `call("옛이름"`을 grep. 검사가 없다 |
| 화면이 읽는 항목·설정 모양 | `app.Item`, `CourseSettings`, `SortSummary`의 `json` 태그 | `app.js`의 필드 이름(`item.Week`, `item.Sorted`…) | 검사가 없다. 필드 이름을 grep |
| 오디오 확장자 | `media.Extensions()` | `app.js` `AUDIO_EXT` | 한쪽을 바꾸면 다른 쪽도 |
| macOS·Windows 동작 | `*_darwin.go`, `*_unix.go`, `scripts/build_mac_app.sh` | `*_windows.go`, `*_other.go`, `scripts/build_windows_app.sh`, `package_windows_msix.ps1` | `docs/development.md` "플랫폼별 동작". `GOOS=windows go vet ./...` |
| 무료·유료(Batch) 경로 | `internal/runner` 무료 실행(`freeAttempt`) | Batch 제출·확인(`submitBatch`, `retryBatch`) | 취소·재시도·신호 기록이 두 경로에서 같은가 (`docs/development.md` "상태 신호") |
| 사용자에게 보이는 동작의 설명 | 코드 | `README.md`, `docs/usage.md`, 도움말 문구 | 옛 문구를 grep. Store 설명에 걸리는 변경은 `notes/store-next.md`에 한 줄 |

## 돌리는 법

1. 범위를 인자에서 정한다.
   - 커밋 범위 → 그 diff. 문서의 "diff 점검".
   - 영역(경로, "전체", "runner", "화면") → 그 영역의 파일 전부. 문서의 "영역 정리".
   - 인자 없음 → `git diff master...HEAD`(작업 브랜치). 그것도 비면 직전 머지 커밋의 변경.
2. `docs/code-review.md`의 "돌리는 법"대로 A–D를 적용한다.
3. 문서 H(되풀이된 결함)를 범위에 건다. diff 점검은 diff가 건드린 항목만, 영역 정리와 "전체"는 전부. 범위가 위
   "같은 규칙" 표의 한쪽을 건드렸으면 다른 쪽을 연다. 지우거나 바꾼 이름, 옮긴 화면 요소는 저장소 전체(`docs/`,
   `README.md`, 문구 카탈로그 포함)에서 옛 이름·옛 문구를 grep 한다.
4. 메인 체크아웃에 커밋 안 된 변경이 있으면 그 파일은 건드리지 않고 보고에 적는다.
5. 고칠 것은 작업 브랜치에서 고치고, 위 표의 검사를 통과시킨 뒤 커밋한다. 정리 커밋(`refactor:`, `chore:`)은 동작을
   바꾸지 않는다. 보다가 찾은 버그는 정리 커밋에 섞지 않고 따로 `fix:` 커밋으로 고친다. 제품 판단이 필요한 것(문구,
   보이는 동작, 규칙의 우선순위)은 고치지 않고 보고에서 묻는다. 묻지 않고 규칙을 더하지 않는다.
6. 범위 안에서 구현이 끝난 계획·리뷰 문서를 찾는다. 지금의 규칙·이유·데이터 모양만 그 동작을 다루는 문서
   (`docs/development.md`의 해당 절, `docs/usage.md`)로 옮기고, 실측 기록과 진행 과정은 버린다. 계획 문서를 지우고
   가리키던 링크·문서 지도를 같은 커밋에서 고친다. 손댄 문서의 이력 줄("~까지는", "예전에는")도 걷어낸다.
7. 정리하면서 생긴 공용 헬퍼·이름 규칙은 같은 커밋에서 문서 A의 표에 넣는다. 다음 정리가 그것을 다시 만들지 않게
   하는 것이 그 표의 일이다.
8. 고친 버그가 이미 H에 있는 종류면 그 항목이 왜 못 잡았는지 보고 항목을 고친다. 없는 종류인데 두 번째로 나왔으면
   H에 새 항목을 넣는다. 처음 쓸 때 지킬 규칙이면 `docs/development.md`의 그 동작 절에 규칙과 이유로 적고, H에는 그
   절을 가리키는 한 줄만 둔다.
9. 머지는 저장소 방식대로: 브랜치 → PR → 위 표의 검사를 로컬에서 통과시키고 바로 머지. CI 결과는 릴리스 전에만 기다린다. 커밋에 Co-Authored-By를 넣지 않는다.
10. 보고는 고친 것 / 문제없던 것 / 그대로 둔 판단(이유) 세 묶음과 커밋 해시. 확인하지 못한 것(Windows 실기, 창 폭,
    메뉴가 열린 모습)은 그대로 적는다.

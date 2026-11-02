![Nalmeok 소개 그림](assets/readme-banner.png)

# Nalmeok (nalmeok)

음성 파일이나 폴더를 창에 놓으면 전사한 `.md`가 원본 옆에 생겨요. 강의 전사본을 ChatGPT 프로젝트에 올려 쓰려고 만든 앱이에요.

- `.m4a`, `.mp4`(AAC), `.mp3`, `.wav`를 받아요.
- 폴더를 놓으면 안의 오디오를 이름 순서로 이어 `.md` 하나로 만들어요.
- 무료 키로 바로 전사해요. 유료로 처리하면 1시간에 약 74원이에요.
- 지난 작업은 목록에 남고, 검색할 수 있어요.

## 설치

[Releases](https://github.com/jseook11/nalmeok/releases/latest)에서 내려받아요.

**macOS** — `Nalmeok.app`을 Applications 폴더로 옮기고 열어요. 서명·공증된 앱이라 경고 없이 열려요.

**Windows** — Windows용 실행 파일을 내려받아 더블클릭해요. SmartScreen 경고가 뜨면 **추가 정보 → 실행**을 눌러요.

## 처음 사용

1. 앱을 열어요. 키가 없으면 **API 키** 시트가 먼저 열려요.
2. **무료** 또는 **유료**를 고르고 **API 키 붙여넣기** 칸에 Gemini API 키를 넣어요. **저장**을 누르면 키를 확인하고 저장해요.
3. 음성 파일이나 폴더를 창에 놓아요. 확인 없이 바로 시작해요.
4. 끝나면 원본 옆에 `.md`가 생겨요. 끝난 행에서 **Finder에서 보기**(Windows는 **탐색기에서 보기**)를 누르면 파일 위치가 열려요.

키는 [Google AI Studio](https://aistudio.google.com/apikey)에서 받아요. 무료 키는 바로 전사에, 유료 키는 Batch(대기 후 처리)에 써요. 두 종류를 모두 넣으면 위쪽 **무료** / **유료** 토글로 골라요.

파일은 `강의09.m4a`를 놓으면 `강의09.md`, 폴더는 `ch07`을 놓으면 `ch07/ch07.md`로 원본 옆에 저장돼요.

## 더 알아보기

[사용 설명서](docs/usage.md)에 목록, 검색, 이름 변경, 비용, 키 발급, FAQ가 있어요.

## 라이선스

MIT License

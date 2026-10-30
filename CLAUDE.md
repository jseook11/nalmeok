# nalmeok · 프로젝트 작업 지침

Windows 지원과 배포 작업은 **`docs/windows-support.md`를 단일 기준**으로 삼는다.

- TUI 입력, 터미널 출력, 패키징, Windows 관련 코드를 건드리기 전에 이 문서를 먼저 읽는다.
- 작업을 시작하거나 마치면 문서의 **상태 표(0.1)** 와 **변경 이력(10장)** 을 갱신한다.
- 문서의 **불변 조건(0.3)** 과 **하지 말 것(0.4)** 을 어기지 않는다.
- 문서와 코드가 어긋나면 코드를 고치기 전에 문서를 먼저 갱신한다.

드래그앤드롭 전사(`nalmeok drop`, `nalmeok-drop.cmd`)는 **`docs/drop-target.md`를 단일 기준**으로 삼는다.

- 입력 정규화, 파일 작업 폴더, 문서 폴더 복사, 바로가기 동작을 바꾸기 전에 이 문서를 먼저 읽는다.
- 결정(DD1–DD8)과 출력 규칙을 바꾸려면 문서를 먼저 고친다.

macOS 앱 패키징(DMG·서명·공증·업데이트)은 **`docs/macos-app.md`를 단일 기준**으로 삼는다.

- 런처, 부트스트랩, 다운로드, 서명·공증, 업데이트를 바꾸기 전에 이 문서를 먼저 읽는다.
- Homebrew 자동 설치와 시스템 Python 사용은 금지다(불변 조건 0.3).
- 마일스톤을 마치면 상태 표(0.1)와 변경 이력(10장)을 갱신한다.

### macOS 공증 자격 (로컬 키체인)

공증 자격은 저장소가 아니라 **이 Mac의 로그인 키체인**에 있다.

- notarytool 프로필 이름: **`nalmeok-notary`**
- Apple ID와 app-specific password는 저장소에 적지 않는다.
- Team ID는 Developer ID 인증서에서 확인한다.

```bash
security find-identity -v -p codesigning
# "Developer ID Application: 이름 (TEAMID)" 의 괄호 값이 Team ID
```

최초 1회 저장. 비밀번호는 프롬프트에서 직접 입력하고 셸 히스토리에 남기지 않는다.

```bash
xcrun notarytool store-credentials "nalmeok-notary" \
  --apple-id "<Apple ID>" --team-id "<Team ID>"
```

저장 확인:

```bash
xcrun notarytool history --keychain-profile "nalmeok-notary"
```

빌드에 연결:

```bash
export NALMEOK_SIGN_IDENTITY="Developer ID Application: 이름 (TEAMID)"
export NALMEOK_NOTARY_PROFILE="nalmeok-notary"
python3 scripts/build_macos.py
```

주의:

- app-specific password를 `CLAUDE.md`·README·커밋·셸 히스토리에 남기지 않는다.
- 키체인 프로필은 이 Mac에만 있으므로 CI/GitHub Actions에서는 쓸 수 없다. CI에서는 App Store Connect API 키를 Secrets로 넘긴다.
- 프로필을 잃어버리면 Apple ID에서 app-specific password를 revoke하고 다시 만든다.



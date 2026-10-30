# nalmeok · 프로젝트 작업 지침

**동작·배포·구조를 바꾸면 문서를 먼저 고친다.** 단, 문서의 서술이 코드와 어긋나 있으면 문서를 먼저 바로잡는 것이 우선이다. 상태 표(0.1)와 변경 이력(10장)이 있는 문서는 작업을 마칠 때 그 두 곳을 갱신한다. `docs/drop-target.md`처럼 상태 표가 없는 문서는 상단 상태 줄만 갱신한다.

사용자용 문서는 `README.md`(설치·처음 사용)와 `docs/usage.md`(대시보드·CLI·비용·FAQ)가, 개발자용은 `docs/development.md`(구조·테스트·빌드)가 소유한다. 소비자에게 필요 없는 세부는 README에 두지 않는다.

Windows 지원과 배포 작업은 **`docs/windows-support.md`를 단일 기준**으로 삼는다.

- TUI 입력, 터미널 출력, 패키징, Windows 관련 코드를 건드리기 전에 이 문서를 먼저 읽는다.
- **`.cmd`는 순수 ASCII, 한국어 문구는 UTF-8 BOM이 있는 `.ps1`** 이 소유한다. 어기면 설치 중 `'더의' is not recognized` 같은 오류가 난다(`.cmd` ASCII / D19). 빌드와 테스트가 이를 검사한다.

드래그앤드롭 전사(`nalmeok drop`, `nalmeok-drop.cmd`)는 **`docs/drop-target.md`를 단일 기준**으로 삼는다.

- 입력 정규화, 파일 작업 폴더, 문서 폴더 복사, 바로가기 동작을 바꾸기 전에 이 문서를 먼저 읽는다.
- 결정(DD1–DD8)과 출력 규칙을 바꾸려면 문서를 먼저 고친다.

macOS 앱 패키징(DMG·서명·공증·업데이트)은 **`docs/macos-app.md`를 단일 기준**으로 삼는다.

- 런처, 부트스트랩, 다운로드, 서명·공증, 업데이트를 바꾸기 전에 이 문서를 먼저 읽는다.
- Homebrew 자동 설치와 시스템 Python 사용은 금지다(불변 조건 0.3).

### 제약 조정 (2026-10-30)

아래 제약은 **폐기**했다. 문서에 남아 있으면 근거 없이 따라 하지 말고 함께 지운다.

- **Windows에서 코드서명은 필수 아님.** SmartScreen 확인창 한 번은 감수한다(D4). macOS 배포물은 서명·공증이 필요하다(`docs/macos-app.md` 0.3).
- **"새 런타임 의존성 금지" 폐기.** 직접 구현이 더 비싸면 검증된 라이브러리를 쓴다(예: `platformdirs`, `packaging`, `httpx`).
- **"앱 코드는 단일 `nalmeok.py`" 폐기.** 6,864줄 단일 파일은 유지보수 한계를 넘었다. 다음 작업으로 패키지 분리를 진행한다(현재 코드는 아직 단일 파일).

### macOS 서명·공증 (선택)

**릴리스 필수 요건이 아니다.** 자격이 있을 때만 서명·공증한다(2026-10-30 사용자 결정). 자격이 없으면 unsigned DMG를 배포하고 README에 Gatekeeper 우회 방법을 적는다.

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

자격 환경변수가 없으면 빌드는 실패하지 않고 **unsigned DMG**를 만든다. 배포 전에는 `spctl`·`stapler validate`로 서명·공증 여부를 확인한다.

주의:

- app-specific password를 `CLAUDE.md`·README·커밋·셸 히스토리에 남기지 않는다.
- 키체인 프로필은 이 Mac에만 있으므로 CI/GitHub Actions에서는 쓸 수 없다. **macOS CI는 서명·공증을 하지 않고 unsigned 아티팩트만 올린다.** 서명 배포는 이 Mac에서 로컬로 빌드한다.
- 프로필을 잃어버리면 Apple ID에서 app-specific password를 revoke하고 다시 만든다.



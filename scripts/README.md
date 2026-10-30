# Windows 배포 빌드

`build_windows.py`는 Python·uv·Git이 없는 Windows PC에서 실행할 수 있는 ZIP을 만듭니다. 설계 배경과 수용 기준은 [`docs/windows-support.md`](../docs/windows-support.md)를 따릅니다.

## 요구 사항

- **thin ZIP**은 어디서나(빌드 PC의 OS 무관) 빌드할 수 있고, 내려받는 것이 없습니다.
- **offline ZIP**은 Windows용 실행 파일(`python.exe`, `ffmpeg.exe`)을 넣으므로 **Windows에서만** 빌드하고, 첫 빌드에 인터넷이 필요합니다.
- Python 3.12 (빌드 스크립트 자체를 실행하는 용도)
- `uv` 불필요. offline 빌드는 추출한 런타임의 `runtime\python.exe -m pip`로 의존성을 설치합니다.

## 빌드

```bat
python scripts\build_windows.py
```

결과:

```text
dist\nalmeok-<version>-win64.zip   # installer.cmd + app\ (thin, ~200KB)
dist\nalmeok-<version>-win64-offline.zip   # app\ 안에 runtime\, ffmpeg.exe (--offline)
dist\SHA256SUMS.txt                # ZIP의 SHA-256
build\nalmeok-thin\                # thin ZIP 중간 파일
build\nalmeok-offline\             # offline ZIP 중간 파일
build\cache\                       # 내려받은 런타임/ffmpeg 캐시 (SHA-256 검증됨)
```

`<version>`은 `nalmeok/constants.py`의 `__version__`에서 읽습니다.

## 고정 입력

| 구성 | 버전/태그 | SHA-256 |
| --- | --- | --- |
| python-build-standalone (CPython 3.12.16, x86_64-pc-windows-msvc, install_only) | `20261026` | `590dfcbe3a34c07301e6332e13e2188b98655cd072b08afa45a7998dc4c22bc5` |
| BtbN FFmpeg-Builds (win64-lgpl, FFmpeg n8.1.3-25-g611cec792a) | `autobuild-2026-10-29-19-17` | `74e7863feef0d40b72236caf53823b132bcd429657be742d0a9213c0581cb573` |
| python-build-standalone LICENSE (MPL-2.0) | `20261026` | `1f256ecad192880510e84ad60474eab7589218784b9a50bc7ceee34c2b91f1d5` |
| google-genai | `2.31.0` (uv.lock) | PyPI에서 빌드 시 설치, 버전 고정 |

정확한 URL은 `scripts/build_windows.py` 상단의 상수를 참고하세요. `latest` 태그나 이동하는 URL은 쓰지 않습니다.

> `google-genai`는 `uv.lock`과 같은 `2.31.0`으로 고정합니다. 전이 의존성은 빌드 시점의 PyPI 해석을 따르므로, 완전 재현이 필요하면 requirements lock을 함께 배포하도록 확장해야 합니다.

## ZIP 내용

```text
installer.cmd                      # 루트에 이것만 보인다 (순수 ASCII 런처)
app/
  nalmeok.cmd        nalmeok-drop.cmd
  install.ps1        (설치 로직·한국어 안내, UTF-8 BOM)
  messages.ps1       (런처 안내 문구, UTF-8 BOM)
  shortcuts.ps1      (바로가기 2개 생성: nalmeok, nalmeok에 끌어놓기)
  nalmeok/           (앱 패키지: __init__.py, __main__.py, cli.py, ...)
  app.py             pyproject.toml
  README.md          LICENSE
  nalmeok.ico        (있을 때만. 없으면 install.ps1이 기본 아이콘 사용)
  licenses/          THIRD-PARTY.md (+ offline: Python/ffmpeg 고지)
  ffmpeg.exe ffprobe.exe runtime/        (offline 전용)
```

`installer.cmd`는 `app\*`를 `%LOCALAPPDATA%\Programs\nalmeok`로 복사하므로 설치 폴더는 평평합니다.

ZIP 안의 `.cmd`는 **순수 ASCII** + CRLF로 다시 씁니다. cmd.exe는 `chcp 65001` 뒤 UTF-8 배치 줄을 중간부터 실행해 한글 조각을 명령으로 처리하므로, 한국어 문구는 UTF-8 BOM이 있는 `.ps1`에만 둡니다. 빌드는 비ASCII `.cmd`를 거부합니다.
ZIP 항목의 타임스탬프는 고정값을 사용합니다.

## 검증

thin ZIP에는 런타임이 없으므로 빌드 후에는 **압축을 풀고 실제 실행 경로를 확인**합니다. Windows에서:

```bat
python scripts\build_windows.py
mkdir smoke
tar -xf dist\nalmeok-<version>-win64.zip -C smoke
cd smoke

set PYTHONPATH=app
python -m nalmeok --version
app\nalmeok.cmd --version < NUL
app\nalmeok-drop.cmd --help < NUL
installer.cmd < NUL
```

기대 결과:

- ZIP 루트에는 `installer.cmd`와 `app\`만 있다.
- `app\nalmeok.cmd --version`의 **첫 실행이 PBS를 내려받고** `%LOCALAPPDATA%\nalmeok\runtime\current.txt`를 만든다.
- `installer.cmd` 출력에 `is not recognized`가 없고, `%LOCALAPPDATA%\Programs\nalmeok\nalmeok.cmd`가 생긴다.

ZIP 해시 확인:

```powershell
Get-FileHash .\dist\nalmeok-<version>-win64.zip -Algorithm SHA256
Get-Content .\dist\SHA256SUMS.txt
```

offline 확장판은 런타임이 들어 있으므로 `app\runtime\python.exe -c "import google.genai; print('ok')"`로 SDK를 확인합니다.

## CI

`.github/workflows/windows.yml`이 다음을 수행합니다.

- Windows/Linux/macOS × Python 3.10/3.12 단위 테스트
- windows-latest에서 thin ZIP 빌드
- 압축 해제 후 루트 구성(`installer.cmd` + `app\`) 확인
- `python -m nalmeok --version`(PYTHONPATH=app), `app\nalmeok.cmd --version`(PBS 부트스트랩 포함), `app\nalmeok-drop.cmd --help`
- `installer.cmd` 실행 → `Programs\nalmeok\nalmeok.cmd` 확인, `is not recognized` 회귀 검사
- ZIP·`nalmeok-app.zip`·`windows-manifest.json`·`SHA256SUMS.txt` 아티팩트 업로드

CI는 대화형 콘솔을 검증하지 못하므로, 최종 릴리스 전 Windows 실기 수동 검증이 필요합니다.

## 수동 검증 키트

`verify-windows.cmd`는 **런타임이 옆에 있는 배치**를 전제로 합니다(offline 확장판, 또는 예전 평면 ZIP). thin 설치본에서는 런타임이 `%LOCALAPPDATA%\nalmeok\runtime`에 있으므로, 설치 폴더에서 직접 `doctor`를 실행하세요.

```bat
:: thin 설치본
cd /d "%LOCALAPPDATA%\Programs\nalmeok"
nalmeok.cmd doctor

:: offline 확장판 (runtime\이 같은 폴더에 있음)
verify-windows.cmd conhost
verify-windows.cmd wt
```

offline 배치에서 `verify-report-conhost.txt` / `verify-report-wt.txt`에 `WT_SESSION`, `chcp`, `ver`, `doctor` 출력이 기록됩니다. TUI 항목은 [`docs/windows-support.md`](../docs/windows-support.md) 6.3 매트릭스를 따릅니다.

> thin 레이아웃에 맞춘 `verify-windows.cmd` 갱신은 후속 작업입니다.

# Claude Code 음성 전사(STT) 엔드포인트 기술 검토

> 2026-11-07. **미채택 후보.** Claude Code의 음성 모드가 쓰는 비공개 WebSocket을 Go로 재현해 본 기록이다. 제품에 넣지 않기로 판단한 근거와, 나중에 다시 볼 때 필요한 측정값만 남긴다. 코드는 저장소에 넣지 않았다.

## 결론

- **클라이언트 재현은 쉽다.** 프로토콜은 단순하고, Go로 200줄 안팎이면 동작한다. Codex 백엔드(`internal/codexasr`)보다 오히려 단순하다.
- **Cloudflare TLS 지문은 이 Mac·네트워크에서 막히지 않았다.** Go `crypto/tls`로 핸드셰이크하면 앱 레벨 `401`이 온다. Python OpenSSL 3.0.x가 403을 받는다는 보고는 여기엔 해당하지 않았다.
- **한국어 품질은 좋다.** macOS TTS로 만든 한국어 음성을 숫자 정규화까지 정확히 전사했다.
- **속도 상한이 있다.** 5.6분 오디오는 8배속까지 무손실, 16배속부터 0자. 반면 11.6초 오디오는 무제한(82배속)도 통과했다. 고정 배속 제한이라기보다 **처리 지연 대비 쌓이는 버퍼 한도**로 보인다.
- **전사는 실시간으로 오지 않는다.** 5.6분 세션에서 `TranscriptText` 1개, `TranscriptEndpoint` 1개뿐이었다. `CloseStream` 전까지 UI 진행률을 만들 수 없다.
- **제품화 비용의 본체는 클라이언트가 아니다.** `.m4a`·`.mp4`를 PCM으로 푸는 디코더, 약관·배포 리스크, 비공개 엔드포인트 의존이다.
- **속도 감각:** 80분을 순차 8배속으로 보내면 약 10분. 청크를 병렬로 돌리면 줄지만 동시 세션 한도는 미검증이다.

## 대상 프로토콜

2026-11-07 기준, Claude Code CLI `v2.1.72`를 분석한 공개 PoC([byt3bl33d3r/cc_voice_mode.py](https://gist.github.com/byt3bl33d3r/061dad31daa37b1d81dfa8cc67d45168))의 내용을 실제 엔드포인트로 확인했다.

- 엔드포인트: `wss://claude.ai/api/ws/speech_to_text/voice_stream`
- 헤더 3개: `Authorization: Bearer <OAuth access token>`, `User-Agent: claude-cli/<version> (external, cli)`, `x-app: cli`
  - `x-app: cli`가 없으면 403.
- 쿼리: `encoding=linear16`, `sample_rate=16000`, `channels=1`, `endpointing_ms=300`, `utterance_end_ms=1000`, `language=ko`
  - `language=ko`는 실제로 받아들여졌다.
  - `use_conversation_engine=true`, `stt_provider=deepgram-nova3`, `keyterms=`는 기능 플래그 뒤에 있어 쓰지 않았다.
- 오디오: 16 kHz mono signed 16-bit little-endian PCM. 100 ms = 3,200바이트 청크.

메시지:

| 방향 | 타입 | 의미 |
|---|---|---|
| C→S | `{"type":"KeepAlive"}` | 연결 직후, 이후 8초마다 |
| C→S | binary | PCM 3,200바이트 |
| C→S | `{"type":"CloseStream"}` | 입력 끝 |
| S→C | `{"type":"TranscriptText","data":"..."}` | 현재까지의 중간 전사(교체) |
| S→C | `{"type":"TranscriptEndpoint"}` | 발화 확정 |
| S→C | `{"type":"TranscriptError","description":"...","error_code":"..."}` | 전사 오류 |
| S→C | `{"type":"error","message":"..."}` | 서버 오류 |

토큰은 Claude Code가 쓰는 로컬 자격증명을 **읽기만** 했다. 우선순위는 `CLAUDE_CODE_OAUTH_TOKEN` 환경변수 → `~/.claude/.credentials.json`의 `claudeAiOauth.accessToken` → macOS 키체인의 `Claude Code-credentials` 항목이다. 갱신·쓰기는 하지 않는다.

## 측정

스파이크는 `/tmp`에서만 돌렸고 저장소에 넣지 않았다. 오디오는 macOS `say`(한국어)로 만든 11.6초 음성을 `afconvert -d LEI16@16000 -c 1`로 변환하고, `ffmpeg -stream_loop`로 반복해 길이를 늘렸다.

| 오디오 | 배속 | 전송 시간 | 결과 |
|---|---:|---:|---|
| 11.6초 | 1x | 11.6초 | 85자, 원문과 일치 |
| 11.6초 | 4x | 2.9초 | 85자, 동일 |
| 11.6초 | 무제한 | 0.14초 | 85자, 동일 |
| 335.6초 | 8x | 42.0초 | **2,493자**, 이벤트 1/1, close 1000 |
| 335.6초 | 16x | 21.0초 | 0자, 이벤트 0/0 |
| 335.6초 | 32x | 10.5초 | 0자, 이벤트 0/0 |
| 335.6초 | 무제한 | 0.3~1.0초 | 0자, 20~30초 대기 후에도 close 없음 |

- 5.6분 결과 2,493자는 11.6초 결과 85자 × 29회 + 공백 28자와 정확히 같다. **8배속에서 손실이 없었다.**
- 16배속부터는 연결이 유지된 채 아무 이벤트도 오지 않았다. 클라이언트가 기다리다 닫았다.
- 10x·12x 경계는 테스트를 중단해 확인하지 못했다. **8배속을 보수적 상한으로 본다.**
- 57.9초 오디오의 무제한 전송은 확인하지 못했다.

Cloudflare 확인용 더미 토큰 요청의 실제 응답:

```text
status: 401 Unauthorized
Server: cloudflare
Content-Type: application/json
body: {"type":"error","error":{"type":"authentication_error","message":"Invalid bearer token"},...}
```

`cf-mitigated: challenge`는 없었다. 즉 TLS 지문 단계에서 막히지 않았다.

## 해석

- 11.6초는 무제한도 되고 5.6분은 8배속까지만 되는 것으로 보아, 한도는 배속 자체가 아니라 **서버가 처리하지 못한 채 쌓인 오디오 양**에 걸린다. 청크를 짧게 유지하면 더 빠르게 밀 수 있을 가능성이 있다.
- 세션 하나가 전사 이벤트 하나로 끝난다. 긴 오디오를 청크로 나눠도 청크 단위로만 결과가 오므로, 기존 Gemini/Codex 백엔드 같은 매끄러운 진행률은 포기해야 한다.
- `endpointing_ms`·`utterance_end_ms`를 줬지만 반복 음성에서는 발화가 하나로 합쳐졌다. 실제 강의의 긴 침묵에서 어떻게 끊기는지는 미검증이다.

## 구현한다면

1. `internal/claudeasr`를 `internal/codexasr`와 같은 모양으로 둔다. 자격증명 읽기, `Transcribe(ctx, wavPath)`.
2. `internal/runner/claude.go`에 `gemini.Client` 어댑터를 둔다. `Upload`는 경로만 기록하고, `Generate`는 WAV를 3,200바이트씩 보낸다.
3. `--backend claude` / `NALMEOK_BACKEND=claude`를 추가하고, i18n·`doctor`에 로그인 상태를 한 줄 넣는다.
4. 청크는 5분, 배속은 8x 이하로 보수적으로 잡는다. 동시성 노브는 Codex와 같은 방식으로 두되 기본값은 1로 시작한다.
5. **선행 작업: PCM 디코딩.** 현재 `internal/media`는 컨테이너를 리먹스만 하고 디코딩하지 않는다.
   - WAV·AIFF: 직접 구현 가능
   - MP3·FLAC·Ogg: 순수 Go 라이브러리로 가능
   - **M4A·MP4·MOV·AAC: 순수 Go 디코더가 사실상 없다.** macOS AVFoundation, Windows Media Foundation, 또는 ffmpeg 번들 중 하나가 필요하다. 음성 메모·강의의 주 입력이 `.m4a`·`.mp4`라서 이 비용이 가장 크다.

## 넣지 않는 이유

- **비공개 엔드포인트다.** 공개 API가 아니고, 문서화·지원·버전 보장이 없다.
- **자격증명 재사용이다.** 개인 Claude 구독의 OAuth 토큰을 제3자 앱이 다른 용도로 쓰는 것은 소비자 약관·계정 제한 리스크가 있다.
- **배포물에 넣는 것은 실험과 다르다.** Codex 백엔드는 `NALMEOK_BACKEND=codex`인 숨은 실험이다. Microsoft Store·GitHub 릴리스에 Claude 백엔드를 공식 기능으로 넣으면 안 된다.
- **라이선스.** PoC gist에는 라이선스가 없다. 코드를 복사하지 말고 프로토콜(사실)만 재구현해야 한다.
- **이득이 없다.** 무료 레인은 이미 Gemini 무료 키와 Codex 로그인이 담당한다. Claude를 더해도 기능이 늘지 않고 인증 경로만 하나 늘어난다.
- **Cloudflare는 언제든 바뀔 수 있다.** 지금 Go로 통과해도 보장이 아니다.
- **토큰 만료.** 갱신을 재구현하지 않는 한, Claude Code가 갱신한 토큰이 만료되면 `claude /login` 안내로 끝내야 한다.

## 다시 볼 때 확인할 것

- [ ] 청크 길이별 실제 배속 상한(10x·12x·15x)
- [ ] 5.6분을 넘는 단일 세션의 최대 길이
- [ ] 동시 세션 4·8개의 429 여부와 계정 플래그
- [ ] 실제 한국어 강의(합성 TTS 아님)의 긴 침묵·전문용어 품질
- [ ] 구독에 음성 전사 전용 한도가 있는지
- [ ] Go TLS 지문이 Cloudflare 정책 변경 후에도 통과하는지

## 재현

스파이크 코드는 일부러 저장소에 넣지 않았다. 자격증명을 읽는 코드를 제품 저장소에 두지 않기 위해서다. 재현에는 위 프로토콜 표와 아래 순서면 충분하다.

1. 16 kHz mono s16 WAV 준비
2. WSS 연결, 헤더 3개 + 쿼리
3. 연결 직후 `KeepAlive`, 8초마다 반복
4. `data` 청크를 3,200바이트씩 전송(8배속 이하)
5. `CloseStream` 전송
6. `TranscriptText`는 교체, `TranscriptEndpoint`에서 확정
7. 5~6초 안에 확정 이벤트가 없으면 남은 중간 전사를 채택

"""오디오 길이 측정·추출·자르기 (PyAV).

PyAV wheel 안에 FFmpeg 라이브러리가 들어 있어 별도의 ffmpeg/ffprobe 실행 파일이
필요 없습니다. 이 모듈은 `app` 네임스페이스를 쓰지 않는 일반 모듈입니다
(docs/review-2026-11.md §3-2).

- MP4/M4A 안의 AAC 오디오는 다시 인코딩하지 않고 그대로 복사합니다(빠르고 무손실).
- AAC가 아니면 AAC 128kbps로 인코딩합니다.
"""

from __future__ import annotations

from pathlib import Path

AAC_BITRATE = 128_000


class AudioError(RuntimeError):
    """오디오를 읽거나 쓰지 못했습니다. 사용자에게 그대로 보여줄 수 있는 메시지입니다."""


def _av():
    try:
        import av
    except ImportError as exc:
        raise AudioError(
            "오디오 처리에 필요한 PyAV(av) 패키지가 없습니다. "
            "uv sync 를 실행하거나 nalmeok을 다시 실행해 설치하세요.") from exc
    return av


def available():
    try:
        _av()
    except AudioError:
        return False
    return True


def version():
    """PyAV와 내장 FFmpeg(libavformat) 버전. 진단 화면용입니다."""
    try:
        av = _av()
    except AudioError:
        return None
    libav = av.library_versions.get("libavformat")
    suffix = f" · libavformat {'.'.join(map(str, libav))}" if libav else ""
    return f"PyAV {av.__version__}{suffix}"


def duration(path):
    """오디오 길이(초). 읽을 수 없으면 None."""
    try:
        av = _av()
        with av.open(str(path)) as container:
            if container.duration is not None:
                return container.duration / av.time_base
            stream = container.streams.audio[0]
            if stream.duration is not None and stream.time_base is not None:
                return float(stream.duration * stream.time_base)
    except (AudioError, IndexError, OSError, ValueError):
        return None
    except Exception:  # noqa: BLE001 - av.error.* 는 버전마다 계층이 달라 모두 '읽기 실패'로 봅니다.
        return None
    return None


def extract(source, output, start=None, length=None, progress=None):
    """source의 첫 오디오 스트림을 M4A(output)로 씁니다.

    start/length(초)를 주면 그 구간만 씁니다. progress(seconds)는 쓴 위치(초)를
    받습니다. 실패하면 AudioError를 던지고 output은 남기지 않습니다.
    """
    av = _av()
    source, output = Path(source), Path(output)
    start = float(start or 0.0)
    end = None if length is None else start + float(length)
    try:
        with av.open(str(source)) as reader:
            if not reader.streams.audio:
                raise AudioError(f"오디오 트랙이 없습니다: {source.name}")
            stream = reader.streams.audio[0]
            with av.open(str(output), "w", format="ipod") as writer:
                if stream.codec_context.name == "aac":
                    _copy(reader, stream, writer, start, end, progress)
                else:
                    _encode(reader, stream, writer, start, end, progress)
    except AudioError:
        output.unlink(missing_ok=True)
        raise
    except Exception as exc:  # noqa: BLE001 - av.error.* 를 사용자 메시지로 바꿉니다.
        output.unlink(missing_ok=True)
        raise AudioError(f"오디오를 처리하지 못했습니다: {source.name} ({exc})") from exc
    if not output.exists() or output.stat().st_size == 0:
        output.unlink(missing_ok=True)
        raise AudioError(f"오디오 출력이 비었습니다: {source.name}")


def _seek(reader, stream, start):
    if start > 0 and stream.time_base is not None:
        reader.seek(int(start / stream.time_base), stream=stream, backward=True)


def _copy(reader, stream, writer, start, end, progress):
    """AAC 패킷을 다시 인코딩하지 않고 옮깁니다. 구간 경계는 AAC 프레임(약 23ms) 단위입니다."""
    target = writer.add_stream_from_template(stream)
    _seek(reader, stream, start)
    base = None
    for packet in reader.demux(stream):
        if packet.pts is None or packet.dts is None:
            continue
        seconds = float(packet.pts * stream.time_base)
        if seconds < start - 1e-6:
            continue
        if end is not None and seconds >= end:
            break
        if base is None:
            base = packet.pts
        packet.pts -= base
        packet.dts -= base
        packet.stream = target
        writer.mux(packet)
        if progress:
            progress(seconds - start)


def _encode(reader, stream, writer, start, end, progress):
    av = _av()
    rate = stream.codec_context.sample_rate or 44_100
    # 입력 레이아웃 이름이 '1 channels'처럼 순서 미지정이면 AAC 인코더가 거부합니다.
    # 강의 음성에는 mono/stereo면 충분하므로 채널 수로 고릅니다(3채널 이상은 stereo로 다운믹스).
    channels = 1 if (stream.codec_context.channels or 2) == 1 else 2
    layout = "mono" if channels == 1 else "stereo"
    target = writer.add_stream("aac", rate=rate)
    target.layout = layout
    # FFmpeg 내장 AAC 인코더는 채널당 '샘플레이트×6'을 넘는 비트레이트를 거부합니다.
    target.bit_rate = min(AAC_BITRATE, rate * 6 * channels)
    resampler = av.AudioResampler(format="fltp", layout=layout, rate=rate)
    _seek(reader, stream, start)
    for frame in reader.decode(stream):
        if frame.time is None:
            continue
        if frame.time < start - 1e-6:
            continue
        if end is not None and frame.time >= end:
            break
        frame.pts = None
        for converted in resampler.resample(frame):
            for packet in target.encode(converted):
                writer.mux(packet)
        if progress:
            progress(frame.time - start)
    for converted in resampler.resample(None):
        for packet in target.encode(converted):
            writer.mux(packet)
    for packet in target.encode(None):
        writer.mux(packet)

#!/bin/sh
# Ensures a Python 3.10+ runtime exists, then hands off to run.command.
set -u

HERE=$(cd "$(dirname "$0")" && pwd)
DATA="${NALMEOK_HOME:-$HOME/Library/Application Support/nalmeok}"

fail() {
  printf '\n오류: %s\n\n' "$1" >&2
  printf 'Enter를 누르면 창을 닫습니다...'
  read -r _
  exit 1
}

mkdir -p "$DATA/app" "$DATA/runtime" "$DATA/bin" \
  || fail "앱 데이터 폴더를 만들지 못했습니다."
printf '\033]0;nalmeok\007'

if [ ! -f "$DATA/app/nalmeok/__init__.py" ]; then
  rm -rf "$DATA/app/nalmeok"
  cp -R "$HERE/nalmeok" "$DATA/app/nalmeok" \
    || fail "앱 코드를 복사하지 못했습니다."
fi

usable_python() {
  [ -x "$1" ] || return 1
  "$1" -c 'import sys; raise SystemExit(0 if sys.version_info >= (3, 10) else 1)' 2>/dev/null
}

find_python() {
  for candidate in \
    "$DATA/runtime/current/bin/python3" \
    "/Library/Frameworks/Python.framework/Versions/Current/bin/python3" \
    "$HOME/.pyenv/shims/python3" \
    "/opt/homebrew/bin/python3" \
    "/usr/local/bin/python3"; do
    if usable_python "$candidate"; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  # PATH에 있는 python3는 /usr/bin/python3 스텁만 제외하고 사용합니다.
  extra=$(command -v python3 2>/dev/null || true)
  if [ -n "$extra" ] && [ "$extra" != "/usr/bin/python3" ] \
    && usable_python "$extra"; then
    printf '%s\n' "$extra"
    return 0
  fi
  return 1
}

PYTHON=$(find_python || true)
# 테스트/복구용: 기존 Python을 무시하고 PBS를 다시 받습니다.
if [ "${NALMEOK_FORCE_PBS:-0}" = "1" ]; then
  PYTHON=""
fi

if [ -z "$PYTHON" ]; then
  . "$HERE/bootstrap.env" || fail "bootstrap.env를 읽지 못했습니다."
  echo
  echo "nalmeok을 처음 실행합니다."
  echo "Python ${PBS_VERSION}를 준비합니다 (약 25MB)..."
  echo

  TMP="$DATA/runtime/.pbs.$$"
  curl -fL --retry 3 --progress-bar -o "$TMP" "$PBS_URL" \
    || { rm -f "$TMP"; fail "Python을 내려받지 못했습니다. 네트워크를 확인해 주세요."; }
  echo "$PBS_SHA256  $TMP" | shasum -a 256 -c - >/dev/null 2>&1 \
    || { rm -f "$TMP"; fail "내려받은 Python의 체크섬이 맞지 않습니다."; }

  EXTRACT="$DATA/runtime/.extract.$$"
  rm -rf "$EXTRACT"
  mkdir -p "$EXTRACT"
  tar -xzf "$TMP" -C "$EXTRACT" \
    || { rm -rf "$EXTRACT" "$TMP"; fail "Python 압축을 풀지 못했습니다."; }
  [ -x "$EXTRACT/python/bin/python3" ] \
    || { rm -rf "$EXTRACT" "$TMP"; fail "Python 실행 파일을 찾지 못했습니다."; }

  rm -rf "$DATA/runtime/$PBS_VERSION"
  mv "$EXTRACT/python" "$DATA/runtime/$PBS_VERSION" \
    || { rm -rf "$EXTRACT" "$TMP"; fail "Python을 설치하지 못했습니다."; }
  rm -rf "$EXTRACT" "$TMP"
  ln -sfn "$PBS_VERSION" "$DATA/runtime/current"
  PYTHON="$DATA/runtime/current/bin/python3"
fi

[ -x "$PYTHON" ] || fail "Python 실행 파일을 찾지 못했습니다."
export NALMEOK_PYTHON="$PYTHON"
exec "$HERE/run.command" "$@"

#!/bin/sh
# Runs the nalmeok TUI from the per-user data directory.
set -u

DATA="${NALMEOK_HOME:-$HOME/Library/Application Support/nalmeok}"
PY="${NALMEOK_PYTHON:-$DATA/runtime/current/bin/python3}"

if [ ! -x "$PY" ]; then
  echo "Python을 찾지 못했습니다. nalmeok을 다시 실행해 주세요." >&2
  printf 'Enter를 누르면 창을 닫습니다...'
  read -r _
  exit 1
fi

cd "$HOME" 2>/dev/null || cd /
printf '\033]0;nalmeok\007'

export NALMEOK_HOME="$DATA"
export NALMEOK_PACKAGED=1
export NALMEOK_YES=1
export NALMEOK_FFMPEG="$DATA/bin"
export PYTHONPATH="$DATA/app"

"$PY" -m nalmeok "$@"
status=$?
if [ "$status" -ne 0 ]; then
  printf '\nnalmeok이 오류로 종료되었습니다. (종료 코드 %s)\n' "$status"
  printf 'Enter를 누르면 창을 닫습니다...'
  read -r _
fi
exit "$status"

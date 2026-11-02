"""nalmeok — Gemini Batch·무료 live 전사 앱.

모든 모듈은 `import nalmeok as app` 뒤 `app.이름`으로 서로를 부릅니다. 테스트가
`mock.patch.object(app, ...)`로 한 곳만 바꿔도 모든 호출부에 반영되게 하려는
구조입니다. 각 모듈이 정의한 이름은 아래 `_export`가 자동으로 올립니다.
리팩터링(docs/review-2026-11.md §4)에서 새로 떼어 내는 모듈은 일반 import를 씁니다.
"""

# 테스트와 모듈이 app.os 등으로 접근하는 표준 모듈입니다.
import os  # noqa: F401
import shutil  # noqa: F401
import subprocess  # noqa: F401
import sys  # noqa: F401
import time  # noqa: F401
from pathlib import Path  # noqa: F401
from types import ModuleType

from .constants import *  # noqa: F401,F403
from .constants import __version__  # noqa: F401
from . import runtime, keys, term, update, media, engine, screens  # noqa: F401
from . import cli as _cli_module  # noqa: F401


def _export(module):
    """모듈이 정의한 이름을 app 네임스페이스에 올립니다. 수동 목록을 대신합니다."""
    namespace = globals()
    for name, value in vars(module).items():
        if name.startswith("__") or name == "app" or isinstance(value, ModuleType):
            continue
        namespace[name] = value


for _module in (runtime, keys, term, update, media, engine, screens, _cli_module):
    _export(_module)
del _module

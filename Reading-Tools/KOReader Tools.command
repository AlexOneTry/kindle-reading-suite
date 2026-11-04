#!/bin/bash
set -u
cd "$(dirname "$0")" || exit 1
export PYTHONUTF8=1
if ! command -v python3 >/dev/null 2>&1 || ! python3 -c 'import sys; sys.exit(sys.version_info < (3, 9))' 2>/dev/null; then
    echo 'Нужен Python 3.9 или новее. Установи Python с https://www.python.org/downloads/macos/'
    read -r -p 'Нажми Enter, чтобы закрыть окно. ' _
    exit 1
fi
python3 -X utf8 ./reading.py "$@"
status=$?
if [ "$#" = 0 ]; then
    echo
    read -r -p 'Нажми Enter, чтобы закрыть окно. ' _
fi
exit "$status"

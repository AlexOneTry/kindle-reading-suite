#!/bin/bash
set -u
cd "$(dirname "$0")" || exit 1
export PYTHONUTF8=1
if ! command -v python3 >/dev/null 2>&1 || ! python3 -c 'import sys; sys.exit(sys.version_info < (3, 9))' 2>/dev/null; then
    echo 'Установи Python 3.9 или новее с https://www.python.org/downloads/macos/'
    read -r -p 'Нажми Enter, чтобы закрыть окно. ' _
    exit 1
fi
python3 -X utf8 ./installer.py "$@"
status=$?
if [ "$#" = 0 ]; then
    read -r -p 'Нажми Enter, чтобы закрыть окно. ' _
fi
exit "$status"

#!/bin/bash
set -u
cd "$(dirname "$0")" || exit 1
export PYTHONUTF8=1
if ! command -v python3 >/dev/null 2>&1 || ! python3 -c 'import sys; sys.exit(sys.version_info < (3, 9))' 2>/dev/null; then
    echo 'Установи Python 3.9 или новее с python.org.'
    read -r -p 'Нажми Enter. ' _
    exit 1
fi
python3 -X utf8 -m unittest discover -s ./tests -v > ./Mac-check.txt 2>&1
result=$?
cat ./Mac-check.txt
echo 'Отчёт сохранён в Mac-check.txt рядом с этой командой.'
read -r -p 'Нажми Enter, чтобы закрыть окно. ' _
exit "$result"

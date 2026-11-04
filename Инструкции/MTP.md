# Kindle с подключением MTP

Если Kindle виден как медиаплеер/портативное устройство, но не как обычный диск с путём, автоматический USB-установщик не сможет писать на него. В macOS может понадобиться MTP-клиент, например OpenMTP. В Windows копирование обычно доступно в Проводнике во внутреннюю память устройства.

Прямое автоматическое копирование по MTP в этом выпуске не реализовано. Можно подготовить файлы на компьютере и перенести вручную. Выполняй команды из распакованной папки Suite. На Windows вместо `python3` используй `py -3` или `python`.

## Сначала проверка

```sh
python3 installer.py stage --output "MTP-Check"
```

Скопируй папку `extensions` из `MTP-Check` в корень внутренней памяти Kindle, где уже есть `koreader`. Извлеки устройство и запусти KUAL → Reading Kit: check compatibility.

Подключи снова и скопируй `extensions/reading-kit-check/report.json` с Kindle в папку Suite на компьютере. Не используй отчёт с другого устройства.

## Затем приложение

```sh
python3 installer.py stage --report "report.json" --output "MTP-Ready"
```

Если среда подходит, в `MTP-Ready` появятся `extensions` и `koreader`. Перед заменой существующих файлов сохрани их копию с Kindle на компьютер. Резервная копия установщика в этом режиме относится только к локальной папке MTP-Ready; копированием на Kindle управляет MTP-клиент.

Для первого входа подготовь файл подключения:

```sh
python3 installer.py connect "MTP-Ready"
```

После входа скопируй `extensions` и `koreader` из `MTP-Ready` в корень Kindle с объединением папок. Не удаляй и не заменяй целиком существующую папку koreader. После переноса удали локальный `MTP-Ready/extensions/yandex-books-pw2/pairing.txt`, он содержит данные входа. Извлеки Kindle и открой KUAL → Yandex Books.

## Статьи, заметки и слова по MTP

Статьи сначала сохраняй на компьютер и переноси EPUB через Проводник/MTP-клиент. Для заметок скопируй выбранные EPUB вместе с соседними папками .sdr в локальную папку, затем выполни:

```sh
python3 Reading-Tools/reading.py notes "Local-Books"
```

Для слов создай локальную папку `Local-Kindle/koreader/settings`, скопируй туда `vocabulary_builder.sqlite3` и файл с суффиксом `-wal`, если он есть. Также скопируй `koreader/reader.lua`, чтобы утилита распознала структуру. Затем:

```sh
python3 Reading-Tools/reading.py words "Local-Kindle"
```

Копируй базу только после выхода из KOReader. Сам Python-код не обращается к виртуальным MTP-путям.

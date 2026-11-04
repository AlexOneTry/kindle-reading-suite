#!/bin/sh
set -u
umask 077

dir=/mnt/us/extensions/yandex-books-pw2
state=/var/local/yandex-books-pw2
log=$dir/last-run.log

if [ "$(id -u)" != 0 ] && [ -x /var/local/mkk/su ]; then
    exec /var/local/mkk/su -c "/bin/sh $dir/start.sh"
fi

fail() {
    printf '%s\n' "$1" >> "$log"
    if command -v eips >/dev/null 2>&1; then
        eips 2 8 "Yandex Books: $1"
        sleep 4
    fi
    exit 1
}

[ "$(id -u)" = 0 ] || fail "KUAL root access required"
lock=/tmp/yandex-books-pw2.lock
mkdir "$lock" 2>/dev/null || exit 0

runtime=
app_pid=
framework_paused=0
awake=0
cleanup() {
    if [ -n "$app_pid" ]; then
        kill "$app_pid" 2>/dev/null || :
        wait "$app_pid" 2>/dev/null || :
    fi
    if [ "$framework_paused" = 1 ]; then
        killall -CONT cvm 2>/dev/null || :
        if [ -z "${book_path:-}" ]; then
            lipc-set-prop com.lab126.appmgrd start app://com.lab126.booklet.home 2>/dev/null || :
        fi
    fi
    if [ "$awake" = 1 ]; then
        lipc-set-prop com.lab126.powerd preventScreenSaver 0 2>/dev/null || :
    fi
    if [ -n "$runtime" ]; then
        rm -f "$runtime/yandex-books" "$runtime/fbink" "$runtime/frame.png" "$runtime/open-koreader" "$runtime/open-koreader-notes"
        rmdir "$runtime" 2>/dev/null || :
    fi
    rmdir "$lock" 2>/dev/null || :
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

if [ "${1:-}" != --return ]; then
    : > "$log"
fi
printf 'Yandex Books 0.6.0 | %s\n' "$(date)" >> "$log"
YANDEX_BOOKS_RETURN=0
[ "${1:-}" != --return ] || YANDEX_BOOKS_RETURN=1
export YANDEX_BOOKS_RETURN
runtime=$(mktemp -d /tmp/yandex-books.XXXXXX) || fail "Could not create temp folder"
cp "$dir/yandex-books" "$runtime/yandex-books" || fail "Missing app binary"
chmod 700 "$runtime/yandex-books" || fail "Could not start app"

fbink=
for candidate in "$dir/fbink" /var/local/kmc/bin/fbink /mnt/us/usbnet/bin/fbink /usr/bin/fbink; do
    if [ -f "$candidate" ]; then
        cp "$candidate" "$runtime/fbink" || continue
        chmod 700 "$runtime/fbink" || continue
        if "$runtime/fbink" -e >> "$log" 2>&1; then
            fbink=$runtime/fbink
            break
        fi
        # Installed builds can depend on libraries next to their original path.
        if [ -x "$candidate" ] && "$candidate" -e >> "$log" 2>&1; then
            fbink=$candidate
            break
        fi
    fi
done
[ -n "$fbink" ] || fail "FBInk missing. See last-run.log"

# Let KUAL finish returning to the home screen before taking over the display.
sleep 2
if lipc-set-prop com.lab126.powerd preventScreenSaver 1 2>/dev/null; then
    awake=1
fi
if killall -STOP cvm 2>/dev/null; then
    framework_paused=1
fi

"$runtime/yandex-books" -dir "$dir" -state "$state" -runtime "$runtime" -fbink "$fbink" >> "$log" 2>&1 &
app_pid=$!
wait "$app_pid"
status=$?
app_pid=
if [ "$status" = 0 ] && [ -s "$runtime/open-koreader" ]; then
    IFS= read -r book_path < "$runtime/open-koreader"
    case "$book_path" in
        '/mnt/us/Yandex Books/'*.epub)
            if [ -f "$book_path" ] && [ -f /mnt/us/koreader/koreader.sh ]; then
                printf '%s\n' 'Передача EPUB в KOReader' >> "$log"
                YANDEX_BOOKS_ANNOTATIONS=
                if [ -f "$runtime/open-koreader-notes" ]; then
                    YANDEX_BOOKS_ANNOTATIONS=$book_path
                fi
                export YANDEX_BOOKS_ANNOTATIONS
                cleanup
                trap - EXIT HUP INT TERM
                /bin/sh /mnt/us/koreader/koreader.sh --kual "$book_path" >> "$log" 2>&1
                reader_status=$?
                printf 'KOReader exit: %s\n' "$reader_status" >> "$log"
                if [ "$reader_status" = 0 ]; then
                    exec /bin/sh "$dir/start.sh" --return
                fi
                exit "$reader_status"
            fi ;;
    esac
    book_path=
    fail "Could not open EPUB in KOReader"
fi
if [ "$status" != 0 ]; then
    printf 'Приложение завершилось с кодом %s\n' "$status" >> "$log"
fi
exit "$status"

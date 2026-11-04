#!/bin/sh
set -u
umask 077
dir=/mnt/us/extensions/reading-kit-check
if [ "$(id -u)" != 0 ] && [ -x /var/local/mkk/su ]; then
    exec /var/local/mkk/su -c "/bin/sh $dir/check.sh"
fi
work=$(mktemp -d /tmp/reading-kit-check.XXXXXX) || exit 1
trap 'rm -rf "$work"' EXIT
trap 'exit 1' HUP INT TERM
cp "$dir/probe" "$work/probe" || exit 1
chmod 700 "$work/probe" || exit 1
cp "$dir/fbink" "$work/fbink" || exit 1
chmod 700 "$work/fbink" || exit 1
report=$dir/report.json
if "$work/probe" "$work/fbink" /var/local/kmc/bin/fbink /mnt/us/usbnet/bin/fbink /usr/bin/fbink > "$work/report.json" 2> "$work/probe-error.txt" && [ -s "$work/report.json" ]; then
    cp "$work/report.json" "$dir/.report.tmp" && mv "$dir/.report.tmp" "$report" || exit 1
else
    printf '%s\n' '{"schema":1,"probe_version":"1.0.0","probe_error":"The ARMv7 compatibility probe could not run on this device"}' > "$report"
fi
if command -v eips >/dev/null 2>&1; then
    eips 2 8 'Reading Kit: check saved. Reconnect USB.' 2>/dev/null || :
fi

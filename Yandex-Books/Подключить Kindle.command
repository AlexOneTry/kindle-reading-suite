#!/bin/bash
set -eu
umask 077

echo 'Подключение Яндекс Книг к Kindle Paperwhite'
if [ "${1:-}" ]; then
    target="${1%/}/extensions/yandex-books-pw2"
else
    candidates=()
    for path in /Volumes/*/extensions/yandex-books-pw2; do
        if [ -f "$path/config.xml" ]; then candidates+=("$path"); fi
    done
    if [ "${#candidates[@]}" != 1 ]; then
        echo 'Подключи Kindle по USB и скопируй на него расширение из архива.'
        echo 'Если подключено несколько устройств, передай путь к нужному тому как аргумент.'
        exit 1
    fi
    target="${candidates[0]}"
fi
if [ ! -f "$target/config.xml" ]; then
    echo 'На этом диске нет установленного расширения.'
    exit 1
fi

client_id=4483e97bab6e486a9822973109a14d05
state=$(/usr/bin/openssl rand -hex 24)
url="https://oauth.yandex.ru/authorize?response_type=token&client_id=$client_id&state=$state"
if ! /usr/bin/open "$url"; then
    echo "Открой адрес в браузере: $url"
fi
echo 'Войди в Яндекс. После входа может появиться страница «Авторизация в приложении Букмейт не удалась».'
echo 'На этой странице нажми ⌘L, затем ⌘C. Вставь полный адрес сюда. Ввод скрыт.'

extract_token() {
    local callback="$1" fields part token='' returned_state=''
    local IFS='&'
    case "$callback" in
        "https://yx${client_id}.oauth.yandex.ru/"*) ;;
        *) return 1 ;;
    esac
    if [[ "$callback" == *'#'* ]]; then
        fields="${callback#*#}"
    elif [[ "$callback" == *'?'* ]]; then
        fields="${callback#*\?}"
    else
        return 1
    fi
    set -f
    for part in $fields; do
        case "$part" in
            access_token=*) token="${part#access_token=}" ;;
            state=*) returned_state="${part#state=}" ;;
            error=*) set +f; return 2 ;;
        esac
    done
    set +f
    local token_pattern='^y0_[a-zA-Z0-9_.~-]+$'
    [[ "$returned_state" == "$state" && "$token" =~ $token_pattern ]] || return 1
    [ "${#token}" -ge 20 ] && [ "${#token}" -le 4096 ] || return 1
    printf '%s' "$token"
}

while read -r -s -p 'Адрес из браузера: ' input; do
    echo
    if value=$(extract_token "$input"); then break; fi
    echo 'В адресе нет данных этого входа. Скопируй полный адрес из вкладки, которую открыла команда.'
    echo 'Чтобы отменить, нажми Ctrl+C.'
done
[ -n "${value:-}" ] || exit 1
echo 'Проверяю доступ к «Моим книгам»…'
if ! printf 'header = "Auth-Token: %s"\n' "$value" | /usr/bin/curl --config - \
  --fail --silent --show-error --max-time 15 --output /dev/null \
  -H 'App-Language: ru' -H 'App-Locale: ru' -H 'App-Platform: android' \
  -H 'Bookmate-Version: 20200305' -H 'Device-Os: Android' -A 'okhttp/4.12.0' \
  'https://api.bookmate.yandex.net/api/v5/profile/library_cards?limit=1&offset=0'; then
    echo 'Не удалось подтвердить доступ к полке. Проверь интернет и повтори вход.'
    exit 1
fi
temp=$(/usr/bin/mktemp "$target/.pair.XXXXXX")
trap 'rm -f "$temp"' EXIT
printf '%s\n' "$value" > "$temp"
mv -f "$temp" "$target/pairing.txt"
unset value input
echo 'Готово. Извлеки Kindle и запусти KUAL → Yandex Books.'
echo 'При запуске приложение перенесёт токен из USB-папки во внутреннее хранилище.'

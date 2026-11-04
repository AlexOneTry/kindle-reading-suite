$ErrorActionPreference = 'Stop'

try {
    $targets = @(
        foreach ($drive in [IO.DriveInfo]::GetDrives()) {
            if ($drive.DriveType -notin @('Removable', 'Fixed') -or -not $drive.IsReady) { continue }
            $folder = Join-Path $drive.RootDirectory.FullName 'extensions\yandex-books-pw2'
            if (Test-Path -LiteralPath (Join-Path $folder 'config.xml') -PathType Leaf) {
                $folder
            }
        }
    )
    if ($targets.Count -ne 1) {
        Write-Host 'Подключи один Kindle по USB. Убедись, что на нём есть папка extensions\yandex-books-pw2.'
        exit 1
    }

    $client = '4483e97bab6e486a9822973109a14d05'
    $state = [Guid]::NewGuid().ToString('N')
    $callbackHost = "yx$client.oauth.yandex.ru"
    Start-Process "https://oauth.yandex.ru/authorize?response_type=token&client_id=$client&state=$state"
    Write-Host 'Войди в свой аккаунт Яндекса. После входа браузер может показать ошибку открытия Букмейта.'
    Write-Host 'На этой странице нажми Ctrl+L, затем Ctrl+C. Вернись сюда, вставь адрес и нажми Enter.'
    Write-Host 'Ввод скрыт. Для отмены нажми Ctrl+C.'

    $token = $null
    while (-not $token) {
        $secret = Read-Host 'Адрес из браузера' -AsSecureString
        $ptr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secret)
        try {
            $address = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($ptr)
            $uri = $null
            if ([Uri]::TryCreate($address.Trim(), [UriKind]::Absolute, [ref]$uri) -and
                $uri.Scheme -ceq 'https' -and $uri.Host -ieq $callbackHost) {
                $fields = $uri.Fragment.TrimStart('#') + '&' + $uri.Query.TrimStart('?')
                $tokens = [regex]::Matches($fields, '(?:^|&)access_token=(y0_[A-Za-z0-9_.~-]{17,4093})(?=&|$)')
                $states = [regex]::Matches($fields, '(?:^|&)state=([^&]+)')
                if ($tokens.Count -eq 1 -and $states.Count -eq 1 -and
                    $states[0].Groups[1].Value -ceq $state -and $fields -cnotmatch '(?:^|&)error=') {
                    $token = $tokens[0].Groups[1].Value
                }
            }
        } finally {
            [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($ptr)
            $secret.Dispose()
            $address = $uri = $fields = $tokens = $states = $null
        }
        if (-not $token) {
            Write-Host 'В адресе нет данных этого входа. Скопируй полный адрес из вкладки, которую открыла команда.'
        }
    }

    Write-Host 'Проверяю доступ к «Моим книгам»…'
    $headers = @{
        'Auth-Token' = $token
        'App-Language' = 'ru'
        'App-Locale' = 'ru'
        'App-Platform' = 'android'
        'Bookmate-Version' = '20200305'
        'Device-Os' = 'Android'
    }
    try {
        $null = Invoke-WebRequest -UseBasicParsing -MaximumRedirection 0 -TimeoutSec 15 `
            -Headers $headers -UserAgent 'okhttp/4.12.0' `
            -Uri 'https://api.bookmate.yandex.net/api/v5/profile/library_cards?limit=1&offset=0'
    } catch {
        Write-Host 'Не удалось подтвердить доступ к полке. Проверь интернет и повтори вход.'
        exit 1
    } finally {
        $headers.Clear()
    }

    $temp = Join-Path $targets[0] ('.pair-' + [Guid]::NewGuid().ToString('N'))
    try {
        [IO.File]::WriteAllText($temp, $token + "`n", [Text.Encoding]::ASCII)
        Move-Item -LiteralPath $temp -Destination (Join-Path $targets[0] 'pairing.txt') -Force
    } finally {
        $token = $null
        Remove-Item -LiteralPath $temp -ErrorAction SilentlyContinue
    }
    Write-Host 'Готово. Безопасно извлеки Kindle и запусти KUAL → Yandex Books.'
    exit 0
} catch {
    Write-Host 'Подключение не завершилось. Проверь USB и папку extensions\yandex-books-pw2 на Kindle.'
    exit 1
}

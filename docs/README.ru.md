# ssh-alertd: форк с исправлениями безопасности

Оригинал: `Amekiri-Studio/ssh-alertd`. Форк: `den0043/ssh-alertd`.
Лицензия Apache 2.0 сохранена; локальные изменения перечислены в
[SECURITY_PATCHES.md](../SECURITY_PATCHES.md). Это не официальный релиз автора.

## Текущее состояние

Перенесены локальные исправления Go-демона и негативные тесты. Рабочая ветка —
`fix/security-hardening`. На этом этапе релиз не опубликован, GitHub Actions и
пакетные сценарии ещё не адаптированы/не проверены для нашего выпуска.
Наличие старого workflow не означает, что сборка форка уже прошла в GitHub.

Не скачивайте пакет оригинального проекта в предположении, что в нём есть наши
исправления. Компактный `setup-vps.sh` должен ссылаться на собственный проверенный
релиз с закреплёнными SHA256, только после его публикации.

## Что исправлено

- Парсер принимает полное сообщение `Accepted … ssh2`, проверяет IP и порт;
  подстрока `Accepted` в неудачном входе больше не создаёт ложный успех.
- Journald читается в JSON. Проверяются доверенные `_UID=0` и абсолютный `_EXE`
  с basename `sshd`/`sshd-session`; одного `SYSLOG_IDENTIFIER` недостаточно.
- Telegram: ошибки сети/TLS/HTTP не содержат секретный URL и тело ответа;
  перенаправления не выполняются. Логи маскируют токен и SMTP-пароль.
- Неожиданный EOF/ошибка источника завершают демон ненулевым кодом. Unit имеет
  `Restart=always`, но ручная остановка службы не отменяется этой политикой.
- Каталог `/etc/ssh-alertd` — root:ssh-alertd 0750, JSON — root:ssh-alertd 0640.
  Служба читает конфиг, но не может его переписать. Tmpfiles приведён к этому
  правилу. На существующем сервере нужно проверить локальные overrides.

## Локальная сборка и тесты

Нужен совместимый Go toolchain; локально используется Go 1.27.3.

```bash
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o ssh-alertd .
# TMPDIR должен указывать на отдельный существующий scratch-каталог.
SSH_ALERTD_BINARY="$PWD/ssh-alertd" python3 -I -B tests/runtime-test.py
```

Тесты используют искусственные события, временные файлы, дочерние тестовые
процессы и HTTP/TLS-серверы на loopback. Они не меняют SSH/PAM/файрвол, не
устанавливают службу, не пишут в системный journal и не обращаются к Telegram.
Проверки файлов unit/tmpfiles — статические тесты, не установка через systemd.

Схема JSON и шаблоны Telegram/SMTP сохранены. `hostname` — только метка в
уведомлении; системное имя и `/etc/hosts` программа не меняет.

## Секреты и проверка доставки

- Бот и chat ID уже должны быть готовы; пользователь заранее открывает чат с
  ботом. Не публикуйте конфиг, токен, настоящий Chat ID и не присылайте токен в чат.
- Вводите токен в доверенном редакторе конфигурации или скрытом опросе установщика,
  не в аргументе curl, URL браузера или строке shell history.
- Root и процесс службы имеют доступ к токену. Пользователь службы входит в
  `systemd-journal` и поэтому читает системный журнал — это осознанная привилегия.
- Если токен ранее утёк в журнал, патч прошлую утечку не устраняет: отзовите токен
  и задайте новый. Секретные бэкапы также нужно защищать.

### Ручной тест из сохранённого конфига

Это только тест API, не проверка SSH. Команда читает токен из защищённого файла,
не передаёт его в argv и не печатает URL или текст сетевого исключения.

```bash
sudo /usr/bin/python3 -I - <<'PY'
import json
import urllib.request

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        raise RuntimeError('redirect refused')

try:
    with open('/etc/ssh-alertd/config.json', encoding='utf-8') as stream:
        cfg = json.load(stream)['notifiers']['telegram']
    body = json.dumps({'chat_id': cfg['chat_id'],
                       'text': 'Тест API ssh-alertd, не событие входа'}).encode()
    request = urllib.request.Request(
        'https://api.telegram.org/bot' + cfg['bot_token'] + '/sendMessage',
        data=body, headers={'Content-Type': 'application/json'}, method='POST')
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    with opener.open(request, timeout=20) as response:
        ok = response.status == 200 and json.loads(response.read(1048576)).get('ok') is True
    if not ok:
        raise RuntimeError('API rejected test')
    print('Telegram API подтвердил отправку. Проверьте чат.')
except Exception:
    print('Тест не прошёл. Проверьте локально конфиг, сеть и права; секреты скрыты.')
    raise SystemExit(1) from None
PY
```

После установки на **отдельный тестовый VPS** нужен новый вход с обычного
SSH-клиента, без переиспользования соединения:

```bash
ssh -o ControlMaster=no -o ControlPath=none USER@HOST
```

Подставьте свой порт/ключ при необходимости. Дождитесь уведомления с правильными
пользователем, IP и меткой. Не устанавливайте ключ для SSH на localhost и не
ослабляйте аутентификацию только ради этого теста.

## Что НЕ гарантируется

Нет постоянной очереди, journal cursor, replay и повторной отправки Telegram.
При простое демона, перезапуске и сетевых сбоях события могут потеряться. Unit
восстанавливает мониторинг, но не пропущенные уведомления. Файловый источник
проверяет синтаксис строки, но не даёт доверенных метаданных journald.

Компрометация root не покрывается. Поддельный root-процесс с допустимым именем
тоже выходит за рамки проверки происхождения. Успешные локальные тесты не
заменяют сквозную проверку systemd → новый SSH-вход → Telegram на целевом VPS.

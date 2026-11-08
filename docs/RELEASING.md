# CI и выпуск security-форка

## Статус этапа 2

Автоматизация подготовлена для форка, локально проверяется настоящими сборками.
Это не утверждение, что GitHub Actions уже запущен или релиз опубликован.
Push/теги/публикация выполняются пользователем на следующем этапе.
`setup-vps.sh` пока не меняется и не ссылается на неопубликованные файлы.

## Схема

- `VERSION`: первая собственная версия `0.2.1-security.1`.
- `.go-version`: точный Go 1.27.3; `go.mod` задаёт языковой минимум отдельно.
- `scripts/check.py`: gofmt, go vet, go test с race detector и Python unittest.
- `scripts/release.py`: сборка/проверка артефактов; не устанавливает и не публикует.
- `tests/runtime-test.py`: проверка настоящего native ELF на scratch и loopback.
- `scripts/publish_draft.py`: только для tag-job, создаёт и проверяет черновик.

Builder требует чистое закоммиченное дерево и точный toolchain. Go-модули из сети
не скачиваются; внешних Go-зависимостей сейчас нет. Флаги CGO=0, GOOS=linux,
GOAMD64=v1, GOARM64=v8.0, GOEXPERIMENT пуст, trimpath, buildvcs=false,
пустой buildid. В ELF встраиваются версия/коммит/clean-state. Время и абсолютные
пути сборщика не помещаются в metadata; tar/gzip имеют нормализованные mtime/uid.
Повторяемость сравнивается для одинакового коммита и toolchain, а не обещается
для произвольных окружений. Dirty-эксперименты разрешены только явным флагом
`--allow-dirty` и не проходят обычную release verification.

## CI

`ci.yml` работает для main, fix/**, ci/**, PR в main и ручного запуска.
Проверки и сборка не имеют contents:write. Checkout не сохраняет GitHub token в
Git-конфигурации, cache выключен. После тестов собираются обе архитектуры,
проверяется реальный amd64 ELF. Preview сохраняется на 7 дней, кроме PR.

Actions закреплены полными commit SHA (проверены по официальным репозиториям):

| Action | Версия | Commit |
|---|---|---|
| checkout | v7.0.1 | 3d3c42e5aac5ba805825da76410c181273ba90b1 |
| setup-go | v7.0.0 | b7ad1dad31e06c5925ef5d2fc7ad053ef454303e |
| upload-artifact | v7.0.2 | cf430e030ddbb5b0abf93d22962f4752f3646cd9 |
| download-artifact | v8.0.2 | 9000827ccba6bdab643e8b6fd33ac0654aef8333 |

Теги Action сами могут перемещаться; изменение закреплённого SHA требует ревью.
YAML/schema/expression и shell проверяются локально actionlint 1.7.12 + ShellCheck.
Static policy tests также запрещают shell-интерполяцию GitHub expressions,
незакреплённые Actions, лишние write-права и публикацию из PR/manual dispatch.
Значения GitHub context передаются в shell только через env и кавычки.

## Какие файлы получаются

Для версии V создаются ровно 7 release assets:

- `ssh-alertd-V-linux-amd64` — статический ELF для обычного x86-64 VPS;
- `ssh-alertd-V-linux-arm64` — статический ELF для ARM64;
- `ssh-alertd-V-linux-amd64.tar.gz`;
- `ssh-alertd-V-linux-arm64.tar.gz`;
- `BUILDINFO.json` — версия, source commit/state, toolchain, хеши файлов;
- `LICENSES.txt` — лицензии оригинала, модификаций и Go;
- `SHA256SUMS` — хеши остальных шести файлов, имена без путей.

Архивы содержат свой ELF, исходную лицензию/сводные лицензии, README, русскую
инструкцию, описание патчей, пример конфига, unit/sysusers/tmpfiles и шаблоны.
Builder проверяет ELF64/архитектуру и отсутствие PT_INTERP, структуру/содержимое
архивов, регулярные файлы, права, размеры и хеши. Ничего не извлекается от root.
Собственные .deb пока не выпускаются: старый postinst возвращает владельца
ssh-alertd вместо root. Legacy Debian-builder отключён, Arch packaging оставлен
исторической справкой; их установка требует отдельного аудита.

## Release workflow

1. Push уже существующего тега `v<VERSION>` запускает собственные тесты и сборку.
   Несовпадающий тег останавливает сборку. Никаких произвольных input version.
2. Ручной `workflow_dispatch` только тестирует и сохраняет artifacts; draft-job
   пропускается. Никаких release write-прав у заданий сборки.
3. Только отдельное задание `draft`, зависящее от успешных проверок, получает
   contents:write. Используется встроенный GITHUB_TOKEN, не пользовательский PAT.
   Токен Telegram и Chat ID этому процессу вообще не нужны.
4. Download-artifact настроен на отказ при несовпадении digest. Затем повторно
   проверяются все наши хеши/metadata. Remote tag должен указывать на tested SHA.
5. `gh release create` использует verify-tag, draft, prerelease и latest=false.
   Теги не создаются автоматически, файлы не перезаписываются, clobber нет.
6. После создания читаются точный draft и список assets, файлы скачиваются вновь,
   проверяются и сравниваются побайтно с исходными проверенными артефактами.
   Ошибка не приводит к автоматической публикации; частичный draft нужно осмотреть.
7. Публичный выпуск — отдельное ручное действие пользователя.

Задание использует GitHub environment `release`. Само имя environment **не даёт
review-защиту**: required reviewers нужно настроить отдельно, если они нужны.
До публикации рекомендуется включить immutable releases в настройках GitHub.
Защита immutable действует после публикации, а не на изменяемый черновик.
Не считайте SHA256SUMS цифровой подписью или защитой при подмене всего скрипта.

## Локальная проверка

Нужны Go из .go-version, Python 3.9+, Git, tail; только непривилегированный запуск.
TMPDIR должен указывать на существующий приватный scratch-каталог.
Выходной каталог должен отсутствовать или быть пустым — повторного overwrite нет.

```bash
python3 -I -B scripts/check.py
python3 -I -B scripts/release.py build --output dist
IFS= read -r version < VERSION
commit="$(git rev-parse HEAD)"
python3 -I -B scripts/release.py verify --directory dist --version "$version" --commit "$commit"
SSH_ALERTD_BINARY="$PWD/dist/ssh-alertd-${version}-linux-amd64" python3 -I -B tests/runtime-test.py
```

Для проверки повторяемости используйте два разных пустых выходных каталога и
сравните каждый файл. ARM64 здесь кросс-компилируется и структурно проверяется,
но не запускается на amd64. Все установочные/systemd действия и настоящий
SSH→Telegram тест остаются для отдельного VPS, не для сервера Hermes.

## Следующий этап — публикация пользователем

Сначала отправить подготовленную ветку, проверить GitHub CI и включение Actions
в форке. Затем перенести изменения в main и поставить тег, точно совпадающий с
VERSION, на проверенный коммит. Готовый draft осмотреть, только затем публиковать.
Команды push/tag сейчас не исполнялись. Если при переносе/merge меняется SHA,
встроенный source commit и хеши бинарников тоже изменятся: в setup-vps.sh потом
нужно закрепить **реальные опубликованные** файлы, а не хеши локальной репетиции.

При изменении версии редактировать VERSION до коммита; не перемещать старые
теги и не заменять assets опубликованной версии. При неудачном черновике не
запускать автоматическое удаление: осмотреть и решить вручную.

# Фаза 2: Инсталлятор (codex-install / codex-uninstall)

> Superseded configuration policy: this historical plan predates shared E → existing L → N selection and revision-checked edits. See [current configuration and compatibility](../../../README.md#manual-configuration); historical path/copy guidance below is not an active recipe.

> [!WARNING]
> **ИСТОРИЧЕСКИЙ ДОКУМЕНТ. НЕ РЕАЛИЗОВЫВАТЬ БУКВАЛЬНО.** Manual merge в
> `~/.codex/hooks.json`, изменение `config.toml`, frozen CLI ниже и pointer-file installer отменены.
> Нормативный native plugin/marketplace контракт находится в `00-overview-and-decisions.md`.

Сохраняет исследование отменённого installer-подхода и замечания его критиков.

Примечание по нумерации: "фаза 2 — delivery wiring" по факту полностью покрыта решениями 1.5-1.7 Фазы 1 — самостоятельной фазы не требует. Эта фаза — про то, как код Фазы 1 попадает на диск пользователя и в `~/.codex/hooks.json`/`config.toml`.

## 2.1 — Новый примитив: generic file lock

Новый файл `internal/filelock/filelock.go`:
```go
func WithLock(lockPath string, fn func() error) error
```
Извлечь платформенно-специфичную реализацию из `internal/teamstate/lock_unix.go`/`lock_windows.go` (сейчас `withFileLock(teamName string, fn func() error)`, единственная связанность — `teamName`, тело не трогает другое package-level состояние — подтверждено критиком чтением кода). `teamstate.withFileLock` — тонкая обёртка над новым примитивом, **сигнатура byte-identical**, тесты не трогать.

**`WithLock` обязан делать `os.MkdirAll(filepath.Dir(lockPath), 0700)`** (критик поймал реальный гэп: `os.OpenFile` не создаёт родительские директории; teamstate этого никогда не задевал, т.к. `os.TempDir()` всегда существует, а наш путь может не существовать у чистого Codex-юзера).

**Лок-путь — `~/.claude/claude-notifications-go/codex-hooks-install.lock`, НЕ в `~/.codex/`** (моя первая интуиция "колоцировать с hooks.json" была НЕВЕРНОЙ — критик поправил: лок защищает "наш инсталлер vs наш инсталлер", не "vs Codex" — та гонка отдельная, см. 2.3 content-hash guard; колокация ничего не даёт и создаёт мусорный файл в чужой директории, которая может не существовать, если Codex не установлен). Не unlink'ать лок-файл при разлоке (flock-конвенция: существование файла ≠ владение локом).

## 2.2 — `codex-install`: детект поддержки hooks.json

**ПОЛНОСТЬЮ ПЕРЕПИСАНО после ресёрча фазы 3.** Изначальная и даже отревизированная (exit-code subcommand'а) стратегии проб были основаны на пробе НЕСУЩЕСТВУЮЩЕЙ вещи: **у Codex hooks НЕТ CLI-поверхности вообще** — `codex hooks --help` не существует, конфигурация только файлами (`~/.codex/hooks.json` или inline в `config.toml`), trust только через TUI `/hooks`-команду (источники: официальная дока ChatGPT/Codex, независимый Go-декодер с совпадающими struct-тегами).

**Новая стратегия**:
1. `codex --version` — единственный автоматический сигнал (последний резерв старой ревизии становится единственным доступным путём). Semver-парсинг с осторожностью (не-semver/parse-fail → "неизвестно").
2. Наличие директории `~/.codex/` — вспомогательный сигнал (не решающий сам по себе).
3. **Если версия неоднозначна или неизвестна — интерактивный вопрос юзеру как ПЕРВИЧНЫЙ путь**, не крайний fallback: "поддерживает ли ваш Codex hooks.json? [y/N]" (дефолт N → legacy, безопасное направление).
4. `codex` не найден в PATH вообще → ставим только файлы на диск, hooks.json/config.toml не трогаем, инструкция "перезапустите codex-install после установки Codex CLI".

**Новое, серьёзнее прежнего (ресёрч фазы 3)**: источники расходятся по таймлайну — ранние версии Codex поставляли hooks как **experimental, off-by-default**, требуя явный `[features] codex_hooks = true` в `config.toml` (в новых версиях `codex_hooks` — deprecated-алиас `hooks`); один источник утверждал **"недоступно на Windows"**. Из этого: (а) `codex-install` может быть обязан **писать `[features]`-флаг** в `config.toml` в зависимости от определённой версии — дополнительный шаг, не в исходном 2.3; (б) **Windows-путь для hooks — не подтверждён, БЛОКИРУЮЩИЙ research-item перед написанием Windows-специфичного кода** (см. 2.3 п.1 про `.exe`-путь и 3.4/3.5 Windows-тесты в Фазе 3). До подтверждения — Windows-юзеры по умолчанию могут быть ограничены только legacy-notify-путём (2.5).

**Edge case**: таймаут 2-3с на `codex --version`, `CommandContext` (уже используемый в репо паттерн — `focus_linux.go`, `focus_darwin.go`, `ax_focus_darwin.go`), `Stdin=os.DevNull` во избежание случайного запуска TUI.

## 2.3 — `codex-install`: hooks.json merge (основной путь, если Windows подтверждён / не-Windows)

1. **Путь бинаря — стабильный, неверсионированный. См. §2.8 за полным решением** (исходная версия этого пункта ссылалась на несуществующий симлинк — реальный путь строится через `install.sh INSTALL_TARGET_DIR=~/.claude/claude-notifications-go/bin`, не готовый существующий артефакт). Windows (при подтверждении поддержки) — та же стабильная директория, `.bat` непригоден для exec-формы hooks.json (нет шелла) — указывать прямо на platform-arch-stamped `.exe` внутри нее.
2. **Единая frozen argv-форма** (устранено противоречие, найденное критиком: изначальные 2.3/2.4 замораживали РАЗНЫЕ command-строки — с маркером и без; trust хэширует всю строку, расхождение форм = принудительный re-trust): `<путь> handle-codex-hook Stop --owner=claude-notifications-go` / `... PermissionRequest --owner=claude-notifications-go` — owner-маркер входит в frozen-форму **с первого релиза**, не добавляется позже. Cross-phase ограничение для Фазы 1 (подтверждено трассировкой: `handle-hook`-диспетчер уже использует `len(os.Args) < 3`, не `==`): `handleCodexHook` читает только `os.Args[2]`, терпит хвостовые аргументы, никогда не `flag.Parse()` их.
3. `WithLock` (2.1) вокруг всего read-modify-write цикла.
4. Существующий `hooks.json` — не существует → создаём с нуля; парсится → merge (2.4); malformed → **не трогаем**, чёткая ошибка, `os.Exit(1)` (это интерактивная команда, уместно).
5. **Content-hash guard** (заменяет исходный mtime+size — критик нашёл реальную дыру: same-byte-length правка в пределах одного грубого mtime-тика на FAT/HFS+/сетевых FS проходит незамеченной): read+hash(sha256, файл маленький) → merge в памяти → write temp → **re-hash оригинала прямо перед rename** → изменился → abort+retry (видимый: печатать каждую попытку в stdout, финальная ошибка actionable — "закройте Codex и запустите снова"), не изменился → rename немедленно. Явная оговорка: best-effort ДЕТЕКЦИЯ, не полное исключение TOCTOU — настоящая защита операционная (рекомендация закрыть Codex перед установкой).
6. **Windows retry — конкретизирован**: детект через `*os.LinkError` → `syscall.Errno` на `ERROR_ACCESS_DENIED`(5)/`ERROR_SHARING_VIOLATION`(32) (не строковый матч), 5 попыток, экспоненциальный backoff 50/100/200/400мс (<1с итого).
7. **Backup — точные байты, прочитанные при hash-снимке**, не свежее перечтение. Rolling namespaced `hooks.json.claude-notifications.bak`, перезаписывается на каждую мутацию.
8. Best-effort детект запущенного `codex`-процесса (не блокирует, только предупреждает) — Windows: расширить существующий `defaultProcessSnapshot()`/`CreateToolhelp32Snapshot` (`focus_windows.go`) полем `ExeFile`, уже там есть, не используется. Unix: нет готового primitive по имени (PID-based `FindProcess`+`Signal(0)` есть, but keyed by known PID) — новая, дешёвая работа (`pgrep -x codex`, timeout, skip on error). Не строить тяжёлую кросс-платформенную абстракцию ради soft-warning.
9. `"timeout": 30`, `"async": true` в записи хука.
10. `MkdirAll(~/.codex, 0700)` для случая "Codex в PATH, но ни разу не запускался".

## 2.4 — Owner-marker: идемпотентный merge/remove

- Маркер — `--owner=claude-notifications-go` внутри `args` (единая с 2.3 п.2 форма).
- Поиск — токен-подстрока, не exact-array-equality (критик: Codex как co-writer может переформатировать файл).
- **Открытый вопрос #4 решён**: при повторном `codex-install` — **ВСЕГДА сбрасывать owner-marked записи к канонической форме**, не "не трогать руками отредактированное". Маркер означает "мы этим владеем" — сохранение ручной правки делает установку неидемпотентной (state drift между юзерами одной версии); любая правка command-строки уже сломала trust-хэш, сохранение сломанного состояния никому не помогает. Уточнение: разделить trust-хэшируемую command-строку (заморожена навсегда) от НЕ-хэшируемых метаданных (`timeout`/`async` — безопасно сбрасывать каждый раз) — canonical-reset становится единственным поведением, `--force`-флаг не нужен. Чужие записи — никогда не трогать.

## 2.5 — `codex-install`: legacy notify fallback (если hooks не поддерживается/Windows не подтверждён)

**Понижено из "one-liner стаба" до отдельного под-плана** (критик поймал: моя ссылка на прецедент bootstrap.sh была фактической ошибкой — bootstrap.sh использует jq/python только для ЧТЕНИЯ, миграция config.json — whole-file `cp`, никакого decode→merge-с-сохранением-ключей→reencode нигде в репо не делается; это полностью новая логика).

**`handle-codex-notify` — отдельная подкоманда, отдельная DTO**, транспорт другой (argv-JSON, kebab-case: `type=agent-turn-complete, thread-id, turn-id, cwd, client(опционально), input-messages, last-assistant-message` — **подтверждено точно двумя независимыми источниками в ресёрче фазы 3**), не stdin — переиспользования с Фазой 1 почти нет.

**Критичная функциональная асимметрия (обязательна к документированию)**: legacy notify фаерится ТОЛЬКО на `agent-turn-complete` — **`PermissionRequest` физически недоставим на legacy-пути**. Юзеры старого Codex/Windows(если неподдержан) молча не получат ни одного permission-уведомления, если не проговорено явно (см. 2.6).

**Research-item**: legacy payload не несёт `session_id` (только `thread-id`/`turn-id`) — совместимость с `sessionname.GenerateSessionName` не проверена для ЭТОЙ формы ID, требует подтверждения.

**TOML — НЕ full decode+reencode**: **хирургическая текстовая вставка**. `go-toml/v2`-Marshal (или любой TOML-энкодер) не сохраняет комментарии/форматирование/порядок ключей — decode+reencode помнёт primary hand-edited конфиг юзера. Добавляем ровно один top-level ключ `notify = [...]` — READ нужен только для проверки существования/владения (decode в узкий struct `{Notify []string}`), запись — text-insert. **Конкретный TOML-фогтан**: `notify` — top-level ключ; наивный append-в-EOF при наличии `[table]`-секций положит ключ ВНУТРИ последней секции (в TOML ключи после `[header]` принадлежат ей) — вставлять ПЕРЕД первым `[`-заголовком, если секции есть; если секций нет — в конец файла. `go.mod` подтверждённо не имеет TOML-зависимости (`grep -rn toml` пуст) — добавление `github.com/pelletier/go-toml/v2` (только для READ) обязательно.

Если `notify=[...]` уже существует и НЕ наш — не трогаем, явное сообщение "notify already configured by another tool, skipping".

**Отдельный lock-путь** для `config.toml`-мутации (2.1 определял лок только для hooks.json).

## 2.6 — Финальный вывод `codex-install` юзеру

```
✅ Codex hooks installed -> ~/.codex/hooks.json
   Events: Stop, PermissionRequest   (binary: <abs path>)

⚠ Required last step — Codex won't run these hooks until you trust them:
   1) run:  codex
   2) at the Codex prompt type:  /hooks
   3) approve "claude-notifications-go" when asked
   Until then, no notifications arrive.

Diagnostics: ~/.claude/claude-notifications-go/notification-debug.log
```
Если `/hooks`-флоу к моменту релиза не подтверждён живым тестом — заменить шаги 1-3 на версия-агностичную формулировку. Для legacy-ветки — обязательная строка: "⚠ Permission-уведомления недоступны на вашей версии Codex — будут приходить только уведомления о завершении задачи." Продублировать "codex не найден, перезапустите codex-install позже".

## 2.7 — `codex-uninstall`

- `WithLock` — **тот же лок-путь, что `codex-install`** (иначе не взаимоисключают друг друга).
- Убираем ТОЛЬКО owner-marked записи (токен-поиск, толерантный к переформатированию).
- **Порядок backup/verify исправлен** (критик нашёл реальную дыру: дефолт "удалять backup" опасен — если сам uninstall что-то поломает И удалит backup в том же прогоне, юзеру некуда откатиться): mutate (tmp+rename) → **re-read + verify** (валидный JSON, наши записи ушли, чужие — на месте, через инъектируемый `verifyFn` для тестируемости) → удалить namespaced backup **только после успешной верификации**; при провале — backup остаётся, путь печатается, exit ≠ 0. **Дефолт перевёрнут**: backup остаётся по умолчанию, удаление — opt-in (`--purge-backup`).
- **Симметрия с legacy**: если ставили через 2.5-ветку — uninstall обязан хирургически убрать `notify`-запись и из `config.toml` тоже.
- Повторный uninstall (backup уже отсутствует) — чистый no-op, не ошибка.

## 2.8 — КРИТИЧНЫЙ АРХИТЕКТУРНЫЙ ФИКС: реальный путь к бинарю не существует (найдено сквозным ревью, final-critic-completeness)

**Находка (проверено на реальной машине)**: путь `~/.claude/claude-notifications-go/bin/claude-notifications`, на который ссылался весь раздел 2.3 п.1 как на "существующий симлинк" — **физически не существует**. Реальный бинарь живёт в `$PLUGIN_ROOT/bin/` = управляемый Claude Code marketplace-кэш (версионированный путь, например `~/.claude/plugins/marketplaces/claude-notifications-go/bin/`). Если реализовать 2.3 буквально — hooks.json указывал бы в никуда, хуки Codex **молча никогда бы не сработали ни у одного юзера**.

**Дополнительно найдено**: (а) у Codex direct-exec пути нет автообновления — Claude-хуки идут через `bin/hook-wrapper.sh` (lazy-download + version-check + `install.sh --force` при рассинхроне), Codex по требованию trust-by-hash указывает прямо на бинарь без шелла — при апдейте плагина Codex-путь замерзает на старой версии навсегда; (б) `CLAUDE_PLUGIN_ROOT` не выставлен на direct-exec пути (wrapper обычно его экспортирует), а `terminal_darwin.go` читает его БЕЗ fallback и падает с ошибкой — macOS-резолв ClaudeNotifier.app/звуков сломан на Codex-пути; (в) чистый Codex-юзер без Claude Code вообще не может получить бинарь — единственные пути доставки (`hook-wrapper.sh`, skill `/claude-notifications-go:init`) требуют Claude Code.

**Корень конфликта**: trust-by-hash требует стабильный (неверсионированный) путь; модель доставки плагина — версионированный кэш + wrapper-driven апдейт. Это не примиряется автоматически, нужно явное архитектурное решение.

### Решение — переиспользовать уже существующую, но недоиспользуемую инфраструктуру

**Факт, подтверждённый чтением `bin/install.sh`**: скрипт **уже полностью location-agnostic** через `INSTALL_TARGET_DIR` env var (`bin/install.sh:17-21`) — весь код скачивания/checksum/symlink/detect-platform не привязан к тому, что цель — plugin-кэш. Единственный СЕЙЧАС существующий вызов — `hook-wrapper.sh:run_install()`, который всегда ставит `INSTALL_TARGET_DIR="$SCRIPT_DIR"` (директория самого wrapper'а, то есть кэш). Значит **стабильная, вне-кэша копия бинаря не требует нового кода загрузки** — только новая точка вызова.

**Также найдено**: `hook-wrapper.sh` УЖЕ пишет "stable pointer" файл — `~/.claude/claude-notifications-go/plugin-root` — с текущим `CLAUDE_PLUGIN_ROOT` при каждом запуске (комментарий в коде: "best-effort fallback for older cached paths and for shim wrappers" — авторы уже предвидели именно этот класс проблемы). **Но `getPluginRoot()` в `main.go` этот файл не читает вообще** (grep = 0 совпадений) — write-часть есть, read-части нет. Наполовину реализованный, готовый к достройке механизм.

**Итоговое решение (2 части)**:

1. **Стабильная копия бинаря**: `codex-install` при установке вызывает `install.sh` с `INSTALL_TARGET_DIR=~/.claude/claude-notifications-go/bin` (новый вызов, существующий скрипт, ноль нового кода загрузки) — создаёт независимую от plugin-кэша копию. **Именно этот путь** (не воображаемый из старой версии 2.3) идёт в hooks.json/frozen argv-контракт.
   - **Автообновление**: `codex-install`-копия должна обновляться, когда обновляется основной плагин. Т.к. `handle-codex-hook` — `"async": true` (не блокирует Codex turn), он может позволить себе дешёвую version-check (тот же паттерн, что `hook-wrapper.sh`'s `VERSION_CACHE` — сравнение закэшированной версии с версией plugin.json) и, при рассинхроне, детаченно (не блокируя возврат) триггернуть `install.sh --force INSTALL_TARGET_DIR=~/.claude/claude-notifications-go/bin` в фоне. Не на каждый вызов — по тому же кэш-паттерну, что уже есть у wrapper'а.
   - **Заодно решает пробел "Codex-only юзер без Claude Code не может получить бинарь"** (находка #2 сквозного ревью): standalone-bootstrapping становится возможным — `INSTALL_TARGET_DIR=~/.claude/claude-notifications-go/bin bash install.sh` работает БЕЗ Claude Code вообще (install.sh уже подтверждённо не имеет Claude-специфичных зависимостей), после чего юзер запускает `codex-install` из этой стабильной копии.

2. **`CLAUDE_PLUGIN_ROOT`-резолв — достроить существующий read-fallback**: `getPluginRoot()` (`main.go:411-427`) получает НОВЫЙ fallback #2 (между env var и executable-relative) — читать `~/.claude/claude-notifications-go/plugin-root` (тот файл, что `hook-wrapper.sh` уже пишет). Малое, безопасное изменение, которое заодно чинит устойчивость резолва ресурсов для ЛЮБОГО не-wrapper-based сценария запуска, не только Codex — соответствует изначальному замыслу авторов файла ("for shim wrappers"). `codex-install` также должен писать/обновлять этот pointer-файл при установке (используя `CLAUDE_PLUGIN_ROOT` своего процесса на момент установки, если доступен, иначе — оставить как есть, полагаясь на Claude-путь, который уже пишет его при каждом хуке).

**Ordering-требование**: этот раздел (2.8) — предпосылка для 2.3 п.1, который должен ссылаться СЮДА за реальным путём, а не на несуществующий симлинк.

## Итоговые решения фазы 2 (для Фазы 3)

- Детект: `codex --version` + интерактивный вопрос как первичный путь (НЕ CLI-проба — её не существует); возможный `[features]`-флаг в config.toml; Windows-поддержка hooks — неподтверждённый блокирующий research-item.
- Frozen argv-форма с owner-маркером с первого релиза — единая для 2.3/2.4.
- Content-hash guard (не mtime+size), видимый retry, конкретные Windows error-коды.
- Canonical-reset owner-marked записей всегда (не "не трогать").
- `handle-codex-notify` — отдельный под-план, PermissionRequest НЕ доставим на legacy, обязательно к документированию.
- TOML — хирургическая вставка, не reencode; footgun с секциями учтён.
- Uninstall: verify-then-delete-backup, backup по умолчанию сохраняется, симметрия с legacy.

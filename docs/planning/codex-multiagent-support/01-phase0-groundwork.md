# Фаза 0: архитектурные решения для Codex notification support в claude-notifications-go

> Superseded configuration policy: this historical plan predates shared E → existing L → N selection and revision-checked edits. See [current configuration and compatibility](../../../README.md#manual-configuration); historical path/copy guidance below is not an active recipe.

> [!WARNING]
> **ИСТОРИЧЕСКИЙ ДОКУМЕНТ. НЕ РЕАЛИЗОВЫВАТЬ БУКВАЛЬНО.** Нормативный контракт находится в
> `00-overview-and-decisions.md`. В частности, устарели решения про manual installer, config,
> trust hash, hand-rolled decoder, CLI и Go 1.21. При любом конфликте приоритет у `00`.

## Контекст (что уже есть, факты, не предположения)

- Репозиторий: `/Users/belief/dev/projects/claude/notification_plugin_go`, текущая версия v1.41.0, релиз только что вышел.
- Существующая структура: `cmd/claude-notifications/main.go` — единый бинарь-диспетчер (`handle-hook <HookName>`, `focus-window(s)`, `play-sound`, `daemon`, `windows-hooks`, `version`, `help`). `internal/hooks.Handler` — парсит Claude hook JSON (`HookData{TranscriptPath, SessionID, CWD, ToolName, HookEventName, TeamName, TeammateName}`), делает dedup/lock, зовёт `analyzer.AnalyzeTranscript` → `Status`, зовёт `summary.GenerateFromMessages` → текст, зовёт `notifier.SendDesktop`/`webhook.Send`.
- `internal/analyzer.Status` — enum: `task_complete, review_complete, question, plan_ready, session_limit_reached, api_error, api_error_overloaded, unknown`. `AnalyzeTranscriptWithMessages` читает **файл** JSONL-транскрипта целиком (Claude-специфичный формат, `pkg/jsonl`).
- `internal/notifier.SendDesktop(status analyzer.Status, message, sessionID, cwd string) error` — платформенно-нейтральный, уже не завязан на Claude ничем, кроме типа `analyzer.Status`. Внутри — вся сложность (macOS AX-фокус, Linux daemon+dbus, Windows toast, мультиплексоры).
- `internal/config.Config` — JSON-конфиг в `~/.claude/claude-notifications-go/config.json` (путь фиксирован через `CLAUDE_PLUGIN_ROOT`/executable-relative fallback, независимо от того, стоит ли реально Claude Code). Паттерн tri-state настроек: `*bool` (nil = default).
- `internal/dedup`, `internal/state` — дедупликация уведомлений, персистентное состояние по session ID.
- **Codex wire-факты (изначально из исходников codex-rs, ДОПОЛНЕНО и частично ИСПРАВЛЕНО реальным ресёрчем в фазе 3 — см. АДДЕНДУМ ниже)**:
  - Новая hooks-система: 12 событий, транспорт — **JSON на stdin**, общие поля `session_id, transcript_path, cwd, hook_event_name, model, permission_mode`; `Stop` добавляет `turn_id, stop_hook_active, last_assistant_message`; `PermissionRequest` добавляет `turn_id, tool_name, tool_input`.
  - Trust-модель: несистемные хуки требуют одноразового ручного `/hooks` review+trust, **привязанного к хэшу command-строки** — путь бинаря в hooks.json должен быть стабильным (неверсионированным), иначе re-trust на каждый релиз.
  - `PermissionRequest` не фаерится при `bypassPermissions`/`dontAsk`/`--ask-for-approval never` — **НЕ подтверждено** ни кодом, ни доками (research-item фазы 3, остался открытым).
  - Legacy `notify` (config.toml, argv-JSON, только `agent-turn-complete`) — fallback для старых Codex, помечен legacy в апстриме.
  - Async-хуки (`"async": true`) — Codex не блокируется, до 8 параллельно, дефолтный таймаут 600с.
- **plugin-kit-ai SDK**: только что добавлена типизированная поддержка `codex.OnStop`/`codex.OnPermissionRequest` (ветка `feat/sdk-codex-lifecycle-hooks`, **не закоммичено, не запушено, не смержено, не тегировано**). Дизайн: `StopEvent`/`PermissionRequestEvent` (алиасы на internal DTO), `StopResponse`/`PermissionRequestResponse` (пустые, observation-only), `wrapStop`/`wrapPermissionRequest`. 5 независимых ревью пройдено, тесты зелёные.
- **Лучшие сторонние паттерны установки** (см. память): `omarchy-codex-notifications/scripts/hook-manager` — owner-marker в command-строке для идемпотентного merge/remove, `flock`, backup, отказ трогать malformed-файл. `Exynos-8890/codex_notify` — идемпотентный TOML-merge для legacy notify с сохранением чужих ключей.

## Решение 0.1 — зависимость от plugin-kit-ai SDK: НЕ сейчас, hand-roll + план миграции

**Обоснование (усилено критиком фазы 0, p0-critic-arch)**: SDK-модуль требует `go 1.22`, наш CI явно тестирует `go 1.21` (`ci-ubuntu.yml: go: ['1.21','1.26']`) — зависимость молча уронила бы эту ветку CI. Плюс публичный API SDK — это whole dispatch-фреймворк (владеет stdin/argv/handler registry через `runtime.Engine`), а не декодер — у нас уже свой диспетчер (`hooks.Handler`). Зависеть от SDK означало бы запустить второй runtime рядом со своим ради структуры из ~18 плоских полей.
`go mod replace`→локальный путь непригоден (CI/release-раннер `release.yml` собирает бинари на GitHub Actions, не видит локальный воркитри владельца). `@sha`-псевдоверсия технически возможна, но код на ветке SDK ещё не запушен в origin — момент, plus причины выше всё равно остаются даже после пуша.

**План миграции (зафиксировать как TODO, не блокирует релиз)**: когда `plugin-kit-ai` смержит и тегирует `sdk/vX.Y.Z` с этими событиями — заменить `internal/codexhook` на тонкую обёртку вокруг `github.com/777genius/plugin-kit-ai/sdk/codex`. Понижено из TODO до "опция, требующая отдельного решения об отказе от go1.21".

## Решение 0.2 — доменная модель: переиспользовать `analyzer.Status`, новая функция классификации по строке

**Проблема**: `analyzer.AnalyzeTranscriptWithMessages` читает файл целиком в Claude-специфичном JSONL-формате. У Codex `Stop` даёт только `last_assistant_message` — одну строку, не транскрипт.

**Решение (см. полную ревизию в Фазе 1, разделы 1.3/1.4 — критик фазы 0 поймал, что моя изначальная посылка "извлечь regex из generateQuestionBody" была фактически неверна, классификация у Claude tool-based, не regex)**: `analyzer.ClassifyMessage(text string) Status` — новая функция с нуля. Задействуем подмножество `Status`: `task_complete`, `question`, `plan_ready`, плюс НОВОЕ значение `permission_request`. НЕ задействуем `review_complete` (Claude-специфичная фаза).

## Решение 0.3 — точка входа: новая подкоманда на существующем бинаре

`claude-notifications handle-codex-hook <EventName>` — читает JSON из stdin. **Имена событий БЕЗ префикса "Codex"** (`Stop`/`PermissionRequest`, симметрично `handle-hook Stop`) — критик фазы 0 (p0-critic-arch) поймал: префикс нужен был в SDK из-за общего плоского неймспейса резолвера с Claude; у нас платформа уже разделена именем подкоманды (`handle-codex-hook` vs `handle-hook`). Финальная argv-форма (заморожена решением 0.5/2.3-2.4 ревизии из-за trust-by-hash): `<путь> handle-codex-hook Stop --owner=claude-notifications-go`.

Один бинарь — технических причин для отдельного бинаря нет (весь платформенный код — CGO-audio, dbus, toast, winfocus — задублировался бы зря).

## Решение 0.4 — конфиг: тот же файл, новая секция

Оставить `~/.claude/claude-notifications-go/config.json` как единственный конфиг, даже для чистых Codex-юзеров без Claude Code — путь не зависит от `CLAUDE_PLUGIN_ROOT`, работает без Claude Code (подтверждено критиком: `GetStableConfigDir()` использует `os.UserHomeDir()`). Альтернатива "проверять новый нейтральный путь первым, оставив оба живыми" — ОТКЛОНЕНА критиком как худшая (split-brain: два живых конфига, ровно "state drift across layers" из guardrails). Zero-risk улучшение: вынести `configBaseDir()` в одну точку решения, `os.MkdirAll(dir, 0700)` при первой записи для чистого Codex-only юзера.

## Решение 0.5 — установка: hooks.json merge, детально с edge cases

**Исторический подход**: новая подкоманда `codex-install` (и `codex-uninstall`) на существующем бинаре. Детали сохранены в `03-phase2-installer.md`, но это решение отменено нормативным `00` в пользу native plugin/marketplace discovery.

Исходные принципы (актуальны, детали — в Фазе 2):
1. Путь бинаря в hooks.json — стабильный, неверсионированный (trust по хэшу command-строки).
2. Owner-marker для идемпотентности (паттерн omarchy).
3. Атомарность и защита от гонок (flock + atomic rename).
4. Backup перед первым изменением.
5. Malformed hooks.json — никогда не перезаписывать.
6. Определение поддержки hooks.json vs legacy notify — ИЗНАЧАЛЬНО открытый research-item, **закрыт в Фазе 2/3: CLI-проба невозможна, у hooks нет command-line поверхности вообще**.
7. Взаимоисключение hooks vs legacy — ставим только один активный путь.
8. `/hooks` trust — одноразовый ручной шаг, не автоматизируется.
9. `codex-uninstall` — убирает только owner-marked записи.
10. Обновление — путь стабилен, переустановка hooks.json при апдейте бинаря не нужна.

## АДДЕНДУМ после ресёрча в фазе 3 (p3-critic-research, с источниками)

Пункт 0.5.6 (детект hooks vs legacy) закрыт исследованием: **`codex hooks --help` НЕ существует, у hooks нет CLI-поверхности вообще** — конфигурация только файлами, trust только TUI `/hooks`. Дополнительно: ранние версии Codex поставляли hooks как experimental/off-by-default, требуя `[features] codex_hooks = true` в `config.toml` (deprecated-алиас `hooks` в новых версиях); один источник утверждал "недоступно на Windows" на момент документирования. **Windows-путь для hooks — не подтверждён, блокирующий research-item перед написанием Windows-специфичного кода.** Полная актуальная стратегия детекта — в Фазе 2, раздел "АДДЕНДУМ после ресёрча в фазе 3".

Также закрыто исследованием: `session_id` — hex-UUID (UUIDv7), НЕ ULID (снимает мою эскалацию "FNV-фолбэк обязателен" из Фазы 1 §1.7 обратно до "защитный, не обязательный"). `stop_hook_active`-семантика подтверждена верной как предполагалось. `last_assistant_message` — plain nullable string на stdin, как и планировалось. Legacy notify payload — kebab-case, ARGV (не stdin), поля подтверждены точно. Детали и источники — в Фазе 3, раздел "RESEARCH FINDINGS".

## Итоговые решения, зафиксированные для последующих фаз

1. Hand-roll декодер (`internal/codexhook`), не зависеть от SDK сейчас.
2. `analyzer.ClassifyMessage(text)` — новая функция, детали в Фазе 1.
3. Единый бинарь, `handle-codex-hook Stop/PermissionRequest --owner=claude-notifications-go` — заморожено.
4. Общий `~/.claude/claude-notifications-go/config.json` + новая секция.
5. Установка через `codex-install`/`codex-uninstall` — полная спецификация в Фазе 2 (включая аддендум фазы 3).

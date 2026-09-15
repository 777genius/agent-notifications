# Фаза 1: декодирование Codex-событий, классификация, плюмбинг в существующий pipeline

> Superseded configuration policy: this historical plan predates shared E → existing L → N selection and revision-checked edits. See [current configuration and compatibility](../../../README.md#manual-configuration); historical path/copy guidance below is not an active recipe.

> [!WARNING]
> **ИСТОРИЧЕСКИЙ ДОКУМЕНТ. НЕ РЕАЛИЗОВЫВАТЬ БУКВАЛЬНО.** Нормативный контракт находится в
> `00-overview-and-decisions.md`. Устарели hand-rolled decoder, отдельный CLI и unlimited stdin;
> при любом конфликте приоритет у `00`.

Сохраняет ход исследования фазы 1 и замечания её критиков.

## 1.1 — Новый пакет `internal/codexhook`: DTO + decode

Файл `internal/codexhook/hooks.go`. Hand-rolled (решение 0.1), поля 1:1 с проверенным wire-форматом (codex-rs `schema.rs`, `StopCommandInput`/`PermissionRequestCommandInput`; подтверждено независимо через веб-ресёрч в фазе 3, включая точные примеры `session_id` формата UUIDv7):

```go
package codexhook

type StopInput struct {
    SessionID            string `json:"session_id"`
    TurnID                string `json:"turn_id"`
    CWD                  string `json:"cwd"`
    TranscriptPath       string `json:"transcript_path"` // nullable на wire — decode в string, null→""
    Model                string `json:"model"`
    PermissionMode       string `json:"permission_mode"`
    HookEventName        string `json:"hook_event_name"` // "Stop" или "SubagentStop" — оба несут те же поля
    StopHookActive       bool   `json:"stop_hook_active"`
    LastAssistantMessage string `json:"last_assistant_message"` // nullable, null→""
}

type PermissionRequestInput struct {
    SessionID      string          `json:"session_id"`
    TurnID         string          `json:"turn_id"`
    CWD            string          `json:"cwd"`
    TranscriptPath string          `json:"transcript_path"`
    Model          string          `json:"model"`
    PermissionMode string          `json:"permission_mode"`
    HookEventName  string          `json:"hook_event_name"`
    ToolName       string          `json:"tool_name"`
    ToolInput      json.RawMessage `json:"tool_input"`
}

func DecodeStop(r io.Reader) (*StopInput, error)
func DecodePermissionRequest(r io.Reader) (*PermissionRequestInput, error)
```

**ВАЖНО — два разных wire-формата (найдено критиком фазы 3, p3-critic-research)**: то, что описано выше — это формат **hooks-пути** (stdin, snake_case). **Legacy notify-путь** (config.toml, argv, kebab-case: `type, thread-id, turn-id, cwd, client, input-messages, last-assistant-message`) — это ОТДЕЛЬНЫЙ декодер, `handle-codex-notify` (специфицирован в Фазе 2, §2.5) — НЕ переиспользует эти DTO/функции. `internal/codexhook` в этом разделе — только hooks-путь.

**Edge cases**:
- Пустой stdin → явная ошибка "empty payload", не паника на `json.Unmarshal(nil, ...)`.
- **Лимит payload УБРАН** (ревизия после критика фазы 1): единственный `io.LimitReader` в репо — для HTTP-ответа недоверенного вебхук-сервера (`webhook.go:282`), не для локального stdin. Существующий Claude-путь читает stdin plain-декодером без лимита (`hooks.go:153`). Codex спавнит наш бинарь локально — не security boundary. Длинные сообщения — забота `truncateText` при генерации текста, не decode-reject.
- Malformed JSON → ошибка наружу, `handle-codex-hook` **тихо завершается exit 0 + пустой stdout** — см. 1.2.
- `hook_event_name` отсутствует/не совпадает с ожидаемым argv — не блокировать, берём argv как источник истины.
- **Non-ASCII/UTF-8/эмодзи в `last_assistant_message`** (найдено критиком фазы 3 со ссылкой на реальный баг-репорт openai/codex#23784: "Stop hook receives malformed JSON stdin on Windows with non-ASCII assistant message") — обязательный тест-кейс, не гипотетический; известный риск конкретно на Windows.
- `hook_event_name` дискриминация: `Stop` vs `SubagentStop` (оба несут одинаковый набор полей) — decoder должен маршрутизировать корректно, если оба когда-либо задействованы (v1 — только `Stop`).

## 1.2 — Новая подкоманда `handle-codex-hook <EventName>`

`cmd/claude-notifications/main.go`, **осознанно ОТДЕЛЬНАЯ функция**, НЕ по образцу `handleHook` (критик поймал: существующий `handle-hook` на деле делает `os.Exit(1)` в трёх местах — `InitLogger`, `NewHandler`, `HandleHook`, все в `main.go:203-231` — и `errorhandler.HandleCriticalError`/`HandlePanic` безусловно пишут в stderr; копирование этого паттерна дало бы прямо противоположный нужному контракту):

```go
case "handle-codex-hook":
    handleCodexHook(os.Args[2]) // "Stop" | "PermissionRequest", хвостовые аргументы (--owner=...) игнорируются позиционным чтением
```

**Контракт вывода**: **всегда exit 0, всегда пустой stdout** (не `{}` — у нас нет decision-surface вообще, чистое наблюдение; пустой stdout — самый консервативный вариант). Любая внутренняя ошибка (decode fail, config fail, panic) — **всегда** exit 0 + пустой stdout наружу для Codex; свой `recover()`, логирующий в файл через `logging.Error(...)` (**не** через `errorhandler.HandlePanic`, который пишет в stderr) — на всех трёх аналогичных точках отказа, никогда `os.Exit(1)`.

**Debug-log**: не заводить новую инфру — `internal/logging` уже пишет `notification-debug.log` (`logging.go:33`). Задокументировать юзеру этот путь для диагностики. Stderr в штатном режиме молчит, но не подавляется программно (если сам `InitLogger` не открылся — stderr остаётся единственным каналом).

**Таймаут — 30, не 25** (ревизия: реальный прецедент — `hooks/hooks.json` у ВСЕХ существующих Claude-хуков `"timeout": 30`; 25 — это `maxNotifyDelaySeconds`, внутренний клэмп delay-фичи, обязан быть НИЖЕ таймаута хука с запасом). `"async": true`.

## 1.3 — Классификация: `internal/analyzer.ClassifyMessage`

**Новая функция с нуля** — существующая Claude-классификация tool-based, не regex, переиспользовать напрямую нечего:

```go
func ClassifyMessage(text string) Status
```

**Финальный порядок правил** (после ревизии — критик поймал реальный баг: без ветки на ошибку/лимит любой Codex-стоп из-за rate-limit/auth-ошибки классифицировался бы как ложно-позитивный "✅ Task Complete"):
1. `textutil.CleanMarkdown(text) == ""` → `StatusUnknown`.
2. **Совпадение с фразами session-limit/api-error** (переиспользовать наборы фраз из существующих `detectSessionLimitReached`/`detectAPIErrors`, `analyzer.go:177,202`, оба в том же пакете, `containsIgnoreCase` уже есть, `analyzer.go:239`) → `session_limit_reached`/`api_error`.
3. Похоже на вопрос → `StatusQuestion`. **Сигнал — `strings.Contains(text, "?")` (НЕ suffix)** — совпадает с существующим прецедентом `generateQuestionBody` (`summary.go:130`), не расходится с ним произвольно.
4. Похоже на план → `StatusPlanReady` (эвристика с нуля, прецедента в репо нет).
5. Иначе → `StatusTaskComplete`.

Явно зафиксировано: план, заканчивающийся на "?", классифицируется как `question` (правило 3 раньше правила 4) — намеренный приоритет, не баг.

**Edge cases**:
- Сообщение целиком код (тройные бэктики) → `CleanMarkdown` может вернуть пустую строку → правило 1, `unknown`, не краш.
- `last_assistant_message` — **подтверждено фазой-3-ресёрчем**: plain nullable string, сырой текст модели (не Codex-обёртка) — можно обрабатывать как обычный markdown, симметрично Claude.

## 1.4 — Вынос общих текстовых хелперов: `internal/textutil` (новый пакет)

**Обязательно только для `CleanMarkdown`** (ревизия — критик поймал переоценку скоупа: `analyzer.ClassifyMessage` нужен только `CleanMarkdown` для empty-check правила 1 — это единственная реальная причина цикла импорта `summary→analyzer→summary`, поскольку `summary` уже импортирует `analyzer`). `truncateText`/`extractFirstSentence` — хелперы генерации ТЕЛА уведомления, нужны в хук-хендлере; `internal/hooks` уже импортирует `internal/summary` напрямую — цикла нет, эти два МОГУТ остаться в `summary`, перенос — вопрос стиля.

Если переносим все три для единообразия — учесть: (а) `CleanMarkdown` тянет 10 package-level regex (`summary.go:24-38`); **`emojiPattern` остаётся** в `summary` (используется в `GetDefaultMessage`, `summary.go:666`); (б) `truncateText`/`extractFirstSentence` сейчас НЕэкспортированы — перенос требует экспорта (`TruncateText`/`ExtractFirstSentence`) и правки вызовов (10 call-site в `summary.go`) И **тестов** (`summary_test.go:235,326` зовут их напрямую) — это НЕ zero-touch move.

## 1.5 — Config plumbing (чеклист, иначе `SendDesktop` падает молча)

1. `Config.Statuses` дефолты — `"permission_request": {Title: "🔐 Permission Needed", Sound: "question.mp3"}`.
2. `validStatuses` (config.go:566) — добавить `"permission_request"`.
3. `isTimeSensitiveStatus` (`notifier.go:63`, default `false`) — `permission_request → true` (интерактивно, Codex реально ждёт юзера).
4. **Звук — НЕ новый ассет, переиспользовать `question.mp3`** (ревизия: критик нашёл, что `session_limit_reached`/`api_error`/`api_error_overloaded` УЖЕ переиспользуют `error.mp3` — устоявшийся паттерн репо, не компромисс; экономит коммит ассета + запись в descriptions-map).
5. `analyzer.Status` — добавить `StatusPermissionRequest Status = "permission_request"`.
6. **[Найдено критиком, 6-е место, реальный пропуск]** `internal/summary/summary.go:88` (`GenerateFromMessagesStructured`) — switch генерации ТЕЛА уведомления, `default: generateTaskBody()`. Без явной ветки `permission_request` получит НЕВЕРНЫЙ текст ("task complete"-формулировка на permission-запросе). Добавить `case StatusPermissionRequest` с телом на основе `ToolName`/`ToolInput`.

Безопасно-по-дефолту, не обязательно трогать для v1: `webhook/formatters.go` (4 switch, все с default); `state.go:176` (нет default, no-op — и это ПРАВИЛЬНО, permission не должен попадать под question-cooldown); `cmd/list-sounds` (не перечисляет статусы).

## 1.6 — Дедупликация: два раздельных механизма

1. **`stop_hook_active` — простой bool early-exit**, НЕ через `dedup` (подтверждено фазой-3-ресёрчем: семантика "уже уведомляли на этой continuation-итерации" верна как предполагалось):
```go
if input.StopHookActive {
    return
}
```
2. **`dedup.Manager`** — для другого случая ("тот же `Stop` дважды за 2с окно", near-simultaneous). **Уточнение окна**: **2с**, не "2-5с" (5с — отдельный `AcquireContentLock`, `dedup.go:137`, другой механизм). Сигнатура `AcquireLock(sessionID string, hookEvent ...string)` — вариативная, `AcquireLock(codexSessionID, "Stop")` работает без изменений пакета. Двухфазность обязательна: `CheckEarlyDuplicate` → `AcquireLock`, не только один вызов.

## 1.7 — Session-имя и click-to-focus

- **Session-имя**: `internal/sessionname.GenerateSessionName(sessionID string) string` — детерминированный хэш UUID → "zesty"/"bird". **Подтверждено фазой-3-ресёрчем**: `session_id` Codex — hex-UUID (**UUIDv7**, не ULID) — `hexToInt`'s `Sscanf(hex,"%x")` работает как есть, коллизий не ожидается. **Мой изначальный страх "если ULID — обязателен FNV-фолбэк" снят** — session_id подтверждённо hex, FNV-фолбэк остаётся защитным кодом, не обязательным. Кормим `input.SessionID` от Codex напрямую, без новой логики.
- **Click-to-focus**: `notifier.SendDesktop(status, message, sessionID, cwd)` уже платформенно-нейтрален. Механизм (env-переменные терминала, унаследованные хук-процессом) должен работать без изменений — **не проверено эмпирически**, живой тест обязателен (Фаза 3).

## ЭМПИРИЧЕСКОЕ ПОДТВЕРЖДЕНИЕ (проведено автономно, 2026-09-01, реальный Codex CLI v0.152.0)

**Главный вывод final-critic-risk-verdict закрыт**: реально запущен живой Codex в изолированной песочнице (`CODEX_HOME` override, боевой конфиг юзера не тронут), реальный `hooks.json` с тестовым Stop-хуком, `codex exec` headless. **Хук реально сработал**, получен настоящий payload:
```json
{"session_id":"01a05cd8-b495-7f80-a36b-cc0aa98efc05","turn_id":"01a05cd8-b51b-7343-8b75-b2d4ad9e276e","transcript_path":".../rollout-2026-09-01T15-01-41-....jsonl","cwd":"/Users/belief/dev/projects/claude/notification_plugin_go","hook_event_name":"Stop","model":"gpt-5.6-sol","permission_mode":"bypassPermissions","stop_hook_active":false,"last_assistant_message":"OK"}
```
**Совпадает 1:1 с DTO из §1.1** — ни одного расхождения в именах/формате полей. `session_id` — реальный UUIDv7 (`...-7f80-...`), подтверждает §1.7. `hooks`-фича на текущей версии — `stable`, `true` (включена по умолчанию, не experimental/off-by-default, как предполагали старые источники в аддендуме фазы 0/2 — актуальная версия сняла этот риск).

**PermissionRequest**: НЕ фаерится в headless `codex exec` ни с `bypassPermissions`, ни с дефолтным `approval: never` (обнаружено — headless exec-режим ВСЕГДА использует `approval: never`, независимо от `-c approval_policy=` override) — эмпирически подтверждает research-item §3.1 п.6 (частично: для bypass/never режимов подтверждено; для `dontAsk` — не тестировалось). Попытка через интерактивный TUI (`codex` + pty) не привела к результату за отведённое время — полноэкранный ANSI-интерфейс не поддаётся тривиальной pty+timeout автоматизации, что подтверждает вывод фазы 3 (класс D — принципиально ручной тест, не автоматизируется дёшево).

**Побочная находка**: Codex сам логирует срабатывание хука в свой вывод (`hook: Stop` / `hook: Stop Completed`) — полезный debug-сигнал для живого тестирования, не упомянутый ни в одной из 5 фаз.

## Итоговые решения фазы 1 (для последующих фаз)

- `internal/codexhook` — hooks-путь декодер (НЕ покрывает legacy notify — отдельная DTO в Фазе 2 §2.5).
- `handle-codex-hook Stop|PermissionRequest` — всегда exit 0, пустой stdout, лог в `notification-debug.log`, таймаут 30 в hooks.json.
- `analyzer.ClassifyMessage` — 5 правил, error/limit-детект перед task_complete-дефолтом.
- `internal/textutil` — только `CleanMarkdown` обязателен к переносу.
- Config plumbing — 6 обязательных мест (не 5), `question.mp3` переиспользуется.
- Dedup — bool-check (stop_hook_active) + `dedup.Manager` (near-simultaneous), раздельно.
- Session-имя — работает как есть, `session_id` подтверждённо UUIDv7.

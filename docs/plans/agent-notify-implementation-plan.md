# План реализации уведомлений по инициативе агента

Дата: 2026-09-10. Статус: спецификация после повторной критики. Исправления ревью перечислены в §15-24; требования плана не являются отметками о готовности реализации или прохождении E2E.

План подготовлен после двух раундов спайков и независимых аудитов. Исходная точка чтения: `main`, `c39a5a36e65c83a7ba64b1ebc2f2bc43e735dd8b`, репозиторий `777genius/agent-notifications`. PR [#139](https://github.com/777genius/agent-notifications/pull/139) и [#140](https://github.com/777genius/agent-notifications/pull/140) уже merged в main. Перед реализацией обновить сведения о main/PR и построить новый stack от актуального основания. Старые ветки `feat/codex-*` не являются обязательной базой.

## 1. Результат, который должен получить пользователь

Пользователь устанавливает Agent Notifications существующим установщиком, явно включает возможность уведомлений для нужного клиента и настраивает разрешение ОС. Агент может вызвать `notify` в любой момент работы. В поддержанном локальном Codex Desktop клик открывает конкретный исходный чат, даже если MCP уже завершён и временный workspace удалён. Инструмент возвращает результат отправки сразу после подтверждения ОС либо ограниченного ожидания, не ждёт клика.

Существующие Claude/Codex hooks, конфигурация, звуки, статусы, webhooks и terminal focus сохраняются. Новый use case не запускает синтетический Stop, не анализирует transcript и не изменяет hook cooldown/state. Universal Agent Plugins / Agent Plugins 1.0 является вторым способом установки той же функции. Skill содержит правила вызова того же инструмента.

**Первый поддержанный режим:** локальный macOS, настроенный Codex Desktop, обычный видимый локальный чат в текущем профиле приложения. Ограничения профиля и окна явно описаны ниже. Claude Code остаётся полноценным потребителем существующих hooks; возможность самостоятельного вызова в Claude подключается через тот же CLI/MCP use case с честной точностью terminal target. Не выдавать оконную эвристику за доказанный возврат в конкретную сессию.

## 2. Источники и границы доказательства

Обязательный контекст для исполнителей и reviewers:

- [Полное исследование](agent-notifications-research.md).
- [Архитектурные риски](agent-notify-risk-review.md).
- [Первый раунд спайков](agent-notify-spike-results.md).
- [Второй раунд спайков](agent-notify-round2-results.md).
- [Каталог evidence](/Users/belief/.codex/visualizations/2026/09/08/01a07f59-fd55-7f82-a9f9-561474574fa5/agent-notify-spikes-20260910), в том числе `README.md`, `sha256.json`, `hosted-evidence/report.md`, `round2/README.md`, `round2/hosted-evidence/report.md`, actual receipts и `user-observations.json`.

В исследованиях сохранены исторические отрицательные и ожидающие статусы. Актуальное состояние определяется вторым раундом и подтверждениями пользователя, а не фразой из ранней части отчёта. Старые SHA `7e6c29c`/`324a066` не подменяют текущий source baseline. Перед переносом characterization fixtures сверять конкретные изученные функции, а не только название файла.

| Evidence | Что доказано | Чего это не доказывает |
| --- | --- | --- |
| Actual app-server CLI 0.152.0 и bundled Desktop 0.153.4, A/B/A в одной новой sandbox | На каждом запросе есть правильный `_meta.threadId`; разные чаты дали разные MCP PID; env `CODEX_THREAD_ID` отсутствовал; spoof ID в прямом API заменён сервером | Один процесс на всё приложение, provenance других клиентов, remote auth, Desktop UI/tool selection |
| Actual app-server + локальная mock Responses SSE fixture | Модельный путь передал `threadId`, `callId`, `itemId`; две параллельные turn завершились; одинаковый искусственный callId в разных чатах требует session scope | Живая модель, skill/code-mode, вложенные агенты и их видимые родители |
| Default approval + `never` | Реальный вызов был запрещён; test-only `approve` разрешил harmless capture | Возможность обойти approval через skill или маркировку notify как read-only |
| Native обычный `open URL` | Callback и route logs были, exit=0, но пользователь не увидел переключения даже в одном окне | Успешную навигацию или установленную причину дефекта |
| Прямая ссылка, затем `open -a` и Swift NSWorkspace | Пользователь подтвердил правильный чат; Swift completion подтвердил выбранное приложение | Что виновата backup-копия; default handler на последней проверке уже был правильным |
| Синтетический MCP A/B, EOF, удаление cwd, ручные клики | Оба чата открылись после смерти сервера; callback лежал вне cwd | Полный установленный Desktop plugin flow: metadata задавалась fixture |
| Actual Claude Code2.1.265 `--bare`, two sandbox sessions + local mock Anthropic SSE | Both MCP calls completed; only `claudecode/toolUseId`/`progressToken`, no session ID or session env | Hook-enabled context, session-scoped CLI/skill, live-model selection or exact-session navigation |
| Actual Swift codec, 8 fixtures | Legacy actions roundtrip; неизвестные/malformed actions не декодируются; лишний `schemaVersion=999` старый decoder игнорирует | Готовую совместимость нового action со старым notifier |
| Actual установленный notifier, 3 случая | Литеральное `--help` в title/body/subtitle вызывает Usage и exit=0 без отправки | Что quoting устраняет дефект: он находится в разборе argv |
| 4 actual Go characterization tests | Префикс `[important]` теряется при прямом reuse старого parser; installer фильтрует новые entries; старые locks теряют ownership после состаривания mtime | Полный product suite или native race reproduction |
| Два hosted source audits | Найдены lifecycle, update gap, globals, timeout, provenance и platform hazards; fingerprints привязаны к source | 21 реальный Go-тест: первый Python harness включает модели/analysis-only; второй тоже не native execution |

Повторять уже доказанные тёплые ручные A/B клики без изменения соответствующего кода не нужно. Эти доказательства использовать как вход для implementation tests. Полный installed E2E, cold start и upgrade/rollback ещё требуют проверки.

## 3. Архитектурные решения

### 3.1. ✅ Отдельный use case, один существующий executable

Рекомендованный вариант: `internal/agentnotify` + `internal/notification` для общего delivery contract; подкоманды `notify` и `mcp-server` в существующем `cmd/claude-notifications`. Имя executable/старые command paths не менять в этой фиче. Внешнее название продукта уже Agent Notifications.

| Вариант | Оценка | Примерный объём соответствующей интеграции |
| --- | --- | --- |
| **Один executable, отдельный use case и thin adapters** | 🎯 9/10 · 🛡️ 9/10 · 🧠 4/10 | 1 200-1 900 changed LOC для use case/CLI/MCP без native/install; выбран |
| Отдельный executable/package, общий Go module | 🎯 7/10 · 🛡️ 8/10 · 🧠 6/10 | 1 700-2 600 LOC; добавляет install/audio/callback compatibility |
| Общий daemon/HTTP service для всех агентов | 🎯 4/10 · 🛡️ 6/10 · 🧠 9/10 | 3 000-5 000 LOC до платформенных интеграций; transport/auth/discovery/lifecycle пока не окупаются |

Это оценки альтернативных частей, не суммы всего плана. MCP stdio процессы создаёт клиент: один executable не означает один OS instance. Существующий `play-sound` обязан оставаться доступным: detached audio запускает `os.Executable()` с этой подкомандой.

### 3.2. 🔒 Инварианты

1. Origin запроса и NavigationTarget являются разными типами. CWD, PID, env сервера, текущий активный чат и MCP connection ID не заменяют session ID.
2. Модельная MCP schema задаёт только текст и намерение уведомить, без shell/URL/app path/chat ID/host/profile/window ID. CLI является отдельной локальной точкой управления: caller_asserted context не выдаётся за доверенную metadata клиента.
3. Старые сериализованные `execute` actions остаются совместимыми legacy данными. Через новую модельную схему создать такой action нельзя.
4. Callback живёт независимо от MCP, временных payload files, workspace и plugin cache. Новые Codex actions выполняются самим native notifier.
5. `submitted` означает подтверждённое принятие ОС. Это не показ баннера, прочтение или проверка видимого чата. `unknown` не разрешает автоматическую повторную отправку.
6. Explicit notifications имеют отдельные enable/rate/idempotency policies. Глобальные desktop/sound/clickToFocus opt-outs соблюдаются; Stop state и cooldown не применяются.
7. Произвольный агентский текст локален по умолчанию. Hook webhook settings сами по себе не включают экспорт этого текста.
8. Все side effects происходят после validation/policy/capability/idempotency admission. Это относится и к звуку/bell.
9. В stdio MCP stdout содержит только протокол. Ошибки/логи не включают body, полный metadata, transcript или cancellation reason без очистки.
10. Runtime/registration changes принадлежат установщику; `tools/call` не устанавливает, не обновляет и не запрашивает интерактивные права ОС.
11. Новые обязательства поддерживаются тестами на заявленной платформе. Неподтверждённая возможность получает `unavailable`, а не эвристический target.

### 3.3. Границы модулей

```text
MCP adapter ─┐
             ├─> agentnotify.Service ─> notification delivery port ─> notifier/native
CLI adapter ─┘        │                          ▲
                      ├─ policy snapshot          │
                      └─ idempotency journal      │
                                                  │
hooks: source -> analyzer/state/cooldown -> legacy presentation adapter

skill: инструкции по использованию MCP/CLI, отдельной логики доставки нет
install: runtime + native capabilities + managed client registration
```

Предполагаемый ownership; точные filenames можно уточнить внутри этих границ:

| Область | Ответственность |
| --- | --- |
| `internal/notification` | Небольшие agent-independent DTO/receipt/capability и интерфейс `Deliver(ctx, request)`. Не импортирует `hooks`, `analyzer`, MCP SDK или OS APIs |
| `internal/agentnotify` | Validation, политики, origin/target requirements, idempotency orchestration. Не читает transcript и не запускает shell |
| `internal/agentnotify/journal` | Ограниченное durable хранилище и межпроцессная атомарность, clock/fs seams |
| `internal/agentnotify/mcp` | SDK transport, tools schema, `_meta` extraction, protocol errors и lifecycle. SDK DTO не выходят за adapter |
| `internal/agentnotify/origin` | Небольшие Codex/Claude integration adapters и mapping capabilities. Не создавать registry/plugins framework до появления реального второго mapping |
| `internal/notifier` | Реализация структурированной доставки; legacy `SendDesktop` остаётся compatibility wrapper; audio/platform code переиспользуется |
| `swift-notifier/...` | Versioned payload/receipt/capabilities, typed desktop action, explicit app discovery/open, async callback completion |
| Небольшой `internal/installruntime`, если нужен двум install consumers | Общий план установки runtime/native assets и ownership. Клиентские JSON/TOML edits остаются в своих adapters; весь installer не переписывать |
| Существующий CLI + installer + package manifests/skill | Composition, стабильные команды запуска и пользовательское подключение |

Не переносить `Handler` целиком. Сначала выделить из старого `SendDesktop` структурированные поля и низкоуровневую отправку, оставить bracket parsing и status presentation в legacy wrapper. Старые hooks могут сохранять прежний best-effort результат; новый API не наследует его ложный `error == nil` успех или безусловный fallback. Контракт backend должен позволять отличить известный отказ до submit от неопределённого эффекта.

## 4. Контракты запросов и результатов

Названия ниже являются конкретным стартовым API, а не уже существующими Go-типами. До первого consumer зафиксировать JSON schema и таблицу reason codes в repository fixtures.

### 4.1. Модельный tool

Один side-effectful tool `notify` и один read-only `notification_status` для проверки настройки/возможностей без отправки. У второго нет доступа к тексту предыдущих уведомлений. `notification_status` не создаёт state, не запускает migration/repair/GC и не удаляет spool: отсутствующие или повреждённые файлы отражаются в результате. Очистка принадлежит notify/setup; read-only annotation должна соответствовать реальным файловым эффектам. Не добавлять update/cancel/banner-progress operations в первую версию. Для status без параметров принимать как отсутствующий `arguments`, так и пустой объект `{}`; явно переданные null, не-объект и неизвестные поля отклонять. Gate: raw JSON-RPC fixtures, поскольку SDK client может сам добавлять пустой объект.

```json
{
  "title": "Нужно выбрать вариант",
  "body": "Подготовлены два варианта схемы. Посмотрите текущий чат.",
  "category": "attention",
  "request_id": "schema-choice-1",
  "navigation": "required"
}
```

- `title`, `body` являются literal strings; `category`: `info|attention|progress`. Обычный progress не получает time-sensitive приоритет. Category описывает презентацию, не статус завершения turn.
- `navigation`: `required|best_effort|none`, default `required`, чтобы основное обещание клика не терялось молча. При best_effort допустима информационная отправка с явно ограниченной capability. При `none` callback отсутствует.
- `request_id` необязателен. Приоритет: явный request_id, иначе квалифицированный request-scoped client callId, иначе generated ID. JSON-RPC `id`, `progressToken`, connection ID и произвольное похожее поле `_meta` не являются durable callId: transport ID может повторяться после reconnect. Adapter использует только подтверждённое поле конкретного клиента; при его отсутствии выбирает generated, без эвристики. Gate: повтор JSON-RPC id после reconnect не создаёт ложный replay; одинаковый подтверждённый callId в разных сессиях не сталкивается. Различать key kinds `explicit|client_call|generated`. Skill сохраняет явный ID при намеренном повторе. Fallback ID возвращается как tracking_id, а request_id в receipt равен null, если caller его не задавал. Tracking ID не является разрешением повторить effect и не подставляется skill в request_id. Для намеренного повтора между новыми tool calls нужен исходный explicit request_id; потерянный ответ без него не делает следующий вызов тем же запросом.
- `thread_id`, `url`, `command`, `app_path`, `profile`, `host` и неизвестные поля в tool arguments отклоняются. CLI context передаётся отдельно, но отдельный файл сам по себе не является доказательством доверенного происхождения (см. §4.2).
- Нет флага, позволяющего модели переопределить выключенные уведомления, ускорить priority, обойти rate limit или включить webhooks.

### 4.2. Внутренний контекст

`Origin` содержит integration/provider, opaque session ID, только доступный request-scoped call/turn ID, provenance и locality. Видимый parent используется исключительно при доказанном mapping. Не переносить потерявшие поля hook DTO в MCP boundary.

`NavigationTarget` имеет варианты `none`, `desktop_thread`, `terminal_target`. Внутри desktop target: route kind `codex_thread`, validated thread ID, ссылка на выбранную trusted app identity и объявленный local routing mode. Ни raw URL, ни shell команд нет. Terminal target содержит integration-provided captured pane/window identifiers с их provenance/freshness, если они существуют. У разных вариантов разные гарантии.

**CLI boundary:** `notify` читает content JSON из stdin, optional `--context-file` содержит bounded envelope с provider/session/locality. Проверять тип/UID/mode/размер, но не присваивать этому файлу provenance `client_metadata`: его мог создать сам caller. Ручной CLI context имеет `caller_asserted` и допустим только в явно включённом local routing scope; это не client-attested current-chat context. Доверенную metadata назначает adapter, а не поле JSON. Без session ID required navigation отклоняется; для `navigation=none` допустим отдельный caller namespace без выдуманного чата. Skill не фабрикует context и не ищет ID по cwd; конкретный session-scoped источник для Claude/CLI доказывается в Preflight 0.

`PolicySnapshot` неизменяем на запрос. Config load для новой функции не делает автоматическую миграцию в процессе tool call. Повреждённые/нечитаемые настройки новой функции дают `configuration_invalid`, без fallback к enabled defaults. Физический источник explicit policy: `os.UserConfigDir()/agent-notifications/agent-notifications.json`, schemaVersion=1; для initial macOS это `~/Library/Application Support/agent-notifications/agent-notifications.json`. Это отдельный owned файл вне plugin cache, который hook migration не копирует. Missing означает feature disabled; malformed/unsupported schema означает configuration_invalid, без fallback. Политика и выбранный route не дублируются в legacy config.

Глобальные desktop/sound/clickToFocus opt-outs читаются строго из существующего stable `~/.claude/claude-notifications-go/config.json`, без вызова fallback/migrating loader из notify. Missing/malformed global config при включённой explicit feature требует configuration_required/invalid, без silent default enabled; setup подготавливает canonical config отдельно. Для защиты этого источника включить узкое исправление hook migration: она может создать stable файл только при подтверждённом отсутствии, с атомарным no-clobber promotion; не заменяет существующий повреждённый/новый файл legacy копией. Под общим writer lock повторно проверить отсутствие/snapshot; другие canonical writers используют CAS и сохраняют foreign fields. Hook read fallback может сохраняться, но не переписывает повреждённый stable источник. Это осознанное сужение старого best-effort write behavior, а не переписывание hook policy. До activation все managed writers используют этот контракт; старые unmanaged executable copies не считаются защищёнными общей блокировкой. Gate: migration одновременно с opt-in/disable, malformed stable, повтор после repair и mixed managed versions.

**User intent и runtime eligibility различаются:** authoritative explicit policy хранит opt-in пользователя; ledger enable state означает пригодность текущего проверенного runtime. Обычный совместимый repair/update и удаление одного из нескольких consumers сохраняют intent и восстанавливают eligibility после recovery. Отсутствующий параметр изменения policy означает «сохранить», а не disable. Initial install остаётся выключенным; explicit disable, final uninstall и несовместимый rollback отзывают eligibility. Final uninstall также явно отзывает opt-in для новых отправок; сохранённые callback/journal не являются разрешением автоматически включить функцию при reinstall. Повторная установка после final uninstall требует нового explicit enable, в отличие от repair при существующих consumers. Durable dedup history и namespace при этом сохраняются. Gate: enable -> final uninstall -> reinstall без enable даёт ноль новых отправок, ранее отправленный callback продолжает работать; новый explicit enable не сбрасывает journal. Malformed policy не перезаписывается defaults. Gate: enable -> repair -> update -> remove unrelated consumer сохраняет opt-in; disable одновременно с repair побеждает через generation/CAS, без самопроизвольного повторного включения.

### 4.3. Receipt

```json
{
  "request_id": "schema-choice-1",
  "status": "submitted",
  "reason": "os_accepted",
  "replayed": false,
  "backend": "macos_native",
  "navigation": {
    "capability": "available",
    "precision": "chat_id",
    "scope": "local_current_profile",
    "reason": "configured_codex_desktop"
  }
}
```

| Status | Семантика |
| --- | --- |
| `rejected` | Некорректный запрос, конфликт ключа, недоступная обязательная navigation, настройка/permission/store error или подтверждённый отказ до принятия ОС. Reason сохраняет различие |
| `suppressed` | Пользователь выключил функцию/desktop либо сработала её собственная rate policy; уведомление и звук не отправлялись |
| `submitted` | Native adapter получил положительный completion ОС для конкретного notification ID |
| `unknown` | Возможный submit без доказанного результата: lost ack, timeout/отмена в handoff, запись pending после crash. Автоматического retry/fallback нет |

Mapping зафиксирован: неизвестный method/tool и поддержанные ошибки корректно сформированного JSON-RPC envelope дают штатную protocol error. Oversized frame, неразбираемый JSON и неподдержанная wire identity завершают соединение с очищенной диагностикой по transport contract §9; для них protocol reply не гарантируется. Не смешивать эти fatal wire cases с валидным envelope, содержащим ошибочные tool arguments; schema/domain validation и rejected дают tool result с isError=true и структурированным receipt. Submitted/suppressed имеют isError=false. Unknown является обработанным result (isError=false), с явными status=unknown, retry_safe=false и инструкцией не повторять автоматически; это не подтверждение успеха. Проверить mapping на реальном клиенте. Утерянный stdout ответ не даёт права повторять native effect.

Navigation возвращается отдельно как `available|unavailable|disabled` с precision `chat_id|terminal_pane|terminal_window|application|none`. `available/chat_id` говорит о подготовленном action в поддержанном scope, а не об уже состоявшемся клике. Callback diagnostics позднее различают `callback_received`, `open_requested`, `open_failed`, `open_unknown`. `visible_target_confirmed` появляется только в ручном E2E evidence; production не выдумывает такой telemetry.

### 4.4. Валидация и политика

Стартовые продуктовые лимиты: title 256 UTF-8 bytes, body 4 096 bytes, total decoded request 16 KiB, session/request ID не более 256 bytes. Это наши budgets, не лимиты ОС. Не обрезать текст молча. Сохранять точные Unicode scalar sequences без NFC/NFKC normalization или trim. Запретить Unicode Cc, кроме LF/TAB в body; в title дополнительно запретить U+2028/U+2029. Не использовать широкие control character sets, включающие Cf: ZWJ/ZWNJ и emoji tag sequences являются допустимым текстом. NUL и недопустимый UTF-8 отклоняются. Отдельно проверить unpaired surrogate JSON escapes и duplicate object keys; не полагаться на молчаливую нормализацию разных декодеров, способную изменить digest. В title запретить многострочность. `--help`, `-help`, `-execute`, `[important]`, кавычки и `$()` остаются данными.

Первый релиз explicit feature local-only: `agentNotifications.enabled=false` по умолчанию, существующий installer включает явно. `agentNotifications.webhooks` в initial slice не реализуется и отсутствует в tool schema; его будущий opt-in отделён от hook webhooks. Это снимает необходимость чинить persistent webhook sender ради локального MVP, сохраняя hook pipeline.

Начальные защитные budgets: 2 одновременных submit на MCP процесс, без durable очереди; лишний вызов получает `busy` до эффекта. Общая для runtime rate policy: 6 новых уведомлений/минуту на session и 30/минуту на runtime, короткий burst 3. Эти defaults вынесены в config и проверяются атомарно между процессами; replay не потребляет лимит. Настройка может быть изменена после реального UX, без смены контракта. Не применять rate/cooldown hooks. Для sessionless informational применяется общий anonymous bucket данного source/transport, а не выдуманный per-session лимит; status/help явно показывают эту область. Runtime limit остаётся общим для всех buckets.

Бюджет запроса 15 секунд отсчитывается от получения полного ограниченного transport frame (CLI: полного stdin request), до validation/preflight и durable admission, и включает lock/queue/permission status/launch; native send оставляет время на receipt. Native not-after наследует оставшийся исходный budget, а не начинает новые 15 секунд после admission. Чтение незавершённого frame ограничено по памяти; idle stdio без запроса не считается выполняющимся notify. Ожидание lock обязано поддерживать cancellation/deadline, а не использовать неограниченный blocking flock. Callback completion имеет отдельный deadline 10 секунд. Это начальные implementation budgets с DI clock, а не обещание latency ОС. Нет бесконечной очереди, ожидания клика или sleep для гарантии активации.

## 5. Контекст Codex/Claude и честная матрица возможностей

### 5.1. Поддержанный local Desktop режим без выдуманного profile API

Установщик явно настраивает локальный route в выбранное приложение Codex. Это операторский routing policy, а не доказательство, что любой клиент с тем же MCP config является Desktop. Один `_meta.threadId` и startup `--integration=codex-desktop` не отличают Desktop от CLI, если клиенты разделяют конфигурацию.

Поэтому в первом реальном installed spike фиксируется, какой registration scope видит Desktop, CLI и remote session. При подтверждённом interface signal или изолированной регистрации можно ограничить adapter именно Desktop. Если такого сигнала нет, выбран bounded fallback: **явный пользовательский opt-in локального маршрута Codex в текущем профиле применяется ко всем callers этой регистрации с подходящим threadId**. Origin сохраняет `interface=unknown`, а не ложное Desktop provenance. Нельзя одновременно разрешать неизвестного Desktop caller и обещать автоматическое исключение неотличимого CLI/hidden child. Установщик объясняет scope opt-in; такой маршрут квалифицируется на Desktop, а остальные callers получают ту же попытку открыть переданный ID без дополнительной гарантии совместимости их store.

`required` возвращает unavailable по реально наблюдаемым причинам: нет opt-in/ID, отсутствует локальная GUI capability, известен несовместимый host/context, неподдержанное приложение или доказанно скрытый target без mapping. Отсутствие interface/parent сведений само по себе не позволяет распознать CLI или subagent. Если для конкретного consumer нужен строгий запрет таких callers, потребуется отдельный подтверждённый registration/interface contract; это не эмулируется env-флагом или названием MCP server.

Профиль/аккаунт не извлекается из auth-файлов, внутренних баз и cwd. Идентичность app bundle не является идентичностью аккаунта. В initial scope гарантируется сохранение точного target ID и попытка открыть его в выбранном локальном приложении. После смены профиля, sign-out или удаления/архивации чата результат внутри Codex может быть неизвестен; это явно неподдержанный сценарий, а не скрытая общая блокировка local MVP. Если официальный контракт позволяет обнаружить несовместимый scope, отказывать до открытия. Если не позволяет, не заявлять, что такой случай можно автоматически распознать.

Отправка не создаёт чат, не возобновляет сессию CLI и не отправляет prompt. Не выбирать альтернативный/последний/родительский чат после ошибки. Same-window/same-Space return не обещается: обещание ограничено целевым chat ID в поддержанном окне приложения, с ручной проверкой видимости для заявленной конфигурации.

### 5.2. Матрица initial scope

| Источник/среда | Самостоятельный вызов | Navigation |
| --- | --- | --- |
| Настроенный local Codex Desktop, обычный видимый чат | MCP и CLI integration path | `chat_id`, после полного installed E2E; exact original window не обещается |
| Codex app-server API с корректным ID, но неизвестный interface/store | Тот же explicit local routing opt-in распространяется на этот caller | Без opt-in required unavailable; с opt-in попытка открыть переданный ID в выбранной app, без доказательства store compatibility; metadata не является auth |
| Codex CLI local | Тот же use case при явной интеграции | Отдельный terminal target либо общий явно включённый local Desktop route; shared scope не умеет автоматически исключить CLI |
| Claude Code local | Тот же MCP или skill -> CLI adapter | Существующий captured terminal target, с фактической точностью. Session ID через подтверждённый клиентский контекст; глобальный env MCP не использовать как per-call context |
| Доказанно hidden subagent без видимого parent mapping | Best-effort informational при достаточной locality; иначе отказ | Required unavailable. Если hidden status вообще не передан клиентом, распознать его нельзя; shared opt-in открывает exact supplied ID и не угадывает parent |
| Remote worker/headless host | Вне local desktop feature | Unavailable; установка на сервере не доставляет уведомление на Mac |
| Linux/Windows explicit API | CLI/MCP build совместимость сохраняется; включение delivery только для протестированных backends | Не объявлять durable exact-chat до отдельной native qualification |

**Informational-only является отдельным рабочим режимом:** явный `navigation=none` не требует Codex app path/team, session ID или включённого desktop route. Но он не отменяет admission: неизвестные interface/locality допускаются только с отдельным явным согласием `allowUnknownCaller`, caller-asserted provenance требует также `allowCallerAsserted`; известные remote/headless отклоняются всегда. Эти согласия по умолчанию false и не включаются автоматически при выборе none. Не менять существующий route contract для `required`/`best_effort` и не превращать отказ required в молчаливую информационную отправку. В совместимой policy согласия могут храниться в существующем route object, но их проверка для none не должна требовать `localRouting=true`. CLI/setup различают отсутствующий выбор (сохранить/потребовать настройку) и явно выбранный none (очистить app identity, отключить callback).

Gate: actual production MCP adapter с формой Claude metadata из §2, без session env/ID, с explicit request_id и none проходит persisted setup -> runtime policy -> service -> journal -> fake native boundary только при нужном согласии. Без согласия, при caller-asserted без отдельного согласия и при известных remote/headless эффекты отсутствуют. Тест с искусственным trusted Desktop origin не доказывает этот сценарий; informational-only не обещает возврат в Claude session.

Claude adapter является вторым consumer общих контрактов, а не второй реализацией отправки. Перед его activation нужен узкий context probe в новом тестовом проекте: два вызова/сессии, stale target и process-per-session assumptions. Если Claude MCP не передаёт session context, навигация в конкретную сессию требует отдельно квалифицированного session-scoped CLI/skill adapter; до этого она unavailable. Informational MCP с явными request_id и navigation=none остаётся допустимым при согласиях выше, без выдуманного session ID и без изменения общего Codex route. Существующие hooks не зависят от успеха этого probe.


Preflight update: actual Claude2.1.265 `--bare` MCP calls in two distinct sandbox sessions did not provide session identity (only toolUseId/progressToken). This mode cannot supply exact-session routing. Explicit plugin skill substitution of ${CLAUDE_SESSION_ID} subsequently passed for two distinct sessions; production CLI execution, terminal target and hook-enabled context remain unproven; do not derive session identity from toolUseId or assume installing a skill fixes metadata. Evidence: task worktree `docs/evidence/agent-notify/claude-context-preflight/`.

## 6. Idempotency: выбранное простое durable решение

### 6.1. ✅ Atomic bounded JSON journal + kernel lock

Журнал первой local macOS версии хранится в приватном persistent state root runtime. Использовать один постоянный lock file и OS advisory lock (`flock` через уже имеющийся `golang.org/x/sys/unix`), удерживаемый только на read/modify/atomic-write snapshot. Lock file не удалять и не пересоздавать при GC. Нет TTL владения и stale-unlink алгоритма.

| Вариант | Оценка | Объём journal с risk tests |
| --- | --- | --- |
| **Bounded JSON snapshot + короткий kernel lock** | 🎯 9/10 · 🛡️ 8/10 · 🧠 4/10 | 450-750 LOC; выбран для небольшого local потока, без новой зависимости |
| SQLite transactions | 🎯 8/10 · 🛡️ 9/10 · 🧠 6/10 | 600-1 000 LOC и dependency/build matrix; уместно после реальной потребности в большом журнале/запросах |
| bbolt с открытием/закрытием DB на короткую операцию | 🎯 7/10 · 🛡️ 9/10 · 🧠 5/10 | 500-850 LOC и dependency; держать DB open на весь MCP нельзя из-за file lock между процессами |

Проверено через `gh` 2026-09-10: [bbolt v1.5.0](https://github.com/etcd-io/bbolt/releases/tag/v1.5.0) опубликован 2026-06-21, его release `go.mod` требует Go 1.25.0. Он не выбран. Не ссылаться на текущий main библиотеки как на требования release tag.

Начальные границы journal: 10 000 записей, 8 MiB файла, минимальное idempotency window 7 суток подтверждённого прошедшего времени (консервативное продление см. §6.3). Это верхняя граница, не цель заполнения. Хранить ключ/digest/token/times/minimal receipt и rate counters; текст и raw metadata не сохранять. SHA-256 key строить по length-prefixed полям `protocol namespace v1, persistent state namespace ID, source namespace, typed origin scope (session ID либо anonymous scope ниже), key kind, request key`. State namespace живёт вместе с журналом вне runtime/cache и сохраняется при upgrade/repair/install-path change/takeover. Source namespace описывает источник (codex/local для initial mode), а не приложение доставки. App path, mutable route config, binary version и install method не входят в lookup key. Разные чаты с одинаковым callId различаются; смена маршрута не создаёт новый key для старого запроса. Не выдумывать profile. Если ожидаемый журнал утрачен, требуется recovery, а не пустой журнал по умолчанию. Ручной reset state является явной потерей dedup history, не обычным repair.

Payload digest строить по versioned canonical representation caller payload с приведёнными к явным значениям defaults, но без нормализации текстовых Unicode sequences, включая content/category/navigation, без текущего вычисляемого route config. Первоначальный target/app policy snapshot хранится отдельно в записи. Не включать случайный attempt ID, timestamp, client itemId-дубликаты или текущее значение звука. Для durably admitted key replay возвращает первоначальный receipt, caller request_id (или null), tracking_id/key_kind и target decision; не строит действие заново из изменившейся конфигурации. Другой payload при уже записанном ключе даёт `idempotency_conflict`. Validation/preflight suppression до записи dispatching не потребляет key: там доказанно не было effect, поэтому после исправления настроек запрос можно безопасно переоценить. Это различие входит в публичное описание receipt, а не остаётся случайностью реализации.

**Sessionless informational scope:** если квалифицированного session ID нет, key использует отдельный типизированный anonymous source/transport namespace, стабильный между MCP процессами и reconnect. Это общая область caller IDs для всех таких сессий, а не доказанная изоляция чатов. PID, CWD и случайный connection ID не заменяют её. Skill для этого режима требует новый высокоэнтропийный explicit request_id (например UUID) на каждое новое намерение и сохраняет его только для повтора этого намерения; короткие повторяемые метки вроде `attention-1` непригодны. Тот же ID из другой anonymous сессии закономерно даёт replay/conflict, поэтому API не обещает независимую дедупликацию этих сессий. Gate: два production adapter callers без session с разными UUID проходят независимо; общий ID воспроизводит прежний receipt либо conflict; общий rate bucket сохраняется после reconnect; anonymous scope не коллидирует с настоящим session ID с теми же bytes. Request ID не является доказательством происхождения или правом навигации.

### 6.2. Протокол состояния

1. Validate payload и origin. В короткой journal transaction: прочитать key. При совпадающем terminal record вернуть receipt с `replayed=true`; конфликт отклонить.
2. Для нового key проверить snapshot политики, capability и текущую permission readiness, ещё без side effects. В повторной atomic transaction заново проверить key, rate counters и лимиты хранилища; записать `dispatching` с криптографически случайным attempt token **durably до запуска notifier**. Если между проверками другой процесс уже занял key, использовать его запись.
3. Освободить journal lock, выполнить generation fence перед handoff (§8.2), вызвать backend не более одного раза. Native notification ID детерминирован для scoped key + attempt token (replay того же admission сохраняет ID, новый admission после GC получает другой); native grouping ID относится к session. Два разных request ID одной session создают два уведомления.
4. Короткой transaction записать submitted/rejected/suppressed/unknown только при совпадающем attempt token. Нельзя перезаписать более новую попытку после retention/recreate. При ошибке сохранения после эффекта вернуть unknown; старый dispatching record сохранит защиту от retry.
5. Повтор видит `dispatching`: возвращает `unknown/pending_submission` с тем же request ID, без новой отправки. Не требуется очередь ожидания или lease takeover. В текущем процессе допустимо дождаться уже выполняющегося future в оставшийся budget, но это оптимизация, не условие корректности.
6. Crash между записью dispatching и реальным submit может оставить недоставленное уведомление с unknown. Это осознанная цена запрета дублей после неопределённости. Новый явный запрос с новым request ID возможен; skill предупреждает, что он может дублировать предыдущий эффект.

Отмена до durable dispatch admission возвращает not-submitted reason и не оставляет разрешение на отложенный effect. После dispatch boundary и до подтверждения исхода отмена даёт unknown. Если ОС уже подтвердила submit, поздняя отмена не делает уведомление отменённым. Нет автоматического retract. При повреждённом/unwritable/full journal, lock timeout или неподдержанной filesystem semantics новый effect запрещён.

**Точность terminal receipt:** исходные target/app/policy identity сохраняются неизменными, но terminal outcome хранит фактически подтверждённую capability. Service валидирует navigation из readiness перед admission и учитывает допустимое позднее понижение capability из delivery. При best_effort отправке без action нельзя вернуть available/chat_id лишь потому, что target был настроен. Required без квалифицированного action отклоняется до effect. Terminal navigation сохраняется отдельным CAS-protected outcome и воспроизводится на replay без повторного resolve; повышение capability относительно принятого решения запрещено. Корректный suppressed/disabled после admission сохраняется как подтверждённый no-effect outcome с исходным reason, а не превращается в unknown/invalid_delivery_receipt. Ключ при этом не освобождается. Gate: populated readiness/delivery navigation, поздний downgrade, suppression с нулём effects, replay того же результата, stale CAS и finalization failure. Изменение receipt representation требует проверки старых записей и writer capability floor перед activation.

### 6.3. Crash, retention и filesystem

- State dir mode 0700; journal/temp files 0600. Проверять тип/ownership paths, не следовать чужим symlink. Никакой записи в plugin cache/TMPDIR для persistent journal.
- Новый snapshot создаётся в том же каталоге, полностью пишется, `fsync` файла, atomic rename поверх data file, затем directory durability. Process lock отпускается после durable boundary. Не переименовывать lock inode вместе с data file.
- До rename crash оставляет старый snapshot, после durable rename новый. Corrupt file не заменяется пустыми defaults: `state_repair_required`, без отправки. Repair/backup policy не должна молча превращать старые unknown keys в новые.
- GC под тем же lock. Wall clock используется для диагностики, не eviction: перевод часов вперёд может преждевременно удалить unknown. Использовать persisted logical elapsed counter с platform port `(boot identity, monotonic time since boot)`: в одной boot epoch добавлять неотрицательную delta; при смене boot/регрессии/отсутствии identity не засчитывать недоказанный промежуток. Admission record хранит counter; eviction только после накопленных 7 суток. Restart процесса в том же boot сохраняет возраст; reboot/неучтённый sleep только продлевают retention. Не сериализовать monotonic Go time.Time как Unix timestamp. Port квалифицируется для macOS; без него GC заморожен.
- До этого срока все durably admitted записи, включая terminal rejected/suppressed, не удаляются ради места. Это не относится к pre-admission отказам, которые не занимали key. Gate: post-admission suppressed -> заполнение журнала -> повтор ключа возвращает прежний suppressed без нового effect. На capacity `journal_full`, видимый через status. 10 000 records не обеспечивают 7 дней ёмкости при максимальных 30/min (заполнение примерно за 5.6 часа): это storage budget, а не throughput promise. Можно увеличить лимит по измерениям, но не сокращать retention.
- После подтверждённого retention window key может считаться новым; exactly-once или бесконечная дедупликация не обещаются. Idempotency window, timeout и callback retention являются разными понятиями.
- Native callbacks не зависят от journal; его GC не удаляет действие уже показанного уведомления. Journal файлы не нужны для позднего открытия чата.
- Поддержанный store находится на локальной файловой системе. Network share/cloud-synced state не квалифицирован; не автоматически размещать journal там. Windows journal lock implementation и native failure tests добавляются вместе с включением нового explicit API на этой платформе. Это не разрешает отложить Windows support общего installation kernel: если существующие Windows setup/repair/uninstall переводятся на него в PR2, их рабочая реализация и regression tests обязательны уже в PR2. Blanket unsupported для общего kernel недопустим; compile-only проверка не доказывает сохранность установщика.

Обязательные actual multi-process tests: конкурентный одинаковый key, разные key, конфликт payload, crash в каждой durable boundary, lost ack, late CAS, file corruption, disk error, full store, clock jumps в обе стороны/reboot/clock port unavailable, route/app/install migration и replay после restart. Старые `internal/dedup`/`state` не меняются ради новой функции.

## 7. Native macOS контракт и callback

### 7.1. Literal send и capability handshake

Добавить отдельный versioned structured send mode native notifier. Go передаёт JSON payload в приватный request file и отдельный receipt path через фиксированные options; text никогда не является option. Files временные, необходимы только до окончания submit, не для callback. Native reader проверяет размер/тип/mode/ownership, payload version и допустимые поля; Go проверяет receipt schema, correlation ID и nonce, а не только process exit. Args/parsing legacy режима исправить отдельно: help распознаётся как option position, не как любое значение argv.

**Ownership временного payload и поздний launch:** каждый admitted attempt получает отдельный private request directory и непереиспользуемые request/receipt имена с nonce. Go не удаляет его безусловным defer сразу после выхода `open`. Native копирует и валидирует payload до эффекта, проверяет boot-scoped monotonic admission deadline, учитывающий sleep/suspend; missing/expired request означает no-submit. Retention clock может консервативно не учитывать sleep, но send deadline не должен продлеваться на сон системы. Удалением receipt и request directory после чтения и валидации terminal receipt владеет Go caller; завершение native send instance само по себе не разрешает удалить ещё не прочитанный receipt. Native удаляет только прочитанный body, сохраняя receipt для caller. При timeout/EOF/crash без доказанного завершения возвращается unknown; после deadline caller либо последующий bounded orphan cleanup может удалить собственный завершённый/просроченный attempt; активный reader защищён request ownership/lease. Native не удаляет receipt сразу после публикации. При погибшем caller отсутствие немедленной очистки допустимо в пределах общего spool budget. Native удаляет request body file после полного чтения, receipt не содержит текста. Orphan directories после crash убираются на следующем старте notify/setup ограниченным проходом только по собственным expired entries; без нового запуска не обещается точное время удаления. Никакого нового daemon/scheduler ради cleanup. Общий bytes/count budget orphan spool проверяется до admission; при заполнении отказ, а не накопление без предела. Native не пересоздаёт удалённый каталог и не отправляет по просроченному payload; receipt пишется атомарно. Deadline может остановить будущий submit, но не отменить уже начавшийся: если effect мог начаться, итог unknown даже при позднем completion. Kill launcher не доказывает остановку LaunchServices app; нельзя завершать все notifier процессы по bundle ID. Проверить native exit до первого чтения receipt caller (подтверждение сохраняется), cleanup при активном reader, delayed launch/read, cleanup/late receipt, EOF во время handoff и cleanup orphan spool при следующем owner invocation; не утверждать автоматическое удаление к фиксированному сроку без живого процесса.

В новом notifier `--capabilities-json` выполняется без permission prompt/notification и возвращает protocol versions, supported action kinds, receipt support, backend identity. Старый notifier без такой команды может попасть в `runCallbackMode` и ждать событий; его нельзя слепо запускать для discovery. Сначала installer/adapter читает offline managed artifact manifest с fingerprint/protocol floor. Для известного старого или неизвестного artifact результат `unsupported_notifier`, без попытки capability запуска. Для verified нового artifact probe выполняется прямым отдельным процессом, без LaunchServices reuse, с hard deadline 1 секунда и завершением только своего probe process; он не должен затрагивать уже работающий callback. Проверять строгий JSON/correlation/schema, не принимать пустой stdout или exit=0 за capabilities. Actual old binary fixture доказывает no-effect/bounded rejection. Только после этого создавать request нового формата; версия главного Go binary не заменяет проверку native component.

Новый envelope содержит явную проверяемую schema version. `desktop_thread_v1` payload хранится целиком в `notification.userInfo`: thread ID, route kind, trusted app identity reference/validated path hint, notification correlation ID. Никакой ссылки на временный request file. Unknown version/action/malformed payload дают bounded diagnostic и no-op, а не silent success. Legacy decoder поддерживает старые activate/execute/combined actions в заявленном compatibility window.

Native reply `submitted` записывается только из положительного completion `UNUserNotificationCenter.add`. Отсутствие валидного reply после старта send не эквивалентно отказу. Unknown outcome не запускает beeep fallback. При required navigation old/incompatible notifier даёт `activation_required`/`unsupported_notifier` **до** отправки.

Permission status читает native adapter в рамках общего deadline. Undetermined требует setup, обычный tool call не показывает системный prompt; denied возвращает permission reason. DND/Focus может скрыть баннер при submitted и не вызывает retry. Legacy interactive permission behavior hooks сохраняется через legacy mode.

### 7.2. Выбор приложения и исполнение

Выбранный механизм: `NSWorkspace.open(_:withApplicationAt:configuration:completionHandler:)`, `activates=true`. Источник API: [Apple NSWorkspace](https://developer.apple.com/documentation/appkit/nsworkspace/open(_:withapplicationat:configuration:completionhandler:)). Он уже дал положительный ручной результат в spike, но ещё не интегрирован в product.

Discovery читает registered candidates и configured app identity. При setup выбирается установленное приложение с ожидаемым bundle ID; фиксируется проверяемая идентичность подписанного приложения и canonical location hint. Не хардкодить `/Applications/ChatGPT.app`, не выбирать первую/новейшую/backup копию. При нескольких валидных неоднозначных установках status требует пользовательского выбора в setup. Путь в настройке доступен пользователю/installer, но отсутствует в модельной tool schema.

Сохранённая app identity в callback payload самодостаточна либо ссылается на immutable owned запись, которая сохраняется вместе с callback-only runtime. Mutable route config и journal не являются обязательной зависимостью клика: смена настройки не переназначает уже отправленное уведомление. Gate: отправить с выбранным app A -> выбрать B -> удалить consumer/config -> кликнуть старое уведомление: проверяется сохранённая identity A, а при её недоступности возвращается отказ без открытия B. Перемещение того же приложения обрабатывается по правилу verified identity ниже.

На клик сначала проверять сохранённого кандидата; после перемещения разрешить повторный lookup только для той же verified identity. Несовпадение bundle/signature, исчезнувшее приложение или неоднозначность даёт reason без запуска произвольного fallback path. Concrete signature requirement определить по поддержанным дистрибутивам в native implementation fixture, не копировать из текущей машины как универсальную константу.

URI строит trusted Codex route encoder из валидного opaque thread ID, в известном формате `codex://threads/<id>`. Вход модели не может вставить другую scheme/query/fragment. Не устанавливать global URL handler и не менять пользовательский default app.

Callback executor асинхронный, с одним process-level lifecycle owner и in-flight counter для всех кликов. Startup idle watchdog из runCallbackMode переводится в idle-only режим при принятии callback: он не прерывает активную операцию. Completion/deadline завершает один request ровно один раз; idle exit допускается лишь после завершения callbacks и drain принадлежащих процессу children (уточнение ниже). Нельзя оставить внешний 10-секундный startup timer или старый terminate через 0.5 секунды поверх executor. Два быстрых клика и callback, пришедший непосредственно перед idle timeout, имеют отдельные tests. Старые 0.5 секунды после запуска action не являются барьером. Double completion, callback timeout, unknown button ID и dismiss не должны вызывать повторную navigation. Только default click и явно зарегистрированное Open action могут исполнять target. Логи bounded, без body и shell text; async failure не теряется при terminate.

**Две границы завершения:** completion callback и завершение принадлежащей процессу работы учитываются отдельно. Discovery и синхронная проверка подписи выполняются вне main queue в ограниченном числе worker slots. Timeout завершает callback ровно один раз и запрещает начинать новый `NSWorkspace.open` после deadline, но slot остаётся занят, пока реально не вернётся неотменяемая Security operation; после deadline новые кандидаты не проверяются. Нельзя освобождать slot по таймеру и запускать неограниченное число зависших проверок. Main queue принимает второй клик и обслуживает deadline независимо от первой проверки. Если `NSWorkspace.open` уже вызван, timeout не доказывает отмену системного запроса: terminal reason `open_unknown`, возможное позднее открытие и никакого retry. Поздний completion не создаёт второй terminal event. Gate: held discovery не начинает open после deadline; уже handed-off open с delayed completion сохраняет один terminal event без повторной navigation.

Для legacy `execute`/`combined` action сохранить владение дочерним процессом: по timeout отменять только принадлежащий операции child/process group, с bounded escalation и reaping. Нулевой callback counter не разрешает idle exit, пока owned child не завершён; late termination не вызывает второй completion или последующую activate. Не менять legacy codec и успешный порядок command -> activate. Неотменяемая проверка подписи не должна задерживать OS completion; её поздний результат игнорируется. Gate: held verifier + два клика, заполненные slots + повторные timeout, held child + timeout + late termination, без настоящего запуска приложения.

Диагностика передаёт валидированный correlation UUID через весь callback lifecycle: `callback_received` и ровно один terminal event, в том числе при обратном порядке завершения A/B. Для malformed payload используется новый event ID; произвольные userInfo, thread ID, app path, body и shell text в журнал не попадают. Успешный запрос NSWorkspace не порождает `visible_target_confirmed`.

Причина неуспеха default `open URL` остаётся неизвестной. Explicit application является выбранным рабочим направлением на основании положительного evidence, а не доказанным исправлением backup-handler root cause.

## 8. Установка, обновление, rollback и ownership

### 8.1. ✅ Существующий installer первым

Пользовательский путь: обычная установка Agent Notifications -> явное согласие на configure для выбранного клиента -> стадии §8.1.2 -> client activation/approval -> status/проверочное уведомление по отдельному действию пользователя. Согласие запустить configure не означает, что persisted enable уже записан: при явном permission request он следует только после allowed. Успешная установка, включение MCP в клиенте и разрешение ОС отображаются отдельно. Обновление существующего пользователя само по себе feature не включает.

Сохраняются старые install directory/command identities: `codexsetup.InstallDirName = claude-notifications-go` и стабильная строка hook command участвуют в Codex trust hash. Rebrand не является поводом менять их. Расширить конкретный `runtimeEntry`/`runtimeBinary` allowlist для выбранных assets; не заменить фильтр на unrestricted copy. Новый executable не нужен.

Native compatible component ставится в постоянное owned место вне plugin cache и вне целиком заменяемого `bin`. Persistent state namespace/journal размещаются в control root/state и не удаляются при обычном runtime repair или смене install method; private state root совпадает для всех процессов/consumers. Metadata/skill/MCP launch entries используют один runtime contract. Источником runtime может быть legacy install или portable package, но native callback не ссылается на source cache.

Подготовка настройки является отдельным шагом существующего installer: выбрать authoritative global config через текущий stable-path contract, проверить документ строго и заполнить только отсутствующие необходимые desktop поля. Существующие false и неизвестные пользовательские поля сохраняются; malformed canonical config не заменяется legacy/defaults. Legacy/defaults допускаются только при отсутствии canonical файла, с проверкой выбранного источника и повторной проверкой под config lock. Global config остаётся mutable пользовательским документом, а не immutable runtime asset. Строгая проверка согласована с legacy decoder: case-insensitive aliases собственных ключей (включая Unicode simple-fold) отклоняются до добавления defaults, а не трактуются как отсутствующее поле. Regression: `Enabled:false` и неоднозначные пары ключей не превращаются в canonical `enabled:true`; файл остаётся неизменным. Неизвестные несвязанные поля сохраняются. Prepare не регистрирует MCP, не включает opt-in, не запрашивает permission и не отправляет notification. Status отдельно сообщает «не подготовлено/invalid», «выключено», «activation required» и permission state; ранний ответ disabled не является доказательством валидного global config.

Skill имеет один авторский источник в репозитории. Существующий проверенный executable может доставлять его через embed, после чего installer записывает точный owned asset `<RuntimeRoot>/skills/agent-notify/SKILL.md` общей transaction, сохраняя действующие hook prepare callbacks. Runtime source сам по себе не означает client discovery. Для Codex выбрать ровно один доказанный discovery source: skill включённого собственного plugin либо явно выбранный поддержанный user-skill path с owned projection; для Claude квалифицировать обнаружение skill установленным plugin. Наличие plugin source само по себе не доказывает активную версию или discovery. Не добавлять вторую копию логики, второго владельца runtime или параллельный MCP ради skill. При update существующей projection installer явно запрашивает её refresh; отсутствие такого запроса сохраняет прежнюю копию и не считается обновлением skill.

Gate основного installer: fresh isolated home без config; сохранённый desktop=false; malformed config без fallback; оба порядка Claude/Codex install с командой из ledger primary runtime; canonical skill -> managed runtime -> client discovery; refresh, foreign skill conflict даже при совпадении bytes, interrupted projection update и independent uninstall. Проверять настоящий installer adapter -> общий commit -> client registration/projection, а не только несвязанные unit mocks. Родительские каталоги projection готовит explicit setup с безопасными physical paths; read-only status их не создаёт. Известный конфликт global/plugin MCP обнаруживается до activation; отсутствие дубля в одном config не доказывает отсутствие plugin registration.

### 8.1.1. Явный permission setup без отправки

Setup разделяет регистрацию клиента, изменение opt-in, read-only permission status и явный запрос разрешения. Успех любого одного шага не означает успех остальных. `status`, registration, repair, enable и обычный `notify` не вызывают системный запрос; setup не отправляет тестовое уведомление без отдельного действия пользователя. Запрос разрешения должен работать до enable, иначе возникает цикл «включение требует разрешение, разрешение требует включение».

- Native permission request имеет отдельный versioned capability и строгий request/response с correlation/nonce. Сначала проверить offline identity/floor установленного artifact и безопасную capability; неизвестный или старый notifier не запускать с новым флагом. Не расширять молча базовую capability schema, которую существующий строгий reader может отвергнуть.
- Использовать постоянный managed `.app` с той же notification identity, что и production delivery. Разрешение тестовому или временному bundle не доказывает разрешение production bundle. Перед OS API проверить bundle metadata; legacy `ensurePermission` вместе с send flow не является setup adapter.
- При already allowed/denied вернуть текущее состояние без повторного запроса. Только явное действие при undetermined может один раз вызвать requestAuthorization. Ошибка, таймаут или потерянный ответ не трактуются как denied/allowed и не запускают автоматический повтор. Следующее read-only чтение может уточнить состояние.
- У интерактивного setup отдельный конечный бюджет, не 15 секунд notify; зафиксировать его в CLI/help и тестах. Отмена прекращает ожидание и собирает owned child, но не обещает отозвать уже показанный OS prompt. Никакого enable по одному exit=0 или после отменённой операции.
- Проверка установленного snapshot и запуск permission helper защищаются тем же component lease; setup может использовать verified disabled runtime, но не обходить owner/fingerprint/generation/recovery проверки. На время UI сохранять ресурс helper и ограничивать ожидание конкурирующего update/uninstall. Не держать journal/config locks во время ожидания пользователя.

Gate: старый executable spy получает ноль запусков; disabled verified runtime допускает permission setup; malformed correlation, denial, timeout, cancellation и delayed completion не отправляют notification и не меняют opt-in; конкурентный update не удаляет используемый helper. Actual OS prompt проверяется отдельно в тестовом macOS окружении с фиксацией bundle identity; unit mocks не закрывают этот acceptance.

### 8.1.2. Единый пользовательский configure flow

Существующие bootstrap, Claude init и `setup-codex` получают явный opt-in `--configure-notifications`. Они вызывают один typed orchestration use case `setup-notifications configure --provider codex|claude|both` после успешной установки всех выбранных consumers. Обычная установка/обновление остаётся без opt-in; несовместимые remove/print/dry-run флаги отклоняются до изменений. Для выбранной explicit-функции на Darwin отсутствие qualified native assets является ошибкой до hook commit; прежняя hook-only установка сохраняет свой контракт.

CLI parsing/rendering отделены от callable configure composition; не запускать рекурсивно свой executable и не разбирать собственный JSON. Reuse существующих config/setup/clientsetup cores; read-only ownership inspection выделить из общего preflight, не копировать parser ledger в CLI. Пользователь не вводит generation, primary binary или служебные skill paths вручную.

- Provider задаётся явно. MCP command берётся из ledger primary RuntimeRoot независимо от порядка установки клиентов и места вызывающего executable. Standalone source defaults проверяются относительно собственного установленного bundle, без cwd fallback.
- Codex config/skill используют выбранный абсолютный CODEX_HOME; относительные explicit/env пути отклоняются до нормализации. Claude user MCP использует HOME/.claude.json либо абсолютный CLAUDE_CONFIG_DIR/.claude.json. Это не путь global notification config: обе интеграции используют один production canonical HOME/.claude/claude-notifications-go/config.json. Не добавлять configure-only override, который последующий MCP не сможет воспроизвести.
- Одна shared route policy применяется один раз. Отсутствие route options сохраняет имеющуюся настройку; fresh setup требует явный app route либо none. Подключение Claude не подставляет none и не стирает Codex app identity. Claude может вызывать request-level none при сохранённом Codex route и отдельно разрешённом unknown caller; согласия не выводятся из provider.
- Порядок: read-only preflight всех выбранных clients/routes/config/native/collisions -> prepare global один раз -> безопасные physical parents -> registration/projection в фиксированном порядке -> только при явном флаге permission request -> повторная проверка generation/inventory -> enable intent один раз. Один конечный budget 3 минуты; результаты стадий передают наблюдаемую generation следующей. Конкурентное изменение останавливает flow, без автоматического takeover/retry.
- Если permission request явно выбран, только allowed позволяет продолжить к новому enable intent. Ошибка/denied/unknown/отмена сохраняет ранее выполненные стадии и прежний intent, но не создаёт новый. Без request флага разрешение проверяется read-only и показывается отдельно; enable intent не означает permission или работоспособность клиента.
- Частичный отказ, в том числе второго клиента, возвращает результаты стадий и последнее достоверное состояние. Не обещать общую атомарность installer+configure, не восстанавливать полный backup поверх foreign edits. Явный повтор согласует сохранённый прогресс через существующие cores; прежнее enabled=false и ограничения MCP не снимаются. Registration возвращает activation_required до отдельной проверки клиента.

**Ограниченный collision preflight:** проверять выбранный user registration и точные собственные installed plugin identities, без обхода всех проектов. Различать clear/collision/unknown; unreadable/unsupported registry или неизвестная effective package version не означает отсутствие дубля. Для включённого собственного plugin проверить фактически используемые MCP declarations и skill. `plugin list` source.path нельзя считать installedPath или доказательством cache content. Пока effective source не квалифицирован на клиенте, соответствующая ветка возвращает unknown; не создавать user projection как скрытый fallback. Discovery привязывается к фактически запускаемому executable клиента и его версии, а не к версии соседнего Desktop bundle. Для fallback cache layout нужен отдельный qualified version adapter; неизвестная версия возвращает actionable unknown, без выбора «самого нового» cache directory. Проверять различающиеся PATH CLI/Desktop versions, namespaced skill name с точным pluginId, отсутствие skill, несколько cached versions и source/cache drift; successful source read не доказывает effective package. Existing owned projection + активный plugin skill является конфликтом: nil projection сохраняет файл, а не удаляет его. Disabled plugin не включать; предупредить, что последующее внешнее включение требует повторной настройки. Перечитать относящиеся к проверке fingerprints перед registration и enable; это не обещает контроля будущих внешних изменений.

Gate: все три entrypoints вызывают одну composition; оба install orders используют primary command; both сохраняет shared route; explicit permission failure не добавляет enable; второй client failure даёт повторяемый partial result без foreign rollback; plugin/global MCP и plugin/user skill не дублируются; unknown inventory блокирует настройку; обычный hook install не изменён. Client discovery и installed Desktop E2E остаются отдельными проверками.

### 8.2. Маленький ownership record

Достаточный JSON ledger: schema version, stable component/install ID, выбранный install method, canonical runtime root, managed file identities, consumer IDs и precise registration keys/commands, native capabilities/compatibility floor. Без manager daemon, distributed consensus и универсального package governance.

**Общая атомарность owner:** control root `os.UserConfigDir()/agent-notifications` определяется до выбора install method и совпадает для всех consumers. В нём постоянный `.component-install.lock` (kernel lock, inode не удаляется), ownership.json и recovery transaction record. Операции shell/setup/portable, изменяющие shared component, проходят через одну реализацию commit phase в существующем Go executable; download/staging делаются до lock. Acquisition пишет только в новый, атомарно созданный physical staging directory с явно заданным output; существующий destination, symlink и live runtime отвергаются до записи. Режим acquire-only не копирует файлы в SCRIPT_DIR и не обходит kernel promotion. Bootstrap запускает configure из проверенного установленного consumer bundle после commit, а не из временного staging executable. Gate: попытка acquisition в существующий runtime оставляет его bytes неизменными; fresh output содержит assets без изменения ledger/live paths; configure после удаления staging использует primary runtime. Разрозненные старые locks в SCRIPT_DIR и CODEX_HOME не защищают новый ledger и не переиспользуются как его authority.

Под component lock заново проверить generation/owner/consumers, записать transaction ID, expected fingerprints, целевую generation и phase, затем promoted runtime/owned registrations и итоговый ledger. Сбой восстанавливается по transaction record и фактическим fingerprints, не по случайно последней JSON записи. Это crash-recoverable операция, не обещание транзакции между всеми внешними файлами. Lock order: component, затем client/global config locks в каноническом порядке путей. Только config migration берёт свой config lock и никогда не ждёт component; journal lock не вкладывается в install locks. При последнем uninstall проверка consumers и удаление выполняются в той же component transaction. Нужны actual двухпроцессные install/install, takeover/install и add-consumer/final-uninstall tests. Если manager не может соблюдать протокол, activation shared component отклоняется до mutation, а не создаётся второй owner.

- Один компонент имеет одного writer-owner; Claude/Codex являются consumers. Повторный install/repair не создаёт второй MCP server/hook.
- Unmatched foreign entries сохраняются. Старый путь похожего hook не удаляется по substring. Managed entries распознаются по ledger и точной прежней identity.
- Install от другого manager обнаруживает owner; предлагает явный takeover/использование того же runtime, либо прекращает изменение с actionable status. Не молча создаёт параллельную установку.
- Удаление consumer удаляет только его managed registration, не shared runtime второго. Final uninstall меняет explicit policy/registration generation и закрывает последующие native handoff через generation fence ниже. Старый caller может успеть записать dispatching, но эта запись не разрешает effect при отозванном generation. Уже admitted send может завершиться submitted/unknown, мгновенный отзыв не обещается; следующие calls перечитывают policy snapshot. Судьба pending notifications описана пользователю.
- Сохранение callback-only native компонента по умолчанию при final uninstall позволяет поздним уведомлениям отработать; отдельный purge удаляет его и честно сообщает, что старые уведомления больше не смогут открыть чат. Не удалять shared callback автоматически при uninstall одного клиента.
- Rollback конфигурации меняет только owned entries с проверкой ожидаемого snapshot. Нельзя восстанавливать целиком старый hooks/config backup поверх новых foreign edits.

**Граница admission и отключения:** policy snapshot для нового запроса содержит generation установки. После durable dispatching, но до первого native handoff, delivery под component lock перечитывает текущие generation/owner/enable state и recovery marker. Устаревшее или отключённое поколение даёт подтверждённый отказ до effect; receipt сохраняется через отдельную journal transaction после освобождения component lock. Journal и component locks не вкладываются друг в друга. Штатный setup enable/disable меняет explicit policy и увеличивает generation через тот же component protocol (component, затем config lock); legacy global opt-outs и ручное внешнее редактирование config имеют per-request snapshot semantics и не обещает мгновенный отзыв уже выполняющегося вызова. Проверка generation и начало handoff защищены одной installation lease; простой повторный read без защиты между ним и launch не устраняет гонку. Uninstall, начавшийся после handoff, не отзывает уже запущенную отправку. После unknown освобождение lease не доказывает завершения native process: сохранение его ресурсов регулируется §8.3. Gate: остановить caller между preflight и handoff, выполнить disable/uninstall, продолжить caller и получить ноль новых native effects; отдельно проверить uninstall после handoff без ложного обещания retract.

**Единый контракт installer -> delivery:** fingerprint, private path checks, generation/recovery read и permanent lock имеют одну общую реализацию с read-only интерфейсом для delivery. Не копировать их в notifier и installer. Подпись bundle и происхождение пакета проверяются отдельно: совпадение самостоятельно заявленного hash не доказывает доверенного publisher. В sealed `Contents/Resources/managed-runtime.json` находятся schema/protocol/decoder floor; hash окончательно подписанного executable находится во внешнем post-signing attestation, проверяемом установщиком. Нельзя включать hash подписанного executable в ресурс, изменение которого потребует переподписать тот же executable. Installed ledger связывает проверенный итоговый bundle с fingerprint; sender читает этот контракт, не требует удалённый source cache/sidecar на каждом вызове.

Gate интеграции PR2+PR3: настоящий producer установщика создаёт staged artifact, затем установленный ledger и bundle читает production delivery consumer. Проверить смену staged/final basename, удаление source cache, несовпадение fingerprint, interrupted recovery и повторное обновление. Два независимо написанных mock manifest не заменяют этот тест. Сам permanent lock открывается без следования symlink, с проверкой типа/UID/mode через открытый descriptor; не разделять lstat и обычный OpenFile, оставляя окно подмены. Проверить symlink, неверного владельца/тип и сохранность inode при повторных operations.

**Confinement и mutable config:** проверка canonical destination root не разрешает запись через вложенные symlink/reparse parents. Staging, commit и recovery должны удерживать проверенную границу owned directory, с descriptor-relative no-follow traversal там, где это поддержано; неподдержанный безопасный путь отклоняется. Gate: вложенная ссылка и замена parent между stage/commit не меняют foreign tree. `config/config.json` в legacy runtime является mutable user config: defaults создаются только при отсутствии под config lock/no-clobber, существующие bytes сохраняются, файл не попадает в immutable asset ledger и автоматическое removal. Проверить первый managed adoption с пользовательским disable и последующий repair после ручного изменения config.

### 8.3. Устранение update gap

У нового native bundle есть стабильный путь. На macOS заменять подготовленный и проверенный bundle атомарным обменом sibling directories на той же filesystem, а не `old -> stage; new -> target` с отсутствующим target. В уже имеющемся `x/sys/unix v0.30.0` есть `RenamexNp`/`RenameatxNp` и `RENAME_SWAP`; это кандидат механизма, требующий квалификации на supported filesystem. Atomic path swap не доказывает совместимость уже запущенного процесса с заменёнными resources. Upgrade учитывает owned send/callback процессы и удерживает необходимый им artifact; при недоказанном hot swap activation ждёт безопасного drain owned процессов, оставляя новый action выключенным. Не останавливать пользовательский Codex или все процессы с bundle ID. Старый bundle после swap сохраняется как rollback artifact. Неподдержанная atomic swap semantics даёт отказ обновления с сохранением работающего старого компонента, а не небезопасный fallback.

Выбор нового native artifact основан на требуемой версии, verified identity и compatibility, а не на наличии executable старого helper. Existing-installer update обязан получать и проверять нужный release artifact даже при установленном legacy helper. Callback live path определяется managed identity, независимо от basename исходного `terminal-notifier.app`/`ClaudeNotifier.app`; миграция старого имени явная и сохраняет известные concrete callback paths. Gate: legacy executable -> staged modern artifact -> повторное обновление через реальный adapter; удалённый source cache не ломает installed producer -> sender contract.

Для legacy стабильных binaries внутри `bin` использовать file-level atomic replacement и не перемещать целиком live `bin`. Старые зарегистрированные command strings остаются прежними. Операции `stageBundle` должны отделить изменяемые обычные assets от callback-critical paths. Crash recovery marker записывается до activation нового набора и позволяет определить текущие fingerprints/восстановление; rollback в Go defer сам по себе crash recovery не заменяет.

Перед activation нового sender проверить native capability, signature/architecture и возможность прочитать старые queued action fixtures. Отдельно проверить registration/callback dispatch самого notifier: наличие нового sender не доказывает, что ОС не вызовет старую копию с тем же bundle ID. Для managed installation должен быть подтверждён постоянный callback owner; old-cache/alternate-install copies входят в queued-upgrade gate. Не менять bundle ID и permission identity молча ради устранения неоднозначности и не объявлять handshake доказательством правильного OS dispatch.

Runtime rollback **не** понижает native decoder ниже версии, способной прочитать уже отправленные новые actions. Feature можно выключить/откатить, оставив совместимый native reader. Попытка forced downgrade decoder получает отказ или требует явного purge pending actions, а не молча ломает клики.

Совместимость rollback включает **writer floor**, а не только native decoder floor: старый Go/setup/shell writer не должен перезаписать ledger/policy неизвестной schema или вернуть destructive promotion callback paths. Совместимый install kernel отклоняет promotion/activation writer ниже этого floor с actionable reason до изменения файлов и без запуска старого writer; исторический binary не обязан понимать новый floor. Поддержанный rollback выключает новую функцию, сохраняет совместимый reader и исполняется совместимым install kernel; возвращение к полностью unmanaged старой установке является отдельным явным reset/purge с потерей соответствующих гарантий. Произвольный запуск пользователем старого внешнего установщика вне managed protocol не контролируется этим механизмом и не объявляется безопасным. Gate: совместимый install kernel отклоняет managed rollback на старый writer против нового state/native floor без mutation и с нулём запусков старого writer; совместимый rollback сохраняет queued v1 callback и foreign config.

Offline writer-floor check выполняется до каждого managed execution/delegation: включая Windows hook-format probe, lazy updater, hook-wrapper вызов install.sh и generic `claude-notifications` executable. Alias допускается лишь при разрешении в отдельно проверенный managed target. Marker совместимости не заменяет аутентификацию source. Gate: инертные old-writer spies во всех entrypoints получают ноль запусков и ноль mutations.

Гарантия относится к новым actions и managed runtime. Уже показанные старые shell actions могут содержать `EvalSymlinks` concrete executable path в другом installation/cache. При управляемой миграции сохранить известные прежние concrete paths/совместимые команды; не считать новый symlink достаточным. Нельзя обещать сохранность неизвестного callback, если сторонний package manager удалит его старый cache. Этот legacy boundary документируется, новые actions такого ограничения не имеют.

Retention требует исполняемого перехода retirement, а не постоянного запрета третьего обновления: A -> B, отказ пока A нужен owned процессам/resources, подтверждённый drain под component lease, retirement A, затем B -> C. Проверка должна исключать новый owned launch между доказательством drain и удалением. Отсутствие процесса само по себе не доказывает отсутствие queued OS notification; текущий стабильный reader сохраняет совместимость, а удаляемые concrete paths не должны оставаться требуемыми callbacks. Пока proof недоступен, безопасный отказ сохраняется; kill-all, TTL и purge вместо обычного upgrade недопустимы.

Explicit purge должен восстанавливаться после остановки внутри recursive deletion. Persist per-entry owned identities либо эквивалентный durable deletion protocol: уже удалённые записи допустимы, изменённые/новые foreign entries сохраняются с конфликтом. Проверить crash после удаления одного дочернего файла, повтор cleanup и foreign insertion; whole-tree hash перед `RemoveAll` недостаточен для recovery частично удалённого дерева.

До cleanup проверить active sender/native compatibility. Держать текущий и предыдущий совместимый artifact в bounded retention; callback reader поддерживает все отправленные в initial v1 action schemas. Не использовать TTL journal как срок жизни системного уведомления. Изменение action schema в будущем требует отдельного compatibility решения.

### 8.4. Agent Plugins 1.0 как второй путь

Добавить portable `plugin.json`, `mcp.json`, `skills` и необходимые client compatibility manifests так, чтобы они запускали тот же executable/use case и ссылались на тот же persistent runtime. Существующие hooks остаются в корректных client extensions; корневой manifest не должен случайно отключить старые commands/hooks или зарегистрировать их дважды.

[UAP README](https://github.com/777genius/universal-agent-plugins/blob/main/README.md) был повторно прочитан через gh 2026-09-10: install CLI существует, standard-first authoring CLI помечен unreleased. Не добавлять SDK по предположению. Уже существующий `plugin-kit-ai/sdk` используется текущим проектом; не удалять его и не считать автоматически SDK нового стандарта.

В portable artifact выполнить manifest/schema validation и настоящий sandbox install по полному 40-символьному SHA, с `//path` при нескольких пакетах. Конкретные CLI команды взять из актуального UAP на шаге реализации, не публиковать непроверенные. Cache cleanup должен удалять package source, сохраняя managed callback runtime. Реальная client activation является отдельным gate после schema validation.

## 9. MCP lifecycle и зависимости

Использовать официальный Go SDK MCP вместо самописного JSON-RPC сервера. Через `gh` 2026-09-10 проверены [v1.7.0](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.7.0), опубликованный 2026-07-28, и [go.mod release tag](https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/go.mod): нужен Go 1.25.0. Текущий проект имеет Go 1.22 floor. Ничего не установлено при подготовке плана.

**Предлагаемое решение:** поднять minimum build Go до 1.25 отдельным небольшим prerequisite перед первым PR, использующим новые API или SDK, синхронно изменить `go.mod`, min-version lanes CI с 1.22 и документацию; закрепить SDK release после свежей проверки стабильной версии. В текущих macOS/Ubuntu/Windows matrices есть 1.22 и 1.26; `release.yml` уже использует Go 1.26, эта release lane не требует миграции. Latest stable toolchain для верхней CI lane сверяется отдельно, не выводится из требования SDK. Пользователю готового binary Go не нужен. Проверить поддерживаемые OS/architectures/toolchain ограничения; незаметно исключать старую заявленную платформу нельзя. Если обнаружен реальный конфликт поддерживаемой ОС, этот конкретный dependency выбор пересматривается до merge, остальные native/core PR продолжаются. Не создавать отдельный Go module только ради сохранения цифры в go.mod. Если PR2 использует `os.Root.Link` или другие новые файловые API, baseline PR предшествует PR2, а не откладывается до PR5. Каждый промежуточный PR должен собираться на собственном объявленном minimum с `GOTOOLCHAIN=local`; успешная сборка только на Go 1.26 не доказывает совместимость с Go 1.22/1.25. [Go 1.25 release notes](https://go.dev/doc/go1.25) подтверждают появление `Root.Link` и minimum macOS 12. Сверить этот OS floor с заявленной поддержкой и реальными release artifacts; наличие Go 1.26 в release workflow само по себе не доказывает совместимость старых ОС.

MCP composition создаёт service/resources один раз; EOF/signal закрывает admission, bounded drain, затем cancel и join. `Close`/`Shutdown` выполняются один раз на service lifetime. `HandleHook`, per-call notifier.Close и webhook.Shutdown не используются. Паника/ошибка одного request не должна закрывать чужие in-flight operations.

Для нового local native пути webhook sender вообще не создаётся. Если в общий путь попадает beeep, его global `AppName` защищается узкой process-local сериализацией вокруг вызова, без удержания journal lock. Не переиспользовать общий unsynchronized `rand.Rand` из webhook retry. Detached child audio должен быть reaped в долгоживущем процессе; новый invoke после Close отклоняется. Config maps не мутируются между requests.

Metadata extraction выполняется на каждый `tools/call`, не в initialize. Отсутствующий/невалидный thread context даёт reason. Сырой stdio frame ограничить 64 KiB до unbounded JSON allocation в SDK, включая whitespace и _meta; decoded content budget остаётся 16 KiB. Это лимит одного frame, не всего stream. Отдельно ограничить число одновременно разобранных и ожидающих `tools/call`: лимит двух native submit не защищает от неограниченных SDK goroutines/очередей. Admission без ожидания до дорогой обработки возвращает busy при занятой ёмкости; notification/cancellation/control frames не должны ждать освобождения submit slots. Concrete transport implementation обязан доказать bounded buffering и доставку cancel при насыщении, а не только добавить semaphore вокруг backend. Fatal wire cases из §4.3 (oversized frame, неразбираемый JSON, неподдержанная wire identity) безопасно завершают соединение с очищенной диагностикой, без дампа запроса и без остановки чужих callback. Нужен actual SDK transport test неизвестных _meta fields, frame boundary/large whitespace, cancellation и session isolation. Transport ID сохраняется без потери точности: числовой JSON ID нельзя проводить через float64 с округлением. Явно определить поддержанный диапазон и lexical forms, одинаковую канонизацию для request/response/cancellation и поведение duplicate outstanding ID. Проверить соседние integer IDs выше 2^53, границы int64, string IDs, escaped equivalents и reconnect на raw frames через actual SDK. Внутреннее remapping не должно создавать неограниченную таблицу; reservation живёт до полной записи response, включая backpressure. JSON-RPC ID по-прежнему не является durable request key. Не логировать request JSON при parse error. Начальный logger/errorhandler routing выбирается до любой global once-init, чтобы `mcp-server --help`, malformed input и panic не загрязняли protocol stdout.

MCP approval остаётся клиентским. Tool имеет side effect, `readOnlyHint=false`. Idempotency hint нельзя объявлять универсально истинным при необязательном стабильном request_id; использовать консервативную аннотацию и документированный journal contract. Проверить permission-notification hook на вызов notify: нет автоматической цепочки повторных notify и отключения общей защиты. Skill предлагает уведомления для полезных событий, не каждого шага.

## 10. План dependency-safe PR

### Preflight 0: проверить интеграционный путь до большой работы

Bounded compatibility checkpoint в disposable registration, без product migration. До PR2-6 подтвердить конкретный способ установить/активировать MCP и получить per-call контекст выбранного local Codex route; отдельно уточнить Claude context/CLI path. Выход: executable registration/config пример, наблюдаемые metadata, shared callers, честная capability matrix. Неизвестный Claude source не подменяется обещанием session-scoped skill: только доказанный CLI path или явно informational-only до следующего adapter. Этот checkpoint допустим параллельно с полезным literal bug fix PR1. Нельзя впервые обнаруживать недоступность основного client context после полной installer/journal работы. Ориентир 100-300 LOC harness/docs, не обязательный новый пакет; прежние metadata/native evidence не повторять без новой причины. Неисследованный второстепенный consumer не блокирует подтверждённый local route.


Ориентир около 2 000 changed LOC на PR, включая тесты, отдельно объясняя lockfile/generated changes. Диапазоны ниже оценочные и не являются заданием набрать строки. Каждый PR собирается и проходит focused gates независимо от следующих. Никакой runtime feature не включается до своих prerequisites. Один финальный полный CI на exact head mergeable PR; после изменения базы перепроверять недоказанные части, не повторять уже сохранённое evidence без причины.

| PR | Содержание / ownership | Зависимость | Оценка changed LOC | Оценка |
| --- | --- | --- | --- | --- |
| **1. Literal/native protocol foundation** | Swift parser/capability/receipt envelope + focused Go sender handshake seams, legacy decode fixtures | Актуальный main | 700-1 200 | 🎯 9 · 🛡️ 9 · 🧠 4 /10 |
| **2. Callback-safe managed runtime** | `codexsetup`/native install helpers, capability-aware shell installer, common ownership transaction и config no-clobber migration | PR1 + Preflight 0; build-baseline prerequisite при новых Go API | 1 300-2 000 | 🎯 8 · 🛡️ 9 · 🧠 7 /10 |
| **3. Shared structured delivery + Codex native action** | `notification`, `notifier` legacy adapter, Swift typed target/discovery/async executor | PR1 + Preflight 0; activation требует PR2 | 1 300-1 900 | 🎯 9 · 🛡️ 9 · 🧠 6 /10 |
| **4. Explicit use case + journal + CLI** | `agentnotify`, journal, policies, `notify`, isolated tests | PR2 + PR3 | 1 300-1 900 | 🎯 9 · 🛡️ 8 · 🧠 6 /10 |
| **5. MCP adapter + context qualification** | SDK/toolchain, MCP composition, origin adapters, transport tests | PR4 | 850-1 400, dependency sums отдельно | 🎯 8 · 🛡️ 8 · 🧠 6 /10 |
| **6. Existing installer opt-in + skill** | Managed client entries, status/setup UX, Claude/Codex activation, install docs | PR2 + PR5 | 850-1 400 | 🎯 8 · 🛡️ 9 · 🧠 6 /10 |
| **7. Portable Agent Plugins 1.0 package** | Manifests, same runtime contract, UAP adapter/compat smoke | PR6 | 450-850 | 🎯 8 · 🛡️ 8 · 🧠 4 /10 |
| **8. Release qualification** | Недостающие focused fixtures/native E2E evidence/docs и найденные bounded fixes | PR6; portable release требует PR7 | 300-700 плюс выявленные дефекты | 🎯 8 · 🛡️ 9 · 🧠 5 /10 |

Общий порядок величины: 7 150-11 650 changed LOC (включая bounded Preflight 0) с проверками/документацией, без lock/generated files. Это предварительная сумма диапазонов, не обязательный scope/объём работ: не писать код ради оценки и не превращать installer в универсальную платформу. Часть code extraction переносит существующие строки и не является новым production code. После PR1-2 оценку пересчитать по фактическому patch; не собирать mega-PR. Нижний полезный checkpoint - literal bug fix; первый рабочий новый vertical slice - CLI в PR4; готовый основной пользовательский путь - PR6 после installed E2E. Portable route может поставляться следующим PR, не задерживая уже доказанный существующий installer.

### PR1: нативная граница

- Исправить help parsing positional способом; сохранить старые legit help и legacy CLI commands.
- Добавить pure typed payload/result/capabilities codecs и bounded validation, explicit malformed/version reason.
- Зафиксировать `--capabilities-json` как side-effect-free. Structured send завершается valid receipt; legacy send не обязан менять формат stdout.
- Gate: actual Swift tests по 8 исходным legacy fixtures + новые unknown version, literal control words во всех полях, byte boundaries/malformed UTF-8, correlation mismatch. Spy backend доказывает отсутствие send/bell на invalid input.
- Acceptance: существующие hooks по старым args продолжают работать; `--help` как content больше не вызывает успешную неотправку; новый протокол сам ещё не активируется установщиком. Полезный PR можно merge сразу после focused проверки.

### PR2: безопасная доставка runtime

- Ввести небольшой общий commit path shared component, kernel lock и recoverable ownership transaction; сохранить точные старые hook commands. Добавить узкое no-clobber/CAS исправление config migration и отдельный authoritative explicit policy file (§4.2).
- Исключить живой `bin`/новый native bundle из destructive two-rename promotion. Добавить atomic swap/file replacement и recovery evidence.
- Upgrade сначала проверяет offline manifest/fingerprint старого notifier. Неизвестный/legacy binary не запускается с capability flag; только verified protocol-aware artifact допускает bounded direct probe (§7.1). Одного `isExecutable` недостаточно; sender несовместимого action не активируется. Не blanket overwrite config/assets пользователя.
- Gate: disposable filesystem fixtures с fault injection перед/после каждого promotion boundary; actual concurrent install/install, add-consumer/final-uninstall, config migration/disable; idempotent repair, old/new assets, несколько consumers, foreign hooks, independent uninstall и forced downgrade. Разрешение ОС/notification center здесь stubbed.
- Acceptance: serialized callback paths managed fixture разрешаются во всех поддержанных crash states; либо до activation сохраняется полностью старый рабочий набор. Native actual queued-upgrade остаётся release gate, не подменяется filesystem test.

- Дополнительные обязательные PR2 cases: сохранение opt-in при repair; nested destination link/parent substitution; mutable legacy config при adoption; adapter-level writer-floor spies; обновление при старом executable helper и смене basename; A -> B -> qualified retirement -> C; crash внутри purge. Эти проверки относятся к соответствующим контрактам §4.2/§8, а не откладываются целиком на GUI E2E.

### PR3: общая доставка и новый action

- Выделить literal presentation/delivery DTO. `SendDesktop` оставить adapter для старого analyzer/status/bracket wrapper; не передавать новый body в `extractSessionInfo`.
- Typed Codex target строится только trusted adapter. Implement NSWorkspace discovery, click-time identity checks, async completion и action allowlist.
- Новая доставка содержит deadline/receipt/fallback policy; known unsupported required target отказывает до send. Существующая hook policy не переписывается на агентскую.
- Gate: legacy title/subtitle/templates/status/sound/clickToFocus snapshots, no-action when disabled, new payload roundtrip, malformed/unknown action, late/double completion, app ambiguity/move/signature mismatch, unknown submit без retry. Бэкенды и app launching в unit tests stubbed.
- Acceptance: новая API готова к вызову без hook classification; manual native integration только на новых тестовых чатах/owned runtime после PR2. Старые actions декодируются; новый action не требует Go MCP/файла в cwd.

### PR4: первый новый vertical slice

- Реализовать §4/§6: service, validation/policy, private state root, scoped digest, journal, CLI JSON stdin/output. Test fixture context и caller_asserted CLI context различаются явно; ни файл, ни модельное поле не создают client_metadata provenance.
- CLI one-shot закрывает resources один раз, MCP composition ещё не требуется. Зафиксировать ownership stdin и ресурсов: закрытие owned input должно прерывать незавершённый Read, cancellation watcher завершается до выхода; borrowed backend/output adapter самостоятельно не закрывает. Production composition отвечает за bounded завершение при blocked pipe/socket stdout и сигнале, а не полагается только на cooperative fake writer. Обычные file redirections и `/dev/null` поддерживаются отдельно: Go deadline не обещает прерывание зависшего filesystem syscall; это ограничение фиксируется без detached I/O workers. Borrowed descriptors не закрываются; при дублировании учитывать общие file-status flags и восстанавливать внесённые изменения после завершения всех owned streams. Gate: actual subprocess на macOS с files/null, blocking/nonblocking pipes, aliased stdout/stderr, setup failure и cancellation; Linux PASS не заменяет Darwin qualification. EOF отделяет единственный stdin JSON request; второй объект/trailing data отклоняются. Deadline отправки начинается после полного ввода согласно §4.4; до EOF effect запрещён, отмена незавершённого ввода остаётся доступной. `notify --help` не отправляет; literal body не становится flag.
- Gate: actual Go multi-process/crash tests journal; disabled/corrupt settings/full state; two chats same cwd, equal callId, repeat/conflict/new same-body requests; request cancel/deadline, no webhook/transcript access, detached audio compatibility. Нативный backend fixture считает эффекты.
- Acceptance: CLI может отправить одно авторизованное уведомление в sandbox, receipt честный, повтор key не вызывает второй effect. Нельзя объявлять user-facing feature готовой, пока нет client origin/установки.

### PR5: MCP и provenance

- Добавить SDK поверх уже согласованного build baseline (если он ещё не изменён, prerequisite входит в этот этап), bounded transport, `notify`/`notification_status`, stderr-only composition, admission/drain/close.
- Codex request `_meta` используется напрямую; explicit model target отвергается. Не стирать source/interface/parent unknown fields через hook normalizer.
- Использовать registration/context contract Preflight 0 и проверить его на production SDK adapter, включая shared CLI/Desktop и ограничения §5. Отсутствие Claude context не подменять одним фактом установки skill.
- Gate: actual SDK server/client initialize/tools/list/tools/call, two sessions/request contexts, concurrent calls/EOF/cancellation/malformed frames, stdout purity, repeated calls после чужого completion. Reuse local mock Responses fixture без облачной модели для автоматической проверки модельного execution path.
- Acceptance: real app-server -> production MCP handler -> spy/native fixture сохраняет target A/B. Test-only approve для harmless fixture не переносится в пользовательские настройки; approval denial проверяется отдельно.

### PR6: основной install UX

- Реализовать единый configure flow §8.1.2 через существующие bootstrap/init/setup-codex; сохранить низкоуровневые setup операции. Добавить explicit opt-in в существующий installer/setup/status; доставить MCP config/skill assets через точные allowlists. Разрешения ОС запрашиваются только явным setup действием.
- Managed entries не дублируются при reinstall/path change, foreign config сохраняется. Refresh/activation-required отражается отдельно от installed. Нет auto-opt-in на upgrade.
- Обновлять только owned transport fields с expected identity/CAS; сохранять `enabled=false`, `disabled_tools`, timeouts, env и неизвестные настройки того же MCP entry. Повторный `codex mcp add` не считать безопасным updater: сохранённый изолированный spike CLI 0.152.0 показал потерю этих полей. Изменённые пользователем command/args означают конфликт, а не implicit takeover. No-op сохраняет исходные bytes; при изменении форматирование/comments сохраняются только если это обеспечивает выбранный editor, иначе ограничение документируется. Gate: disabled entry -> path update -> всё ещё disabled; concurrent edit -> conflict без потери чужих данных; global и plugin registration не активируются одновременно.
- Skill описывает request_id, unknown outcome, полезную частоту, текущие capabilities и отсутствие вывода completed по факту отправки уведомления.
- Gate: offline/mock installer tests, repeat/repair/upgrade/rollback/uninstall, concurrent config edit, Claude+Codex consumers. Затем настоящий model-selected production tool из установленного Desktop в двух новых тестовых чатах -> остановка MCP -> удаление temporary workspace -> ручные клики. Эффекты только в тестовой установке.
- Acceptance: пользователь проходит понятный existing-installer flow, видит инструмент и корректно возвращается в A/B. Это первый полностью работающий основной способ установки. Если Claude context probe не даёт надёжного session mapping, его explicit capability остаётся ограниченной, существующие hooks работают.

### PR7: portable package

**Portable runtime locator:** по [Agent Plugins 1.0, §9](https://agent-plugins.org/specification) обязательны `PLUGIN_ROOT`/`PLUGIN_DATA`, но не ambient `HOME` или client identity; `PLUGIN_DATA` может удаляться при uninstall. Launcher использует проверенный setup-owned locator либо OS home API; callback runtime и journal остаются вне removable package state. Не делать package-relative symlink наружу вместо launcher. Выбор integration задаётся квалифицированной client projection/extension либо доказанным per-call adapter, без угадывания клиента по env/cwd. Gate: запуск с минимальным разрешённым environment, пути с пробелами, отсутствие locator, cache/data cleanup и отсутствие двойной MCP регистрации. Эти проверки не считаются пройденными по одному schema validation.

- Добавить Agent Plugins 1.0 manifests и compatibility adapters без копии use case/skill logic и без неготового authoring SDK.
- Gate: schema validation, pinned-SHA UAP install, package/root manifest precedence, install method collision/takeover, cache cleanup, no duplicate hooks/MCP и сохранение native callback после удаления source cache.
- Acceptance: оба install routes доставляют один и тот же runtime contract и одинаковые tool capabilities; package installed не подменяет client activation.

### PR8: квалификация и draft

- Исполнить только оставшиеся native/install scenarios §11. Исправления находок привязывать к соответствующему invariant и review отдельно, если объём выходит за bounded qualification PR.
- Собрать draft артефакты и проверить exact binaries/signature/architecture/capabilities/legacy payload compatibility. Не публиковать релиз и не включать updater distribution.
- Acceptance: заполненная support matrix с версией, test environment и ссылкой evidence для заявленных возможностей; прочие режимы честно unavailable/experimental. Отдельное разрешение пользователя требуется только для публикации конкретной готовой версии.

## 11. Проверки, которые ещё нужны

Все тесты agent/runtime/hooks выполняются в новых sandbox/test projects. Clone repo сам по себе не изолирует HOME/config/state/IPC. Harness должен инжектировать config/state/log/temp roots, CODEX_HOME, XDG roots и private IPC; registry, D-Bus, app launch/audio в обычных тестах stubbed. Не запускать installer/terminal/runtime на реальном проекте или заменять пользовательскую установку.

| Gate | Что именно проверить | Где / prerequisite |
| --- | --- | --- |
| Full production Desktop E2E | Живая модель выбирает установленный tool, A/B same cwd, правильная metadata, оба receipts; остановить только тестовый MCP, удалить временный cwd, кликнуть в другом порядке, подтвердить видимый target | PR6, новые тестовые чаты и disposable plugin installation; ручной UI |
| Cold start | Notification до штатного закрытия тестового Codex; callback запускает именно выбранное приложение и открывает ID, отдельная проверка permission/foreground | Выделенный macOS test account/VM/хост. Не завершать рабочий Codex пользователя ради теста |
| Upgrade/rollback | Old sender/new reader, new sender/old reader rejection, already queued v1 после main rollback; клик во время/после managed update и source-cache cleanup | PR2+3, отдельная тестовая native installation; actual codesigned draft artifact |
| Crash recovery | Прерывание installers в конкретных boundaries, старый serialized path, interrupted native swap, interrupted config write и later foreign changes | Автоматические filesystem faults, затем один native canary на поддержанном пути |
| Permission/DND | Undetermined -> activation-required без prompt, denied -> no fallback, setup approval отдельно, DND может дать submitted без баннера | Unit seams + отдельный macOS test user, не менять реальные настройки пользователя |
| Missing/stale target | Несуществующий/архивный/удалённый чат, sign-out/profile switch, отсутствующая/перемещённая app, 2 копии, неизвестный button | Pure fixtures и выбранные ручные native tests; отсутствие profile API документировать, не симулировать detector как факт |
| Multi-window/Spaces | Target видим хотя бы в поддержанном окне, scope не обещает исходное окно; приложение было не foreground | Native test setup после основного single-window gate; если не пройден, limitation в matrix |
| Context variants | CLI vs Desktop shared config, code-mode, hidden subagent, parent unknown, remote worker | Сперва read-only/context-only fixture; проверить наблюдаемые причины unavailable и общий local routing opt-in для неотличимых callers, без ложного CLI/parent detection |
| Literal/limits | Все управляющие-looking строки как data, UTF-8 byte границы, large/malformed frame, NUL/controls, errors не логируют body | Go/Swift unit + protocol integration |
| Idempotency | Two processes same key, conflict, repeated equal body/new IDs, crash before/after send, late CAS, retention/capacity/clock/corruption | Actual Go processes, injectable native effect counter, local fs |
| Lifecycle | Early/late cancel, EOF одновременно с request, delayed native ack, double completion, per-call no Close, child process reaping, config snapshot | Focused `-race` + deterministic stubs; не нагрузка на пользовательский Desktop |
| Legacy | Claude keys byte-for-byte, Codex turn scope, status/cooldown/transcript behavior, templates, webhook immediacy/delayed desktop, sound/bell/focus, disabled options | Existing suites + characterization around moved code |
| Linux/Windows | Сборка и старые hooks сохраняются; explicit delivery/navigation claims только после отдельных OS click tests | Соответствующая CI/тестовая ОС; Linux in-memory daemon не считается durable callback |

Нельзя складывать успех synthetic lifecycle и metadata spike в готовый full production E2E. Manual observation привязывается к notification ID, target ID, artifact version и timestamp, без реальных сообщений пользователя. UI-инструмент запретил автоматический доступ к Codex; проверять вручную или в поддержанной тестовой среде, не обходить запрет через AppleScript/CDP.

## 12. Проверки по коду и процесс выполнения

Focused команды подставляются только после появления соответствующих packages, в отдельном test checkout с изолированными roots. Не исполнять их из этого плана на пользовательском runtime:

```sh
go test ./internal/notification/... ./internal/agentnotify/...
go test -race ./internal/agentnotify/... ./internal/notifier/... ./internal/hooks/...
go test ./internal/codexsetup/... ./cmd/claude-notifications/...
swift test --package-path swift-notifier
```

Installer shell tests сначала прочитать на предмет isolation и исполнять поддержанный offline/mock режим. Existing CI gates (`go vet`, formatting, unit/race, OS builds, Swift tests, installer tests) сохраняются. Не заменять их исключительно extraction harness. Тяжёлые builds/race выполняются на configured hosted test workers/CI, локально лёгкое чтение и интеграция. Mac GUI gates требуют соответствующего тестового Mac, Linux host их не доказывает.

Перед каждым writer определить exact base SHA и bounded ownership. Рабочие реализации: Astra low; независимый architecture/review обычно Astra medium/high по риску. Подготовка этого плана по прямому запросу выполнена Astra xhigh. Workers не должны откатывать чужие изменения и не используют реальные проекты для smoke. Нет обязательных cooldown/ритуального повторного CI; сохраняется evidence на SHA.

Коммиты conventional; связанные issue указываются `Refs #N`, если issue существует. Branch names `fix/...`, `feat/...`, `refactor/...`, без `codex/`. После merge основания retarget/rebase stack по факту, сохраняя coherent reversible PR. Независимые lanes допустимы только с непересекающимся ownership; native/protocol и journal можно готовить параллельно после фиксации DTO, installer activation зависит от compatibility.

## 13. Реестр оставшихся неопределённостей

| Вопрос | Решение сейчас | Что изменит решение |
| --- | --- | --- |
| Почему default open не переключал visible chat | Root cause unknown; использовать explicit verified application + NSWorkspace | Native failure на production adapter, новые route/focus evidence |
| Как отличить Desktop от CLI при shared MCP config | Не фабриковать provenance; scoped local routing policy, проверить actual registration boundary | Подтверждённый client interface metadata или isolation API |
| Profile/parent/remote mapping | Не изобретать; scope current local profile, доказанно hidden/remote required unavailable. Неотличимый caller подчиняется общему local routing opt-in без parent/store guarantees | Документированный/исполнением доказанный клиентский контракт |
| Claude per-call session для MCP | Узкий probe, затем supported adapter; session-scoped CLI/skill как честный путь при отсутствии metadata | Реальный client context evidence. Hooks это не блокирует |
| Atomic native swap на целевых filesystem | Выбран Darwin rename swap, отказ обновления при unsupported; нужна actual fault qualification | OS/filesystem тест, доказавший отсутствие нужной semantics |
| Старые чужие cache callback paths | Гарантия только для managed paths и новых structured actions | Явный manager contract retention/migration |
| Latest SDK/toolchain и поддержанные старые ОС | SDK official v1.7.0 требует Go1.25; proposed explicit build-floor migration | Fresh stable/version/OS compatibility check перед dependency merge |
| UAP package activation | Второй route после существующего installer; SDK не добавляется | Actual sandbox package install/activation и свежий schema contract |
| Клики после смены профиля/архивации | Нет обещания автоматического обнаружения/восстановления, не создавать чат | API target availability/profile query без чтения credentials |

Эти пункты не требуют остановить всё ради заранее идеальной платформы. PR1 можно делать независимо; PR2-4 опираются на bounded Preflight 0 и принятые контракты. Неопределённость конкретного client/OS ограничивает его capability и соответствующий release claim.

## 14. Критерий завершения функции

- [x] Пройден основной existing-installer flow §8.1.2 с explicit opt-in, обоими порядками установки клиентов, partial-failure recovery, без дубликатов и изменения чужих настроек. Evidence: isolated flow plus `TestNotificationBootstrapOffline`, `TestNotificationInitOfflineBranch`, `TestNotificationConfigureParserAndSetupOptIn`, `TestSetupCodexE2EConfigureNotifications`; bootstrap configure uses committed Codex runtime after staging delete; `docs/evidence/agent-notify/plan14-local-e2e-2026-09-11.md`.
- [ ] Production tool действительно вызван из установленного local Desktop и возвращает правильный source target. Не доказано: рабочий Codex нельзя завершать, CLI/app-server/subscription запрещены, нет disposable Desktop-профиля.
- [ ] Ручные A/B клики после остановки MCP/удаления cwd открывают правильные чаты; evidence относится к exact artifact. Не доказано: клик в dedicated test profile; banner/`os_accepted` ≠ click routing. Opt-in send использует shared `com.claude.desktop.notifier`.
- [x] Literal payload, native capability/receipt, stable callback и compatibility работают в actual implementation tests. Evidence: `TestPR3LiteralBytesAndBounds`, `TestPR3DeliveryLiteralReceiptAndSnapshot`, `TestLiteralAndReceipt`, isolated-flow `--capabilities-json` после A→B.
- [x] Unknown/crash/replay не вызывают автоматических дублирующих side effects; store corruption/fullness fail closed. Evidence: `TestReplayIdentityAndCAS`, `TestActualCrashBoundaries`, `TestSDKServiceDurableReplayAndScopedOutcomes`, isolated-flow replay.
- [x] Hook regression gates пройдены, отдельное отключение новой функции не ломает hooks. Evidence: `TestHandler_NotificationsDisabled`, isolated-flow disable byte compare.
- [x] Cold-start и managed upgrade/rollback qualification пройдены для заявленного supported режима; остальные ограничения отображены честно. Supported here: exact-head generations A→B, alias retarget, helper `open -a` of `com.agentnotify.test.flow`, rollback inode reuse, retire without deleting published A/B. Not claimed: Codex-quit cold-start, production-bundle NC click after update.
- [x] Portable package проверен отдельно перед обещанием этого install route. Evidence: UAP `portable-launch` production MCP, reverse handoff, `TestUAPProjectedCodexLaunchRunsProductionMCP`. Live client UI activation не заявлена.
- [x] Draft артефакты готовы; публикация конкретной версии ожидает отдельного явного разрешения владельца. Evidence: `docs/evidence/agent-notify/plan14-local-e2e-2026-09-11.md`, `docs/evidence/agent-notify/draft-qualification-2026-09-11.md`. Publish не выполнялся.

Реализация начинается с bounded native literal/protocol PR и Preflight 0. Доказанный installed local Desktop путь в PR6 является готовностью основного способа установки, но не завершением всего этого плана. Для завершения плана нужны все пункты §14, включая portable route, cold-start, managed upgrade/rollback и квалифицированные draft артефакты. Незакрытыми остаются live Desktop tool invocation и ручные NC A/B клики. Platform/remote/general parent routing остаются вне initial scope и не маскируются общей галочкой готовности; публикация draft не входит в критерий завершения и требует отдельного разрешения.


## 15. Исправления повторного review 2026-09-10

План проверен основным агентом и независимым hosted Astra high reviewer на исходном SHA. Найдены семь предметных замечаний к исходному плану, все учтены; это review спецификации, а не успешные тесты ещё не существующего кода. [Независимый отчёт](/Users/belief/.codex/visualizations/2026/09/08/01a07f59-fd55-7f82-a9f9-561474574fa5/agent-notify-spikes-20260910/plan-review/independent-review.md) относится к исходной нумерации строк.

| Замечание | Исправление | Обязательный regression scenario |
| --- | --- | --- |
| P1 mutable route/installation меняют dedup identity | §6: stable state namespace + caller payload digest, target сохраняется при первом admission | unknown(A) -> config B/install takeover -> repeat R: ноль новых effects, прежнее решение A |
| P1 request_id против callId не имели приоритета | §4: explicit > callId > generated, typed namespaces; tracking ID отдельно от caller ID | R при C1/C2 -> replay; разные R при одинаковом C -> разные admissions |
| P1 clock forward/reboot сокращают retention | §6: только доказанные monotonic elapsed intervals в boot epoch | wall clock +8 дней через минуту и reboot: свежий key не удаляется |
| P1 поздний LaunchServices send и файлы после timeout | §7: not-after от исходного admission, payload ownership, atomic receipt, honest unknown/cleanup | caller died -> delayed read/expired request/late receipt; no fresh deadline и no reuse чужого payload |
| P2 hook migration затрёт opt-in/global disable | §4/PR2: отдельная explicit policy, strict global source, no-clobber migration + config CAS | migration одновременно с disable; malformed stable не заменяется legacy автоматически |
| P2 context feasibility отложена после дорогих шагов | Preflight 0 до PR2-6, честный CLI caller_asserted boundary | настоящий registration scope/metadata определён до фиксации большого installer/target API |
| P2 разные installers могут захватить одного owner | §8/PR2: общий component kernel lock, generation/recovery transaction, lock order | install/install и add-consumer/final-uninstall сохраняют корректный owner и consumers |

Дополнительно уточнены process-wide callback drain/idle watchdog, ограничение сырого MCP frame до JSON allocation, явный isError mapping, lifetime старых native processes при swap, семидневная ёмкость журнала и отсутствие обещания CLI trust от отдельного context file. Эти коррекции добавлены в соответствующие implementation gates, а не объявлены выполненными.

План пригоден для bounded Preflight 0 и самостоятельного PR1. Приступать к остальным этапам по dependency gates; full installed Desktop E2E, cold start, queued upgrade/rollback и portable activation всё ещё должны быть доказаны actual implementation. Никаких новых user approvals или ритуального повторения уже пройденных спайков этот review не вводит.

### Дополнительная проверка согласованности

- Общий request deadline начинается до validation/preflight, включает ожидание journal lock и передаётся native как оставшийся budget. Regression: занятый lock или медленный permission probe исчерпывает бюджет без позднего нового submit; idle MCP connection не закрывается по notify timeout.
- PR6 является готовностью основного install route, а не основанием закрыть весь план. Завершение всего плана проверяется по полному §14; успешные старые спайки не заменяют production cold-start/upgrade/portable qualification.

### Уточнения последней проверки контракта

- Read-only status больше не отвечает за очистку spool или скрытое создание/migration state. Gate: снимок файлов до/после status при missing/corrupt state не меняется.
- Убрано противоречие PR2 с §7.1: старый/неизвестный notifier нельзя запускать ради discovery. Gate: legacy executable spy получает ноль запусков, verified новый probe имеет deadline.
- Unicode contract сохраняет ZWJ/ZWNJ/emoji tags и точные bytes. Gate: общие Go/Swift fixtures для joined emoji, персидского текста, C0/C1, U+2028/U+2029 и byte limits; canonical digest меняет только representation/defaults, не caller text.
- MCP имеет отдельный предел входящей работы, помимо submit concurrency. Gate: flood валидных frames при зависшем backend сохраняет bounded memory/workers, обслуживает cancellation и не оставляет отложенные effects после deadline. Проверяется на выбранном SDK adapter, не на отдельной модели semaphore.

Эти уточнения являются требованиями к реализации. Новых результатов native/installed E2E данная проверка не добавляет.


## 16. Дополнительная проверка границ реализации

Повторное чтение текущего source и сохранённых candidate review выявило недостающие условия acceptance. Исправлены §7.2 и §8.2-8.3:

| Риск | Зафиксированное требование |
| --- | --- |
| Timer стоит на main queue вместе с блокирующей signature/discovery проверкой | Bounded async preflight; после deadline не начинать новый open; уже переданный системе open имеет unknown outcome; зависшая операция продолжает занимать slot |
| Callback завершён, но legacy child ещё исполняется | Раздельные completion/drain, owned cancellation и reaping, без позднего activate |
| A/B diagnostics невозможно связать с уведомлением | Валидированный correlation UUID, received + ровно один terminal event |
| Installer и sender отдельно проходят тесты с разными manifest | Общий контракт и production producer -> consumer integration fixture; post-signing hash вне sealed resources |
| Symlink заменяет permanent lock | Descriptor-based no-follow/type/owner проверки, неизменный lock inode |
| Disable/uninstall происходит после preflight | Generation fence перед native handoff под component lease, без вложенного journal lock |
| Rollback сохраняет decoder, но старый writer затем удаляет его | Managed writer floor и тест rollback против нового state |

Это уточнения плана, а не закрытие implementation findings. Сохранённый native review относится к patch SHA-256 `7546e8a7a19cce9d7754d56a514ad0ba6c107303cc8646a1871cc8d858674ae7`; lock failure к раннему candidate `62fa730b7df89a03d31a1e19c9df624949d9b81011899ad7683713d2ea60668b`. Более поздние source changes могут уже исправлять часть замечаний; их готовность требует проверки собственного exact artifact. Новые ручные клики, full installed E2E и cold-start этой проверкой не выполнялись. Подтверждения пользователя из §2 сохраняют исходные границы доказательства.


## 17. Review текущей сборки и уточнение acceptance

Независимый source review PR2 на patch SHA-256 `51b7cb856c4fb801760aa5135a622c3d6b969c4fafb66c4b3965ee9225a651aa` выявил восемь замечаний: сброс eligibility при repair, обход writer floor, nested destination symlink, перезапись mutable legacy config, отсутствие обновления имеющегося native helper, зависимость live path от basename, невосстановимая частичная purge и отсутствие retirement перед третьим native artifact. Семь воспроизведений подтверждают ошибочное поведение, а не успешную acceptance. Требования внесены в §4.2/§8/PR2; исправление текста не означает исправления source. Отчёт сохранён в controller worktree: `docs/evidence/agent-notify/pr2-assembled-review/independent-review.md`.

Readiness delta `fa87c858482a7e3ae6ca70c1d552f8c88215fa6e7ac935c84a7890e8f6740d06` прошла независимый source review без actionable findings. Отдельный Linux race run не дошёл до тестов из-за заполненного filesystem; результат inconclusive, не PASS. Перед повторным тяжёлым запуском проверять реальные свободные ресурсы и повторять только недоказанную проверку. Mac focused/probe evidence остаётся отдельным доказательством и не заменяет race или installed E2E.

Интеграция PR4 обязана передавать валидированные configurable rate limits из authoritative policy в единственную атомарную journal admission operation. Текущие hardcoded 6/session/min, 30/runtime/min и burst 3/2 sec не доказывают config support из §4.4. Нельзя создавать второй process-local limiter вместо общего journal. Проверить non-default limits в двух процессах, replay без расхода quota и изменение policy без сброса накопленных counters; одинаковые policy snapshots дают одинаковое решение.

Дополнение к §17: повторный focused Linux race readiness после восстановления свободного места прошёл (nativeprotocol 1.029s, notifier 3.272s), все 337 source hashes совпадают с reviewed assembly. Предыдущий inconclusive run сохранён как история; actual retry evidence: controller `docs/evidence/agent-notify/readiness-qualified/race-retry-evidence.json`. Это не installed Desktop E2E.


## 18. Итог текущей проверки плана

Дополнительно исправлены пять контрактных пробелов:

- Build baseline переносится перед первым использующим новые Go API PR; minimum проверяется без автоматического переключения toolchain.
- Ограниченный scope новой explicit-функции не разрешает сломать существующий Windows installer при подключении общего kernel.
- Durable callId отделён от JSON-RPC id/progressToken; reconnect не должен создавать ложный replay.
- Receipt сохраняется до чтения caller; native completion не даёт права удалить подтверждение раньше времени.
- Deadline запрещает новые open, но не обещает отмену уже переданного NSWorkspace запроса; такой timeout имеет честный unknown outcome.

Последние два замечания независимо подтверждены Astra high reviewer. Изменён только план; реализации, сборок и новых native/UI спайков в этом review не выполнялось. Предыдущие A/B клики остаются доказательством описанных в §2 сценариев, а не всего установленного продукта.


## 19. Уточнения транспортной границы после дополнительной проверки

- Разделены fatal wire errors с закрытием соединения и ошибки валидного запроса с protocol/tool response: acceptance больше не требует двух противоположных результатов.
- Status принимает отсутствие arguments и пустой объект; raw-wire тесты исключают скрытую нормализацию SDK client.
- Request/response/cancellation используют точные transport IDs; большие integer IDs не округляются через float64, remapping и blocked output остаются ограниченными по памяти.
- Retention распространяется на все admitted ключи, включая post-admission rejected/suppressed; GC не освобождает их досрочно ради места.
- CLI фиксирует EOF framing, ownership input/resources и отмену blocked I/O в production composition. Завершение backend не доказывает завершения процесса.

Основание для MCP cases: сохранённый отчёт `REPORT-MCP-FIX.md` в `/private/tmp/notification-agent-notify-preflight/mcp-fixed/` и описанные в нём actual SDK regressions. В этой проверке они повторно не запускались. Добавление требований в план не означает прохождение installed Desktop E2E или готовность CLI composition.


## 20. Уточнения установки и stdio после финальной проверки

В PR6 добавлены сохранение пользовательских MCP ограничений и запрет слепого обновления через `mcp add`; в PR7 - portable locator без гарантированного HOME и отдельная проверка выбора integration; в PR4 - поддержка file/null redirection с честной границей отмены filesystem I/O и Darwin subprocess gates. Основания: сохранённые `registration-update-spike/README.md`, `portable-contract-refresh/findings.md` в controller `docs/evidence/agent-notify/` и `REPORT-STDIO-FIX.md` в `/private/tmp/notification-agent-notify-preflight/stdio-final/`. Это уточнения acceptance, не новые результаты installed E2E. Независимая повторная проверка retention/EOF/deadline новых противоречий не выявила.


## 21. Проверка явного permission setup

Уточнён §8.1.1: независимые стадии setup, capability нового permission режима, работа до enable, постоянная bundle identity, отдельный bounded UI budget и защита helper при update. Основание: в текущем legacy source `PermissionManager.ensurePermission` вызывается из `checkAuthAndSend`; повторное использование всего этого пути не обеспечивает setup без отправки. Это требования и результат source review, а не новый успешный permission/UI spike.

Независимая проверка также выявила неоднозначность final uninstall -> reinstall: в §4.2 теперь явно отзывается opt-in при удалении последнего consumer, без удаления dedup history и без поломки сохранённых callbacks. Repair с существующими consumers по-прежнему сохраняет intent.

В §7.2 уточнена долговечность сохранённой app identity: callback не зависит от mutable route config или journal и не переназначается после выбора другого приложения. Добавлен сценарий смены app и удаления consumer/config перед кликом.


## 22. Проверка пользовательского подключения и informational-only

Уточнены §5.2 и §8.1: отделены согласие на неизвестного caller от настройки клика, подготовка mutable global config от enable/register, доставка canonical skill от его обнаружения клиентом. Добавлены adapter-level gates для обоих порядков установки и обновления owned skill.

Основание: чтение текущего `internal/config/config.go` и рабочей composition в `/private/tmp/notification-agent-notify-composition`: admission в `internal/agentnotify/service.go` требует LocalRouting даже перед веткой none; `clientsetup/README.md` явно отделяет core от CLI/activation и описывает сохранение projection при nil; `cmd/claude-notifications/install_runtime.go` содержит существующий hook Prepare, который нельзя перезаписать при добавлении skill. Это локальное source evidence на момент проверки, не утверждение о состоянии main или итоговом исправлении реализации. Исправлен план; новые installed/native/UI проверки в этом review не выполнялись.


## 23. Проверка orchestration и discovery

Независимый bounded review также подтвердил противоречие Claude session fallback с informational MCP; оно исправлено в §5.2. Уточнены §8.1/§8.1.2/PR6: единый удобный installer flow, shared route при двух клиентах, primary runtime как authority, различие Claude MCP config и global settings, частично завершённая настройка и finite collision preflight. Исправлено безусловное требование второй Codex skill projection: нужен один доказанный источник discovery.

Основания: сохранённый source review `docs/evidence/agent-notify/configure-flow-contract/REPORT.md`, actual isolated CLI observations `claude-user-config-path/result.json` и `codex-plugin-inventory/README.md` в `/private/tmp/notification-agent-notify`; повторно прочитан production `clientsetup/skill.go` в composition. Эти observations не доказывают effective cached skill/MCP discovery. §22 описывает историческое состояние admission: текущая composition уже отделяет none от LocalRouting; это не новый UI/E2E результат. В рамках данного review изменён только Markdown, повторные native эффекты не выполнялись.


## 24. Проверка acquisition, discovery и anonymous callers

В этой проверке исправлен только план:

- §8.2: acquisition ограничен новым staging output, не получает права перезаписывать live runtime; configure запускается из установленного bundle.
- §8.1: строгий parser отклоняет case-fold aliases owned config keys до defaults, сохраняя пользовательский opt-out.
- §8.1.2: discovery квалифицируется по реально запускаемому client executable/version и effective cache, включая отсутствие skill и source/cache drift.
- §4.4/§6.1: anonymous informational callers имеют общий явно описанный scope/rate bucket; skill использует уникальные explicit IDs для новых намерений. Независимый Astra high reviewer подтвердил риск пересечения коротких IDs двух Claude sessions.

Основание: прочитаны текущие `internal/config/setup_prepare.go`, `cmd/claude-notifications/notification_codex_inventory.go`, `internal/agentnotify/mcp/run.go` и `internal/agentnotify/origin/origin.go` в `/private/tmp/notification-agent-notify-composition`. Это source review и новые regression requirements, не утверждение об исправлении production implementation. Новые runtime/native эффекты и installed E2E в этой проверке не выполнялись; ограничения доказательства §2/§11 сохраняются.


## 25. Уточнение portable setup по исполненному UAP spike

Проверен UAP `6af7f412cb4a35d2e623fcba110f1fe53d9a2a5d`: настоящий Loader → Planner → PluginDataManager → Stager → transaction → Activator в sandbox. Evidence: controller `docs/evidence/agent-notify/portable-current-spike/qualified/`. Это materialization proof с inert executable и synthetic Claude listing, не actual client activation.

- Codex и Claude одной установки разделяют `PLUGIN_DATA`. Использовать отдельный bounded locator filename для каждой physical binding, выбранный явным setup input. Один `runtime.json` с перезаписываемым integration запрещён. Не выводить integration из `PLUGIN_ROOT`, cwd, HOME или MCP clientInfo.
- Root `mcp.json` является источником projected MCP; authored compatibility MCP не переопределяет его. Root hooks при этом projection удаляет. Существующий managed installer сохраняет единственное владение runtime/hooks (`existing-installer`); portable добавляет client discovery, а не второго component writer.
- Stock UAP CLI не исполняет package-authored setup hook для этих клиентов. Следующий bounded setup slice композирует закреплённый UAP Service через его Stager/Activator interfaces: locator selector вносится до materialization/digest; после committed binding/DataReceipt проверяются ownership и destination, затем locator публикуется и вызывается activation. Нельзя патчить manifest после digest или обещать, что stock `agentplugins add` один завершает notification setup.
- Launcher работает с гарантированными `PLUGIN_ROOT`/`PLUGIN_DATA`, без HOME/PATH: строгий private locator, установленный ledger/lease и typed runtime options. Primary executable выводится из проверенного ledger. Locator не содержит произвольной команды и не становится отдельным источником ownership authority.
- До открытия portable discovery удаляются только CAS-подтверждённые owned global MCP/user-skill surfaces выбранного клиента. Hooks и consumer сохраняются. При сбое допускается явно восстанавливаемый discovery gap, но не молчаливое дублирование. Обратный переход сначала подтверждает отключение portable discovery. File absence не доказывает, что уже загруженная клиентская сессия прекратила использование старой регистрации.
- Удаление одного portable binding отзывает его locator/consumer, сохраняя другой binding и общий data/runtime/journal. Прямой внешний UAP uninstall не имеет notification callback: stale consumer исправляется явным managed reconciliation, без вывода о последнем consumer по отсутствию cache.

Зависимые deliverables: (1) locator/primary entrypoint; (2) pinned setup composition и owned discovery handoff; (3) full-SHA artifact install, настоящая client activation и portable native E2E. Первый пункт отдельно не закрывает PR7. Dependency pin и supply checks выполняются перед добавлением модуля; spike не является разрешением использовать незакреплённый UAP main.

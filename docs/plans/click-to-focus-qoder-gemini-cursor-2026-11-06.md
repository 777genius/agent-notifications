# План: Click-to-Focus для Qoder CLI, Gemini CLI и Cursor CLI

**2026-11-06 · Proposal for adversarial review · Только план, без реализации.**
Исходная база: main 896206c14ea538ff2c9d5499f3ca7163cd28091f.

## 1. Scope и решение

Цель — открывать **исходное локальное окно терминала**, когда пользователь нажимает на уведомление Qoder CLI, Gemini CLI или Cursor CLI; при проверенной нативной поддержке можно активировать конкретную вкладку/панель. Переиспользуем существующие OS focus/callback компоненты, без трёх реализаций.

- **OpenCode V1/V2 исключён полностью.** Не разрабатывать server/TUI navigation, не менять его consumer, installer и поддержку.
- Claude Code, Codex CLI и Pi уже имеют свои маршруты; только регрессии общего кода. Отдельные физические E2E-долги Pi [#561](https://github.com/777genius/agent-notifications/issues/561) остаются.
- Qoder custom audio [#562](https://github.com/777genius/agent-notifications/issues/562), новые события, exact desktop chat, Copilot, Kiro, новый install flow — **отдельные задачи**.
- Не внедрять feature flags, новые БД, очереди, службу фокусировки, session registry, универсальную модель агентских событий, новый daemon, массовый рефакторинг или schema migration.
- **Никакого «последнего активного окна»**, поиска по одному cwd/title, подмены session ID на Codex Desktop thread ID, удалённого/облачного фокуса.

**Три независимых факта:** ОС приняла уведомление ≠ пользователь увидел баннер ≠ клик активировал *правильное* исходное окно. План и тесты различают их.

## 2. Evidence из текущего кода и upstream

| Агент | Реальный путь | Почему пока не поддержан Click-to-Focus | Первая проверка |
| --- | --- | --- | --- |
| **Qoder CLI** | internal/hooks/qoder_source.go -> hooks.Handler -> internal/notifier/notifier.go | Уже использует legacy notifier с focus, но Ghostty ID capture намеренно Claude-only; [docs/QODER.md](../QODER.md) прямо говорят, что точность/работоспособность не квалифицированы | Проверить настоящий installed Qoder: возможно оконная активация уже работает, тогда нужны тесты и только точечное исправление |
| **Gemini CLI** | geminisource -> geminievent.Consumer -> notification.InformationalDesktopRequest | Navigation=None; факт session_id не связан с OS window; TTL и lease принадлежат отдельному observer | Проверить происхождение child hook и окна на установленном Gemini CLI для AfterAgent/ToolPermission |
| **Cursor CLI** | UAP-selected native Stop -> cursorsource -> cursorevent.Consumer -> InformationalDesktopRequest | Navigation=None; ConversationID/GenerationID/Stop status не являются OS target; Cursor IDE != Cursor CLI | Проверить только реальную локальную Cursor CLI с выбранным binary/profile и remote/headless отрицательными случаями |

Существующая инфраструктура: internal/notification/delivery.go (Request, DesktopTarget, DeliveryPort, PolicySnapshot, Receipt), internal/notifier/send_context_* и focus_*; Linux internal/daemon/focus*, Windows internal/winfocus, signed macOS helper. **DesktopTarget.ThreadID — проверенный маршрут Codex Desktop, не универсальный ID чата.**

Официальные документы подтверждают наличие событий, но **не** точного происхождения терминала на конкретном релизе:
- [Gemini CLI hooks](https://geminicli.com/docs/hooks/reference/): SessionStart, AfterAgent, Notification; session_id/cwd; event AfterAgent означает окончание хода, а не успешную задачу.
- [Cursor hooks](https://cursor.com/docs/hooks), [Cursor CLI](https://cursor.com/docs/cli/overview): stop и переменные проекта; некоторые страницы описывают также IDE/remote. Из IDE docs нельзя выводить поддержку в реальной CLI автоматически.
- [Qoder CLI hooks](https://docs.qoder.com/cli/hooks): локальные hook subprocess, session_id/cwd, Stop/StopFailure; это не подтверждение target.
- [Текущая support matrix](../AGENT_SUPPORT_MATRIX.md), [платформенные ограничения](../CLICK_TO_FOCUS.md).

## 3. Конкретная минимальная архитектура

1. **Capture only a proven local origin** в уже доверенном hook process после текущей проверки выбранной установки и прав. OS target/UID, процессное происхождение, профиль/поколение и route должны быть независимо проверены. TERM_PROGRAM, WT_SESSION, TMUX_PANE, WARP_FOCUS_URL, WINDOWID, PID, session ID, cwd или title — только hints, не полномочие по отдельности.
2. **Не менять источник событий и политику доставки.** Qoder остаётся через hooks.Handler/legacy notifier. Gemini и Cursor остаются в своих typed consumers и lease/recheck/cache/consent. Сохранить NotAfter deadline, single-effect semantics, no retry after unknown native result, независимые webhook/sound настройки.
3. **Qoder:** сначала characterization текущего focus в installed CLI. Важный отдельный guard: legacy hooks.Handler / notifier не интерпретирует notification.Navigation=None. Если origin неизвестен/не прошёл проверку, нужен **per-notification no-focus** (например, узкий SendOption по образцу существующего WithoutSound), который запрещает все OS focus/activation actions **при сохранённом баннере**. Не мутировать общий Config.ClickToFocus, не отключать desktop channel; macOS helper не получает -activate/-execute, Linux daemon не получает focus hints/action, Windows toast не получает activation URI. Независимо проверить, что legacy fallback тоже не переключает окно. Не заимствовать raw Claude-only Ghostty session capture.
4. **Gemini/Cursor:** только после позитивного proof-of-origin добавить **узкий terminal-target маршрут** к уже существующему desktop delivery/OS callback. Такой target должен быть отделён от Codex Desktop chat; не переделывать старые агенты или OpenCode. Если native adapter принимает достаточную цель без новой публичной структуры — предпочесть малый adapter.
5. **Снимок клика фиксируется при доставке**, живёт до click после смерти sender, проверяет owner/установку/поколение и корректно отказывает при отозванном разрешении, чужом окне, повторно использованном PID, update/remove. Не искать «текущее» окно в момент клика. **Windows:** существующий legacy `winfocus.FocusContext.EncodeURI/DecodeURI` содержит изменяемый base64url JSON (`HWND`, `PID`, title, folder, WarpURL), а `runFocusWindows` декодирует и активирует его без криптографической проверки исходного уведомления. Поэтому **не подключать Qoder к этому Windows callback** без доказанного integrity/authorization binding к исходному снимку: сначала проверить возможность безопасно переиспользовать уже существующий защищённый callback; если его нет — минимальная scoped защита и tamper-negative E2E, либо оставить Qoder Windows click неподдерживаемым. Не переписывать все legacy-маршруты в этом scope.
6. **Политика:** при remote/headless/unknown origin оставлять Navigation=None и обычное уведомление. Навигация BestEffort только если доказан local target; при невозможности безопасно сфокусировать — не переключать ничего. Не менять Required/None семантику существующих агентов.
7. **UX:** для **новых** Qoder/Gemini/Cursor click-маршрутов `global clickToFocus=false` — абсолютный veto даже при `agents.<agent>.notifications.desktop.clickToFocus=true`. Проверять raw global setting и новое явно подтверждённое per-agent согласие; одного merged effective boolean недостаточно. Veto действует **при отправке и при callback**. Не менять поведение Claude/Codex/Pi и общую merge-семантику. Старая запись `clickToFocus=true` (включая уже существующую schema 2) **не даёт нового согласия**, даже если это значение было сохранено явно: для Qoder оно раньше документировалось как inert. Для нового маршрута использовать имеющуюся schema 2 с **минимальным добавочным версионированным consent-маркером, по умолчанию отсутствующим** (например, `terminalClickConsentVersion: 1` внутри agent desktop profile). Это не rollout feature flag и не новый schemaVersion, а фиксация намеренного включения новой чувствительной функции. В реализации проверить строгую валидацию маркера/подписи текущего профиля и отсутствие самопроизвольного enable при upgrade. Не добавлять wizard, новую БД/реестр. Если даже такой узкий маркер не удаётся безопасно реализовать, *не включать* функцию для этой интеграции.
8. **Privacy/security:** никаких raw IDs, prompt/transcript/URL, пользовательских путей и OS tokens в публичных логах. Не делать shell execution из кликового содержимого. Host permission denied stays denied.

**Stop rule:** если источник конкретного окна в agent/OS/version не доказан, сохранить Navigation=None именно там. Не увеличивать scope ради «всех платформ» или обхода security.

### Конкретный UX однократного согласия (не реализован, задача P0/P1)

**Сегодня** `config edit` ограничен глобальными полями: `/agents/...` отвергается (`internal/config/edits.go`). Новые установки создают schema 1. Поэтому старое `agents.<agent>.notifications.desktop.clickToFocus=true` или повторное чтение того же файла **недостаточно**: оно не показывает намерение включить *новую* функцию.

**Минимальный маршрут для первого релиза:** обновить runtime до версии с поддержкой, сделать backup выбранного файла (`agent-notifications config path`), воспользоваться существующей [ручной schema-2 процедурой](../AGENT_CONFIGURATION.md): `schemaVersion: 2`; в **одном выбранном** `agents.qoder|gemini|cursor.notifications.desktop` выставить `clickToFocus: true` и новый, ранее отсутствующий `terminalClickConsentVersion: 1`. Выполнить `agent-notifications config inspect --json` для проверки структуры, затем перезапустить выбранный CLI. **Само редактирование конфигурации не доказывает физический callback** — это лишь явное намерение владельца. В реализации необходимо добавить строгое типизированное распознавание только этого нового узкого consent-поля (без миграции прежних документов), проверить, что существующие версии/агенты продолжают игнорировать его при чтении, и не разрешать включение по одному старому `clickToFocus:true`. Если существующий редактор/валидатор не позволяют эту процедуру безопасно провести, ограничиться отсутствием поддержки на этой паре agent/OS до минимальной адресной правки — **не делать** общий wizard или новая схему.

**Upgrade E2E:** schema 1; schema 2 с явным старым `clickToFocus:true`, но без нового consent-маркера; schema 2 с `global:false + agent:true + consent:1`; добровольный свежий opt-in; последующий opt-out/revoke. Все старые случаи должны остаться без focus action, не выключая сам баннер. Единичный POST/OS-accepted не заменяет проверку самого клика.

## 4. Вертикальные этапы, без преждевременного общего рефакторинга

| Этап | Проверка/изменение | Критерий завершения |
| --- | --- | --- |
| **P0 — доказать origin** | Реальные установленные версии трёх CLI на Linux X11/Wayland, macOS, Windows; local vs remote, два окна в одном cwd, data source для terminal process. Не внедрять SessionStart только потому, что upstream его документирует | Для каждого agent/OS получить проверяемый, приватный proof локального исходного окна; иначе честное No |
| **P1 — Qoder** | Переиспользовать legacy notifier; минимально исправить только установленный дефект. Без Qoder audio #562 | Реальный click возвращает в правильное Qoder CLI окно, негативные случаи не фокусируют чужое |
| **P2 — Gemini** | Добавить проверенный local target в текущий native delivery; event and lease boundaries не менять | AfterAgent/ToolPermission работают, остальные events не расширяются; remote/unknown остаётся informational |
| **P3 — Cursor** | То же для selected local Cursor CLI; отличать Cursor IDE/remote/cloud, сохранить UAP proof | Stop remains informational; click корректен только для подтверждённой CLI session |
| **P4 — E2E & docs** | OS native test, security review, exact-head CI, README/support matrix/site только по доказанному support | Merge после green полного CI и click E2E на поддерживаемых платформах; postmerge CI повторно |

**P0 выполняется инкрементально:** один реально доступный локальный host/terminal для каждого кандидата → решение, существует ли безопасный путь → только затем расширение на конкретные macOS/Linux/Windows варианты. Не требовать прохождения всей OS-матрицы *до первого* маленького изменения; требовать отдельный physical-click E2E **перед обещанием поддержки на каждой заявленной платформе**. Если для Qoder всё уже работает, первый PR — characterization tests/документация. Если Gemini/Cursor не дают локального origin, не увеличивать архитектуру.

### Проверенные ограничения существующего native delivery

- **Cursor:** internal/copilotvscodeinstall/desktop.go:65-73 в текущем production code *намеренно отвергает* любой request с Navigation != None, Policy.ClickToFocus=true либо ненулевым DesktopTarget. Для новой функции недостаточно поменять флаг в cursorevent.Consumer: сначала нужен доказанный typed route с сохранением именно этого single-lease security gate. Отказ/неизвестный результат не должен приводить к второму отправлению уведомления.
- **Linux:** internal/notifier/delivery_linux.go:59-67 разрешает native navigation только для Provider=codex + ThreadID + verified Linux binding. Простое помещение Gemini/Cursor session ID в этот target недопустимо. У Win/macOS есть свои отдельные activation/signing boundaries — их тоже не перепрыгивать.
- **Конфигурация:** internal/config/config_defaults.go задаёт global ClickToFocus=true; `Document.Effective` сливает per-agent профили, поэтому global=false + agent=true может превратиться в effective true. **P0 — обязательный негативный тест этой комбинации** и доказательство raw global veto на notify-time и click-time, не трогая текущую merge-семантику Claude/Codex/Pi. Inherited default true **не равно явному согласию** Qoder/Gemini/Cursor. Проверить хранение explicit agents.<agent>.notifications.desktop.clickToFocus=true в существующей schema 2; если нет безопасного способа — не включать новую функцию автоматически.
- **Приоритет доказательства:** selected installed binary + local OS user/session + конкретный route bound to native window. Даже при валидном hook provenance две одинаковые рабочие директории, PID/title reuse и remote forwarded env не делают window идентичность доверенной. Native callback должен revalidate immutable ownership at click time.
- **Доказательная база:** обычный headless GitHub CI проверяет политику и native API contracts; interactive physical click+visible window требует настоящего desktop runner/owner evidence, без имитации успеха. Не распространять результат одной OS/terminal пары на другие.

## 5. Минимальные обязательные E2E

- Installed **публичная** версия CLI и конкретная OS/arch, test source SHA и подписанный helper / D-Bus daemon / Windows protocol handler. Проверить *физический клик* после остановки sender, и что сфокусировалось точно нужное окно.
- Два терминала с одинаковым cwd/title; два разных профиля; PID/HWND reuse; смена terminal tab; TUI и remote/headless; stale installation generation after update/removal; отказ прав, отсутствие window manager; double click.
- Провал безопасен: без подтверждённого target — обыкновенный баннер, никакого switch в чужую сессию. Для Qoder проверить отдельную per-notification no-focus ветку на macOS/Linux/Windows; не спутать её с modern Navigation=None. Права не запрашивать обходными методами; OS-accepted и мок-клик **не** дают права менять README на Supported.
- **Opt-out E2E:** при включённом desktop и валидном local target физический click **не** должен менять фокус, если (a) global clickToFocus=false и agent clickToFocus=true, либо (b) agent clickToFocus=false, либо (c) нет явного per-agent consent. Это проверить как при отправке, так и после доставки при изменении/отзыве policy. **Upgrade E2E:** старый профиль без нового согласия после обновления остаётся без focus action, баннер/звук/webhook ведут себя по-прежнему.
- **Windows tamper E2E:** изменить любые HWND/PID/title/folder/WarpURL поля в URI после отправки, попытаться запустить callback другого окна/профиля/пользователя; никакой focus effect, включая при PID/HWND reuse. До подтверждения криптографически или эквивалентно подтверждённой immutable origin binding Windows Qoder click = **not supported**. База64 сам по себе не защита. Не расширять этот тест до ненужного глобального рефакторинга Win32.
- Для всех новых маршрутов недоверенный payload никогда не запускает чужую команду. Не использовать отдельный публичный feature flag.
- Сохранить sound mute, webhook-only consent, session dedup, anti-spam, event classification, one-shot effect. Gemini turn ended ≠ success, Cursor Stop ≠ success, Qoder StopFailure ≠ completion.
- Focused unit/regression для boundary, native E2E на Linux/macOS/Windows, UAP install/update/uninstall, exact-head Go race+CI; review + SHA-guarded squash merge + postmerge проверки.

## 6. Что сознательно НЕ выбираем

| Альтернатива | Оценка / 10 | Почему |
| --- | ---: | --- |
| **Qoder: существующий notifier + proof** | **9** | Уже имеется focus-код; минимум изменений |
| **Gemini/Cursor: тонкий typed local-target adapter, только после proof** | **8** | Использование существующих OS backend и policy, без общей миграции |
| Перевести Gemini/Cursor на Claude legacy notifier | 4 | Обход современных admission/lease, риск дубликатов и регрессий |
| Новый глобальный daemon/session registry/универсальный event bus | 3 | Неоправданный state/LOC |
| Фокус по одному project folder, title, последнему активному окну | 1 | Неоднозначность и неверная сессия |

**Оценка (грубая, не квота):** Qoder 50–180 prod LOC, Gemini 120–330, Cursor 120–350, shared adapter 150–400, tests/OS E2E 700–1700. **~1140–2960 изменённых LOC** после успешного P0. Если доказанного маршрута нет — меньший scope, а не 10k новых строк.

## 7.0. Результаты критики и принятые изменения

- **CodeRabbit (независимый первый проход):** [inline review в PR #628](https://github.com/777genius/agent-notifications/pull/628). Найдено: Qoder использует legacy notifier, поэтому современный Navigation=None не отключает возможный Linux focus callback; **принято**, добавлен обязательный per-notification no-focus путь для всех трёх OS. Также **приняты** negative E2E с global/agent opt-out и старым профилем после update.
- **CodeRabbit (второй раунд):** [review в PR #628](https://github.com/777genius/agent-notifications/pull/628) указал на (a) неподписанный Windows legacy focus URI, который нельзя считать доказательством original sender, и (b) возможность перезаписать global false через агентный true. **Принято:** Windows tamper-negative E2E и запрет расширения Windows Qoder click без защищённого immutable target; raw global veto для новых интеграций на delivery + callback, включая explicit agent=true. Отказ по каждой платформе остаётся допустимым результатом P0.
- **CodeRabbit (третий раунд):** [review на `92c5ce3` в PR #628](https://github.com/777genius/agent-notifications/pull/628) проверил конфиг: `config edit` не может менять `/agents/...`, новые установки — schema 1, pre-existing Qoder schema-2 `clickToFocus=true` не доказывает *новое* согласие. **Принято:** конкретная существующая ручная схема-2 процедура + минимальный additive versioned consent marker, отсутствующий в старых документах; отрицательные upgrade-тесты, запрет автоподключения до свежего согласия. Нового wizard или общего migration framework не требуется.
- **Повторное исследование исходников:** internal/copilotvscodeinstall/desktop.go прямо отвергает навигацию Cursor; modern Linux backend знает лишь Codex Desktop target; у config global ClickToFocus=true. План ограничен точечным расширением после реального provenance proof, не «переключением флага» и не глобальной автоматической активацией.
- **Codex security review / Strix:** запрошены, но внешние сервисы сообщили о лимите Codex reviews и проблеме оплаты Strix. Это **не зачтённые** проверки, нельзя писать «все независимые агенты одобрили». Повторить при доступности; CodeRabbit и авторское adversarial review не подменяют native E2E.
- Финальные source/OS/UX security gate относятся к последующим implementation PR, а не к самому плану.

## 7. Запрос критическим агентам перед кодом

План на review, **функция ещё не реализована**. Нужны независимые ревью security, Clean Architecture/SOLID/DRY, OS/native E2E и UX scope. Каждое замечание: точная проблема/путь/условие, severity, самая короткая правка или честный stop.

1. Доказуемо ли local terminal origin в **установленном** Qoder/Gemini/Cursor, без доверия cwd/session/env/foreground по отдельности? Какой самый маленький позитивный/негативный experiment?
2. Можно ли использовать существующий notifier/DeliveryPort, не нарушив binding leases, неизвестный результат native delivery и installed callback lifetime?
3. Где проходит граница typed terminal target vs Codex Desktop ThreadID? Может ли изменение accidentally включить OpenCode?
4. Нужны ли вообще SessionStart, config schema, installer changes? Если нет — удалить их из плана.
5. Какие реальные click E2E обязательны, а какие планы тестов излишни до первого вертикального среза?

**Окончательное проектное решение — после ревью. Никаких runtime feature claims до реального physical-click E2E.**

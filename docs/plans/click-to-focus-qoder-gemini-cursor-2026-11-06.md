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
3. **Qoder:** сначала characterization текущего focus в installed CLI. Исправлять только конкретный дефект и подтверждать, что новый capture не заимствует raw Claude-only Ghostty identity и не ведёт к ошибочному окну.
4. **Gemini/Cursor:** только после позитивного proof-of-origin добавить **узкий terminal-target маршрут** к уже существующему desktop delivery/OS callback. Такой target должен быть отделён от Codex Desktop chat; не переделывать старые агенты или OpenCode. Если native adapter принимает достаточную цель без новой публичной структуры — предпочесть малый adapter.
5. **Снимок клика фиксируется при доставке**, живёт до самого click после смерти sender, проверяет owner/установку/поколение и корректно отказывает при отозванном разрешении, чужом окне, повторно использованном PID, update/remove. Не искать «текущее» окно в момент клика.
6. **Политика:** при remote/headless/unknown origin оставлять Navigation=None и обычное уведомление. Навигация BestEffort только если доказан local target; при невозможности безопасно сфокусировать — не переключать ничего. Не менять Required/None семантику существующих агентов.
7. **UX:** глобальный clickToFocus=false и per-agent consent authoritative. Не включать новые маршруты автоматически для существующего профиля без согласия; прежде проверить, что текущие per-agent overrides позволяют использовать имеющееся поле вместо нового UI. Нет лишнего мастера/флажка, если уже есть настройка.
8. **Privacy/security:** никаких raw IDs, prompt/transcript/URL, пользовательских путей и OS tokens в публичных логах. Не делать shell execution из кликового содержимого. Host permission denied stays denied.

**Stop rule:** если источник конкретного окна в agent/OS/version не доказан, сохранить Navigation=None именно там. Не увеличивать scope ради «всех платформ» или обхода security.

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
- **Конфигурация:** internal/config/config_defaults.go задаёт глобальный ClickToFocus=true; внутренний Config уже содержит effective booleans. Поэтому этот inherited default **не равен новому явному согласию** Gemini/Cursor/Qoder. В P0 отдельно проверить, можно ли сохранить explicit agents.<agent>.notifications.desktop.clickToFocus=true на существующей schema 2, не вводя новых настроек. Если нет, не включать feature автоматически и вынести минимальный UX consent decision перед implementation.
- **Приоритет доказательства:** selected installed binary + local OS user/session + конкретный route bound to native window. Даже при валидном hook provenance две одинаковые рабочие директории, PID/title reuse и remote forwarded env не делают window идентичность доверенной. Native callback должен revalidate immutable ownership at click time.
- **Доказательная база:** обычный headless GitHub CI проверяет политику и native API contracts; interactive physical click+visible window требует настоящего desktop runner/owner evidence, без имитации успеха. Не распространять результат одной OS/terminal пары на другие.

## 5. Минимальные обязательные E2E

- Installed **публичная** версия CLI и конкретная OS/arch, test source SHA и подписанный helper / D-Bus daemon / Windows protocol handler. Проверить *физический клик* после остановки sender, и что сфокусировалось точно нужное окно.
- Два терминала с одинаковым cwd/title; два разных профиля; PID/HWND reuse; смена terminal tab; TUI и remote/headless; stale installation generation after update/removal; отказ прав, отсутствие window manager; double click.
- Провал безопасен: без подтверждённого target — обыкновенный баннер, никакого switch в чужую сессию. Права не запрашивать обходными методами; OS-accepted и мок-клик **не** дают права менять README на Supported.
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

## 7. Запрос критическим агентам перед кодом

План на review, **функция ещё не реализована**. Нужны независимые ревью security, Clean Architecture/SOLID/DRY, OS/native E2E и UX scope. Каждое замечание: точная проблема/путь/условие, severity, самая короткая правка или честный stop.

1. Доказуемо ли local terminal origin в **установленном** Qoder/Gemini/Cursor, без доверия cwd/session/env/foreground по отдельности? Какой самый маленький позитивный/негативный experiment?
2. Можно ли использовать существующий notifier/DeliveryPort, не нарушив binding leases, неизвестный результат native delivery и installed callback lifetime?
3. Где проходит граница typed terminal target vs Codex Desktop ThreadID? Может ли изменение accidentally включить OpenCode?
4. Нужны ли вообще SessionStart, config schema, installer changes? Если нет — удалить их из плана.
5. Какие реальные click E2E обязательны, а какие планы тестов излишни до первого вертикального среза?

**Окончательное проектное решение — после ревью. Никаких runtime feature claims до реального physical-click E2E.**

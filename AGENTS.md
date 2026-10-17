# AGENTS.md

## Удаление или переименование файла в упаковке

Вычеркнутый из упаковки плагина файл может «всплыть» снова из трёх мест накопления (урок issue #490: удалённый `skills/agent-notify` всплывал рядом с `skills/agent-notifications`). Удаляешь или переименовываешь shipped-файл — почисти все три места и закрой их тестами:

1. **Copy-back той же версии** (`bin/bootstrap.sh`) — список восстанавливаемых отпечатков не должен возвращать удалённый файл в обновлённый plugin root.
2. **Остатки в plugin root** (зачистка после refresh в `bin/bootstrap.sh`) — обновлённый корень должен совпадать с упаковкой: overlay-обновление Claude Code вычеркнутые файлы сам не удаляет.
3. **Ledger managed-runtime** (`internal/installruntime`, `cmd/claude-notifications/install_runtime.go`) — owned-запись ретирится той же транзакцией; user-edited или unowned legacy не удаляется молча — установка обязана отказаться (см. `TestEmbeddedSkillRename*`).

Проверка: `bin/release_channel_native_test.sh` (CI Ubuntu) + тесты `cmd/claude-notifications` (`TestEmbeddedSkillRename*`).

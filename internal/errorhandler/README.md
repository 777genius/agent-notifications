# Error Handler Package

Глобальный обработчик ошибок с автоматическим логированием в файл и консоль.

## Возможности

- Централизованная обработка ошибок
- Автоматическое логирование в файл и консоль
- Обработка критических ошибок
- Восстановление после panic с помощью recover
- Защищённый запуск горутин
- Разные уровни логирования: Debug, Info, Warn, Error, Critical

## Инициализация

```go
import "github.com/777genius/agent-notifications/internal/errorhandler"

// Инициализация при старте приложения
errorhandler.Init(
    true,  // logToConsole: выводить логи в консоль
    false, // exitOnCritical: завершать программу при критических ошибках
    true,  // recoveryEnabled: включить восстановление после panic
)
```

## Использование

### Обработка обычных ошибок

```go
if err := someOperation(); err != nil {
    errorhandler.HandleError(err, "Failed to perform operation")
}
```

### Обработка критических ошибок

```go
if err := criticalOperation(); err != nil {
    errorhandler.HandleCriticalError(err, "Critical failure in operation")
    // Критические ошибки всегда выводятся в stderr
}
```

### Защита от panic

```go
func riskyFunction() {
    defer errorhandler.HandlePanic()

    // Ваш код, который может вызвать panic
    panic("something went wrong")
    // Panic будет перехвачен и залогирован
}
```

### Обёртка функций с автоматическим recover

```go
// Обычная функция
errorhandler.WithRecovery(func() {
    // Код, который может вызвать panic
})

// Функция с возвратом ошибки
err := errorhandler.WithRecoveryFunc(func() error {
    // Код, который может вызвать panic или вернуть ошибку
    return someOperation()
})
```

### Безопасный запуск горутин

```go
// Вместо обычного go func()
errorhandler.SafeGo(func() {
    // Код в горутине с защитой от panic
    riskyAsyncOperation()
})
```

### Логирование

```go
errorhandler.Debug("Debug message: %s", value)
errorhandler.Info("Info message: %s", value)
errorhandler.Warn("Warning: %s", warning)
```

## Интеграция с logging

Пакет автоматически интегрируется с `internal/logging` и:
- Логирует все ошибки в файл `notification-debug.log`
- При включённом `logToConsole` выводит логи в stderr (для ошибок/предупреждений) или stdout (для info/debug)
- Критические ошибки всегда выводятся в stderr, даже если `logToConsole=false`
- **Все выводы в консоль имеют префикс `[claude-notifications]` для удобной идентификации**

## Примеры из кода

### main.go
```go
func main() {
    errorhandler.Init(true, false, true)
    defer errorhandler.HandlePanic()

    // Основная логика
}
```

### hooks.go
```go
func (h *Handler) HandleHook(hookEvent string, input io.Reader) error {
    defer errorhandler.HandlePanic()

    // Обработка хука
}
```

### Асинхронные операции
```go
// notifier.go
errorhandler.SafeGo(func() {
    defer n.wg.Done()
    n.playSound(soundPath)
})

// webhook.go
errorhandler.SafeGo(func() {
    defer s.wg.Done()
    if err := s.Send(status, message, sessionID); err != nil {
        errorhandler.HandleError(err, "Async webhook send failed")
    }
})
```

## Тестирование

```bash
go test ./internal/errorhandler/... -v
```

## Примеры вывода

### Вывод в консоль (stderr/stdout)
Все сообщения в консоль имеют префикс `[claude-notifications]`:
```
[claude-notifications] [2025-10-19 15:30:45] [ERROR] CRITICAL ERROR - Failed to initialize logger: permission denied
[claude-notifications] PANIC: unexpected nil pointer
[claude-notifications] [2025-10-19 15:30:46] [INFO] Notification sent successfully
[claude-notifications] [2025-10-19 15:30:47] [WARN] Rate limit approaching threshold
```

### Вывод в файл (notification-debug.log)
Файловый лог не содержит префикс плагина:
```
[2025-10-19 15:30:45] [ERROR] CRITICAL ERROR - Failed to initialize logger: permission denied
[2025-10-19 15:30:45] [ERROR] PANIC RECOVERED: unexpected nil pointer
runtime/debug.Stack()...
[2025-10-19 15:30:46] [INFO] Notification sent successfully
[2025-10-19 15:30:47] [WARN] Rate limit approaching threshold
```

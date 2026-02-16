# Startup Sync — автосинк незаполненных сериалов при запуске

## Overview
При запуске сервера автоматически синхронизировать метаданные из TVDB для сериалов, которые были добавлены сканером но не получили полную синхронизацию. Критерий: `tvdb_id IS NOT NULL AND overview IS NULL`.

## Context
- Сканер создаёт серии с базовыми полями (title, original_title, poster_url, status) через `GetSeriesDetailsWithRussian`
- Полная синхронизация (overview, genres, networks, characters, seasons) происходит через `SyncSeriesMetadata()` в `internal/api/sync.go`
- Сейчас полная синхронизация запускается только по расписанию (`CheckForTVDBUpdates`) или вручную через API
- Если сервер перезапустился до первого `CheckForTVDBUpdates` — сериалы остаются без метаданных

## Development Approach
- **Testing approach**: Regular (сначала код, потом тесты)
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Добавить `GetUnsyncedSeries` в database
- [x] В `internal/database/series.go` добавить метод `GetUnsyncedSeries() ([]Series, error)` — возвращает серии с `tvdb_id IS NOT NULL AND overview IS NULL`
- [x] Возвращать только поля `ID`, `TVDBId`, `Title` (остальные не нужны для синхронизации)
- [x] Написать тесты: пустая БД, серия без tvdb_id (не возвращается), серия с tvdb_id и overview (не возвращается), серия с tvdb_id без overview (возвращается)
- [x] Запустить тесты — должны пройти перед задачей 2

### Task 2: Добавить `SyncUnsyncedSeries` в sync.go
- [x] В `internal/api/sync.go` добавить функцию `SyncUnsyncedSeries(db *database.DB, tvdbClient *tvdb.Client)`
- [x] Вызывает `db.GetUnsyncedSeries()`, для каждого вызывает `SyncSeriesMetadata()`
- [x] Логирует: количество найденных, прогресс, ошибки (не прерывая цикл), итог
- [x] Написать тесты для `SyncUnsyncedSeries` (мок или integration test — аналогично существующим тестам в `sync_test.go`)
- [x] Запустить тесты — должны пройти перед задачей 3

### Task 3: Вызвать при запуске из main.go
- [ ] В `cmd/server/main.go` после инициализации TVDB клиента (строка 62), запустить `api.SyncUnsyncedSeries(db, tvdbClient)` в фоновой горутине
- [ ] Обернуть в `if tvdbClient != nil`
- [ ] Запустить тесты
- [ ] Собрать проект `go build ./...`

### Task 4: Финальная проверка
- [ ] Запустить полный тестовый набор
- [ ] Запустить линтер — все проблемы должны быть исправлены
- [ ] Проверить что `go vet ./...` проходит

## Technical Details
- Запрос: `SELECT id, tvdb_id, title FROM series WHERE tvdb_id IS NOT NULL AND overview IS NULL`
- `SyncSeriesMetadata()` уже делает полную синхронизацию: series fields + seasons + characters через `SyncSeriesAndChildren()`
- Горутина не блокирует запуск сервера
- Ошибка синхронизации одного сериала не останавливает остальные

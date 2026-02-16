# TVDB Rematch — кнопка "Fix Match" на странице сериала

## Overview
Добавить возможность перематчить сериал на другой TVDB ID со страницы сериала (detail page), как "Fix Match" в Plex. Исправить бэкенд — текущий `handleMatchSeries` не делает полную синхронизацию после смены tvdb_id.

## Context
- `POST /api/series/{id}/match` уже существует (`internal/api/router.go:603`), но при rematch:
  - Вызывает только `GetSeriesDetailsWithRussian` (базовые поля: title, poster, status, total_seasons)
  - НЕ вызывает `SyncSeriesMetadata` — overview, genres, networks, rating, characters, seasons с TVDB ID не обновляются
  - Старые персонажи от предыдущего матча остаются в `series_characters`
- Фронтенд: кнопка Match показывается только в списке сериалов для `!tvdb_id` (`app.js:178`), на detail page её нет
- Модалка поиска TVDB уже реализована: `openMatchModal`, `searchTVDBForMatch`, `matchSeries`
- `SyncSeriesMetadata()` в `internal/api/sync.go` — полная синхронизация через `SyncSeriesAndChildren` (series + seasons + characters в одной транзакции)

## Development Approach
- **Testing approach**: Regular (сначала код, потом тесты)
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Исправить `handleMatchSeries` — полная синхронизация после match
- [x] В `internal/api/router.go` `handleMatchSeries()`: после UPDATE series с новым tvdb_id (строка 757-761), вызвать `SyncSeriesMetadata(s.db, s.tvdbClient, id, req.TVDBId)` для полной синхронизации (overview, genres, networks, characters, seasons)
- [x] Если `SyncSeriesMetadata` вернул ошибку — залогировать как warning, но НЕ возвращать ошибку клиенту (базовый матч уже сохранён, полная синхронизация подтянется позже)
- [x] В ответе вернуть полные данные сериала (сделать `GET` из БД после синхронизации) вместо ручной сборки response map
- [x] Написать тест: rematch сериала с существующим tvdb_id — проверить что tvdb_id обновился
- [x] Написать тест: match сериала без tvdb_id — проверить обратную совместимость
- [x] Запустить тесты — должны пройти перед задачей 2

### Task 2: Кнопка Rematch на detail page
- [x] В `web/static/app.js` `loadSeriesDetail()`: добавить кнопку "Fix Match" рядом с заголовком сериала (или в metadata area) — кнопка видна для всех сериалов
- [x] По клику вызывать существующую `openMatchModal(seriesId)`
- [x] В `matchSeries()`: после успешного матча, если текущий view — `series-detail`, вызвать `loadSeriesDetail(state.currentSeriesId)` вместо `loadSeries()` чтобы обновить detail page
- [x] Запустить тесты

### Task 3: Финальная проверка
- [ ] Запустить полный тестовый набор
- [ ] Запустить линтер — все проблемы должны быть исправлены
- [ ] `go vet ./...` проходит
- [ ] Собрать проект `go build ./...`

## Technical Details
- `handleMatchSeries` flow после исправления:
  1. Проверить существование сериала
  2. Проверить дубликат tvdb_id → merge (без изменений, уже работает)
  3. UPDATE series SET tvdb_id (базовые поля)
  4. `SyncSeriesMetadata()` — полная синхронизация (characters, seasons, overview, genres)
  5. Вернуть полные данные сериала
- `SyncSeriesMetadata` использует `SyncSeriesAndChildren` с guard `WHERE id = ? AND tvdb_id = ?` — tvdb_id уже обновлён на шаге 3, guard пройдёт
- Модалка поиска переиспользуется as-is, только добавляется точка входа с detail page

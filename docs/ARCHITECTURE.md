# EpisodeX - Архитектурный отчёт

## 1. Общее описание

EpisodeX - сервис для учёта скачанных сериалов в медиа-библиотеке (Plex).

**Что делает:**
- Сканирует папку с сериалами и автоматически определяет что скачано (по именам папок)
- Идентифицирует сериалы через TVDB API, подтягивает метаданные (рейтинг, обложки, описание, актёров)
- Нотифицирует о выходе новых сезонов (сравнивая `aired_seasons` в TVDB с тем что уже скачано)
- Позволяет привязать озвучку (LostFilm, Кубик в Кубе и т.д.) к каждому сезону
- Вырезает лишние аудиодорожки из MKV файлов (через mkvmerge)

**Стек:** Go 1.25, Chi v5, SQLite (pure Go), Vanilla JS фронт, Docker + Alpine

---

## 2. Архитектура: что когда запускается

```
┌─────────────────────────────────────────────────────────────┐
│                    cmd/server/main.go                         │
│                                                               │
│  1. Загрузить конфигурацию (.env)                            │
│  2. Инициализировать SQLite БД (миграции, сиды)              │
│  3. Создать TVDB клиент (если есть API ключ)                 │
│  4. Создать Scanner (сканер медиапапки)                      │
│  5. Запустить Scheduler (3 задачи)                           │
│  6. Запустить HTTP сервер                                    │
│  7. Graceful shutdown (30с таймаут)                          │
└─────────────────────────────────────────────────────────────┘
```

### Планировщик задач (scheduler)

| Задача | Расписание | Что делает |
|--------|-----------|------------|
| `database_backup` | Ежедневно в `BACKUP_HOUR` (по умолч. 3:00) | VACUUM INTO → проверка integrity → ротация старых |
| `media_scan` | Каждые `SCAN_INTERVAL_HOURS` (по умолч. 1ч), **сразу при старте** | Сканирует MEDIA_PATH, ищет новые сериалы/сезоны, хэширует файлы |
| `tvdb_check` | Ежедневно в `TVDB_CHECK_HOUR` (по умолч. 5:00) | Проверяет TVDB на новые вышедшие сезоны, создаёт алерты |

```
                Scheduler
                    │
    ┌───────────────┼──────────────────┐
    ▼               ▼                  ▼
 Backup         Media Scan         TVDB Check
 (daily)        (hourly)           (daily)
    │               │                  │
    ▼               ▼                  ▼
 SQLite          Scanner            sync.go
 VACUUM       ┌─────────┐      ┌──────────────┐
              │ Парсит   │      │ Для каждого  │
              │ папки    │      │ сериала:     │
              │ ищет     │◄────►│ GetSeries-   │
              │ сериалы  │ TVDB │ Details()    │
              │ хэширует │      │ сравнить     │
              │ файлы    │      │ aired_seasons│
              └─────────┘      └──────────────┘
```

---

## 3. Потоки данных

### 3.1. Сканирование медиа-папки (Scanner.Scan)

```
/Volumes/Plex/TV Show/
├── Breaking Bad S01/           ← парсится как: title="Breaking Bad", season=1
│   ├── S01E01.mkv
│   └── S01E02.mkv
├── The Office/                 ← проверяются подпапки:
│   ├── Season 1/              ←   season=1
│   └── Season 2/              ←   season=2
└── Stranger Things S03 1080p/  ← title="Stranger Things", season=3
```

**Поток:**

```
1. Читаем папки в MEDIA_PATH
       │
2. Парсим имя папки (go-parse-torrent-name + regex)
   → Извлекаем: title, season_number
       │
3. Есть TVDB клиент?
   ├── ДА → SearchSeries(title)
   │        → GetSeriesDetailsWithRussian(tvdb_id)
   │        → INSERT/UPDATE series (с рус. названием)
   └── НЕТ → INSERT series (только title)
       │        → Создать алерт "Series X not found in TVDB"
       │
4. UPSERT INTO seasons (series_id, season_number, folder_path)
   → is_watched = 1, is_owned = 1
       │
5. scanMediaFiles() → для каждого .mkv/.mp4/.avi/.m4v:
   → SHA256 хэш (от первых/последних байт + размер)
   → UPSERT INTO media_files
   → UPSERT INTO episodes (если удалось распарсить номер серии)
       │
6. cleanupRemovedSeasons()
   → Проверить, что папки owned-сезонов всё ещё существуют
   → Если нет → is_owned = 0, folder_path = NULL
```

### 3.2. Проверка обновлений TVDB (CheckForTVDBUpdates)

```
1. SELECT все сериалы с tvdb_id из БД
       │
2. Для каждого сериала:
   → GetSeriesDetails(tvdb_id) из TVDB
   → Получить список сезонов, отфильтровать:
     - season 0 (specials) — пропустить
     - type != "official"/"aired"/"" — пропустить
   → Посчитать aired_seasons (MaxAiredSeasonNumber)
       │
3. Если aired_seasons изменился:
   ├── Если aired > max_watched И max_watched > 0:
   │   → INSERT INTO system_alerts (type='new_seasons')
   │     "New seasons available for Breaking Bad"
   │
   └── autoSync включён?
       ├── ДА → SyncSeriesMetadata() (полная синхронизация)
       └── НЕТ → UPDATE series SET aired_seasons = ?
```

**Важно:** `aired` = сезон ВЫШЕЛ (year <= текущий год). Это НЕ "продлён", а именно факт выхода.

### 3.3. Полная синхронизация (SyncSeriesMetadata)

```
1. GetSeriesExtendedFull(tvdb_id) — одним запросом ВСЕ:
   - метаданные сериала (название, рейтинг, даты и т.д.)
   - список сезонов с ID
   - актёры/персонажи
   - артворки (постеры, фоны, баннеры)
       │
2. GetSeriesTranslation(tvdb_id, "rus") — русское название и описание
       │
3. db.SyncSeriesAndChildren() — ОДНА транзакция:
   - UPDATE series (с tvdb_id guard: WHERE tvdb_id = ?)
   - Для каждого сезона: UPSERT season (не трогает is_owned, folder_path)
   - DELETE + INSERT characters
   - DELETE + INSERT artworks
```

### 3.4. Обработка аудио (AudioCutter)

```
1. GET /api/series/{id}/seasons/{num}/audio
   → mkvmerge -J <file> → JSON со списком аудиодорожек
   → Показать пользователю: codec, language, channels, name
       │
2. POST .../audio/preview
   → ffmpeg -i <file> -ss 60 -t 30 -map 0:a:<idx> preview.mp3
   → Вернуть hash для GET /api/audio/preview/{hash}
       │
3. POST .../audio/process (SSE стрим)
   → Для каждого .mkv файла в папке сезона:
     → Проверить: уже обработан? (processed_files) → skip
     → mkvmerge -o <tmp> --audio-tracks <keep_id> <file>
     → Если keep_original → сохранить ещё и eng/und дорожки
     → Атомарный rename tmp → original
     → INSERT INTO processed_files
     → SSE события: start → progress → file_done → complete
```

---

## 4. Схема базы данных

### ER-диаграмма

```
┌──────────────┐       ┌──────────────┐       ┌──────────────┐
│   series     │1     N│   seasons    │1     N│  episodes    │
│──────────────│───────│──────────────│───────│──────────────│
│ id (PK)      │       │ id (PK)      │       │ id (PK)      │
│ tvdb_id (UQ) │       │ series_id(FK)│       │ season_id(FK)│
│ title        │       │ season_number│       │ episode_num  │
│ original_title│      │ tvdb_season_id│      │ tvdb_ep_id   │
│ slug         │       │ name         │       │ title        │
│ overview     │       │ overview     │       │ overview     │
│ poster_url   │       │ poster_url   │       │ image_url    │
│ backdrop_url │       │ first_aired  │       │ air_date     │
│ status       │       │ episode_count│       │ runtime      │
│ first_aired  │       │ folder_path  │       │ rating       │
│ last_aired   │       │ voice_actor_id│──┐   │ file_path    │
│ year         │       │ is_watched   │  │   │ file_hash    │
│ runtime      │       │ is_owned     │  │   │ file_size    │
│ rating       │       │ discovered_at│  │   │ is_watched   │
│ content_rating│      └──────────────┘  │   │ watched_at   │
│ orig_country │                          │   └──────────────┘
│ orig_language│       ┌──────────────┐  │
│ genres (JSON)│       │ voice_actors │  │
│ networks(JSON)│      │──────────────│──┘
│ studios(JSON)│       │ id (PK)      │
│ total_seasons│       │ name (UQ)    │
│ aired_seasons│       └──────────────┘
│ created_at   │
│ updated_at   │       ┌──────────────────┐
└──────┬───────┘       │ series_characters │
       │1             N│──────────────────│
       ├───────────────│ id (PK)          │
       │               │ series_id (FK)   │
       │               │ character_name   │
       │               │ actor_name       │
       │               │ image_url        │
       │               │ sort_order       │
       │               └──────────────────┘
       │
       │1             N┌──────────────┐
       ├───────────────│  artworks    │
       │               │──────────────│
       │               │ id (PK)      │
       │               │ series_id(FK)│
       │               │ season_id(FK)│
       │               │ type         │
       │               │ url          │
       │               │ thumbnail_url│
       │               │ language     │
       │               │ score        │
       │               │ width/height │
       │               │ is_primary   │
       │               └──────────────┘
       │
       │1             N┌──────────────────┐
       └───────────────│  media_files     │
                       │──────────────────│
                       │ id (PK)          │
                       │ series_id        │──┐ Composite FK
                       │ season_number    │──┘ → seasons
                       │ file_path (UQ)   │
                       │ file_name        │
                       │ file_size        │
                       │ file_hash        │
                       │ mod_time         │
                       └──────────────────┘

┌──────────────────┐   ┌──────────────────┐   ┌──────────────┐
│ processed_files  │   │ system_alerts    │   │ backups      │
│──────────────────│   │──────────────────│   │──────────────│
│ file_path (PK)   │   │ id (PK)          │   │ id (PK)      │
│ series_id (FK)   │   │ type             │   │ filename     │
│ season_number    │   │ message          │   │ size_bytes   │
│ track_kept       │   │ created_at       │   │ integrity_ok │
│ track_language   │   │ dismissed        │   │ created_at   │
│ track_name       │   └──────────────────┘   └──────────────┘
│ processed_at     │
└──────────────────┘   ┌──────────────────┐   ┌──────────────────┐
                       │ scan_history     │   │ watched_seasons  │
                       │──────────────────│   │──────────────────│
                       │ id (PK)          │   │ series_id (PK)   │
                       │ started_at       │   │ season_number(PK)│
                       │ finished_at      │   │ voice_actor_id   │
                       │ folders_scanned  │   │ folder_path      │
                       │ new_series       │   │ source           │
                       │ new_seasons      │   │ discovered_at    │
                       └──────────────────┘   └──────────────────┘
                                                  ↑ DEPRECATED
```

### Каскадные удаления

- `DELETE series` → каскадно удаляет: seasons, episodes, characters, artworks, media_files
- `DELETE season` → каскадно удаляет: episodes

### Предзаполненные данные (seeds)

Таблица `voice_actors` при первом запуске заполняется:
LostFilm, Кубик в Кубе, Amedia, NewStudio, ColdFilm, Jaskier, AlexFilm, SDI Media, Original

---

## 5. API эндпоинты

### Сериалы

| Метод | URL | Описание |
|-------|-----|----------|
| `GET` | `/api/series` | Список всех сериалов (title, poster, status, watched_seasons) |
| `POST` | `/api/series` | Создать сериал (по tvdb_id или вручную по title) |
| `GET` | `/api/series/{id}` | Полная карточка сериала: метаданные + сезоны + актёры |
| `DELETE` | `/api/series/{id}` | Удалить сериал (каскад) |
| `POST` | `/api/series/{id}/match` | Привязать к TVDB ID. Если такой tvdb_id уже есть — мержит сезоны |

### Сезоны

| Метод | URL | Описание |
|-------|-----|----------|
| `GET` | `/api/series/{id}/seasons` | Все сезоны: owned и locked (пустые) |
| `GET` | `/api/series/{id}/seasons/{num}` | Детали сезона |
| `PUT` | `/api/series/{id}/seasons/{num}` | Обновить voice_actor_id |

### Аудио

| Метод | URL | Описание |
|-------|-----|----------|
| `GET` | `/api/series/{id}/seasons/{num}/audio` | Список аудиодорожек (mkvmerge -J) |
| `POST` | `/api/series/{id}/seasons/{num}/audio/preview` | Создать 30с MP3 превью дорожки |
| `GET` | `/api/audio/preview/{hash}` | Отдать MP3 превью |
| `POST` | `/api/series/{id}/seasons/{num}/audio/process` | Удалить лишние дорожки (SSE стрим) |

### Обновления и нотификации

| Метод | URL | Описание |
|-------|-----|----------|
| `GET` | `/api/updates` | Сериалы, для которых вышли новые сезоны (не скачаны) |
| `POST` | `/api/updates/check` | Запустить проверку TVDB вручную |
| `GET` | `/api/alerts` | Системные алерты (до 10 непрочитанных) |
| `POST` | `/api/alerts/{id}/dismiss` | Скрыть алерт |

### Прочее

| Метод | URL | Описание |
|-------|-----|----------|
| `GET` | `/api/health` | Healthcheck (проверка БД) |
| `GET` | `/api/voices` | Список студий озвучки |
| `POST` | `/api/scan/trigger` | Запустить сканирование вручную |
| `GET` | `/api/search?q=...` | Поиск по TVDB |

---

## 6. Конфигурация

| Переменная | Умолчание | Описание |
|-----------|-----------|----------|
| `PORT` | 8080 | Порт HTTP сервера |
| `HOST` | 0.0.0.0 | Хост |
| `DB_PATH` | ./data/episodex.db | Путь к БД |
| `BACKUP_PATH` | ./data/backups | Папка для бекапов |
| `BACKUP_RETENTION` | 10 | Сколько бекапов хранить |
| `BACKUP_HOUR` | 3 | Час бекапа (0-23) |
| `MEDIA_PATH` | /Volumes/Plex/TV Show | Путь к медиатеке |
| `TVDB_API_KEY` | — | Ключ TVDB API (опционален) |
| `SCAN_INTERVAL_HOURS` | 1 | Интервал сканирования (часы) |
| `TVDB_CHECK_HOUR` | 5 | Час проверки TVDB (0-23) |

---

## 7. БЛОК: Лишние фичи, неиспользуемый код и данные

### 7.1. Таблицы

#### `watched_seasons` — МЁРТВАЯ таблица
- Помечена как DEPRECATED в коде (комментарий в `db.go:175`)
- Данные мигрируются в `seasons` при каждом старте (`migrateToSchemaV2`)
- **Никто не пишет в неё** — сканер пишет напрямую в `seasons`
- **Никто не читает из неё** — все запросы идут к `seasons`
- Код миграции можно было бы убрать после уверенности что все инстансы уже мигрированы
- **Вердикт: таблицу и код миграции можно удалить**

#### `scan_history` — МЁРТВАЯ таблица
- Создаётся в `initTables()`, создаётся индекс `idx_scan_history_started`
- **Никто не пишет в неё** — сканер не записывает историю сканирований
- **Никто не читает из неё** — нет API эндпоинтов, нет UI
- **Вердикт: таблицу и индекс можно удалить**

### 7.2. Таблица `episodes` — используется частично

- **Записи создаются** сканером (`scanMediaFiles`) и синхронизацией (`SyncSeriesMetadata` через `GetSeasonEpisodes`)
- **НО: TVDB метаданные серий НЕ подтягиваются при синхронизации!** `SyncSeriesMetadata` не вызывает `GetSeasonEpisodes` — он синхронизирует только сериал, сезоны, актёров и артворки. Серии (episodes) создаются только при сканировании файлов (с file_path, file_hash, is_watched)
- **Никто не читает episodes в API!** Нет ни одного API эндпоинта, возвращающего список серий (episodes) сезона
- **Фронтенд не показывает отдельные серии** — только сезоны целиком
- Поля `title`, `overview`, `image_url`, `air_date`, `runtime`, `rating` из TVDB — **никогда не заполняются** (т.к. `GetSeasonEpisodes` не вызывается из sync)
- `is_watched`, `watched_at` — **нигде не меняются вручную** (только при скане файлов `is_watched = true`)
- **Вердикт: таблица `episodes` по факту работает как дублирование `media_files` с привязкой к номеру серии. Метаданные TVDB для серий не используются. Если не планируется UI со списком серий сезона — таблицу можно значительно упростить или удалить.**

### 7.3. Таблица `media_files` — дубль с `episodes`

- `media_files` хранит: file_path, file_hash, file_size, mod_time
- `episodes` хранит: file_path, file_hash, file_size + номер серии
- Оба записываются одновременно в `scanMediaFiles()`
- `media_files` используется для: `CheckFileChanged`, `InvalidateCachedData`, `DeleteMediaFilesBySeason`
- `episodes` используется для: merge при match (перенос file_path)
- **Вердикт: информация дублируется. Можно было бы оставить одну из двух таблиц.**

### 7.4. Таблица `artworks` — избыточное хранение

- При `SyncSeriesMetadata` все артворки TVDB (постеры, фоны, баннеры всех размеров и языков) сохраняются в таблицу
- Реально используются только как **fallback**: если у series нет `poster_url` или `backdrop_url`, ищем лучший арт из `artworks`
- Фронтенд отображает только `poster_url` и `backdrop_url` из `series`
- Пользователь не может выбирать альтернативные постеры
- **Вердикт: хранится много лишних артворков (десятки записей на сериал). Достаточно было бы сохранять только лучший poster и backdrop прямо в `series.poster_url` / `series.backdrop_url` при синхронизации. Таблицу artworks можно удалить.**

### 7.5. Таблица `series_characters` — оправдано, но тяжело

- Показывается в UI: карточка сериала → блок "Cast" (top 10)
- Данные перезаписываются при каждом sync (DELETE + INSERT)
- **Вердикт: используется. Но хранит ВСЕ персонажей из TVDB, а показывает только 10. Можно ограничить сохранение до 10-15.**

### 7.6. Неиспользуемые данные из TVDB API

| Данные | Хранится в БД? | Показывается в UI? | Вердикт |
|--------|:-----:|:-----:|---------|
| `series.slug` | Да | Нет | Лишнее |
| `series.first_aired` | Да | Нет | Лишнее (year есть) |
| `series.last_aired` | Да | Нет | Лишнее |
| `series.original_country` | Да | Нет | Лишнее |
| `series.original_language` | Да | Нет | Лишнее |
| `series.studios` (JSON) | Да | Нет | Лишнее |
| `seasons.overview` | Да (schema) | Нет | Не заполняется при sync |
| `seasons.first_aired` | Да (schema) | Нет | Не заполняется при sync |
| `seasons.episode_count` | Да (schema) | Нет | Не заполняется при sync |
| `episodes.*` (TVDB metadata) | В schema есть | Нет | Не заполняются |
| `artworks` (все) | Да | Только fallback | Избыточно |
| `series.content_rating` | Да | Да | OK |
| `series.runtime` | Да | Да | OK |
| `series.rating` | Да | Да | OK |
| `series.genres` | Да | Да | OK |
| `series.networks` | Да | Да | OK |
| `series.year` | Да | Да | OK |
| `series.overview` | Да | Да | OK |
| `series.poster_url` | Да | Да | OK |
| `series.backdrop_url` | Да | Да | OK |
| `series.original_title` | Да | Да | OK |

### 7.7. Неиспользуемый Go код

#### `database/media_files.go`:
- **`GetStaleMediaFiles()`** — нигде не вызывается
- **`CleanupOrphanedMediaFiles()`** — заглушка (возвращает 0, nil), нигде не вызывается
- **`DeleteMediaFile()`** — нигде не вызывается (используется `DeleteMediaFilesBySeason`)
- **`GetMediaFilesBySeason()`** — вызывается из тестов

#### `database/series.go`:
- **`UpsertSeries()`** — нигде не вызывается напрямую (sync использует `SyncSeriesAndChildren`)
- **`UpsertCharacters()`** — нигде не вызывается (sync использует `upsertCharactersTx`)
- **`UpsertArtworks()`** — нигде не вызывается (sync использует `upsertArtworksTx`)

#### `tvdb/client.go`:
- **`SeriesSearchResult.Overview`** — заполняется при поиске, но не возвращается в API (`handleSearch` отдаёт только id, name, poster, year, status)
- **`SeriesSearchResult.PrimaryType`** — заполняется, нигде не используется
- **`SeriesSearchResult.FirstAired`** — заполняется, нигде не используется
- **`Genre.ID`, `Genre.Slug`** — хранятся в struct, но в JSON пишутся только names
- **`Company.ID`, `Company.Slug`** — аналогично

#### `api/router.go`:
- **`handleGetSeason`** — фронтенд вызывает его, но в UI отображается очень мало полей (voice_actor_id, owned). Остальные поля (folder_path, discovered_at) не используются в UI

### 7.8. Данные, которые запрашиваются из TVDB, но не нужны

1. **`GetSeriesExtendedFull` запрашивает `?meta=translations`**, но русский перевод берётся отдельным вызовом `GetSeriesTranslation`. Параметр `meta=translations` тянет ВСЕ переводы (10+ языков) внутри extended-ответа, но они полностью игнорируются в парсинге

2. **`GetSeriesDetails` (используется в `CheckForTVDBUpdates`)** — запрашивает `/series/{id}/extended`, что возвращает characters, artworks и т.д. Но из ответа используются ТОЛЬКО: `Name`, `Status`, `Seasons` (для подсчёта aired). Достаточно было бы обычного `/series/{id}` (non-extended) или хотя бы не парсить тяжёлые поля

3. **Сезонные артворки** не привязываются к `season_id` при sync — `SyncSeriesMetadata` записывает все artworks с `SeriesID`, но `SeasonID` всегда nil

---

## 8. Итоговые рекомендации

### Что можно безопасно удалить сейчас:
1. Таблица `watched_seasons` + весь код миграции (`migrateLegacyWatchedSeasons`, `migrateToSchemaV2` частично)
2. Таблица `scan_history` + индекс
3. Функции-заглушки: `CleanupOrphanedMediaFiles`, `GetStaleMediaFiles`, `DeleteMediaFile`
4. Неиспользуемые public-функции: `UpsertSeries`, `UpsertCharacters`, `UpsertArtworks` (есть Tx-варианты)

### Что стоит упростить:
1. Убрать `?meta=translations` из `GetSeriesExtendedFull` (используется отдельный вызов)
2. В `CheckForTVDBUpdates` использовать обычный `/series/{id}` вместо `/series/{id}/extended`
3. Не хранить artworks в отдельной таблице — достаточно вписывать лучший poster/backdrop прямо в series при синхронизации
4. Ограничить characters при INSERT до 10-15 записей
5. Определиться: `episodes` или `media_files`? Сейчас дублируется информация о файлах

### Что нужно решить концептуально:
1. Нужна ли таблица `episodes` и отображение отдельных серий в UI?
   - Если **нет** → удалить таблицу, убрать создание записей из сканера
   - Если **да** → добавить вызов `GetSeasonEpisodes` в `SyncSeriesMetadata`, создать API и UI для просмотра
2. Колонки `series.slug`, `series.first_aired`, `series.last_aired`, `series.original_country`, `series.original_language`, `series.studios` — хранятся, но не отображаются. Удалить или запланировать использование в UI?

-- Axiom schema (SQLite). Draft for phase 0; becomes migration 00001.
-- Conventions: INTEGER PRIMARY KEY ids, timestamps as TEXT RFC 3339 UTC,
-- dates as TEXT YYYY-MM-DD, booleans as INTEGER 0/1, JSON as TEXT.
PRAGMA foreign_keys = ON;

-- ---------------------------------------------------------------- accounts

CREATE TABLE users (
    id              INTEGER PRIMARY KEY,
    username        TEXT NOT NULL UNIQUE COLLATE NOCASE,
    password_hash   TEXT NOT NULL,            -- argon2id; Django pbkdf2_sha256 accepted and rehashed on login
    is_admin        INTEGER NOT NULL DEFAULT 0,
    is_active       INTEGER NOT NULL DEFAULT 1,
    timezone        TEXT NOT NULL DEFAULT 'UTC',
    language        TEXT NOT NULL DEFAULT 'en',
    webhook_token   TEXT NOT NULL UNIQUE,     -- /webhook/jellyfin/{token} (migrated from Django users.token)
    calendar_token  TEXT UNIQUE,              -- /calendar/{token}.ics; separate so rotating one never breaks the other (00006)
    jellyfin_users  TEXT NOT NULL DEFAULT '', -- Jellyfin usernames matched to this user, comma separated
    watched_percent INTEGER NOT NULL DEFAULT 75,
    created_at      TEXT NOT NULL,
    last_login_at   TEXT
);

-- Which media types a user tracks, and per-type list preferences.
CREATE TABLE user_media_settings (
    user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind      TEXT NOT NULL,                  -- see items.kind
    enabled   INTEGER NOT NULL DEFAULT 1,
    sort      TEXT NOT NULL DEFAULT 'activity',
    status    TEXT NOT NULL DEFAULT 'In progress',
    PRIMARY KEY (user_id, kind)
);

CREATE TABLE sessions (
    id_hash     TEXT PRIMARY KEY,             -- sha256 of the cookie value
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TEXT NOT NULL,
    expires_at  TEXT NOT NULL,
    user_agent  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user ON sessions(user_id);

CREATE TABLE api_tokens (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    scope        TEXT NOT NULL CHECK (scope IN ('read', 'write')),
    prefix       TEXT NOT NULL,
    key_hash     TEXT NOT NULL UNIQUE,        -- sha256 (as today)
    key_md5      TEXT NOT NULL DEFAULT '',    -- KOSync sends md5(key)
    created_at   TEXT NOT NULL,
    last_used_at TEXT,
    revoked_at   TEXT
);
CREATE INDEX api_tokens_md5 ON api_tokens(key_md5);

-- ---------------------------------------------------------------- catalog

-- One row per title, shared by all users.
CREATE TABLE items (
    id            INTEGER PRIMARY KEY,
    kind          TEXT NOT NULL CHECK (kind IN
                    ('tv', 'anime', 'movie', 'anime_movie', 'manga', 'comic', 'book', 'game', 'boardgame')),
    source        TEXT NOT NULL,              -- primary metadata source: tvdb (all shows), tmdb (movies; legacy shows with no TVDB match), mal, anilist, mangaupdates, comicvine, hardcover, openlibrary, igdb, bgg, manual
    external_id   TEXT NOT NULL,              -- id at the primary source
    title         TEXT NOT NULL,
    native_title  TEXT NOT NULL DEFAULT '',
    image         TEXT NOT NULL DEFAULT '',
    year          INTEGER,
    synopsis      TEXT NOT NULL DEFAULT '',
    total         INTEGER,                    -- episodes / chapters / pages, when known
    kind_override INTEGER NOT NULL DEFAULT 0, -- 1 = user set kind (anime vs tv), don't reclassify
    meta          TEXT NOT NULL DEFAULT '{}', -- provider extras (genres, runtime, authors, platforms, …)
    refreshed_at  TEXT,
    UNIQUE (source, external_id, kind)
);

-- Every known id for an item (tmdb, tvdb, imdb, anilist, mal, …), for matching.
CREATE TABLE item_ids (
    item_id     INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    source      TEXT NOT NULL,
    external_id TEXT NOT NULL,
    PRIMARY KEY (source, external_id, item_id)
);
CREATE INDEX item_ids_item ON item_ids(item_id);

CREATE TABLE item_seasons (
    item_id       INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    number        INTEGER NOT NULL,           -- 0 = specials
    title         TEXT NOT NULL DEFAULT '',
    image         TEXT NOT NULL DEFAULT '',
    air_date      TEXT,
    episode_count INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (item_id, number)
);

CREATE TABLE item_episodes (
    item_id  INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    season   INTEGER NOT NULL,
    number   INTEGER NOT NULL,                -- TVDB aired-order number (TMDB number for a legacy TMDB show)
    position INTEGER NOT NULL,                -- 1-based position within the season
    title    TEXT NOT NULL DEFAULT '',
    air_date TEXT,
    runtime  INTEGER,                         -- minutes
    image    TEXT NOT NULL DEFAULT '',
    overview TEXT NOT NULL DEFAULT '',
    ext_id   TEXT NOT NULL DEFAULT '',        -- migration 00007: TVDB episode id ('' for TMDB shows); indexed where non-empty
    PRIMARY KEY (item_id, season, number)
);

-- Fribb anime-lists rows (refreshed weekly).
CREATE TABLE anime_mappings (
    id          INTEGER PRIMARY KEY,
    mal_id      INTEGER,
    anilist_id  INTEGER,
    kind        TEXT NOT NULL DEFAULT '',
    tmdb_tv     INTEGER,
    tmdb_movie  INTEGER,
    tmdb_season INTEGER,
    tmdb_offset INTEGER NOT NULL DEFAULT 0,
    tvdb_id     INTEGER,
    tvdb_season INTEGER,                      -- migration 00007: season of the TVDB series (0 = specials), NULL when the dataset omits it
    tvdb_offset INTEGER NOT NULL DEFAULT 0    -- migration 00007: episodes of that season before this cour
);
CREATE INDEX anime_mappings_mal ON anime_mappings(mal_id);
CREATE INDEX anime_mappings_anilist ON anime_mappings(anilist_id);
CREATE INDEX anime_mappings_tmdb_tv ON anime_mappings(tmdb_tv);
CREATE INDEX anime_mappings_tmdb_movie ON anime_mappings(tmdb_movie);
CREATE INDEX anime_mappings_tvdb ON anime_mappings(tvdb_id);

CREATE TABLE people (
    id          INTEGER PRIMARY KEY,
    source      TEXT NOT NULL,
    external_id TEXT NOT NULL,
    name        TEXT NOT NULL,
    native_name TEXT NOT NULL DEFAULT '',
    image       TEXT NOT NULL DEFAULT '',
    UNIQUE (source, external_id)
);

CREATE TABLE credits (
    id             INTEGER PRIMARY KEY,
    item_id        INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    person_id      INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    role           TEXT NOT NULL,             -- cast, voice, crew
    character      TEXT NOT NULL DEFAULT '',
    character_image TEXT NOT NULL DEFAULT '',
    character_role TEXT NOT NULL DEFAULT '',
    language       TEXT NOT NULL DEFAULT '',
    job            TEXT NOT NULL DEFAULT '',
    ord            INTEGER NOT NULL DEFAULT 0,
    episode_count  INTEGER
);
CREATE INDEX credits_item ON credits(item_id);
CREATE INDEX credits_person ON credits(person_id);

CREATE TABLE credit_fetches (
    item_id    INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    fetched_at TEXT NOT NULL,
    count      INTEGER NOT NULL DEFAULT 0,
    error      TEXT NOT NULL DEFAULT ''
);

-- Upcoming/recent releases (episodes, chapters, release days) for the calendar feed.
CREATE TABLE releases (
    id         INTEGER PRIMARY KEY,
    item_id    INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    season     INTEGER,
    number     INTEGER,                       -- episode / chapter; NULL = the title itself
    at         TEXT NOT NULL,
    UNIQUE (item_id, season, number)
);
CREATE INDEX releases_at ON releases(at);

-- ---------------------------------------------------------------- tracking

-- A user's tracking of an item. Rewatches/rereads are new runs; the highest run is current.
CREATE TABLE entries (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id      INTEGER NOT NULL REFERENCES items(id),
    run          INTEGER NOT NULL DEFAULT 1,
    status       TEXT NOT NULL CHECK (status IN ('Planning', 'In progress', 'Paused', 'Dropped', 'Completed')),
    score        REAL,
    progress     INTEGER NOT NULL DEFAULT 0, -- chapters / issues / pages / count-based anime; derived from watches for tv/anime
    progressed_at TEXT,
    started_at   TEXT,
    finished_at  TEXT,
    notes        TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    UNIQUE (user_id, item_id, run)
);
CREATE INDEX entries_user_status ON entries(user_id, status);

-- Per-season status for shows (tv / anime), so a season can be Completed while the show is In progress.
CREATE TABLE entry_seasons (
    entry_id INTEGER NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    season   INTEGER NOT NULL,
    status   TEXT NOT NULL,
    score    REAL,
    notes    TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (entry_id, season)
);

-- Plays of episodes and movies. Never deleted by imports or rewatches.
CREATE TABLE watches (
    id         INTEGER PRIMARY KEY,
    entry_id   INTEGER NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    season     INTEGER,                       -- NULL for movies
    episode    INTEGER,                       -- episode number as the show's item numbers it (TVDB aired order)
    watched_at TEXT NOT NULL,
    source     TEXT NOT NULL DEFAULT 'manual' -- manual, jellyfin, anilist, import
);
CREATE INDEX watches_entry ON watches(entry_id, season, episode);
CREATE INDEX watches_at ON watches(watched_at);

-- History for activity and year views (replaces django-simple-history).
CREATE TABLE entry_events (
    id       INTEGER PRIMARY KEY,
    entry_id INTEGER NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    at       TEXT NOT NULL,
    field    TEXT NOT NULL CHECK (field IN ('status', 'progress', 'score', 'created')),
    old      TEXT,
    new      TEXT
);
CREATE INDEX entry_events_entry ON entry_events(entry_id, at);
CREATE INDEX entry_events_at ON entry_events(at);

-- ---------------------------------------------------------------- reading

CREATE TABLE reading_documents (
    id                  INTEGER PRIMARY KEY,
    user_id             INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    document_hash       TEXT NOT NULL,        -- KOReader partial md5
    title               TEXT NOT NULL DEFAULT '',
    authors             TEXT NOT NULL DEFAULT '',
    total_pages         INTEGER,
    item_id             INTEGER REFERENCES items(id),
    merged_into         INTEGER REFERENCES reading_documents(id), -- duplicates (same book, other file)
    progress            TEXT NOT NULL DEFAULT '', -- KOSync progress string
    percentage          REAL,
    device              TEXT NOT NULL DEFAULT '',
    device_id           TEXT NOT NULL DEFAULT '',
    progress_updated_at TEXT,
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL,
    UNIQUE (user_id, document_hash)
);

CREATE TABLE page_events (
    id               INTEGER PRIMARY KEY,
    document_id      INTEGER NOT NULL REFERENCES reading_documents(id) ON DELETE CASCADE,
    page             INTEGER NOT NULL,
    start_time       TEXT NOT NULL,
    duration_seconds INTEGER NOT NULL,
    total_pages      INTEGER,
    UNIQUE (document_id, page, start_time)
);

CREATE TABLE reading_sessions (
    id               INTEGER PRIMARY KEY,
    user_id          INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source           TEXT NOT NULL,           -- koreader, manual, …
    document_id      INTEGER REFERENCES reading_documents(id) ON DELETE SET NULL,
    item_id          INTEGER REFERENCES items(id),
    title            TEXT NOT NULL DEFAULT '',
    started_at       TEXT NOT NULL,
    ended_at         TEXT NOT NULL,
    duration_seconds INTEGER NOT NULL,
    pages_read       INTEGER NOT NULL DEFAULT 0,
    start_page       INTEGER,
    end_page         INTEGER,
    chapters_read    INTEGER NOT NULL DEFAULT 0,
    device           TEXT NOT NULL DEFAULT ''
);
CREATE INDEX reading_sessions_user ON reading_sessions(user_id, started_at);

-- Triple P + Reflect, one per read-through (entry run). Preview is seeded from the synopsis.
CREATE TABLE book_notes (
    entry_id     INTEGER PRIMARY KEY REFERENCES entries(id) ON DELETE CASCADE,
    preview      TEXT NOT NULL DEFAULT '',
    predict      TEXT NOT NULL DEFAULT '',
    purpose      TEXT CHECK (purpose IN ('educate', 'entertain', 'persuade')),
    purpose_note TEXT NOT NULL DEFAULT '',
    reflect      TEXT NOT NULL DEFAULT '',
    updated_at   TEXT NOT NULL
);

-- ---------------------------------------------------------------- lists

CREATE TABLE lists (
    id          INTEGER PRIMARY KEY,
    owner_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    visibility  TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'public')), -- 00004
    created_at  TEXT NOT NULL
);
CREATE TABLE list_members (
    list_id INTEGER NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (list_id, user_id)
);
CREATE TABLE list_items (
    list_id  INTEGER NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
    item_id  INTEGER NOT NULL REFERENCES items(id),
    added_at TEXT NOT NULL,
    position INTEGER NOT NULL DEFAULT 0,      -- manual order (00004)
    PRIMARY KEY (list_id, item_id)
);

-- ---------------------------------------------------------------- runtime

-- Durable one-off jobs (imports, refreshes). Periodic jobs are scheduled in code.
CREATE TABLE jobs (
    id          INTEGER PRIMARY KEY,
    kind        TEXT NOT NULL,
    user_id     INTEGER REFERENCES users(id) ON DELETE CASCADE,
    payload     TEXT NOT NULL DEFAULT '{}',
    status      TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'done', 'failed')),
    attempts    INTEGER NOT NULL DEFAULT 0,
    run_at      TEXT NOT NULL,
    started_at  TEXT,
    finished_at TEXT,
    result      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX jobs_due ON jobs(status, run_at);

-- Small state: sync markers (anilist_sync:<user>:<name>), last job runs, mapping refresh time.
CREATE TABLE kv (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- Provider responses (TMDB, AniList, …) with expiry. Replaces Valkey.
CREATE TABLE provider_cache (
    key        TEXT PRIMARY KEY,
    value      BLOB NOT NULL,                 -- gzip JSON
    expires_at TEXT NOT NULL
);
CREATE INDEX provider_cache_expiry ON provider_cache(expires_at);

CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY);
CREATE TABLE data_imports (
    path TEXT PRIMARY KEY,
    sha256 TEXT NOT NULL,
    metadata TEXT NOT NULL,
    imported_at TEXT NOT NULL,
    present INTEGER NOT NULL CHECK (present IN (0, 1))
);
CREATE TABLE terms (
    id INTEGER PRIMARY KEY,
    simplified TEXT NOT NULL UNIQUE,
    traditional TEXT NOT NULL,
    pinyin TEXT NOT NULL,
    english TEXT NOT NULL,
    hsk_level_min INTEGER NOT NULL CHECK (hsk_level_min BETWEEN 1 AND 7),
    hsk_level_max INTEGER NOT NULL,
    source_path TEXT NOT NULL REFERENCES data_imports(path),
    active INTEGER NOT NULL CHECK (active IN (0, 1)),
    available INTEGER NOT NULL CHECK (available IN (0, 1))
);
CREATE TABLE cards (
    id INTEGER PRIMARY KEY,
    term_id INTEGER NOT NULL REFERENCES terms(id),
    direction TEXT NOT NULL CHECK (direction IN ('read', 'write', 'pinyin')),
    assessed_at TEXT,
    initial_known INTEGER CHECK (initial_known IN (0, 1)),
    due TEXT NOT NULL,
    stability REAL NOT NULL DEFAULT 0,
    difficulty REAL NOT NULL DEFAULT 0,
    scheduled_days INTEGER NOT NULL DEFAULT 0,
    reps INTEGER NOT NULL DEFAULT 0,
    lapses INTEGER NOT NULL DEFAULT 0,
    state INTEGER NOT NULL DEFAULT 0,
    last_review TEXT,
    remaining_steps INTEGER NOT NULL DEFAULT 0,
    UNIQUE (term_id, direction)
);
CREATE INDEX cards_due ON cards(direction, due);
CREATE TABLE settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    value TEXT NOT NULL
);
CREATE TABLE sessions (
    id INTEGER PRIMARY KEY,
    settings TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'paused', 'complete', 'ended')),
    revision INTEGER NOT NULL DEFAULT 0,
    initial_size INTEGER NOT NULL,
    last_term_id INTEGER NOT NULL DEFAULT 0,
    undo_floor INTEGER NOT NULL DEFAULT 0,
    notice TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL,
    completed_at TEXT
);
CREATE UNIQUE INDEX one_unfinished_session ON sessions((1))
    WHERE status IN ('active', 'paused');
CREATE TABLE session_cards (
    session_id INTEGER NOT NULL REFERENCES sessions(id),
    card_id INTEGER NOT NULL REFERENCES cards(id),
    position INTEGER NOT NULL,
    cleared INTEGER NOT NULL DEFAULT 0 CHECK (cleared IN (0, 1)),
    PRIMARY KEY (session_id, card_id)
);
CREATE TABLE review_events (
    id INTEGER PRIMARY KEY,
    session_id INTEGER NOT NULL REFERENCES sessions(id),
    card_id INTEGER NOT NULL REFERENCES cards(id),
    occurred_at TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('assessment', 'review')),
    right_answer INTEGER NOT NULL CHECK (right_answer IN (0, 1)),
    rating INTEGER,
    before_state TEXT NOT NULL,
    after_state TEXT NOT NULL,
    scheduler_config TEXT NOT NULL,
    undone_at TEXT
);
CREATE TABLE mutations (
    id TEXT PRIMARY KEY,
    request TEXT NOT NULL,
    response TEXT NOT NULL
);

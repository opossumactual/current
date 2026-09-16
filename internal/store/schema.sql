CREATE TABLE IF NOT EXISTS categories (
    id       INTEGER PRIMARY KEY,
    title    TEXT NOT NULL,
    position INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS feeds (
    id                 INTEGER PRIMARY KEY,
    category_id        INTEGER REFERENCES categories(id) ON DELETE SET NULL,
    title              TEXT NOT NULL DEFAULT '',
    url                TEXT NOT NULL UNIQUE,
    site_url           TEXT NOT NULL DEFAULT '',
    description        TEXT NOT NULL DEFAULT '',
    icon_url           TEXT NOT NULL DEFAULT '',
    etag               TEXT NOT NULL DEFAULT '',
    last_modified      TEXT NOT NULL DEFAULT '',
    last_fetched_at    INTEGER,
    next_fetch_at      INTEGER NOT NULL DEFAULT 0,
    last_error         TEXT NOT NULL DEFAULT '',
    error_count        INTEGER NOT NULL DEFAULT 0,
    fetch_interval_min INTEGER NOT NULL DEFAULT 60,
    extract_fulltext   INTEGER NOT NULL DEFAULT 0,
    created_at         INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS feeds_category ON feeds(category_id);
CREATE INDEX IF NOT EXISTS feeds_next_fetch ON feeds(next_fetch_at);

CREATE TABLE IF NOT EXISTS articles (
    id           INTEGER PRIMARY KEY,
    feed_id      INTEGER NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
    guid         TEXT NOT NULL,
    url          TEXT NOT NULL DEFAULT '',
    title        TEXT NOT NULL DEFAULT '',
    author       TEXT NOT NULL DEFAULT '',
    content      TEXT NOT NULL DEFAULT '',
    summary      TEXT NOT NULL DEFAULT '',
    image_url    TEXT NOT NULL DEFAULT '',
    fulltext     TEXT NOT NULL DEFAULT '',
    published_at INTEGER NOT NULL,
    fetched_at   INTEGER NOT NULL,
    read         INTEGER NOT NULL DEFAULT 0,
    starred      INTEGER NOT NULL DEFAULT 0,
    read_at      INTEGER,
    UNIQUE(feed_id, guid)
);
CREATE INDEX IF NOT EXISTS articles_feed_pub ON articles(feed_id, published_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS articles_pub ON articles(published_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS articles_unread ON articles(read, published_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS articles_starred ON articles(starred) WHERE starred = 1;

CREATE VIRTUAL TABLE IF NOT EXISTS articles_fts USING fts5(
    title, content, content='articles', content_rowid='id', tokenize='unicode61'
);
CREATE TRIGGER IF NOT EXISTS articles_ai AFTER INSERT ON articles BEGIN
    INSERT INTO articles_fts(rowid, title, content) VALUES (new.id, new.title, new.content);
END;
CREATE TRIGGER IF NOT EXISTS articles_ad AFTER DELETE ON articles BEGIN
    INSERT INTO articles_fts(articles_fts, rowid, title, content) VALUES ('delete', old.id, old.title, old.content);
END;
CREATE TRIGGER IF NOT EXISTS articles_au AFTER UPDATE OF title, content ON articles BEGIN
    INSERT INTO articles_fts(articles_fts, rowid, title, content) VALUES ('delete', old.id, old.title, old.content);
    INSERT INTO articles_fts(rowid, title, content) VALUES (new.id, new.title, new.content);
END;

CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

func OpenStore(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// modernc/sqlite is happiest with a single writer.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// audioTable caches generated speech per (chapter, voice-model settings). Keying
// by the settings hash means a different model/voice/prompt regenerates into its
// own row and file instead of clobbering another model's cached audio.
const audioTable = `
CREATE TABLE IF NOT EXISTS audio (
  chapter_id    TEXT NOT NULL REFERENCES chapters(id) ON DELETE CASCADE,
  settings_hash TEXT NOT NULL,
  path          TEXT NOT NULL,
  bytes         INTEGER NOT NULL DEFAULT 0,
  timings       TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL,
  PRIMARY KEY (chapter_id, settings_hash)
);`

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  username      TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  display_name  TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
  token      TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);

CREATE TABLE IF NOT EXISTS novels (
  id            TEXT PRIMARY KEY,
  title         TEXT NOT NULL,
  author        TEXT NOT NULL DEFAULT '',
  description   TEXT NOT NULL DEFAULT '',
  cover_path    TEXT NOT NULL DEFAULT '',
  source        TEXT NOT NULL DEFAULT 'upload',
  source_url    TEXT NOT NULL DEFAULT '',
  created_by    INTEGER REFERENCES users(id) ON DELETE SET NULL,
  chapter_count INTEGER NOT NULL DEFAULT 0,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS chapters (
  id         TEXT PRIMARY KEY,
  novel_id   TEXT NOT NULL REFERENCES novels(id) ON DELETE CASCADE,
  idx        INTEGER NOT NULL,
  title      TEXT NOT NULL,
  content    TEXT NOT NULL,
  word_count INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_chapters_novel ON chapters(novel_id, idx);

CREATE TABLE IF NOT EXISTS progress (
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  novel_id   TEXT NOT NULL REFERENCES novels(id) ON DELETE CASCADE,
  chapter_id TEXT NOT NULL REFERENCES chapters(id) ON DELETE CASCADE,
  position   REAL NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (user_id, novel_id)
);

CREATE TABLE IF NOT EXISTS history (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  novel_id   TEXT NOT NULL REFERENCES novels(id) ON DELETE CASCADE,
  chapter_id TEXT REFERENCES chapters(id) ON DELETE SET NULL,
  event      TEXT NOT NULL,
  detail     TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_history_user ON history(user_id, id DESC);

CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tts_providers (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT NOT NULL,
  base_url   TEXT NOT NULL DEFAULT '',
  api_key    TEXT NOT NULL DEFAULT '',
  model      TEXT NOT NULL DEFAULT '',
  voice      TEXT NOT NULL DEFAULT '',
  speed      REAL NOT NULL DEFAULT 1.0,
  lang       TEXT NOT NULL DEFAULT '',
  timings    INTEGER NOT NULL DEFAULT -1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE VIRTUAL TABLE IF NOT EXISTS novel_search USING fts5(
  novel_id UNINDEXED, title, author, body
);
`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	if _, err := s.db.Exec(audioTable); err != nil {
		return err
	}

	// Releases before this cached one file per chapter regardless of the voice
	// model, and later added the per-chunk word timings column. Rebuild any
	// older table keyed by (chapter, settings) with a timings column. Audio is a
	// regenerable cache, so dropping the old table is safe.
	var audioSQL string
	err := s.db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type='table' AND name='audio'`,
	).Scan(&audioSQL)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if audioSQL != "" && (!strings.Contains(audioSQL, "PRIMARY KEY (chapter_id, settings_hash)") ||
		!strings.Contains(audioSQL, "timings")) {
		if _, err := s.db.Exec(`DROP TABLE audio`); err != nil {
			return err
		}
		if _, err := s.db.Exec(audioTable); err != nil {
			return err
		}
	}
	return nil
}

func now() string        { return time.Now().UTC().Format(time.RFC3339) }
func nowTime() time.Time { return time.Now().UTC() }

// ---------- users ----------

var errNotFound = errors.New("not found")

type User struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	CreatedAt   string `json:"createdAt"`
}

func (s *Store) CreateUser(username, hash, display string) (User, error) {
	ts := now()
	res, err := s.db.Exec(
		`INSERT INTO users (username, password_hash, display_name, created_at) VALUES (?,?,?,?)`,
		username, hash, display, ts,
	)
	if err != nil {
		return User{}, err
	}
	id, _ := res.LastInsertId()
	return User{ID: id, Username: username, DisplayName: display, CreatedAt: ts}, nil
}

func (s *Store) UserByUsername(username string) (User, string, error) {
	var u User
	var hash string
	err := s.db.QueryRow(
		`SELECT id, username, display_name, created_at, password_hash FROM users WHERE username = ?`,
		username,
	).Scan(&u.ID, &u.Username, &u.DisplayName, &u.CreatedAt, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, "", errNotFound
	}
	return u, hash, err
}

func (s *Store) UserByID(id int64) (User, error) {
	var u User
	err := s.db.QueryRow(
		`SELECT id, username, display_name, created_at FROM users WHERE id = ?`, id,
	).Scan(&u.ID, &u.Username, &u.DisplayName, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, errNotFound
	}
	return u, err
}

func (s *Store) UpdateUserProfile(id int64, display string) error {
	_, err := s.db.Exec(`UPDATE users SET display_name = ? WHERE id = ?`, display, id)
	return err
}

func (s *Store) SetPassword(id int64, hash string) error {
	_, err := s.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, hash, id)
	return err
}

func (s *Store) UserCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// ---------- sessions ----------

func (s *Store) CreateSession(token string, userID int64, ttl time.Duration) error {
	_, err := s.db.Exec(
		`INSERT INTO sessions (token, user_id, created_at, expires_at) VALUES (?,?,?,?)`,
		token, userID, now(), nowTime().Add(ttl).Format(time.RFC3339),
	)
	return err
}

func (s *Store) UserBySession(token string) (User, error) {
	var u User
	var exp string
	err := s.db.QueryRow(`
		SELECT u.id, u.username, u.display_name, u.created_at, s.expires_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token = ?`, token,
	).Scan(&u.ID, &u.Username, &u.DisplayName, &u.CreatedAt, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, errNotFound
	}
	if err != nil {
		return User{}, err
	}
	if t, perr := time.Parse(time.RFC3339, exp); perr == nil && time.Now().UTC().After(t) {
		_ = s.DeleteSession(token)
		return User{}, errNotFound
	}
	return u, nil
}

func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	return err
}

// ---------- novels & chapters ----------

type Novel struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Author       string `json:"author"`
	Description  string `json:"description"`
	CoverPath    string `json:"-"`
	Source       string `json:"source"`
	SourceURL    string `json:"sourceUrl"`
	ChapterCount int    `json:"chapterCount"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
}

type Chapter struct {
	ID        string `json:"id"`
	NovelID   string `json:"novelId"`
	Index     int    `json:"index"`
	Title     string `json:"title"`
	Content   string `json:"content,omitempty"`
	WordCount int    `json:"wordCount"`
}

type NovelInput struct {
	Title       string
	Author      string
	Description string
	Source      string
	SourceURL   string
	CreatedBy   int64
	Chapters    []ChapterInput
}

type ChapterInput struct {
	Title   string
	Content string
}

func (s *Store) CreateNovel(in NovelInput) (Novel, error) {
	n := Novel{
		ID:        randomID(12),
		Title:     in.Title,
		Author:    in.Author,
		Source:    in.Source,
		SourceURL: in.SourceURL,
		CreatedAt: now(),
		UpdatedAt: now(),
	}
	if n.Title == "" {
		n.Title = "Untitled"
	}
	if n.Source == "" {
		n.Source = "upload"
	}

	tx, err := s.db.Begin()
	if err != nil {
		return Novel{}, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`INSERT INTO novels (id,title,author,description,source,source_url,created_by,chapter_count,created_at,updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		n.ID, n.Title, n.Author, in.Description, n.Source, n.SourceURL, in.CreatedBy, len(in.Chapters), n.CreatedAt, n.UpdatedAt,
	); err != nil {
		return Novel{}, err
	}

	var words int
	for i, c := range in.Chapters {
		title := c.Title
		if title == "" {
			title = fmt.Sprintf("Chapter %d", i+1)
		}
		wc := wordCount(c.Content)
		if _, err := tx.Exec(
			`INSERT INTO chapters (id,novel_id,idx,title,content,word_count,created_at) VALUES (?,?,?,?,?,?,?)`,
			randomID(12), n.ID, i, title, c.Content, wc, now(),
		); err != nil {
			return Novel{}, err
		}
		words += wc
	}

	if err := tx.Commit(); err != nil {
		return Novel{}, err
	}

	n.ChapterCount = len(in.Chapters)
	n.Description = in.Description
	if err := s.indexNovel(n, words); err != nil {
		return Novel{}, err
	}
	return n, nil
}

func (s *Store) indexNovel(n Novel, words int) error {
	body := fmt.Sprintf("%s %d chapters %d words", n.Description, n.ChapterCount, words)
	_, err := s.db.Exec(`DELETE FROM novel_search WHERE novel_id = ?`, n.ID)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO novel_search (novel_id, title, author, body) VALUES (?,?,?,?)`,
		n.ID, n.Title, n.Author, body,
	)
	return err
}

func (s *Store) ListNovels(query string) ([]Novel, error) {
	q := `
		SELECT n.id,n.title,n.author,n.description,n.source,n.source_url,n.chapter_count,n.created_at,n.updated_at
		FROM novels n`
	args := []any{}
	if query != "" {
		q = `
			SELECT n.id,n.title,n.author,n.description,n.source,n.source_url,n.chapter_count,n.created_at,n.updated_at
			FROM novel_search s JOIN novels n ON n.id = s.novel_id
			WHERE novel_search MATCH ? ORDER BY rank`
		args = append(args, ftsQuery(query))
	} else {
		q += ` ORDER BY n.title COLLATE NOCASE`
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNovels(rows)
}

func (s *Store) GetNovel(id string) (Novel, error) {
	var n Novel
	err := s.db.QueryRow(`
		SELECT id,title,author,description,source,source_url,chapter_count,created_at,updated_at
		FROM novels WHERE id = ?`, id,
	).Scan(&n.ID, &n.Title, &n.Author, &n.Description, &n.Source, &n.SourceURL, &n.ChapterCount, &n.CreatedAt, &n.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Novel{}, errNotFound
	}
	return n, err
}

func (s *Store) DeleteNovel(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM novel_search WHERE novel_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM novels WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListChapters(novelID string, withContent bool) ([]Chapter, error) {
	cols := "id,novel_id,idx,title,word_count"
	if withContent {
		cols = "id,novel_id,idx,title,word_count,content"
	}
	rows, err := s.db.Query(`SELECT `+cols+` FROM chapters WHERE novel_id = ? ORDER BY idx`, novelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chapter
	for rows.Next() {
		var c Chapter
		if withContent {
			if err := rows.Scan(&c.ID, &c.NovelID, &c.Index, &c.Title, &c.WordCount, &c.Content); err != nil {
				return nil, err
			}
		} else {
			if err := rows.Scan(&c.ID, &c.NovelID, &c.Index, &c.Title, &c.WordCount); err != nil {
				return nil, err
			}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetChapter(id string) (Chapter, error) {
	var c Chapter
	err := s.db.QueryRow(
		`SELECT id,novel_id,idx,title,word_count,content FROM chapters WHERE id = ?`, id,
	).Scan(&c.ID, &c.NovelID, &c.Index, &c.Title, &c.WordCount, &c.Content)
	if errors.Is(err, sql.ErrNoRows) {
		return Chapter{}, errNotFound
	}
	return c, err
}

func (s *Store) NextChapter(novelID string, idx int) (Chapter, error) {
	var c Chapter
	err := s.db.QueryRow(
		`SELECT id,novel_id,idx,title,word_count FROM chapters WHERE novel_id = ? AND idx > ? ORDER BY idx LIMIT 1`,
		novelID, idx,
	).Scan(&c.ID, &c.NovelID, &c.Index, &c.Title, &c.WordCount)
	if errors.Is(err, sql.ErrNoRows) {
		return Chapter{}, errNotFound
	}
	return c, err
}

// ---------- progress & history ----------

type Progress struct {
	NovelID      string  `json:"novelId"`
	NovelTitle   string  `json:"novelTitle"`
	ChapterID    string  `json:"chapterId"`
	ChapterIdx   int     `json:"chapterIndex"`
	ChapterTitle string  `json:"chapterTitle"`
	Position     float64 `json:"position"`
	UpdatedAt    string  `json:"updatedAt"`
}

func (s *Store) SaveProgress(userID int64, novelID, chapterID string, position float64) error {
	_, err := s.db.Exec(`
		INSERT INTO progress (user_id,novel_id,chapter_id,position,updated_at) VALUES (?,?,?,?,?)
		ON CONFLICT(user_id,novel_id) DO UPDATE SET chapter_id=excluded.chapter_id, position=excluded.position, updated_at=excluded.updated_at`,
		userID, novelID, chapterID, position, now())
	return err
}

func (s *Store) GetProgress(userID int64, novelID string) (Progress, error) {
	var p Progress
	err := s.db.QueryRow(`
		SELECT p.novel_id, n.title, p.chapter_id, c.idx, c.title, p.position, p.updated_at
		FROM progress p
		JOIN novels n ON n.id = p.novel_id
		JOIN chapters c ON c.id = p.chapter_id
		WHERE p.user_id = ? AND p.novel_id = ?`, userID, novelID,
	).Scan(&p.NovelID, &p.NovelTitle, &p.ChapterID, &p.ChapterIdx, &p.ChapterTitle, &p.Position, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Progress{}, errNotFound
	}
	return p, err
}

func (s *Store) ListProgress(userID int64) ([]Progress, error) {
	rows, err := s.db.Query(`
		SELECT p.novel_id, n.title, p.chapter_id, c.idx, c.title, p.position, p.updated_at
		FROM progress p
		JOIN novels n ON n.id = p.novel_id
		JOIN chapters c ON c.id = p.chapter_id
		WHERE p.user_id = ?
		ORDER BY p.updated_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Progress
	for rows.Next() {
		var p Progress
		if err := rows.Scan(&p.NovelID, &p.NovelTitle, &p.ChapterID, &p.ChapterIdx, &p.ChapterTitle, &p.Position, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type HistoryEvent struct {
	ID           int64  `json:"id"`
	NovelID      string `json:"novelId"`
	NovelTitle   string `json:"novelTitle"`
	ChapterID    string `json:"chapterId"`
	ChapterTitle string `json:"chapterTitle"`
	Event        string `json:"event"`
	Detail       string `json:"detail"`
	CreatedAt    string `json:"createdAt"`
}

func (s *Store) AddHistory(userID int64, novelID, chapterID, event, detail string) error {
	var cid any
	if chapterID != "" {
		cid = chapterID
	}
	_, err := s.db.Exec(
		`INSERT INTO history (user_id,novel_id,chapter_id,event,detail,created_at) VALUES (?,?,?,?,?,?)`,
		userID, novelID, cid, event, detail, now())
	return err
}

func (s *Store) ListHistory(userID int64, limit int) ([]HistoryEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(`
		SELECT h.id, h.novel_id, n.title, COALESCE(h.chapter_id,''), COALESCE(c.title,''), h.event, h.detail, h.created_at
		FROM history h
		JOIN novels n ON n.id = h.novel_id
		LEFT JOIN chapters c ON c.id = h.chapter_id
		WHERE h.user_id = ?
		ORDER BY h.id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HistoryEvent
	for rows.Next() {
		var e HistoryEvent
		if err := rows.Scan(&e.ID, &e.NovelID, &e.NovelTitle, &e.ChapterID, &e.ChapterTitle, &e.Event, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) Stats(userID int64) (map[string]int, error) {
	out := map[string]int{}
	row := func(key, q string, args ...any) error {
		var n int
		if err := s.db.QueryRow(q, args...).Scan(&n); err != nil {
			return err
		}
		out[key] = n
		return nil
	}
	if err := row("novels", `SELECT COUNT(*) FROM novels`); err != nil {
		return nil, err
	}
	if err := row("chapters", `SELECT COUNT(*) FROM chapters`); err != nil {
		return nil, err
	}
	if err := row("inProgress", `SELECT COUNT(*) FROM progress WHERE user_id = ?`, userID); err != nil {
		return nil, err
	}
	if err := row("audio", `SELECT COUNT(*) FROM audio`); err != nil {
		return nil, err
	}
	return out, nil
}

// ---------- audio cache ----------

type AudioRecord struct {
	ChapterID    string `json:"chapterId"`
	Path         string `json:"-"`
	Bytes        int64  `json:"bytes"`
	SettingsHash string `json:"settingsHash"`
	Timings      string `json:"-"` // JSON array of word timings, "" if the provider gives none
	CreatedAt    string `json:"createdAt"`
}

func (s *Store) GetAudio(chapterID, settingsHash string) (AudioRecord, error) {
	var a AudioRecord
	err := s.db.QueryRow(
		`SELECT chapter_id, path, settings_hash, bytes, timings, created_at FROM audio WHERE chapter_id = ? AND settings_hash = ?`,
		chapterID, settingsHash,
	).Scan(&a.ChapterID, &a.Path, &a.SettingsHash, &a.Bytes, &a.Timings, &a.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return AudioRecord{}, errNotFound
	}
	if err != nil {
		return AudioRecord{}, err
	}
	return a, nil
}

func (s *Store) PutAudio(chapterID, path, settingsHash string, bytes int64, timings string) error {
	_, err := s.db.Exec(`
		INSERT INTO audio (chapter_id,settings_hash,path,bytes,timings,created_at) VALUES (?,?,?,?,?,?)
		ON CONFLICT(chapter_id, settings_hash) DO UPDATE SET path=excluded.path, bytes=excluded.bytes, timings=excluded.timings, created_at=excluded.created_at`,
		chapterID, settingsHash, path, bytes, timings, now())
	return err
}

// DeleteAudio removes the cached audio for one chapter under one set of voice
// settings, returning its stored path so the caller can delete the file. Other
// models' cached audio for the chapter is left untouched.
func (s *Store) DeleteAudio(chapterID, settingsHash string) (string, error) {
	rec, err := s.GetAudio(chapterID, settingsHash)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return "", nil
		}
		return "", err
	}
	if _, err := s.db.Exec(
		`DELETE FROM audio WHERE chapter_id = ? AND settings_hash = ?`,
		chapterID, settingsHash,
	); err != nil {
		return "", err
	}
	return rec.Path, nil
}

// ---------- settings (key/value) ----------

func (s *Store) GetSetting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errNotFound
	}
	return v, err
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings (key,value) VALUES (?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key, value)
	return err
}

// ---------- tts providers ----------

// TTSProvider is a configured speech backend. Timings is tri-state:
// -1 unknown (probe at generation time), 0 the provider has no word timings,
// 1 the provider returns word timings (e.g. Kokoro's captioned endpoint).
type TTSProvider struct {
	ID        int64   `json:"-"`
	Name      string  `json:"name"`
	BaseURL   string  `json:"baseUrl"`
	APIKey    string  `json:"-"`
	Model     string  `json:"model"`
	Voice     string  `json:"voice"`
	Speed     float64 `json:"speed"`
	Lang      string  `json:"lang"`
	Timings   int     `json:"-"`
	CreatedAt string  `json:"createdAt"`
	UpdatedAt string  `json:"updatedAt"`
}

func scanTTSProviders(rows *sql.Rows) ([]TTSProvider, error) {
	var out []TTSProvider
	for rows.Next() {
		var p TTSProvider
		if err := rows.Scan(&p.ID, &p.Name, &p.BaseURL, &p.APIKey, &p.Model, &p.Voice,
			&p.Speed, &p.Lang, &p.Timings, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

const ttsProviderCols = `id,name,base_url,api_key,model,voice,speed,lang,timings,created_at,updated_at`

func (s *Store) ListTTSProviders() ([]TTSProvider, error) {
	rows, err := s.db.Query(`SELECT ` + ttsProviderCols + ` FROM tts_providers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTTSProviders(rows)
}

func (s *Store) GetTTSProvider(id int64) (TTSProvider, error) {
	rows, err := s.db.Query(`SELECT `+ttsProviderCols+` FROM tts_providers WHERE id = ?`, id)
	if err != nil {
		return TTSProvider{}, err
	}
	defer rows.Close()
	list, err := scanTTSProviders(rows)
	if err != nil {
		return TTSProvider{}, err
	}
	if len(list) == 0 {
		return TTSProvider{}, errNotFound
	}
	return list[0], nil
}

func (s *Store) CountTTSProviders() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM tts_providers`).Scan(&n)
	return n, err
}

func (s *Store) CreateTTSProvider(p TTSProvider) (TTSProvider, error) {
	ts := now()
	res, err := s.db.Exec(`
		INSERT INTO tts_providers (name,base_url,api_key,model,voice,speed,lang,timings,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		p.Name, p.BaseURL, p.APIKey, p.Model, p.Voice, p.Speed, p.Lang, p.Timings, ts, ts)
	if err != nil {
		return TTSProvider{}, err
	}
	id, _ := res.LastInsertId()
	p.ID = id
	p.CreatedAt = ts
	p.UpdatedAt = ts
	return p, nil
}

func (s *Store) UpdateTTSProvider(p TTSProvider) error {
	_, err := s.db.Exec(`
		UPDATE tts_providers SET name=?, base_url=?, api_key=?, model=?, voice=?, speed=?, lang=?, timings=?, updated_at=?
		WHERE id=?`,
		p.Name, p.BaseURL, p.APIKey, p.Model, p.Voice, p.Speed, p.Lang, p.Timings, now(), p.ID)
	return err
}

func (s *Store) DeleteTTSProvider(id int64) error {
	_, err := s.db.Exec(`DELETE FROM tts_providers WHERE id = ?`, id)
	return err
}

// ---------- helpers ----------

func scanNovels(rows *sql.Rows) ([]Novel, error) {
	var out []Novel
	for rows.Next() {
		var n Novel
		if err := rows.Scan(&n.ID, &n.Title, &n.Author, &n.Description, &n.Source, &n.SourceURL, &n.ChapterCount, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

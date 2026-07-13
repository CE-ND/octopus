package op

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestDiscoverCodexSessions(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	statePath := filepath.Join(codexHome, "state_5.sqlite")

	stateDB, err := sql.Open("sqlite", statePath)
	if err != nil {
		t.Fatalf("open state database: %v", err)
	}
	_, err = stateDB.Exec(`CREATE TABLE threads (
		id TEXT PRIMARY KEY,
		title TEXT,
		cwd TEXT,
		updated_at INTEGER,
		updated_at_ms INTEGER,
		recency_at_ms INTEGER,
		source TEXT,
		thread_source TEXT,
		model TEXT,
		archived INTEGER DEFAULT 0
	)`)
	if err != nil {
		t.Fatalf("create threads table: %v", err)
	}
	_, err = stateDB.Exec(`INSERT INTO threads (id, title, cwd, updated_at_ms, recency_at_ms, source, thread_source, model, archived) VALUES
		('session-active', 'Active session', '\\?\C:\workspace', 4000, 4000, 'vscode', 'user', 'gpt-5.5', 0),
		('session-subagent', 'Internal worker', 'C:\workspace', 3000, 3000, '{"subagent":{}}', 'subagent', 'gpt-5.5', 0),
		('session-cli', 'CLI session', 'C:\workspace', 2000, 2000, 'cli', 'user', 'gpt-5.5', 0),
		('session-archived', 'Archived session', 'C:\old', 1000, 1000, 'vscode', 'user', 'gpt-5.5', 1)`)
	if err != nil {
		t.Fatalf("insert threads: %v", err)
	}
	if err := stateDB.Close(); err != nil {
		t.Fatalf("close state database: %v", err)
	}

	sessions, err := discoverCodexSessions()
	if err != nil {
		t.Fatalf("discover sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected one active session, got %d", len(sessions))
	}
	if sessions[0].ID != "session-active" || sessions[0].Title != "Active session" {
		t.Fatalf("unexpected session: %+v", sessions[0])
	}
	if sessions[0].CWD != `C:\workspace` {
		t.Fatalf("expected normalized Windows path, got %q", sessions[0].CWD)
	}
	if sessions[0].Model != "gpt-5.5" {
		t.Fatalf("expected current model, got %q", sessions[0].Model)
	}
}

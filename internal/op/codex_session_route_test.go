package op

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func createCodexStateDB(t *testing.T, codexHome string) {
	t.Helper()
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
}

func TestDiscoverCodexSessions(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	createCodexStateDB(t, codexHome)

	sessions, err := discoverCodexSessions()
	if err != nil {
		t.Fatalf("discover sessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected two active sessions, got %d: %+v", len(sessions), sessions)
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
	if sessions[1].ID != "session-cli" || sessions[1].Source != "cli" {
		t.Fatalf("expected CLI session included, got %+v", sessions[1])
	}
}

func TestDiscoverCodexSessionsFromRollouts(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "09", "06")
	if err := os.MkdirAll(rolloutDir, 0o755); err != nil {
		t.Fatalf("create rollout dir: %v", err)
	}
	rolloutPath := filepath.Join(rolloutDir, "rollout-2026-09-06T10-00-00-019f8f59-968c-70a3-8aea-23d838fe5d1b.jsonl")
	rollout := `{"timestamp":"2026-09-06T10:00:01Z","type":"session_meta","payload":{"id":"019f8f59-968c-70a3-8aea-23d838fe5d1b","cwd":"D:\\work\\demo","originator":"codex_cli_rs","cli_version":"0.55.0","source":"cli","thread_source":"user"}}
{"timestamp":"2026-09-06T10:00:02Z","type":"event_msg","payload":{"type":"task_started"}}
{"timestamp":"2026-09-06T10:00:03Z","type":"turn_context","payload":{"cwd":"D:\\work\\demo","model":"gpt-5.5-codex"}}
`
	if err := os.WriteFile(rolloutPath, []byte(rollout), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	index := `{"id":"019f8f59-968c-70a3-8aea-23d838fe5d1b","thread_name":"CLI session title","updated_at":"2026-09-06T10:05:00.123456Z"}
`
	if err := os.WriteFile(filepath.Join(codexHome, "session_index.jsonl"), []byte(index), 0o644); err != nil {
		t.Fatalf("write session index: %v", err)
	}

	sessions, err := discoverCodexSessions()
	if err != nil {
		t.Fatalf("discover sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected one rollout session, got %d: %+v", len(sessions), sessions)
	}
	session := sessions[0]
	if session.ID != "019f8f59-968c-70a3-8aea-23d838fe5d1b" {
		t.Fatalf("unexpected session id: %+v", session)
	}
	if session.Title != "CLI session title" {
		t.Fatalf("expected indexed title, got %q", session.Title)
	}
	if session.Model != "gpt-5.5-codex" {
		t.Fatalf("expected rollout model, got %q", session.Model)
	}
	if session.CWD != `D:\work\demo` {
		t.Fatalf("expected rollout cwd, got %q", session.CWD)
	}
	if session.Source != "cli" {
		t.Fatalf("expected cli source, got %q", session.Source)
	}
	if session.UpdatedAt == 0 {
		t.Fatalf("expected indexed update timestamp")
	}
}

func TestDiscoverCodexSessionsSkipsDesktopRolloutsAndSubagentThreads(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	createCodexStateDB(t, codexHome)

	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "09", "06")
	if err := os.MkdirAll(rolloutDir, 0o755); err != nil {
		t.Fatalf("create rollout dir: %v", err)
	}
	files := map[string]string{
		// Desktop sessions live in the state DB already, rollout copy must be skipped.
		"rollout-2026-09-06T09-00-00-11111111-1111-4111-8111-111111111111.jsonl": `{"type":"session_meta","payload":{"id":"11111111-1111-4111-8111-111111111111","cwd":"C:\\x","source":"vscode","originator":"Codex Desktop","thread_source":"user"}}`,
		// CLI subagent threads are not user sessions.
		"rollout-2026-09-06T09-01-00-22222222-2222-4222-8222-222222222222.jsonl": `{"type":"session_meta","payload":{"id":"22222222-2222-4222-8222-222222222222","cwd":"C:\\x","source":"cli","thread_source":"subagent"}}`,
		// Malformed session id in file name.
		"rollout-broken.jsonl": `{"type":"session_meta","payload":{"id":"broken","source":"cli","thread_source":"user"}}`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(rolloutDir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write rollout %s: %v", name, err)
		}
	}

	sessions, err := discoverCodexSessions()
	if err != nil {
		t.Fatalf("discover sessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected only state DB sessions, got %d: %+v", len(sessions), sessions)
	}
	for _, session := range sessions {
		if session.ID != "session-active" && session.ID != "session-cli" {
			t.Fatalf("unexpected session discovered: %+v", session)
		}
	}
}

func TestDiscoverCodexSessionsWithoutStateDB(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "09", "06")
	if err := os.MkdirAll(rolloutDir, 0o755); err != nil {
		t.Fatalf("create rollout dir: %v", err)
	}
	rolloutPath := filepath.Join(rolloutDir, "rollout-2026-09-06T10-00-00-019f8f59-968c-70a3-8aea-23d838fe5d1b.jsonl")
	rollout := `{"type":"session_meta","payload":{"id":"019f8f59-968c-70a3-8aea-23d838fe5d1b","cwd":"D:\\work\\demo","originator":"codex_cli_rs","source":"cli","thread_source":"user"}}
{"type":"turn_context","payload":{"model":"gpt-5.5-codex"}}
`
	if err := os.WriteFile(rolloutPath, []byte(rollout), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	sessions, err := discoverCodexSessions()
	if err != nil {
		t.Fatalf("expected discovery to succeed without state DB, got %v", err)
	}
	if len(sessions) != 1 || sessions[0].Model != "gpt-5.5-codex" {
		t.Fatalf("expected rollout session discovered, got %+v", sessions)
	}
}

func TestRolloutSessionID(t *testing.T) {
	cases := map[string]string{
		"rollout-2026-07-23T22-20-45-019f8f59-968c-70a3-8aea-23d838fe5d1b.jsonl": "019f8f59-968c-70a3-8aea-23d838fe5d1b",
		"rollout-broken.jsonl":                                                   "",
		"unrelated.jsonl":                                                        "",
	}
	for name, want := range cases {
		if got := rolloutSessionID(name); got != want {
			t.Fatalf("rolloutSessionID(%q) = %q, want %q", name, got, want)
		}
	}
}

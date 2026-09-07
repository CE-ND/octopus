package op

import (
	"os"
	"path/filepath"
	"testing"
)

func createClaudeTranscript(t *testing.T, claudeHome, workspace, sessionID, content string) {
	t.Helper()
	dir := filepath.Join(claudeHome, "projects", workspace)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir projects dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionID+".jsonl"), []byte(content), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

func TestDiscoverClaudeSessions(t *testing.T) {
	claudeHome := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeHome)

	createClaudeTranscript(t, claudeHome, "D--Desktop-project", "5f262125-d0bb-4aa0-a468-41454e4fb07e",
		`{"type":"mode","mode":"normal","sessionId":"5f262125-d0bb-4aa0-a468-41454e4fb07e"}
{"type":"user","parentUuid":null,"cwd":"D:\\Desktop\\project","isMeta":false,"sessionId":"5f262125-d0bb-4aa0-a468-41454e4fb07e","message":{"role":"user","content":[{"type":"text","text":"<system-reminder>ctx</system-reminder>\n帮我修复登录的 bug"}]}}
{"type":"assistant","cwd":"D:\\Desktop\\project","sessionId":"5f262125-d0bb-4aa0-a468-41454e4fb07e","message":{"role":"assistant","model":"claude-fable-5-1","content":[{"type":"text","text":"ok"}]}}
`)
	createClaudeTranscript(t, claudeHome, "D--Desktop-project", "15118156-381a-415b-8f43-208224f9e53a",
		`{"type":"user","cwd":"D:\\Desktop\\project","isMeta":true,"message":{"role":"user","content":"Caveat: meta"}}
{"type":"user","cwd":"D:\\Desktop\\project","isMeta":false,"message":{"role":"user","content":"<command-name>/clear</command-name>"}}
{"type":"user","cwd":"D:\\Desktop\\project","isMeta":false,"message":{"role":"user","content":"列出目录结构"}}
{"type":"assistant","cwd":"D:\\Desktop\\project","message":{"role":"assistant","model":"claude-opus-5[1m]","content":[]}}
{"type":"summary","summary":"Resumable session summary"}
`)

	sessions := discoverClaudeSessions(map[string]struct{}{"15118156-381a-415b-8f43-208224f9e53a": {}})
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session (known id skipped), got %d: %+v", len(sessions), sessions)
	}
	session := sessions[0]
	if session.ID != "5f262125-d0bb-4aa0-a468-41454e4fb07e" {
		t.Fatalf("unexpected session id: %q", session.ID)
	}
	if session.Source != "claude" {
		t.Fatalf("unexpected source: %q", session.Source)
	}
	if session.CWD != `D:\Desktop\project` {
		t.Fatalf("unexpected cwd: %q", session.CWD)
	}
	if session.Model != "claude-fable-5-1" {
		t.Fatalf("unexpected model: %q", session.Model)
	}
	if session.Title != "帮我修复登录的 bug" {
		t.Fatalf("unexpected title: %q", session.Title)
	}
}

func TestDiscoverClaudeSessionsMetadataOnly(t *testing.T) {
	claudeHome := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeHome)

	// 只有本地合成 / 后台小模型记录时，模型应留空而不是误报
	createClaudeTranscript(t, claudeHome, "D--Desktop-empty", "90670c74-b47d-466f-ab7a-96f6946f5bab",
		`{"type":"user","cwd":"D:\\Desktop\\empty","isMeta":false,"message":{"role":"user","content":"hi"}}
{"type":"assistant","message":{"role":"assistant","model":"<synthetic>","content":[]}}
`)

	sessions := discoverClaudeSessions(nil)
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d: %+v", len(sessions), sessions)
	}
	if sessions[0].Model != "" {
		t.Fatalf("expected empty model for synthetic-only transcript, got %q", sessions[0].Model)
	}
	if sessions[0].Title != "hi" {
		t.Fatalf("unexpected title: %q", sessions[0].Title)
	}
}

func TestClaudeSessionModelVariants(t *testing.T) {
	cases := map[string]string{
		"claude-fable-5-1":      "claude-fable-5-1",
		"claude-fable-5-1[1m]":  "claude-fable-5-1",
		"claude-opus-5[effort]": "claude-opus-5",
		"<synthetic>":           "",
		"haiku":                 "",
		"":                      "",
	}
	for raw, want := range cases {
		if got := claudeSessionModel(raw); got != want {
			t.Fatalf("claudeSessionModel(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestDiscoverAgentSessionsMergesClaude(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	claudeHome := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeHome)

	createClaudeTranscript(t, claudeHome, "D--Desktop-project", "5f262125-d0bb-4aa0-a468-41454e4fb07e",
		`{"type":"user","cwd":"D:\\Desktop\\project","isMeta":false,"message":{"role":"user","content":"hello"}}`)

	sessions, err := discoverAgentSessions()
	if err != nil {
		t.Fatalf("discover agent sessions: %v", err)
	}
	found := false
	for _, session := range sessions {
		if session.ID == "5f262125-d0bb-4aa0-a468-41454e4fb07e" {
			found = true
			if session.Source != "claude" {
				t.Fatalf("unexpected source: %q", session.Source)
			}
		}
	}
	if !found {
		t.Fatalf("claude session missing from merged list: %+v", sessions)
	}
}

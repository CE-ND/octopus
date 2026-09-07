package op

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/cache"
	"github.com/bestruirui/octopus/internal/utils/log"
	"gorm.io/gorm/clause"
)

const (
	codexRolloutScanLimit  = 200
	codexRolloutModelLimit = 64
)

var codexSessionRouteCache = cache.New[string, model.CodexSessionRoute](16)

func codexSessionRouteRefreshCache(ctx context.Context) error {
	var routes []model.CodexSessionRoute
	if err := db.GetDB().WithContext(ctx).Find(&routes).Error; err != nil {
		return err
	}
	codexSessionRouteCache.Clear()
	for _, route := range routes {
		codexSessionRouteCache.Set(codexSessionRouteKey(route.SessionID, route.RequestModel), route)
	}
	return nil
}

func CodexSessionRouteSet(sessionID, requestModel string, groupID int, ctx context.Context) error {
	sessionID = strings.TrimSpace(sessionID)
	requestModel = strings.TrimSpace(requestModel)
	if sessionID == "" {
		return fmt.Errorf("session id is required")
	}
	if requestModel == "" {
		return fmt.Errorf("request model is required")
	}
	cacheKey := codexSessionRouteKey(sessionID, requestModel)
	if groupID <= 0 {
		if err := db.GetDB().WithContext(ctx).Where("session_id = ? AND request_model = ?", sessionID, requestModel).Delete(&model.CodexSessionRoute{}).Error; err != nil {
			return err
		}
		codexSessionRouteCache.Del(cacheKey)
		return nil
	}
	if _, ok := groupCache.Get(groupID); !ok {
		return fmt.Errorf("group not found")
	}
	route := model.CodexSessionRoute{SessionID: sessionID, RequestModel: requestModel, GroupID: groupID}
	if err := db.GetDB().WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "session_id"}, {Name: "request_model"}},
		DoUpdates: clause.AssignmentColumns([]string{"group_id", "updated_at"}),
	}).Create(&route).Error; err != nil {
		return err
	}
	codexSessionRouteCache.Set(cacheKey, route)
	return nil
}

func CodexSessionRouteResolve(sessionID string, requestModel string, ctx context.Context) (model.Group, bool, error) {
	if route, ok := codexSessionRouteCache.Get(codexSessionRouteKey(sessionID, requestModel)); ok {
		group, err := GroupGetEnabled(route.GroupID, ctx)
		if err == nil {
			return group, true, nil
		}
		// 绑定存在但组没了/不可用：回退自动路由前先留痕，避免"绑了但没生效"无声失败
		log.Warnf("codex session route matched but group unavailable, falling back to auto routing (session=%s, request_model=%s, group=%d): %v",
			sessionID, requestModel, route.GroupID, err)
	}
	group, err := GroupGetEnabledMap(requestModel, ctx)
	return group, false, err
}

func codexSessionRouteKey(sessionID, requestModel string) string {
	return strings.TrimSpace(sessionID) + "\x00" + strings.TrimSpace(requestModel)
}

func codexSessionRouteDeleteByGroup(groupID int) {
	for cacheKey, route := range codexSessionRouteCache.GetAll() {
		if route.GroupID == groupID {
			codexSessionRouteCache.Del(cacheKey)
		}
	}
}

type codexLocalSession struct {
	ID        string
	Title     string
	CWD       string
	UpdatedAt int64
	Model     string
	Source    string
}

func CodexSessionRouteList(ctx context.Context) ([]model.CodexSessionRouteView, error) {
	sessions, err := discoverAgentSessions()
	if err != nil {
		return nil, err
	}
	views := make([]model.CodexSessionRouteView, 0, len(sessions))
	for _, session := range sessions {
		view := model.CodexSessionRouteView{
			SessionID:    session.ID,
			Title:        session.Title,
			CWD:          session.CWD,
			UpdatedAt:    session.UpdatedAt,
			CurrentModel: session.Model,
			Source:       session.Source,
		}
		if route, ok := codexSessionRouteCache.Get(codexSessionRouteKey(session.ID, session.Model)); ok {
			view.GroupID = route.GroupID
			if group, exists := groupCache.Get(route.GroupID); exists {
				view.GroupName = group.Name
			}
		}
		views = append(views, view)
	}
	return views, nil
}

func discoverCodexSessions() ([]codexLocalSession, error) {
	stateSessions := make([]codexLocalSession, 0)
	knownIDs := make(map[string]struct{})
	statePath, err := findCodexStateDB()
	if err != nil {
		log.Debugw("codex_session.state_db_unavailable", "error", err.Error())
	} else if sessions, err := readCodexStateSessions(statePath); err != nil {
		log.Warnw("codex_session.state_db_read_failed", "error", err.Error())
	} else {
		stateSessions = sessions
		for _, session := range sessions {
			knownIDs[session.ID] = struct{}{}
		}
	}

	sessions := append(stateSessions, discoverRolloutSessions(knownIDs)...)
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt > sessions[j].UpdatedAt
	})
	return sessions, nil
}

// discoverAgentSessions 汇总 Codex 与 Claude Code 的本地会话，按最近使用排序。
func discoverAgentSessions() ([]codexLocalSession, error) {
	sessions, err := discoverCodexSessions()
	if err != nil {
		return nil, err
	}
	knownIDs := make(map[string]struct{}, len(sessions))
	for _, session := range sessions {
		knownIDs[session.ID] = struct{}{}
	}
	sessions = append(sessions, discoverClaudeSessions(knownIDs)...)
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt > sessions[j].UpdatedAt
	})
	return sessions, nil
}

func readCodexStateSessions(statePath string) ([]codexLocalSession, error) {
	dsn := "file:" + filepath.ToSlash(statePath) + "?mode=ro"
	localDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open Codex state database: %w", err)
	}
	defer localDB.Close()

	rows, err := localDB.Query(`SELECT id, COALESCE(title, ''), COALESCE(cwd, ''), COALESCE(updated_at_ms, updated_at * 1000, 0), COALESCE(model, ''), COALESCE(source, '') FROM threads WHERE COALESCE(archived, 0) = 0 AND thread_source = 'user' ORDER BY COALESCE(recency_at_ms, updated_at_ms, updated_at * 1000, 0) DESC`)
	if err != nil {
		return nil, fmt.Errorf("query Codex sessions: %w", err)
	}
	defer rows.Close()

	sessions := make([]codexLocalSession, 0)
	for rows.Next() {
		var session codexLocalSession
		if err := rows.Scan(&session.ID, &session.Title, &session.CWD, &session.UpdatedAt, &session.Model, &session.Source); err != nil {
			return nil, err
		}
		session.CWD = strings.TrimPrefix(session.CWD, `\\?\`)
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

// rolloutSessionMeta mirrors the session_meta payload on the first line of a
// rollout-*.jsonl file written by Codex CLI / desktop.
type rolloutSessionMeta struct {
	ID           string `json:"id"`
	CWD          string `json:"cwd"`
	Source       string `json:"source"`
	ThreadSource string `json:"thread_source"`
	Originator   string `json:"originator"`
}

func discoverRolloutSessions(knownIDs map[string]struct{}) []codexLocalSession {
	home := codexHomeDir()
	if home == "" {
		return nil
	}
	sessionsDir := filepath.Join(home, "sessions")
	entries, err := collectRolloutFiles(sessionsDir)
	if err != nil {
		log.Debugw("codex_session.rollout_walk_failed", "error", err.Error())
		return nil
	}
	if len(entries) == 0 {
		return nil
	}

	index := readCodexSessionIndex(home)
	sessions := make([]codexLocalSession, 0)
	for _, entry := range entries {
		sessionID := rolloutSessionID(entry.name)
		if sessionID == "" {
			continue
		}
		if _, ok := knownIDs[sessionID]; ok {
			continue
		}
		meta := readRolloutSessionMeta(entry.path)
		if meta == nil || !isCLIRolloutSessionMeta(meta) {
			continue
		}
		view := codexLocalSession{
			ID:     sessionID,
			CWD:    strings.TrimPrefix(meta.CWD, `\\?\`),
			Model:  readRolloutSessionModel(entry.path),
			Source: "cli",
		}
		if meta.Source != "" {
			view.Source = meta.Source
		}
		if info, ok := index[sessionID]; ok {
			view.Title = info.ThreadName
			view.UpdatedAt = info.UpdatedAtMs
		}
		if view.UpdatedAt == 0 {
			view.UpdatedAt = entry.modTime.UnixMilli()
		}
		sessions = append(sessions, view)
	}
	return sessions
}

func isCLIRolloutSessionMeta(meta *rolloutSessionMeta) bool {
	if meta.ThreadSource != "" && meta.ThreadSource != "user" {
		return false
	}
	return meta.Source == "cli" || meta.Originator == "codex_cli_rs"
}

type rolloutFileEntry struct {
	path    string
	name    string
	modTime time.Time
}

func collectRolloutFiles(sessionsDir string) ([]rolloutFileEntry, error) {
	entries := make([]rolloutFileEntry, 0)
	err := filepath.WalkDir(sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		entries = append(entries, rolloutFileEntry{path: path, name: d.Name(), modTime: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].modTime.After(entries[j].modTime)
	})
	if len(entries) > codexRolloutScanLimit {
		entries = entries[:codexRolloutScanLimit]
	}
	return entries, nil
}

// rolloutSessionID extracts the thread UUID encoded at the end of a rollout
// file name, e.g. rollout-2026-07-23T22-20-45-<uuid>.jsonl.
func rolloutSessionID(fileName string) string {
	name := strings.TrimSuffix(fileName, ".jsonl")
	if len(name) < 45 {
		return ""
	}
	id := name[len(name)-36:]
	if id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return ""
	}
	return id
}

func readRolloutSessionMeta(path string) *rolloutSessionMeta {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	var line struct {
		Type    string             `json:"type"`
		Payload rolloutSessionMeta `json:"payload"`
	}
	if err := json.NewDecoder(bufio.NewReaderSize(file, 64*1024)).Decode(&line); err != nil || line.Type != "session_meta" {
		return nil
	}
	return &line.Payload
}

func readRolloutSessionModel(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()

	decoder := json.NewDecoder(bufio.NewReaderSize(file, 64*1024))
	var line struct {
		Type    string `json:"type"`
		Payload struct {
			Model string `json:"model"`
		} `json:"payload"`
	}
	for i := 0; i < codexRolloutModelLimit; i++ {
		line.Type = ""
		line.Payload.Model = ""
		if err := decoder.Decode(&line); err != nil {
			return ""
		}
		if line.Type == "turn_context" {
			return strings.TrimSpace(line.Payload.Model)
		}
	}
	return ""
}

type codexSessionIndexEntry struct {
	ThreadName  string
	UpdatedAtMs int64
}

func readCodexSessionIndex(home string) map[string]codexSessionIndexEntry {
	index := make(map[string]codexSessionIndexEntry)
	file, err := os.Open(filepath.Join(home, "session_index.jsonl"))
	if err != nil {
		return index
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var entry struct {
			ID         string `json:"id"`
			ThreadName string `json:"thread_name"`
			UpdatedAt  string `json:"updated_at"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil || entry.ID == "" {
			continue
		}
		index[entry.ID] = codexSessionIndexEntry{ThreadName: entry.ThreadName}
		if updated, err := time.Parse(time.RFC3339Nano, entry.UpdatedAt); err == nil {
			index[entry.ID] = codexSessionIndexEntry{ThreadName: entry.ThreadName, UpdatedAtMs: updated.UnixMilli()}
		}
	}
	return index
}

func codexHomeDir() string {
	if root := strings.TrimSpace(os.Getenv("CODEX_HOME")); root != "" {
		return root
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex")
}

func findCodexStateDB() (string, error) {
	root := codexHomeDir()
	if root == "" {
		return "", fmt.Errorf("cannot resolve Codex home directory")
	}
	matches, err := filepath.Glob(filepath.Join(root, "state_*.sqlite"))
	if err != nil || len(matches) == 0 {
		return "", fmt.Errorf("Codex state database not found in %s", root)
	}
	sort.Slice(matches, func(i, j int) bool {
		left, _ := os.Stat(matches[i])
		right, _ := os.Stat(matches[j])
		if left == nil || right == nil {
			return matches[i] > matches[j]
		}
		return left.ModTime().After(right.ModTime())
	})
	return matches[0], nil
}

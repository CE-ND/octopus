package op

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/cache"
	"gorm.io/gorm/clause"
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
		if group, err := GroupGetEnabled(route.GroupID, ctx); err == nil {
			return group, true, nil
		}
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
}

func CodexSessionRouteList(ctx context.Context) ([]model.CodexSessionRouteView, error) {
	sessions, err := discoverCodexSessions()
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
	statePath, err := findCodexStateDB()
	if err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.ToSlash(statePath) + "?mode=ro"
	localDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open Codex state database: %w", err)
	}
	defer localDB.Close()

	rows, err := localDB.Query(`SELECT id, COALESCE(title, ''), COALESCE(cwd, ''), COALESCE(updated_at_ms, updated_at * 1000, 0), COALESCE(model, '') FROM threads WHERE COALESCE(archived, 0) = 0 AND thread_source = 'user' AND source <> 'cli' ORDER BY COALESCE(recency_at_ms, updated_at_ms, updated_at * 1000, 0) DESC`)
	if err != nil {
		return nil, fmt.Errorf("query Codex sessions: %w", err)
	}
	defer rows.Close()

	sessions := make([]codexLocalSession, 0)
	for rows.Next() {
		var session codexLocalSession
		if err := rows.Scan(&session.ID, &session.Title, &session.CWD, &session.UpdatedAt, &session.Model); err != nil {
			return nil, err
		}
		session.CWD = strings.TrimPrefix(session.CWD, `\\?\`)
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

func findCodexStateDB() (string, error) {
	root := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, ".codex")
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

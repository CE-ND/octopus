package model

import "time"

// CodexSessionRoute binds a Codex desktop thread UUID to a routing group.
// The UUID is received as the Responses API prompt_cache_key.
type CodexSessionRoute struct {
	ID           int       `json:"id" gorm:"primaryKey"`
	SessionID    string    `json:"session_id" gorm:"size:64;not null;uniqueIndex:idx_codex_session_model_route"`
	RequestModel string    `json:"request_model" gorm:"size:128;not null;uniqueIndex:idx_codex_session_model_route"`
	GroupID      int       `json:"group_id" gorm:"not null;index"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (CodexSessionRoute) TableName() string {
	return "codex_session_model_routes"
}

type CodexSessionRouteUpdateRequest struct {
	SessionID    string `json:"session_id" binding:"required"`
	RequestModel string `json:"request_model" binding:"required"`
	GroupID      int    `json:"group_id"`
}

type CodexSessionRouteView struct {
	SessionID    string `json:"session_id"`
	Title        string `json:"title"`
	CWD          string `json:"cwd"`
	UpdatedAt    int64  `json:"updated_at"`
	CurrentModel string `json:"current_model"`
	GroupID      int    `json:"group_id"`
	GroupName    string `json:"group_name"`
}

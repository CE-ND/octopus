package handlers

import (
	"net/http"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/group/codex-session").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(router.NewRoute("/list", http.MethodGet).Handle(getCodexSessionRoutes)).
		AddRoute(router.NewRoute("/route", http.MethodPost).Handle(updateCodexSessionRoute))
}

func getCodexSessionRoutes(c *gin.Context) {
	sessions, err := op.CodexSessionRouteList(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, sessions)
}

func updateCodexSessionRoute(c *gin.Context) {
	var req model.CodexSessionRouteUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	if err := op.CodexSessionRouteSet(req.SessionID, req.RequestModel, req.GroupID, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, "codex session route updated")
}

package handlers

import (
	"net/http"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/update").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/now-version", http.MethodGet).
				Handle(getNowVersion),
		).
		AddRoute(
			router.NewRoute("/info", http.MethodGet).
				Handle(getVersionInfo),
		).
		AddRoute(
			router.NewRoute("/releases", http.MethodGet).
				Handle(getVersionReleases),
		)
}

func getNowVersion(c *gin.Context) {
	resp.Success(c, conf.Version)
}

func getVersionInfo(c *gin.Context) {
	resp.Success(c, op.VersionInfoGet())
}

func getVersionReleases(c *gin.Context) {
	result, err := op.ReleaseCheckGet(c.Request.Context(), c.Query("refresh") == "true")
	if err != nil {
		resp.ErrorWithAppError(c, http.StatusBadGateway, err)
		return
	}
	resp.Success(c, result)
}

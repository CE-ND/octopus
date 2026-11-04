package handlers

import (
	"net/http"
	"strconv"

	"github.com/bestruirui/octopus/internal/plugin"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/plugin").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(listPlugins),
		).
		AddRoute(
			router.NewRoute("/install", http.MethodPost).
				Handle(installPlugin),
		).
		AddRoute(
			router.NewRoute("/market", http.MethodGet).
				Handle(pluginMarketplace),
		).
		AddRoute(
			router.NewRoute("/toggle", http.MethodPost).
				Handle(togglePlugin),
		).
		AddRoute(
			router.NewRoute("/remove", http.MethodPost).
				Handle(removePlugin),
		).
		AddRoute(
			router.NewRoute("/providers", http.MethodGet).
				Handle(listPluginProviders),
		).
		AddRoute(
			router.NewRoute("/auth/methods", http.MethodGet).
				Handle(pluginAuthMethods),
		).
		AddRoute(
			router.NewRoute("/auth/start", http.MethodPost).
				Handle(startPluginLogin),
		).
		AddRoute(
			router.NewRoute("/auth/status", http.MethodGet).
				Handle(pluginLoginStatus),
		).
		AddRoute(
			router.NewRoute("/auth/logout", http.MethodPost).
				Handle(logoutPlugin),
		).
		AddRoute(
			router.NewRoute("/models/refresh", http.MethodPost).
				Handle(refreshPluginModels),
		).
		AddRoute(
			router.NewRoute("/config", http.MethodGet).
				Handle(pluginConfig),
		).
		AddRoute(
			router.NewRoute("/config/save", http.MethodPost).
				Handle(savePluginConfig),
		).
		AddRoute(
			router.NewRoute("/ui/page", http.MethodGet).
				Handle(pluginPage),
		).
		AddRoute(
			router.NewRoute("/logs", http.MethodGet).
				Handle(pluginLogs),
		)
}

func listPlugins(c *gin.Context) {
	status, err := plugin.Default().Status(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, status)
}

func installPlugin(c *gin.Context) {
	var req struct {
		Spec string `json:"spec" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	installed, err := plugin.Default().Install(c.Request.Context(), req.Spec)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, installed)
}

func pluginMarketplace(c *gin.Context) {
	refresh := c.Query("refresh") == "1"
	items, err := plugin.Default().Marketplace(c.Request.Context(), refresh)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, items)
}

func togglePlugin(c *gin.Context) {
	var req struct {
		ID      string `json:"id" binding:"required"`
		Enabled bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	if err := plugin.Default().Toggle(c.Request.Context(), req.ID, req.Enabled); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, nil)
}

func removePlugin(c *gin.Context) {
	var req struct {
		ID string `json:"id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	if err := plugin.Default().Remove(c.Request.Context(), req.ID); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, nil)
}

func listPluginProviders(c *gin.Context) {
	refresh := c.Query("refresh") == "1"
	providers, err := plugin.Default().Providers(c.Request.Context(), refresh)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, providers)
}

func pluginAuthMethods(c *gin.Context) {
	methods, err := plugin.Default().AuthMethods(c.Request.Context(), c.Query("plugin_id"), c.Query("provider_id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, methods)
}

func startPluginLogin(c *gin.Context) {
	var req plugin.AuthStartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	session, err := plugin.Default().StartLogin(c.Request.Context(), req)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, session)
}

func pluginLoginStatus(c *gin.Context) {
	status, err := plugin.Default().LoginStatus(c.Request.Context(), c.Query("session_id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, status)
}

func logoutPlugin(c *gin.Context) {
	var req struct {
		PluginID   string `json:"plugin_id" binding:"required"`
		ProviderID string `json:"provider_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	if err := plugin.Default().Logout(c.Request.Context(), req.PluginID, req.ProviderID); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, nil)
}

func refreshPluginModels(c *gin.Context) {
	var req struct {
		PluginID   string `json:"plugin_id"`
		ProviderID string `json:"provider_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	providers, err := plugin.Default().RefreshModels(c.Request.Context(), req.PluginID, req.ProviderID)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, providers)
}

func pluginConfig(c *gin.Context) {
	cfg, err := plugin.Default().PluginConfig(c.Request.Context(), c.Query("plugin_id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, cfg)
}

func savePluginConfig(c *gin.Context) {
	var req struct {
		PluginID string         `json:"plugin_id" binding:"required"`
		Options  map[string]any `json:"options"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	if err := plugin.Default().SetOptions(c.Request.Context(), req.PluginID, req.Options); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, nil)
}

func pluginPage(c *gin.Context) {
	html, err := plugin.Default().PluginPage(c.Request.Context(), c.Query("plugin_id"), c.Query("page_id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{"html": html})
}

func pluginLogs(c *gin.Context) {
	n, _ := strconv.Atoi(c.DefaultQuery("lines", "200"))
	lines, err := plugin.Default().Logs(n)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"lines": lines})
}

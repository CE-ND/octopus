package handlers

import (
	"crypto/subtle"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/bestruirui/octopus/internal/utils/shutdown"
	"github.com/gin-gonic/gin"
)

func init() {
	if os.Getenv("OCTOPUS_DESKTOP") != "1" {
		return
	}

	router.NewGroupRouter("/api/v1/desktop").
		AddRoute(
			router.NewRoute("/health", http.MethodGet).
				Handle(desktopHealth),
		).
		AddRoute(
			router.NewRoute("/shutdown", http.MethodPost).
				Handle(desktopShutdown),
		)
}

func validateDesktopRequest(c *gin.Context) bool {
	ip := net.ParseIP(c.ClientIP())
	if ip == nil || !ip.IsLoopback() {
		resp.Error(c, http.StatusForbidden, "desktop API is only available from loopback")
		return false
	}

	expectedToken := os.Getenv("OCTOPUS_DESKTOP_SHUTDOWN_TOKEN")
	actualToken := c.GetHeader("X-Octopus-Desktop-Token")
	if expectedToken == "" || subtle.ConstantTimeCompare([]byte(actualToken), []byte(expectedToken)) != 1 {
		resp.Error(c, http.StatusForbidden, "invalid desktop shutdown token")
		return false
	}
	return true
}

func desktopHealth(c *gin.Context) {
	if !validateDesktopRequest(c) {
		return
	}
	resp.Success(c, "ok")
}

func desktopShutdown(c *gin.Context) {
	if !validateDesktopRequest(c) {
		return
	}
	resp.Success(c, "shutting down")

	go func() {
		time.Sleep(100 * time.Millisecond)
		shutdown.Shutdown()
		os.Exit(0)
	}()
}

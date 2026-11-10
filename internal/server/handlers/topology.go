package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/topology").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/live", http.MethodGet).
				Handle(getTopologyLive),
		).
		AddRoute(
			router.NewRoute("/stream-token", http.MethodGet).
				Handle(getTopologyStreamToken),
		)

	router.NewGroupRouter("/api/v1/topology").
		AddRoute(
			router.NewRoute("/stream", http.MethodGet).
				Handle(streamTopology),
		)
}

// topologyLiveView 实时调用拓扑视图：本机已安装 agent + 进行中/最近的调用方
type topologyLiveView struct {
	Now       int64               `json:"now"`
	Installed []op.InstalledAgent `json:"installed"`
	Callers   []op.TopologyCaller `json:"callers"`
}

func getTopologyLive(c *gin.Context) {
	resp.Success(c, topologyLiveSnapshot())
}

func topologyLiveSnapshot() topologyLiveView {
	snapshot := op.TopologySnapshotGet()
	return topologyLiveView{
		Now:       snapshot.Now,
		Installed: op.InstalledAgentsGet(),
		Callers:   snapshot.Callers,
	}
}

func getTopologyStreamToken(c *gin.Context) {
	token, err := op.TopologyStreamTokenCreate()
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"token": token})
}

func streamTopology(c *gin.Context) {
	token := c.Query("token")
	if token == "" || !op.TopologyStreamTokenVerify(token) {
		resp.Error(c, http.StatusUnauthorized, "invalid stream token")
		return
	}
	op.TopologyStreamTokenRevoke(token)

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	events := op.TopologySubscribe()
	defer op.TopologyUnsubscribe(events)

	writeSnapshot := func() bool {
		data, err := json.Marshal(topologyLiveSnapshot())
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", data); err != nil {
			return false
		}
		c.Writer.Flush()
		return true
	}

	if !writeSnapshot() {
		return
	}

	// 兜底快照：即使事件通道因慢客户端丢过一次 begin/end，也会在 5 秒内重新对齐。
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-events:
			if !ok {
				return
			}
			if !writeSnapshot() {
				return
			}
		case <-ticker.C:
			if !writeSnapshot() {
				return
			}
		}
	}
}

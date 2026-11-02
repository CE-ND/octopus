package handlers

import (
	"net/http"

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
		)
}

// topologyLiveView 实时调用拓扑视图：本机已安装 agent + 进行中/最近的调用方
type topologyLiveView struct {
	Now       int64               `json:"now"`
	Installed []op.InstalledAgent `json:"installed"`
	Callers   []op.TopologyCaller `json:"callers"`
}

func getTopologyLive(c *gin.Context) {
	snapshot := op.TopologySnapshotGet()
	resp.Success(c, topologyLiveView{
		Now:       snapshot.Now,
		Installed: op.InstalledAgentsGet(),
		Callers:   snapshot.Callers,
	})
}

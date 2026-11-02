package handlers

import (
	"errors"
	"net/http"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/agent-config").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(getAgentConfig),
		).
		AddRoute(
			router.NewRoute("/save", http.MethodPost).
				Handle(saveAgentConfig),
		).
		AddRoute(
			router.NewRoute("/auto-config", http.MethodPost).
				Handle(autoConfigAgent),
		)
}

// agentConfigListResponse 配置列表 + 自动配置支持信息
type agentConfigListResponse struct {
	Files             []op.AgentConfigFileView `json:"files"`
	AutoConfigFileKey string                   `json:"auto_config_file_key"`
	DSH               *model.DshConfigState    `json:"dsh,omitempty"`
}

// getAgentConfig 读取 agent 的可编辑配置文件（?agent=codex）
func getAgentConfig(c *gin.Context) {
	agent := c.Query("agent")
	if agent == "" {
		resp.Error(c, http.StatusBadRequest, "agent is required")
		return
	}
	if agent == "deepseek-harness" {
		files, state, err := op.DshConfigList(c.Query("profile"), c.Request.Context())
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		resp.Success(c, agentConfigListResponse{Files: files, AutoConfigFileKey: "profile", DSH: state})
		return
	}
	files, err := op.AgentConfigList(agent)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, agentConfigListResponse{
		Files:             files,
		AutoConfigFileKey: op.AgentAutoConfigFileKey(agent),
	})
}

// saveAgentConfig 校验并保存配置文件；外部修改冲突返回 409
func saveAgentConfig(c *gin.Context) {
	var req model.AgentConfigSaveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	if req.Agent == "" || len(req.Files) == 0 {
		resp.Error(c, http.StatusBadRequest, "agent and files are required")
		return
	}
	inputs := make([]op.AgentConfigSaveInput, 0, len(req.Files))
	for _, f := range req.Files {
		inputs = append(inputs, op.AgentConfigSaveInput{
			Key:            f.Key,
			Content:        f.Content,
			BaseModifiedAt: f.BaseModifiedAt,
		})
	}
	var saveErr error
	if req.Agent == "deepseek-harness" {
		saveErr = op.DshConfigSave(req, c.Request.Context())
	} else if req.DSH != nil {
		saveErr = errors.New("DSH options require DeepSeek Harness")
	} else {
		saveErr = op.AgentConfigSave(req.Agent, inputs)
	}
	if err := saveErr; err != nil {
		if errors.Is(err, op.ErrAgentConfigConflict) {
			resp.Error(c, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, op.ErrDshConfigBusy) {
			resp.Error(c, http.StatusLocked, err.Error())
			return
		}
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, "agent config saved")
}

// autoConfigAgent 生成「自动配置章鱼环境」补丁后的配置内容（不写盘，前端填入草稿）
func autoConfigAgent(c *gin.Context) {
	var req model.AgentAutoConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	if req.Agent == "" || req.APIKeyID <= 0 {
		resp.Error(c, http.StatusBadRequest, "agent and api_key_id are required")
		return
	}
	if req.Agent == "deepseek-harness" {
		prepared, err := op.DshAutoConfigPrepare(req, c.Request.Context())
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		resp.Success(c, prepared)
		return
	}
	fileKey, content, err := op.AgentAutoConfigPrepare(req, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, model.AgentAutoConfigResponse{FileKey: fileKey, Content: content})
}

package handlers

import (
	"fmt"
	"net/http"
	"strings"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/relay"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/bestruirui/octopus/internal/transformer/inbound"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/v1beta").
		Use(middleware.APIKeyAuthGemini()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/models/*path", http.MethodPost).
				Handle(geminiGenerateContent),
		).
		AddRoute(
			router.NewRoute("/models", http.MethodGet).
				Handle(geminiListModels),
		).
		AddRoute(
			router.NewRoute("/models/*path", http.MethodGet).
				Handle(geminiGetModel),
		)
}

func geminiGenerateContent(c *gin.Context) {
	modelName, method, err := parseGeminiModelMethod(c.Param("path"))
	if err != nil {
		resp.ErrorWithCode(c, http.StatusNotFound, "gemini.invalid_path", err.Error())
		return
	}

	stream := method == "streamGenerateContent"
	relay.HandlerWithOptions(inbound.InboundTypeGemini, c, relay.HandlerOptions{
		ModelOverride:  modelName,
		StreamOverride: &stream,
	})
}

func geminiListModels(c *gin.Context) {
	models, err := op.GroupListModel(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	apiKey, err := op.APIKeyGet(c.GetInt("api_key_id"), c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	if apiKey.SupportedModels != "" {
		allowed := make(map[string]struct{})
		for _, model := range strings.Split(apiKey.SupportedModels, ",") {
			if name := strings.TrimSpace(model); name != "" {
				allowed[name] = struct{}{}
			}
		}
		filtered := models[:0]
		for _, model := range models {
			if _, ok := allowed[model]; ok {
				filtered = append(filtered, model)
			}
		}
		models = filtered
	}

	result := dbmodel.GeminiModelList{
		Models: make([]dbmodel.GeminiModel, 0, len(models)),
	}
	for _, model := range models {
		result.Models = append(result.Models, dbmodel.GeminiModel{
			Name:                       "models/" + strings.TrimPrefix(model, "models/"),
			DisplayName:                model,
			Description:                "Octopus model group",
			SupportedGenerationMethods: []string{"generateContent", "streamGenerateContent"},
		})
	}
	c.JSON(http.StatusOK, result)
}

func geminiGetModel(c *gin.Context) {
	modelName := strings.TrimSpace(strings.TrimPrefix(c.Param("path"), "/"))
	modelName = strings.TrimPrefix(modelName, "models/")
	if modelName == "" || strings.Contains(modelName, ":") {
		resp.ErrorWithCode(c, http.StatusNotFound, "gemini.invalid_model", "invalid model path")
		return
	}

	models, err := op.GroupListModel(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	found := false
	for _, model := range models {
		if strings.TrimPrefix(model, "models/") == modelName {
			found = true
			break
		}
	}
	if !found {
		resp.ErrorWithCode(c, http.StatusNotFound, "gemini.model_not_found", "model not found")
		return
	}

	c.JSON(http.StatusOK, dbmodel.GeminiModel{
		Name:                       "models/" + modelName,
		DisplayName:                modelName,
		Description:                "Octopus model group",
		SupportedGenerationMethods: []string{"generateContent", "streamGenerateContent"},
	})
}

func parseGeminiModelMethod(path string) (string, string, error) {
	raw := strings.TrimSpace(strings.TrimPrefix(path, "/"))
	if raw == "" {
		return "", "", fmt.Errorf("missing model and method")
	}

	separator := strings.LastIndex(raw, ":")
	if separator <= 0 || separator == len(raw)-1 {
		return "", "", fmt.Errorf("expected /models/{model}:generateContent or :streamGenerateContent")
	}

	modelName := strings.TrimSpace(raw[:separator])
	modelName = strings.TrimPrefix(modelName, "models/")
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return "", "", fmt.Errorf("missing model")
	}

	method := strings.TrimSpace(raw[separator+1:])
	switch method {
	case "generateContent", "streamGenerateContent":
		return modelName, method, nil
	default:
		return "", "", fmt.Errorf("unsupported Gemini method %q", method)
	}
}

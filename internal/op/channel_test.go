package op

import (
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	transformermodel "github.com/bestruirui/octopus/internal/transformer/model"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
	"github.com/bestruirui/octopus/internal/utils/cache"
)

func TestChannelLLMListUsesConfiguredProtocol(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	originalCache := channelCache
	channelCache = cache.New[int, model.Channel](16)
	t.Cleanup(func() { channelCache = originalCache })
	site, account := createSiteOpTestSiteAccount(t, ctx, "protocol-site", "protocol-account")

	protocols := []struct {
		channelType outbound.OutboundType
		endpoint    string
	}{
		{outbound.OutboundTypeOpenAIChat, "openai_chat"},
		{outbound.OutboundTypeOpenAIResponse, "openai_response"},
		{outbound.OutboundTypeAnthropic, "anthropic"},
		{outbound.OutboundTypeGemini, "gemini"},
		{outbound.OutboundTypeVolcengine, "volcengine"},
		{outbound.OutboundTypeOpenAIEmbedding, "openai_embedding"},
	}
	for _, source := range []string{"manual", "site"} {
		for _, protocol := range protocols {
			t.Run(source+"/"+protocol.endpoint, func(t *testing.T) {
				channel := model.Channel{
					Name:        source + "-" + protocol.endpoint,
					Type:        protocol.channelType,
					Enabled:     true,
					Model:       "claude-sonnet-5-5-A",
					CustomModel: "custom-model-alias",
				}
				if err := ChannelCreate(&channel, ctx); err != nil {
					t.Fatalf("ChannelCreate failed: %v", err)
				}
				if source == "site" {
					binding := model.SiteChannelBinding{
						SiteID:        site.ID,
						SiteAccountID: account.ID,
						GroupKey:      protocol.endpoint,
						ChannelID:     channel.ID,
					}
					if err := db.GetDB().WithContext(ctx).Create(&binding).Error; err != nil {
						t.Fatalf("create site channel binding failed: %v", err)
					}
				}

				models, err := ChannelLLMList(ctx)
				if err != nil {
					t.Fatalf("ChannelLLMList failed: %v", err)
				}
				count := 0
				for _, item := range models {
					if item.ChannelID != channel.ID {
						continue
					}
					count++
					if item.EndpointType != protocol.endpoint {
						t.Errorf("model %q endpoint = %q, want %q", item.Name, item.EndpointType, protocol.endpoint)
					}
					if source == "manual" && (item.SiteID != nil || item.SiteAccountID != nil) {
						t.Error("manual channel should not have site metadata")
					}
					if source == "site" && (item.SiteID == nil || *item.SiteID != site.ID || item.SiteAccountID == nil || *item.SiteAccountID != account.ID || item.SiteName != site.Name || item.SiteAccountName != account.Name) {
						t.Errorf("site metadata was not preserved: %+v", item)
					}
				}
				if count != 2 {
					t.Errorf("model count = %d, want 2", count)
				}
			})
		}
	}
}

func TestManualChannelProtocolUpdatePersistsAndMatchesModelList(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	originalCache := channelCache
	channelCache = cache.New[int, model.Channel](16)
	t.Cleanup(func() { channelCache = originalCache })
	channel := model.Channel{
		Name:    "rbl-test",
		Type:    outbound.OutboundTypeOpenAIResponse,
		Enabled: true,
		Model:   "claude-sonnet-5-5-A",
	}
	if err := ChannelCreate(&channel, ctx); err != nil {
		t.Fatalf("ChannelCreate failed: %v", err)
	}

	for _, update := range []struct {
		channelType outbound.OutboundType
		endpoint    string
	}{
		{outbound.OutboundTypeOpenAIChat, "openai_chat"},
		{outbound.OutboundTypeAnthropic, "anthropic"},
	} {
		updated, err := ChannelUpdate(&model.ChannelUpdateRequest{ID: channel.ID, Type: &update.channelType}, ctx)
		if err != nil {
			t.Fatalf("ChannelUpdate failed: %v", err)
		}
		var persisted model.Channel
		if err := db.GetDB().WithContext(ctx).First(&persisted, channel.ID).Error; err != nil {
			t.Fatalf("query persisted channel failed: %v", err)
		}
		if updated.Type != update.channelType || persisted.Type != update.channelType {
			t.Fatalf("updated/cached type = %d, persisted type = %d, want %d", updated.Type, persisted.Type, update.channelType)
		}
		models, err := ChannelLLMList(ctx)
		if err != nil {
			t.Fatalf("ChannelLLMList failed: %v", err)
		}
		if len(models) != 1 || models[0].EndpointType != update.endpoint {
			t.Fatalf("model list after protocol update = %+v, want endpoint %q", models, update.endpoint)
		}
	}

	// 转发适配器读取的是渠道的实际类型，不依赖分组列表的显示字段。
	cached, err := ChannelGet(channel.ID, ctx)
	if err != nil {
		t.Fatalf("ChannelGet failed: %v", err)
	}
	adapter := outbound.Get(cached.Type)
	if adapter == nil {
		t.Fatal("configured channel has no outbound adapter")
	}
	text := "protocol check"
	request, err := adapter.TransformRequest(ctx, &transformermodel.InternalLLMRequest{
		Model: cached.Model,
		Messages: []transformermodel.Message{{
			Role:    "user",
			Content: transformermodel.MessageContent{Content: &text},
		}},
	}, "https://example.invalid/v1", "test-channel-key")
	if err != nil {
		t.Fatalf("TransformRequest failed: %v", err)
	}
	defer request.Body.Close()
	if request.URL.Path != "/v1/messages" || request.Header.Get("X-API-Key") != "test-channel-key" || request.Header.Get("Anthropic-Version") == "" {
		t.Fatalf("configured Anthropic channel generated an unexpected request: path=%q, headers=%v", request.URL.Path, request.Header)
	}
}

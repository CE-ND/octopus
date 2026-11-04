package model

import "time"

// PluginChannelBinding 记录一个插件 provider 路由与 Octopus 渠道的绑定关系。
// 渠道本身仍然落在 channels 表，插件只负责管理它的生命周期。
type PluginChannelBinding struct {
	ID         int       `json:"id" gorm:"primaryKey"`
	PluginID   string    `json:"plugin_id" gorm:"not null;uniqueIndex:idx_plugin_channel_route"`
	ProviderID string    `json:"provider_id" gorm:"not null;uniqueIndex:idx_plugin_channel_route"`
	RouteID    string    `json:"route_id" gorm:"not null;uniqueIndex:idx_plugin_channel_route"`
	Protocol   string    `json:"protocol" gorm:"not null"`
	ChannelID  int       `json:"channel_id" gorm:"not null;index"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (PluginChannelBinding) TableName() string {
	return "plugin_channel_bindings"
}

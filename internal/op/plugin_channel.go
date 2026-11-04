package op

import (
	"context"
	"fmt"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

// PluginChannelBindingList 返回全部插件渠道绑定。
func PluginChannelBindingList(ctx context.Context) ([]model.PluginChannelBinding, error) {
	var bindings []model.PluginChannelBinding
	if err := db.GetDB().WithContext(ctx).Order("id ASC").Find(&bindings).Error; err != nil {
		return nil, err
	}
	return bindings, nil
}

// PluginChannelBindingMapByChannelIDs 返回 channel_id -> binding 的映射。
func PluginChannelBindingMapByChannelIDs(channelIDs []int, ctx context.Context) (map[int]model.PluginChannelBinding, error) {
	result := make(map[int]model.PluginChannelBinding)
	if len(channelIDs) == 0 {
		return result, nil
	}
	var bindings []model.PluginChannelBinding
	if err := db.GetDB().WithContext(ctx).Where("channel_id IN ?", channelIDs).Find(&bindings).Error; err != nil {
		return nil, err
	}
	for _, binding := range bindings {
		result[binding.ChannelID] = binding
	}
	return result, nil
}

// PluginChannelBindingGet 按插件 provider 路由查找绑定。
func PluginChannelBindingGet(pluginID, providerID, routeID string, ctx context.Context) (*model.PluginChannelBinding, bool, error) {
	var binding model.PluginChannelBinding
	err := db.GetDB().WithContext(ctx).
		Where("plugin_id = ? AND provider_id = ? AND route_id = ?", pluginID, providerID, routeID).
		First(&binding).Error
	if err == gorm.ErrRecordNotFound {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &binding, true, nil
}

// PluginChannelBindingSave 新建或更新插件渠道绑定。
func PluginChannelBindingSave(binding *model.PluginChannelBinding, ctx context.Context) error {
	if binding == nil {
		return fmt.Errorf("plugin channel binding is nil")
	}
	if binding.ID == 0 {
		if err := db.GetDB().WithContext(ctx).Create(binding).Error; err != nil {
			return err
		}
		return nil
	}
	return db.GetDB().WithContext(ctx).Save(binding).Error
}

// PluginChannelBindingDeleteByChannelID 删除渠道对应的插件绑定。
func PluginChannelBindingDeleteByChannelID(channelID int, ctx context.Context) error {
	return db.GetDB().WithContext(ctx).Where("channel_id = ?", channelID).Delete(&model.PluginChannelBinding{}).Error
}

// PluginChannelBindingDeleteByPlugin 删除某个插件的全部渠道绑定。
func PluginChannelBindingDeleteByPlugin(pluginID string, ctx context.Context) error {
	return db.GetDB().WithContext(ctx).Where("plugin_id = ?", pluginID).Delete(&model.PluginChannelBinding{}).Error
}

// PluginChannelBindingRenamePlugin 把旧插件 ID 的渠道绑定迁移到新插件 ID。
// 用于插件包按 octopus.replaces 改名时保留渠道和分组配置。
func PluginChannelBindingRenamePlugin(oldPluginID, newPluginID string, ctx context.Context) error {
	if oldPluginID == "" || newPluginID == "" || oldPluginID == newPluginID {
		return nil
	}
	return db.GetDB().WithContext(ctx).
		Model(&model.PluginChannelBinding{}).
		Where("plugin_id = ?", oldPluginID).
		Update("plugin_id", newPluginID).Error
}

// ChannelDelPlugin 删除插件管理的渠道（绕过 site managed 校验）。
func ChannelDelPlugin(id int, ctx context.Context) error {
	if _, managed, err := PluginChannelBindingGetByChannelID(id, ctx); err != nil {
		return err
	} else if !managed {
		return fmt.Errorf("channel is not a plugin managed channel")
	}
	return channelDel(id, ctx, true)
}

// PluginChannelBindingGetByChannelID 检查某个渠道是否由插件管理。
func PluginChannelBindingGetByChannelID(channelID int, ctx context.Context) (*model.PluginChannelBinding, bool, error) {
	var binding model.PluginChannelBinding
	err := db.GetDB().WithContext(ctx).Where("channel_id = ?", channelID).First(&binding).Error
	if err == gorm.ErrRecordNotFound {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &binding, true, nil
}

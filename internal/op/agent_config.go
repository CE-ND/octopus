package op

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/bestruirui/octopus/internal/model"
	toml "github.com/pelletier/go-toml/v2"
	yaml "go.yaml.in/yaml/v3"
)

// agent 配置文件编辑：读取/保存本地 AI Agent 的配置文件（第一期仅 Codex）。
// 安全模型：真实路径永远由 AgentDefinitions 注册表解析，调用方只传 agent id +
// 文件 key 白名单；写入前做格式校验、外部修改冲突检测、.bak 备份和原子替换。

// ErrAgentConfigConflict 配置文件在加载后被外部程序修改，拒绝盲覆盖
var ErrAgentConfigConflict = errors.New("agent config file was modified externally")

// AgentConfigFileView 配置文件的读取视图
type AgentConfigFileView struct {
	Key        string `json:"key"`
	Path       string `json:"path"`
	Language   string `json:"language"`
	Sensitive  bool   `json:"sensitive"`
	Content    string `json:"content"`
	Exists     bool   `json:"exists"`
	ModifiedAt int64  `json:"modified_at"` // unix 毫秒
	Size       int64  `json:"size"`
}

// AgentConfigSaveInput 单个文件的保存输入（内部类型，handler 从请求模型转换）
type AgentConfigSaveInput struct {
	Key            string
	Content        string
	BaseModifiedAt int64
}

var agentConfigLocks sync.Map // agent id -> *sync.Mutex

func agentConfigLock(agent string) *sync.Mutex {
	lock, _ := agentConfigLocks.LoadOrStore(agent, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// agentConfigDescriptors 按注册表解析 agent 的可编辑配置文件；不支持编辑时返回 nil
func agentConfigDescriptors(agent string) []AgentConfigFile {
	for i := range AgentDefinitions {
		if AgentDefinitions[i].ID != agent {
			continue
		}
		if AgentDefinitions[i].ConfigFiles == nil {
			return nil
		}
		return AgentDefinitions[i].ConfigFiles()
	}
	return nil
}

// AgentConfigList 读取 agent 的全部可编辑配置文件
func AgentConfigList(agent string) ([]AgentConfigFileView, error) {
	descriptors := agentConfigDescriptors(agent)
	if len(descriptors) == 0 {
		return nil, fmt.Errorf("agent %q has no editable config files", agent)
	}
	views := make([]AgentConfigFileView, 0, len(descriptors))
	for _, f := range descriptors {
		view := AgentConfigFileView{
			Key:       f.Key,
			Path:      f.Path,
			Language:  f.Language,
			Sensitive: f.Sensitive,
		}
		if info, err := os.Stat(f.Path); err == nil && info.Mode().IsRegular() {
			view.Exists = true
			view.ModifiedAt = info.ModTime().UnixMilli()
			view.Size = info.Size()
			if data, err := os.ReadFile(f.Path); err == nil {
				view.Content = string(data)
			}
		}
		views = append(views, view)
	}
	return views, nil
}

// AgentConfigSave 校验并保存配置文件。文件路径按 key 从注册表白名单解析；
// 当前磁盘 mtime 与 BaseModifiedAt 不一致时返回 ErrAgentConfigConflict。
func AgentConfigSave(agent string, inputs []AgentConfigSaveInput) error {
	if agent == dshAgentID {
		files := make([]model.AgentConfigSaveFile, len(inputs))
		for i, input := range inputs {
			files[i] = model.AgentConfigSaveFile{Key: input.Key, Content: input.Content, BaseModifiedAt: input.BaseModifiedAt}
		}
		return DshConfigSave(model.AgentConfigSaveRequest{Agent: agent, Files: files}, context.Background())
	}
	descriptors := agentConfigDescriptors(agent)
	if len(descriptors) == 0 {
		return fmt.Errorf("agent %q has no editable config files", agent)
	}
	byKey := make(map[string]AgentConfigFile, len(descriptors))
	for _, f := range descriptors {
		byKey[f.Key] = f
	}

	lock := agentConfigLock(agent)
	lock.Lock()
	defer lock.Unlock()

	seenKeys := make(map[string]bool, len(inputs))
	// 多文件统一保存时，先校验全部文件，再执行写入；格式或冲突错误不会留下部分更新。
	for _, input := range inputs {
		descriptor, ok := byKey[input.Key]
		if !ok {
			return fmt.Errorf("unknown config file key %q for agent %q", input.Key, agent)
		}
		if seenKeys[input.Key] {
			return fmt.Errorf("duplicate config file key %q", input.Key)
		}
		seenKeys[input.Key] = true
		if err := validateAgentConfigFile(descriptor.Language, input.Content); err != nil {
			return fmt.Errorf("%s 校验失败: %w", descriptor.Path, err)
		}
		// 冲突检测：加载后的 mtime 与当前磁盘 mtime 不一致说明被外部修改
		if info, err := os.Stat(descriptor.Path); err == nil {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("config path is not a regular file: %s", descriptor.Path)
			}
			if info.ModTime().UnixMilli() != input.BaseModifiedAt {
				return fmt.Errorf("%w: %s", ErrAgentConfigConflict, descriptor.Path)
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("read config metadata: %w", err)
		} else if input.BaseModifiedAt != 0 {
			return fmt.Errorf("%w: %s", ErrAgentConfigConflict, descriptor.Path)
		}
	}
	for _, input := range inputs {
		descriptor := byKey[input.Key]
		if err := writeAgentConfigFileAtomic(descriptor, []byte(input.Content)); err != nil {
			return err
		}
	}
	return nil
}

// validateAgentConfigFile 按语言校验内容合法性；空内容视为清空文件，合法
func validateAgentConfigFile(language, content string) error {
	if content == "" {
		return nil
	}
	switch language {
	case "toml":
		var parsed map[string]any
		if err := toml.Unmarshal([]byte(content), &parsed); err != nil {
			return err
		}
	case "yaml":
		_, err := parseDshYAML(content, yaml.SequenceNode)
		return err
	case "json":
		var parsed map[string]any
		if err := json.Unmarshal([]byte(content), &parsed); err != nil {
			return err
		}
	}
	return nil
}

// writeAgentConfigFileAtomic 备份旧文件 → 同目录临时文件 → rename 原子替换。
// Sensitive 文件（auth.json）固定 0600，其余保留原 mode（新文件 0644）。
func writeAgentConfigFileAtomic(descriptor AgentConfigFile, content []byte) error {
	dir := filepath.Dir(descriptor.Path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	mode := os.FileMode(0644)
	if info, err := os.Stat(descriptor.Path); err == nil {
		mode = info.Mode().Perm()
		// 覆盖前滚动备份一份，误操作可从 path+".bak" 回退
		if data, err := os.ReadFile(descriptor.Path); err == nil {
			_ = os.WriteFile(descriptor.Path+".bak", data, mode)
		}
	}
	if descriptor.Sensitive {
		mode = 0600
	}

	tmp := descriptor.Path + ".tmp"
	if err := os.WriteFile(tmp, content, mode); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmp, descriptor.Path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace config file: %w", err)
	}
	_ = os.Chmod(descriptor.Path, mode)
	return nil
}

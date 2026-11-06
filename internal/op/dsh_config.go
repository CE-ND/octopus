package op

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	yaml "go.yaml.in/yaml/v3"
)

const dshAgentID = "deepseek-harness"
const dshManagedReference = "OCTOPUS_DESKTOP_API_KEY"

var ErrDshConfigBusy = errors.New("DSH 正在写入配置，请稍后重试")
var dshReferencePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var dshRecordPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*/[a-z][a-z0-9-]*$`)

type dshPaths struct{ base, profile, credentials, directory string }
type dshProfile struct{ patch, base *yaml.Node }

func resolveDshPaths(profile string) (dshPaths, error) {
	if profile != "" && profile != "desktop" {
		return dshPaths{}, fmt.Errorf("首版 DSH 自动配置仅支持 desktop profile")
	}
	home := strings.TrimSpace(os.Getenv("DSH_HOME"))
	if home == "" {
		home = homeJoin(".dsh")
	}
	if home == "" {
		return dshPaths{}, fmt.Errorf("无法定位 DSH 用户目录")
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return dshPaths{}, fmt.Errorf("无法定位 DSH 用户目录")
	}
	dir := filepath.Join(home, "profiles", "desktop")
	return dshPaths{base: filepath.Join(dir, "cordis.yml"), profile: filepath.Join(dir, "cordis.patch.yml"), credentials: filepath.Join(home, ".credentials.yaml"), directory: dir}, nil
}

func dshConfigFiles() []AgentConfigFile {
	paths, err := resolveDshPaths("")
	if err != nil {
		return nil
	}
	return []AgentConfigFile{{Key: "profile", Path: paths.profile, Language: "yaml"}}
}

func loadDshProfile(paths dshPaths, override *string) (*dshProfile, string, dshFileSnapshot, error) {
	info, err := os.Lstat(paths.directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, "", dshFileSnapshot{}, fmt.Errorf("请先启动 DSH 桌面版以生成 desktop profile")
	}
	baseFile, err := readDshFile(paths.base, false)
	if err != nil {
		return nil, "", dshFileSnapshot{}, err
	}
	file, err := readDshFile(paths.profile, false)
	if err != nil {
		return nil, "", file, err
	}
	base, err := parseDshYAML(string(baseFile.content), yaml.SequenceNode)
	if err != nil {
		return nil, "", file, err
	}
	content := string(file.content)
	if override != nil {
		content = *override
	}
	patch, err := parseDshYAML(content, yaml.SequenceNode)
	if err != nil {
		return nil, "", file, err
	}
	p := &dshProfile{base: base, patch: patch}
	// DSH 的 patch 层允许同一插件 id 重复出现：按文件顺序应用，后出现的条目整体覆盖前者。
	// 这里不能要求 id 唯一；entry() 取最后一个匹配项，与 DSH 的 last-write-wins 语义一致。
	for _, doc := range []*yaml.Node{base, patch} {
		for _, entry := range doc.Content[0].Content {
			id := dshText(dshGet(entry, "id"))
			if entry.Kind != yaml.MappingNode || id == "" {
				return nil, "", file, fmt.Errorf("DSH profile 的每个条目都需要插件 ID，组合指令暂不支持")
			}
			if config := dshGet(entry, "config"); config != nil && config.Kind != yaml.MappingNode {
				return nil, "", file, dshYAMLError(config)
			}
		}
	}
	for _, id := range []string{"llm-pi-ai", "agent-default-model"} {
		if _, err := p.entry(id, false); err != nil {
			return nil, "", file, err
		}
	}
	credentialsEntry, err := p.entry("credentials-local", false)
	if err != nil {
		return nil, "", file, err
	}
	if config := dshGet(credentialsEntry, "config"); config != nil {
		if dshGet(config, "path") != nil || dshGet(config, "dshHome") != nil {
			return nil, "", file, fmt.Errorf("当前 DSH 使用自定义凭据路径，自动配置暂不支持")
		}
	}
	return p, content, file, nil
}

// patch 的 config 是完整替换；创建覆盖项时复制 base 的完整 config。
func (p *dshProfile) entry(id string, create bool) (*yaml.Node, error) {
	pkg := "@deepseek-ai/dsh-" + id
	find := func(doc *yaml.Node) (*yaml.Node, error) {
		var found *yaml.Node
		for _, entry := range doc.Content[0].Content {
			entryID, name := dshText(dshGet(entry, "id")), dshText(dshGet(entry, "name"))
			if entryID != id && name != pkg {
				continue
			}
			if entryID == id && name != "" && name != pkg {
				return nil, fmt.Errorf("DSH 插件配置无法唯一识别：%s", id)
			}
			// 同一 id 的多个 patch 条目按 DSH 语义顺序覆盖，最后一个才是生效配置。
			found = entry
		}
		return found, nil
	}
	patch, err := find(p.patch)
	if err != nil || patch != nil {
		return patch, err
	}
	base, err := find(p.base)
	if err != nil || !create {
		return base, err
	}
	entry := dshMap()
	entryID := id
	if base != nil {
		entryID = dshText(dshGet(base, "id"))
	}
	dshSet(entry, "id", dshString(entryID))
	dshSet(entry, "config", cloneDshNode(dshGet(base, "config")))
	p.patch.Content[0].Content = append(p.patch.Content[0].Content, entry)
	return entry, nil
}

func (p *dshProfile) config(id string, create bool) (*yaml.Node, error) {
	entry, err := p.entry(id, create)
	if err != nil || entry == nil {
		return nil, err
	}
	if !create {
		return dshGet(entry, "config"), nil
	}
	return dshChildMap(entry, "config")
}

func (p *dshProfile) provider(create bool) (*yaml.Node, error) {
	config, err := p.config("llm-pi-ai", create)
	if err != nil || config == nil {
		return nil, err
	}
	if !create {
		return dshGet(dshGet(config, "providers"), "octopus"), nil
	}
	providers, err := dshChildMap(config, "providers")
	if err != nil {
		return nil, err
	}
	return dshChildMap(providers, "octopus")
}

func dshValidAPI(api string) bool {
	return api == "openai-completions" || api == "openai-responses" || api == "anthropic-messages"
}

func dshModels(provider *yaml.Node) (*yaml.Node, error) {
	node := dshGet(provider, "models")
	if node == nil {
		return nil, nil
	}
	if node.Kind != yaml.SequenceNode {
		return nil, dshYAMLError(node)
	}
	seen := map[string]bool{}
	for _, entry := range node.Content {
		id := dshText(dshGet(entry, "id"))
		if entry.Kind != yaml.MappingNode || id == "" || seen[id] {
			return nil, dshYAMLError(entry)
		}
		seen[id] = true
	}
	return node, nil
}

func dshModel(provider *yaml.Node, modelName string) (*yaml.Node, error) {
	models, err := dshModels(provider)
	if err != nil || models == nil {
		return nil, err
	}
	for _, item := range models.Content {
		if dshText(dshGet(item, "id")) == modelName {
			return item, nil
		}
	}
	return nil, nil
}

func dshSupportsEffort(model *yaml.Node, effort string) bool {
	if effort == "" {
		return true
	}
	efforts := dshGet(model, "reasoningEfforts")
	return efforts != nil && efforts.Kind == yaml.MappingNode && dshGet(efforts, effort) != nil
}

func validateDshOctopus(p *dshProfile) error {
	defaults, err := p.config("agent-default-model", false)
	if err != nil {
		return err
	}
	provider, err := p.provider(false)
	if err != nil {
		return err
	}
	if provider == nil && dshText(dshGet(defaults, "provider")) == "octopus" {
		return fmt.Errorf("DSH 默认提供商 Octopus 尚未配置")
	}
	if provider == nil {
		return err
	}
	if provider.Kind != yaml.MappingNode {
		return dshYAMLError(provider)
	}
	if !dshValidAPI(dshText(dshGet(provider, "api"))) || dshText(dshGet(provider, "baseURL")) == "" || !dshReferencePattern.MatchString(dshText(dshGet(provider, "apiKeyEnv"))) {
		return fmt.Errorf("DSH Octopus 提供商需要有效的协议、地址及凭据引用")
	}
	endpoint, err := url.Parse(dshText(dshGet(provider, "baseURL")))
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil {
		return fmt.Errorf("DSH Octopus 需要有效的 HTTP API 地址")
	}
	models, err := dshModels(provider)
	if err != nil {
		return err
	}
	if models == nil || len(models.Content) == 0 {
		return fmt.Errorf("DSH Octopus 模型目录不能为空")
	}
	if dshText(dshGet(defaults, "provider")) == "octopus" {
		item, err := dshModel(provider, dshText(dshGet(defaults, "model")))
		if err != nil {
			return err
		}
		if item == nil {
			return fmt.Errorf("DSH 默认模型不在 Octopus 模型目录中")
		}
		effort := dshText(dshGet(defaults, "reasoningEffort"))
		if effort == "" {
			effort = dshText(dshGet(provider, "reasoning"))
		}
		if !dshSupportsEffort(item, effort) {
			return fmt.Errorf("DSH 默认推理等级不适用于所选模型，请检查模型的 reasoningEfforts")
		}
	}
	return nil
}

func dshCredentialValue(doc *yaml.Node, ref string) string {
	return dshText(dshGet(dshGet(doc.Content[0], "refs"), ref))
}
func dshCredentialRevision(doc *yaml.Node) string {
	hash := sha256.Sum256([]byte(dshManagedReference + "\x00" + dshCredentialValue(doc, dshManagedReference)))
	return hex.EncodeToString(hash[:])
}

func readDshCredentials(paths dshPaths) (*yaml.Node, dshFileSnapshot, error) {
	file, err := readDshFile(paths.credentials, true)
	if err != nil {
		return nil, file, err
	}
	doc, err := parseDshCredentials(string(file.content))
	return doc, file, err
}

func DshConfigList(profile string, ctx context.Context) ([]AgentConfigFileView, *model.DshConfigState, error) {
	paths, err := resolveDshPaths(profile)
	if err != nil {
		return nil, nil, err
	}
	p, content, file, err := loadDshProfile(paths, nil)
	if err != nil {
		return nil, nil, err
	}
	credentials, _, err := readDshCredentials(paths)
	if err != nil {
		return nil, nil, err
	}
	provider, err := p.provider(false)
	if err != nil {
		return nil, nil, err
	}
	defaults, err := p.config("agent-default-model", false)
	if err != nil {
		return nil, nil, err
	}
	ref := dshText(dshGet(provider, "apiKeyEnv"))
	api := dshText(dshGet(provider, "api"))
	if api == "" {
		api = "openai-completions"
	}
	state := &model.DshConfigState{Profile: "desktop", API: api, CredentialReference: ref, CredentialRevision: dshCredentialRevision(credentials), CurrentProvider: dshText(dshGet(defaults, "provider")), CurrentModel: dshText(dshGet(defaults, "model"))}
	value := dshCredentialValue(credentials, ref)
	state.CredentialConfigured = value != ""
	if value != "" {
		if key, err := APIKeyGetByAPIKey(value, ctx); err == nil {
			state.APIKeyID = key.ID
		}
	}
	view := AgentConfigFileView{Key: "profile", Path: paths.profile, Language: "yaml", Content: content, Exists: file.exists, ModifiedAt: file.modifiedAt, Size: int64(len(file.content))}
	return []AgentConfigFileView{view}, state, nil
}

func dshSelectedKey(id, groupID int, ctx context.Context) (model.APIKey, string, error) {
	key, err := APIKeyGet(id, ctx)
	if err != nil || !key.Enabled || (key.ExpireAt > 0 && key.ExpireAt < time.Now().Unix()) || strings.TrimSpace(key.APIKey) == "" {
		return key, "", fmt.Errorf("请选择有效且启用的章鱼 API 密钥")
	}
	if groupID <= 0 {
		return key, "", fmt.Errorf("请选择 DSH 的默认模型分组")
	}
	name, err := agentModelGroupName(groupID, key.SupportedModels, ctx)
	return key, name, err
}

func DshAutoConfigPrepare(req model.AgentAutoConfigRequest, ctx context.Context) (model.AgentAutoConfigResponse, error) {
	result := model.AgentAutoConfigResponse{}
	if req.Agent != dshAgentID || req.DSH == nil || req.ModelGroups != nil || req.CodexModelGroupID != 0 {
		return result, fmt.Errorf("无效的 DSH 自动配置参数")
	}
	_, name, err := dshSelectedKey(req.APIKeyID, req.DSH.ModelGroupID, ctx)
	if err != nil {
		return result, err
	}
	paths, err := resolveDshPaths(req.DSH.Profile)
	if err != nil {
		return result, err
	}
	p, original, _, err := loadDshProfile(paths, req.CurrentContent)
	if err != nil {
		return result, err
	}
	credentials, _, err := readDshCredentials(paths)
	if err != nil {
		return result, err
	}
	provider, err := p.provider(true)
	if err != nil {
		return result, err
	}
	api := req.DSH.API
	if api == "" {
		api = dshText(dshGet(provider, "api"))
	}
	if api == "" {
		api = "openai-completions"
	}
	if !dshValidAPI(api) {
		return result, fmt.Errorf("DSH 暂不支持所选 API 协议")
	}
	baseURL := gatewayBaseURL()
	if api != "anthropic-messages" {
		baseURL += "/v1"
	}
	dshSet(provider, "api", dshString(api))
	dshSet(provider, "baseURL", dshString(baseURL))
	dshSet(provider, "apiKeyEnv", dshString(dshManagedReference))
	if dshGet(provider, "displayName") == nil {
		dshSet(provider, "displayName", dshString("Octopus"))
	}
	models, err := dshModels(provider)
	if err != nil {
		return result, err
	}
	if models == nil {
		models = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		dshSet(provider, "models", models)
	}
	item, err := dshModel(provider, name)
	if err != nil {
		return result, err
	}
	if item == nil {
		item = dshMap()
		dshSet(item, "id", dshString(name))
		dshSet(item, "name", dshString(name))
		models.Content = append(models.Content, item)
	}
	// DSH 根据 input 启用图片输入；补齐旧配置的缺省值，同时保留手动声明的能力。
	if input := dshGet(item, "input"); input == nil || input.Tag == "!!null" {
		dshSet(item, "input", &yaml.Node{
			Kind:    yaml.SequenceNode,
			Tag:     "!!seq",
			Style:   yaml.FlowStyle,
			Content: []*yaml.Node{dshString("text"), dshString("image")},
		})
	}
	defaults, err := p.config("agent-default-model", true)
	if err != nil {
		return result, err
	}
	reasoningReset := !dshSupportsEffort(item, dshText(dshGet(defaults, "reasoningEffort")))
	if reasoningReset {
		dshRemove(defaults, "reasoningEffort")
	}
	dshSet(defaults, "provider", dshString("octopus"))
	dshSet(defaults, "model", dshString(name))
	if err := validateDshOctopus(p); err != nil {
		return result, err
	}
	content, err := renderDshYAML(p.patch, original)
	if err != nil {
		return result, err
	}
	result.FileKey, result.Content = "profile", content
	result.DSH = &model.DshPreparedConfig{Profile: "desktop", CredentialReference: dshManagedReference, CredentialRevision: dshCredentialRevision(credentials), ModelGroupID: req.DSH.ModelGroupID, ModelName: name, API: api, ReasoningReset: reasoningReset}
	return result, nil
}

func DshConfigSave(req model.AgentConfigSaveRequest, ctx context.Context) error {
	return saveDshConfig(req, ctx, renameDshFile)
}

// 写锁下重新读取凭据，只变更本功能的引用；两份文件预校验后有序提交。
func saveDshConfig(req model.AgentConfigSaveRequest, ctx context.Context, writer dshFileWriter) error {
	if req.Agent != dshAgentID || len(req.Files) != 1 || req.Files[0].Key != "profile" {
		return fmt.Errorf("DSH 仅允许编辑 desktop profile 配置文件")
	}
	profile := ""
	if req.DSH != nil {
		profile = req.DSH.Profile
	}
	paths, err := resolveDshPaths(profile)
	if err != nil {
		return err
	}
	lock := agentConfigLock(dshAgentID)
	lock.Lock()
	defer lock.Unlock()
	return withDshFileLocks(ctx, []string{paths.credentials, paths.profile}, func() error {
		input := req.Files[0]
		p, _, profileFile, err := loadDshProfile(paths, &input.Content)
		if err != nil {
			return err
		}
		if profileFile.modifiedAt != input.BaseModifiedAt {
			return fmt.Errorf("%w: DSH profile 已被外部修改", ErrAgentConfigConflict)
		}
		if err := validateDshOctopus(p); err != nil {
			return err
		}
		updates := []dshFileUpdate{}
		if req.DSH != nil && req.DSH.APIKeyID != 0 {
			key, modelName, err := dshSelectedKey(req.DSH.APIKeyID, req.DSH.ModelGroupID, ctx)
			if err != nil {
				return err
			}
			provider, err := p.provider(false)
			if err != nil {
				return err
			}
			if dshText(dshGet(provider, "apiKeyEnv")) != dshManagedReference {
				return fmt.Errorf("DSH 凭据引用已改变，请重新生成快速配置")
			}
			selected, err := dshModel(provider, modelName)
			if err != nil {
				return err
			}
			if selected == nil {
				return fmt.Errorf("所选分组已改变，请重新生成 DSH 配置")
			}
			credentials, credentialFile, err := readDshCredentials(paths)
			if err != nil {
				return err
			}
			if req.DSH.CredentialRevision == "" || dshCredentialRevision(credentials) != req.DSH.CredentialRevision {
				return fmt.Errorf("%w: DSH 章鱼凭据已被外部修改", ErrAgentConfigConflict)
			}
			root := credentials.Content[0]
			refs := dshGet(root, "refs")
			if refs == nil || refs.Tag == "!!null" {
				refs = dshMap()
				dshSet(root, "refs", refs)
			}
			dshSet(refs, dshManagedReference, dshString(key.APIKey))
			text, err := renderDshYAML(credentials, string(credentialFile.content))
			if err != nil {
				return err
			}
			updates = append(updates, dshFileUpdate{path: paths.credentials, previous: credentialFile, content: []byte(text)})
		}
		updates = append(updates, dshFileUpdate{path: paths.profile, previous: profileFile, content: []byte(input.Content)})
		return commitDshFiles(ctx, updates, writer)
	})
}

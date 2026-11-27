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

func (p *dshProfile) providers(create bool) (*yaml.Node, error) {
	config, err := p.config("llm-pi-ai", create)
	if err != nil || config == nil {
		return nil, err
	}
	providers := dshGet(config, "providers")
	if providers == nil {
		if !create {
			return nil, nil
		}
		providers = dshMap()
		dshSet(config, "providers", providers)
	}
	if providers.Kind != yaml.MappingNode {
		return nil, dshYAMLError(providers)
	}
	return providers, nil
}

func (p *dshProfile) providerByID(id string, create bool) (*yaml.Node, error) {
	providers, err := p.providers(create)
	if err != nil || providers == nil {
		return nil, err
	}
	if !create {
		return dshGet(providers, id), nil
	}
	return dshChildMap(providers, id)
}

// provider 保留旧路径，供兼容读取单一 octopus provider 使用。
func (p *dshProfile) provider(create bool) (*yaml.Node, error) {
	return p.providerByID("octopus", create)
}

func (p *dshProfile) defaultProviderNode() (*yaml.Node, error) {
	defaults, err := p.config("agent-default-model", false)
	if err != nil || defaults == nil {
		return nil, err
	}
	return p.providerByID(dshText(dshGet(defaults, "provider")), false)
}

func dshValidAPI(api string) bool {
	return api == "openai-completions" || api == "openai-responses" || api == "anthropic-messages" || api == "google-generative-ai"
}

type dshProviderDefinition struct {
	ID          string
	DisplayName string
	API         string
	BaseSuffix  string
}

// DSH 的 provider api 不能按模型覆盖，因此每个协议固定映射到独立 provider。
var dshProviderDefinitions = []dshProviderDefinition{
	{ID: "octopus-chat", DisplayName: "Octopus Chat", API: "openai-completions", BaseSuffix: "/v1"},
	{ID: "octopus-responses", DisplayName: "Octopus Responses", API: "openai-responses", BaseSuffix: "/v1"},
	{ID: "octopus-anthropic", DisplayName: "Octopus Anthropic", API: "anthropic-messages", BaseSuffix: ""},
	{ID: "octopus-gemini", DisplayName: "Octopus Gemini", API: "google-generative-ai", BaseSuffix: "/v1beta"},
}

var dshManagedProviderIDs = map[string]struct{}{
	"octopus":           {},
	"octopus-chat":      {},
	"octopus-responses": {},
	"octopus-anthropic": {},
	"octopus-gemini":    {},
}

func dshProviderDefinitionForAPI(api string) (dshProviderDefinition, bool) {
	for _, definition := range dshProviderDefinitions {
		if definition.API == api {
			return definition, true
		}
	}
	return dshProviderDefinition{}, false
}

func dshBaseURLForAPI(api string) string {
	definition, ok := dshProviderDefinitionForAPI(api)
	if !ok {
		return gatewayBaseURL() + "/v1"
	}
	return gatewayBaseURL() + definition.BaseSuffix
}

func dshManagedProvider(id string) bool {
	_, ok := dshManagedProviderIDs[id]
	return ok
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

func dshEnsureModelInput(item *yaml.Node) {
	if input := dshGet(item, "input"); input == nil || input.Tag == "!!null" {
		dshSet(item, "input", &yaml.Node{
			Kind:    yaml.SequenceNode,
			Tag:     "!!seq",
			Style:   yaml.FlowStyle,
			Content: []*yaml.Node{dshString("text"), dshString("image")},
		})
	}
}

type dshResolvedRoute struct {
	Selection  model.DshRouteSelection
	Definition dshProviderDefinition
	ModelName  string
}

func dshLoadKey(id int, ctx context.Context) (model.APIKey, error) {
	key, err := APIKeyGet(id, ctx)
	if err != nil || !key.Enabled || (key.ExpireAt > 0 && key.ExpireAt < time.Now().Unix()) || strings.TrimSpace(key.APIKey) == "" {
		return key, fmt.Errorf("请选择有效且启用的章鱼 API 密钥")
	}
	return key, nil
}

func dshResolveRoutes(key model.APIKey, selections []model.DshRouteSelection, defaultGroupID int, ctx context.Context) ([]dshResolvedRoute, int, error) {
	if len(selections) == 0 {
		return nil, 0, fmt.Errorf("请至少配置一个 DSH 模型路由")
	}
	seen := make(map[int]struct{}, len(selections))
	routes := make([]dshResolvedRoute, 0, len(selections))
	for _, selection := range selections {
		if selection.ModelGroupID <= 0 {
			return nil, 0, fmt.Errorf("DSH 模型分组不能为空")
		}
		if _, exists := seen[selection.ModelGroupID]; exists {
			return nil, 0, fmt.Errorf("同一个模型分组不能重复配置 DSH 路由")
		}
		definition, ok := dshProviderDefinitionForAPI(selection.API)
		if !ok {
			return nil, 0, fmt.Errorf("DSH 暂不支持所选 API 协议")
		}
		modelName, err := agentModelGroupName(selection.ModelGroupID, key.SupportedModels, ctx)
		if err != nil {
			return nil, 0, err
		}
		seen[selection.ModelGroupID] = struct{}{}
		routes = append(routes, dshResolvedRoute{Selection: selection, Definition: definition, ModelName: modelName})
	}
	if defaultGroupID == 0 {
		defaultGroupID = routes[0].Selection.ModelGroupID
	}
	for _, route := range routes {
		if route.Selection.ModelGroupID == defaultGroupID {
			return routes, defaultGroupID, nil
		}
	}
	return nil, 0, fmt.Errorf("DSH 默认模型必须在模型路由中")
}

func dshSelectionsFromOptions(opts model.DshAutoConfigOptions, p *dshProfile) ([]model.DshRouteSelection, int, error) {
	if len(opts.Routes) > 0 {
		selections := make([]model.DshRouteSelection, len(opts.Routes))
		copy(selections, opts.Routes)
		defaultGroupID := opts.DefaultModelGroupID
		if defaultGroupID == 0 {
			defaultGroupID = selections[0].ModelGroupID
		}
		return selections, defaultGroupID, nil
	}
	if opts.ModelGroupID <= 0 {
		return nil, 0, fmt.Errorf("请选择 DSH 的默认模型分组")
	}
	api := opts.API
	if api == "" {
		if provider, err := p.provider(false); err == nil {
			api = dshText(dshGet(provider, "api"))
		}
	}
	if api == "" {
		if defaults, err := p.config("agent-default-model", false); err == nil {
			if provider, err := p.providerByID(dshText(dshGet(defaults, "provider")), false); err == nil {
				api = dshText(dshGet(provider, "api"))
			}
		}
	}
	if api == "" {
		api = "openai-completions"
	}
	defaultGroupID := opts.DefaultModelGroupID
	if defaultGroupID == 0 {
		defaultGroupID = opts.ModelGroupID
	}
	return []model.DshRouteSelection{{ModelGroupID: opts.ModelGroupID, API: api}}, defaultGroupID, nil
}

// dshApplyRoutes 重建所有 Octopus 管理的 provider。非 Octopus provider 原样保留。
func dshApplyRoutes(p *dshProfile, routes []dshResolvedRoute, defaultGroupID int, knownGroupNames map[string]struct{}) ([]model.DshRouteState, error) {
	providers, err := p.providers(true)
	if err != nil {
		return nil, err
	}
	existingModels := make(map[string]*yaml.Node)
	existingProviders := make(map[string]*yaml.Node)
	for i := 0; i < len(providers.Content); i += 2 {
		id := providers.Content[i].Value
		node := providers.Content[i+1]
		if !dshManagedProvider(id) && dshText(dshGet(node, "apiKeyEnv")) != dshManagedReference {
			continue
		}
		existingProviders[id] = node
		models, err := dshModels(node)
		if err != nil {
			return nil, err
		}
		if models == nil {
			continue
		}
		for _, item := range models.Content {
			name := dshText(dshGet(item, "id"))
			if name == "" {
				continue
			}
			if _, exists := existingModels[name]; !exists {
				existingModels[name] = cloneDshNode(item)
			}
		}
	}
	for id := range existingProviders {
		dshRemove(providers, id)
	}

	byAPI := make(map[string][]dshResolvedRoute)
	selectedModels := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		byAPI[route.Definition.API] = append(byAPI[route.Definition.API], route)
		selectedModels[route.ModelName] = struct{}{}
	}
	preservedByAPI := make(map[string][]string)
	preservedSeen := make(map[string]map[string]struct{})
	for _, old := range existingProviders {
		definition, ok := dshProviderDefinitionForAPI(dshText(dshGet(old, "api")))
		if !ok {
			continue
		}
		models, err := dshModels(old)
		if err != nil {
			return nil, err
		}
		if models == nil {
			continue
		}
		for _, item := range models.Content {
			name := dshText(dshGet(item, "id"))
			if name == "" {
				continue
			}
			if _, selected := selectedModels[name]; selected {
				continue
			}
			// 已从路由中移除的 Octopus 分组不再保留；未知模型视为用户手工
			// 添加的模型，仍原样保留，避免自动配置删除用户数据。
			if _, known := knownGroupNames[name]; known {
				continue
			}
			if preservedSeen[definition.API] == nil {
				preservedSeen[definition.API] = make(map[string]struct{})
			}
			if _, seen := preservedSeen[definition.API][name]; seen {
				continue
			}
			preservedSeen[definition.API][name] = struct{}{}
			preservedByAPI[definition.API] = append(preservedByAPI[definition.API], name)
		}
	}

	states := make([]model.DshRouteState, 0, len(routes))
	for _, definition := range dshProviderDefinitions {
		group := byAPI[definition.API]
		if len(group) == 0 && len(preservedByAPI[definition.API]) == 0 {
			continue
		}
		var providerNode *yaml.Node
		if existing, ok := existingProviders[definition.ID]; ok {
			providerNode = cloneDshNode(existing)
		} else {
			for _, old := range existingProviders {
				if oldDefinition, ok := dshProviderDefinitionForAPI(dshText(dshGet(old, "api"))); ok && oldDefinition.API == definition.API {
					providerNode = cloneDshNode(old)
					break
				}
			}
		}
		if providerNode == nil {
			providerNode = dshMap()
		}
		dshSet(providerNode, "api", dshString(definition.API))
		dshSet(providerNode, "baseURL", dshString(dshBaseURLForAPI(definition.API)))
		dshSet(providerNode, "apiKeyEnv", dshString(dshManagedReference))
		dshSet(providerNode, "displayName", dshString(definition.DisplayName))
		models := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, route := range group {
			item := existingModels[route.ModelName]
			if item == nil {
				item = dshMap()
				dshSet(item, "id", dshString(route.ModelName))
				dshSet(item, "name", dshString(route.ModelName))
			} else {
				item = cloneDshNode(item)
			}
			dshEnsureModelInput(item)
			models.Content = append(models.Content, item)
			states = append(states, model.DshRouteState{
				Provider:     definition.ID,
				API:          definition.API,
				ModelName:    route.ModelName,
				ModelGroupID: route.Selection.ModelGroupID,
			})
		}
		for _, name := range preservedByAPI[definition.API] {
			item := existingModels[name]
			if item == nil {
				continue
			}
			item = cloneDshNode(item)
			dshEnsureModelInput(item)
			models.Content = append(models.Content, item)
		}
		dshSet(providerNode, "models", models)
		dshSet(providers, definition.ID, providerNode)
	}

	var defaultRoute *dshResolvedRoute
	for i := range routes {
		if routes[i].Selection.ModelGroupID == defaultGroupID {
			defaultRoute = &routes[i]
			break
		}
	}
	if defaultRoute == nil {
		return nil, fmt.Errorf("DSH 默认模型必须在模型路由中")
	}
	defaults, err := p.config("agent-default-model", true)
	if err != nil {
		return nil, err
	}
	dshSet(defaults, "provider", dshString(defaultRoute.Definition.ID))
	dshSet(defaults, "model", dshString(defaultRoute.ModelName))
	return states, nil
}

func dshRouteStates(p *dshProfile, groups []model.Group) ([]model.DshRouteState, error) {
	providers, err := p.providers(false)
	if err != nil || providers == nil {
		return nil, err
	}
	groupIDs := make(map[string]int, len(groups))
	for _, group := range groups {
		groupIDs[group.Name] = group.ID
	}
	states := []model.DshRouteState{}
	for i := 0; i < len(providers.Content); i += 2 {
		id := providers.Content[i].Value
		provider := providers.Content[i+1]
		if !dshManagedProvider(id) && dshText(dshGet(provider, "apiKeyEnv")) != dshManagedReference {
			continue
		}
		api := dshText(dshGet(provider, "api"))
		models, err := dshModels(provider)
		if err != nil {
			return nil, err
		}
		if models == nil {
			continue
		}
		for _, item := range models.Content {
			name := dshText(dshGet(item, "id"))
			states = append(states, model.DshRouteState{Provider: id, API: api, ModelName: name, ModelGroupID: groupIDs[name]})
		}
	}
	return states, nil
}

func dshSupportsEffort(model *yaml.Node, effort string) bool {
	if effort == "" {
		return true
	}
	efforts := dshGet(model, "reasoningEfforts")
	return efforts != nil && efforts.Kind == yaml.MappingNode && dshGet(efforts, effort) != nil
}

func dshReasoningEffort(defaults *yaml.Node) string {
	if effort := dshText(dshGet(defaults, "reasoningEffort")); effort != "" {
		return effort
	}
	return "default"
}

// 只为未声明推理能力的模型补齐 DSH 档位；已有映射及显式 false 均保留。
func dshConfigureReasoning(item, defaults *yaml.Node, selected *string) (bool, error) {
	if selected != nil {
		switch *selected {
		case "default", "off", "low", "high", "max":
		default:
			return false, fmt.Errorf("DSH 推理等级必须为 Default、Off、Low、High 或 Max")
		}
	}
	if efforts := dshGet(item, "reasoningEfforts"); efforts == nil || efforts.Tag == "!!null" {
		efforts := dshMap()
		// DSH 用空的 off 映射表示不发送 reasoning_effort，Default 则不写默认等级。
		dshSet(efforts, "off", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"})
		for _, effort := range []string{"low", "high", "max"} {
			dshSet(efforts, effort, dshString(effort))
		}
		dshSet(item, "reasoningEfforts", efforts)
	}
	if selected != nil {
		if *selected == "default" {
			dshRemove(defaults, "reasoningEffort")
			return false, nil
		}
		if !dshSupportsEffort(item, *selected) {
			return false, fmt.Errorf("所选模型的自定义 reasoningEfforts 未声明 %s 等级，请在配置文件中调整", *selected)
		}
		dshSet(defaults, "reasoningEffort", dshString(*selected))
		return false, nil
	}
	reset := !dshSupportsEffort(item, dshText(dshGet(defaults, "reasoningEffort")))
	if reset {
		dshRemove(defaults, "reasoningEffort")
	}
	return reset, nil
}

func validateDshOctopus(p *dshProfile) error {
	defaults, err := p.config("agent-default-model", false)
	if err != nil {
		return err
	}
	providers, err := p.providers(false)
	if err != nil {
		return err
	}
	defaultProviderID := dshText(dshGet(defaults, "provider"))
	managedProviders := map[string]*yaml.Node{}
	if providers != nil {
		for i := 0; i < len(providers.Content); i += 2 {
			id := providers.Content[i].Value
			provider := providers.Content[i+1]
			if !dshManagedProvider(id) && dshText(dshGet(provider, "apiKeyEnv")) != dshManagedReference {
				continue
			}
			managedProviders[id] = provider
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
		}
	}
	if dshManagedProvider(defaultProviderID) {
		provider := managedProviders[defaultProviderID]
		if provider == nil {
			return fmt.Errorf("DSH 默认提供商 Octopus 尚未配置")
		}
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
	groups, _ := GroupList(ctx)
	routes, err := dshRouteStates(p, groups)
	if err != nil {
		return nil, nil, err
	}
	defaults, err := p.config("agent-default-model", false)
	if err != nil {
		return nil, nil, err
	}
	providers, err := p.providers(false)
	if err != nil {
		return nil, nil, err
	}
	currentProvider := dshText(dshGet(defaults, "provider"))
	currentModel := dshText(dshGet(defaults, "model"))
	var selectedProvider *yaml.Node
	if providers != nil {
		if dshManagedProvider(currentProvider) {
			selectedProvider = dshGet(providers, currentProvider)
		}
		if selectedProvider == nil {
			for i := 0; i < len(providers.Content); i += 2 {
				id := providers.Content[i].Value
				candidate := providers.Content[i+1]
				if dshManagedProvider(id) || dshText(dshGet(candidate, "apiKeyEnv")) == dshManagedReference {
					selectedProvider = candidate
					break
				}
			}
		}
	}
	ref := dshText(dshGet(selectedProvider, "apiKeyEnv"))
	api := dshText(dshGet(selectedProvider, "api"))
	if api == "" {
		api = "openai-completions"
	}
	groupIDs := make(map[string]int, len(groups))
	for _, group := range groups {
		groupIDs[group.Name] = group.ID
	}
	state := &model.DshConfigState{
		Profile:             "desktop",
		API:                 api,
		CredentialReference: ref,
		CredentialRevision:  dshCredentialRevision(credentials),
		CurrentProvider:     currentProvider,
		CurrentModel:        currentModel,
		DefaultModelGroupID: groupIDs[currentModel],
		Routes:              routes,
	}
	state.ReasoningEffort = dshReasoningEffort(defaults)
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

func dshValidateSaveRoutes(p *dshProfile, opts model.DshConfigSaveOptions, key model.APIKey, ctx context.Context) error {
	providers, err := p.providers(false)
	if err != nil {
		return err
	}
	if providers != nil {
		for i := 0; i < len(providers.Content); i += 2 {
			id := providers.Content[i].Value
			provider := providers.Content[i+1]
			if !dshManagedProvider(id) && dshText(dshGet(provider, "apiKeyEnv")) != dshManagedReference {
				continue
			}
			if dshText(dshGet(provider, "apiKeyEnv")) != dshManagedReference {
				return fmt.Errorf("DSH 凭据引用已改变，请重新生成快速配置")
			}
		}
	}
	selections := opts.Routes
	if len(selections) == 0 && opts.ModelGroupID > 0 {
		selections = []model.DshRouteSelection{{ModelGroupID: opts.ModelGroupID}}
	}
	for _, selection := range selections {
		name, err := agentModelGroupName(selection.ModelGroupID, key.SupportedModels, ctx)
		if err != nil {
			return err
		}
		if selection.API != "" {
			definition, ok := dshProviderDefinitionForAPI(selection.API)
			if !ok {
				return fmt.Errorf("DSH 暂不支持所选 API 协议")
			}
			provider, err := p.providerByID(definition.ID, false)
			if err != nil {
				return err
			}
			item, err := dshModel(provider, name)
			if err != nil {
				return err
			}
			if item == nil {
				return fmt.Errorf("所选分组已改变，请重新生成 DSH 配置")
			}
			continue
		}
		found := false
		if providers != nil {
			for i := 0; i < len(providers.Content); i += 2 {
				id := providers.Content[i].Value
				provider := providers.Content[i+1]
				if !dshManagedProvider(id) && dshText(dshGet(provider, "apiKeyEnv")) != dshManagedReference {
					continue
				}
				item, err := dshModel(provider, name)
				if err != nil {
					return err
				}
				if item != nil {
					found = true
					break
				}
			}
		}
		if !found {
			return fmt.Errorf("所选分组已改变，请重新生成 DSH 配置")
		}
	}
	return nil
}

func DshAutoConfigPrepare(req model.AgentAutoConfigRequest, ctx context.Context) (model.AgentAutoConfigResponse, error) {
	result := model.AgentAutoConfigResponse{}
	if req.Agent != dshAgentID || req.DSH == nil || req.ModelGroups != nil || req.CodexModelGroupID != 0 {
		return result, fmt.Errorf("无效的 DSH 自动配置参数")
	}
	paths, err := resolveDshPaths(req.DSH.Profile)
	if err != nil {
		return result, err
	}
	p, original, _, err := loadDshProfile(paths, req.CurrentContent)
	if err != nil {
		return result, err
	}
	selections, defaultGroupID, err := dshSelectionsFromOptions(*req.DSH, p)
	if err != nil {
		return result, err
	}
	key, err := dshLoadKey(req.APIKeyID, ctx)
	if err != nil {
		return result, err
	}
	routes, defaultGroupID, err := dshResolveRoutes(key, selections, defaultGroupID, ctx)
	if err != nil {
		return result, err
	}
	groups, _ := GroupList(ctx)
	knownGroupNames := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		knownGroupNames[group.Name] = struct{}{}
	}
	states, err := dshApplyRoutes(p, routes, defaultGroupID, knownGroupNames)
	if err != nil {
		return result, err
	}
	defaults, err := p.config("agent-default-model", true)
	if err != nil {
		return result, err
	}
	var defaultRoute *dshResolvedRoute
	for i := range routes {
		if routes[i].Selection.ModelGroupID == defaultGroupID {
			defaultRoute = &routes[i]
			break
		}
	}
	if defaultRoute == nil {
		return result, fmt.Errorf("DSH 默认模型必须在模型路由中")
	}
	provider, err := p.providerByID(defaultRoute.Definition.ID, false)
	if err != nil {
		return result, err
	}
	item, err := dshModel(provider, defaultRoute.ModelName)
	if err != nil {
		return result, err
	}
	if item == nil {
		return result, fmt.Errorf("DSH 默认模型不在 Octopus 模型目录中")
	}
	reasoningReset, err := dshConfigureReasoning(item, defaults, req.DSH.ReasoningEffort)
	if err != nil {
		return result, err
	}
	if err := validateDshOctopus(p); err != nil {
		return result, err
	}
	content, err := renderDshYAML(p.patch, original)
	if err != nil {
		return result, err
	}
	credentials, _, err := readDshCredentials(paths)
	if err != nil {
		return result, err
	}
	result.FileKey, result.Content = "profile", content
	result.DSH = &model.DshPreparedConfig{
		Profile:             "desktop",
		CredentialReference: dshManagedReference,
		CredentialRevision:  dshCredentialRevision(credentials),
		ModelGroupID:        defaultGroupID,
		ModelName:           defaultRoute.ModelName,
		API:                 defaultRoute.Definition.API,
		DefaultModelGroupID: defaultGroupID,
		Routes:              states,
		ReasoningEffort:     dshReasoningEffort(defaults),
		ReasoningReset:      reasoningReset,
	}
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
			key, err := dshLoadKey(req.DSH.APIKeyID, ctx)
			if err != nil {
				return err
			}
			if err := dshValidateSaveRoutes(p, *req.DSH, key, ctx); err != nil {
				return err
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

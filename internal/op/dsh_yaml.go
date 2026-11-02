package op

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

// YAML 诊断仅携带位置，不能把凭据所在源码行带入日志或接口。
func dshYAMLError(node *yaml.Node) error {
	if node != nil && node.Line > 0 {
		return fmt.Errorf("DSH YAML 配置结构无效（第 %d 行）", node.Line)
	}
	return fmt.Errorf("DSH YAML 格式无效，请检查配置文件")
}

func parseDshYAML(content string, kind yaml.Kind) (*yaml.Node, error) {
	decoder := yaml.NewDecoder(strings.NewReader(content))
	var doc yaml.Node
	if err := decoder.Decode(&doc); err != nil && err != io.EOF {
		return nil, dshYAMLError(nil)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, dshYAMLError(nil)
	}
	if len(doc.Content) == 0 || doc.Content[0].Tag == "!!null" {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: kind}}}
	}
	root := doc.Content[0]
	if root.Kind != kind {
		return nil, dshYAMLError(root)
	}
	if err := validateDshYAMLTree(root, 0); err != nil {
		return nil, err
	}
	return &doc, nil
}

func validateDshYAMLTree(node *yaml.Node, depth int) error {
	if depth > 80 || node.Kind == yaml.AliasNode {
		return dshYAMLError(node)
	}
	if node.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || seen[key.Value] {
				return dshYAMLError(key)
			}
			seen[key.Value] = true
		}
	}
	for _, child := range node.Content {
		if err := validateDshYAMLTree(child, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func dshMap() *yaml.Node { return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"} }
func dshString(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func dshGet(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func dshSet(node *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			old := node.Content[i+1]
			value.HeadComment, value.LineComment, value.FootComment = old.HeadComment, old.LineComment, old.FootComment
			if value.Kind == yaml.ScalarNode && old.Kind == yaml.ScalarNode && old.Tag == value.Tag {
				value.Style = old.Style
			}
			node.Content[i+1] = value
			return
		}
	}
	node.Content = append(node.Content, dshString(key), value)
}

func dshRemove(node *yaml.Node, key string) {
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			node.Content = append(node.Content[:i], node.Content[i+2:]...)
			return
		}
	}
}

func dshText(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return ""
	}
	return node.Value
}

func dshChildMap(parent *yaml.Node, key string) (*yaml.Node, error) {
	node := dshGet(parent, key)
	if node == nil {
		node = dshMap()
		dshSet(parent, key, node)
	}
	if node.Kind != yaml.MappingNode {
		return nil, dshYAMLError(node)
	}
	return node, nil
}

func cloneDshNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return dshMap()
	}
	copy := *node
	copy.Content = make([]*yaml.Node, len(node.Content))
	for i, child := range node.Content {
		copy.Content[i] = cloneDshNode(child)
	}
	return &copy
}

func renderDshYAML(doc *yaml.Node, original string) (string, error) {
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(doc); err != nil {
		return "", dshYAMLError(nil)
	}
	if err := encoder.Close(); err != nil {
		return "", dshYAMLError(nil)
	}
	text := buffer.String()
	if strings.Contains(original, "\r\n") {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text, nil
}

// refs/records 遵守 DSH v1 文档约定；未知顶层内容作为原有节点保留。
func parseDshCredentials(content string) (*yaml.Node, error) {
	doc, err := parseDshYAML(content, yaml.MappingNode)
	if err != nil {
		return nil, err
	}
	root := doc.Content[0]
	if len(root.Content) == 0 {
		dshSet(root, "version", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"})
	}
	version := dshGet(root, "version")
	if version == nil || version.Tag != "!!int" || version.Value != "1" {
		return nil, dshYAMLError(version)
	}
	for _, section := range []string{"refs", "records"} {
		node := dshGet(root, section)
		if node == nil || node.Tag == "!!null" {
			continue
		}
		if node.Kind != yaml.MappingNode {
			return nil, dshYAMLError(node)
		}
		for i := 0; i < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if section == "refs" {
				if !dshReferencePattern.MatchString(key.Value) || dshText(value) == "" {
					return nil, dshYAMLError(value)
				}
			} else if err := validateDshCredentialRecord(key.Value, value); err != nil {
				return nil, err
			}
		}
	}
	return doc, nil
}

func validateDshCredentialRecord(key string, node *yaml.Node) error {
	if !dshRecordPattern.MatchString(key) || node.Kind != yaml.MappingNode {
		return dshYAMLError(node)
	}
	kind := dshText(dshGet(node, "kind"))
	allowed := map[string]bool{"kind": true}
	switch kind {
	case "api-key":
		allowed["key"], allowed["env"] = true, true
		if value := dshGet(node, "key"); value != nil && dshText(value) == "" {
			return dshYAMLError(value)
		}
		if env := dshGet(node, "env"); env != nil {
			if env.Kind != yaml.MappingNode {
				return dshYAMLError(env)
			}
			for i := 0; i < len(env.Content); i += 2 {
				if !dshReferencePattern.MatchString(env.Content[i].Value) || dshText(env.Content[i+1]) == "" {
					return dshYAMLError(env.Content[i+1])
				}
			}
		}
	case "grant":
		allowed["payload"] = true
		payload := dshGet(node, "payload")
		if payload == nil {
			return dshYAMLError(node)
		}
		if err := validateDshJSONNode(payload); err != nil {
			return err
		}
	default:
		return dshYAMLError(node)
	}
	for i := 0; i < len(node.Content); i += 2 {
		if !allowed[node.Content[i].Value] {
			return dshYAMLError(node.Content[i])
		}
	}
	return nil
}

func validateDshJSONNode(node *yaml.Node) error {
	if node.Kind == yaml.MappingNode || node.Kind == yaml.SequenceNode {
		for _, child := range node.Content {
			if err := validateDshJSONNode(child); err != nil {
				return err
			}
		}
		return nil
	}
	if node.Kind != yaml.ScalarNode {
		return dshYAMLError(node)
	}
	switch node.Tag {
	case "!!str", "!!bool", "!!null", "!!timestamp":
		return nil
	case "!!int", "!!float":
		value, err := strconv.ParseFloat(strings.ReplaceAll(node.Value, "_", ""), 64)
		if err == nil && !math.IsNaN(value) && !math.IsInf(value, 0) {
			return nil
		}
	}
	return dshYAMLError(node)
}

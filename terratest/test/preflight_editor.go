package test

import (
	"bytes"
	"fmt"
	"os"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

func currentPreflightVersions() []string {
	requestedVersions := viper.GetStringSlice("rancher.versions")
	if len(requestedVersions) > 0 {
		versions := make([]string, 0, len(requestedVersions))
		for _, version := range requestedVersions {
			versions = append(versions, normalizeVersionInput(version))
		}
		return versions
	}

	if singleVersion := normalizeVersionInput(viper.GetString("rancher.version")); singleVersion != "" {
		return []string{singleVersion, ""}
	}

	totalInstances := getTotalRancherInstances()
	if totalInstances < 2 {
		totalInstances = 2
	}
	if totalInstances > 4 {
		totalInstances = 4
	}

	return make([]string, totalInstances)
}

func normalizePreflightVersions(versions []string) ([]string, error) {
	if len(versions) < 2 {
		return nil, fmt.Errorf("at least 2 Rancher versions are required (1 host + 1 tenant)")
	}
	if len(versions) > 4 {
		return nil, fmt.Errorf("no more than 4 Rancher versions are supported")
	}

	normalized := make([]string, 0, len(versions))
	for i, version := range versions {
		normalizedVersion := normalizeVersionInput(version)
		if normalizedVersion == "" {
			return nil, fmt.Errorf("version for instance %d cannot be empty", i+1)
		}
		normalized = append(normalized, normalizedVersion)
	}

	return normalized, nil
}

func updateAutoModeConfigFile(configPath string, versions []string) error {
	normalizedVersions, err := normalizePreflightVersions(versions)
	if err != nil {
		return err
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	var document yaml.Node
	if err := yaml.Unmarshal(content, &document); err != nil {
		return fmt.Errorf("failed to parse config file: %w", err)
	}
	if len(document.Content) == 0 {
		return fmt.Errorf("config file is empty")
	}

	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("config root must be a YAML mapping")
	}

	rancherNode := ensureMappingValue(root, "rancher")
	setStringSequenceValue(rancherNode, "versions", normalizedVersions)
	deleteMappingKey(rancherNode, "version")
	setIntValue(root, "total_rancher_instances", len(normalizedVersions))
	deleteMappingKey(root, "total_has")

	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return fmt.Errorf("failed to serialize config file: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("failed to finalize config file: %w", err)
	}

	if err := os.WriteFile(configPath, output.Bytes(), 0o644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	viper.Set("rancher.versions", normalizedVersions)
	viper.Set("total_rancher_instances", len(normalizedVersions))
	viper.Set("rancher.version", "")

	return nil
}

func ensureMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if value := mappingValue(mapping, key); value != nil {
		if value.Kind != yaml.MappingNode {
			value.Kind = yaml.MappingNode
			value.Tag = "!!map"
			value.Style = 0
			value.Content = nil
		}
		return value
	}

	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valueNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mapping.Content = append(mapping.Content, keyNode, valueNode)
	return valueNode
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}

	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}

	return nil
}

func deleteMappingKey(mapping *yaml.Node, key string) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return
	}

	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return
		}
	}
}

func setStringSequenceValue(mapping *yaml.Node, key string, values []string) {
	sequenceNode := mappingValue(mapping, key)
	if sequenceNode == nil {
		mapping.Content = append(mapping.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			&yaml.Node{},
		)
		sequenceNode = mapping.Content[len(mapping.Content)-1]
	}

	sequenceNode.Kind = yaml.SequenceNode
	sequenceNode.Tag = "!!seq"
	sequenceNode.Style = 0
	sequenceNode.Content = make([]*yaml.Node, 0, len(values))
	for _, value := range values {
		sequenceNode.Content = append(sequenceNode.Content, &yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!str",
			Style: yaml.DoubleQuotedStyle,
			Value: value,
		})
	}
}

func setIntValue(mapping *yaml.Node, key string, value int) {
	valueNode := mappingValue(mapping, key)
	if valueNode == nil {
		mapping.Content = append(mapping.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			&yaml.Node{},
		)
		valueNode = mapping.Content[len(mapping.Content)-1]
	}

	valueNode.Kind = yaml.ScalarNode
	valueNode.Tag = "!!int"
	valueNode.Style = 0
	valueNode.Value = fmt.Sprintf("%d", value)
}

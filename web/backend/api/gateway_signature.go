package api

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

func lookupModelConfig(cfg *config.Config, modelName string) *config.ModelConfig {
	modelCfg, err := providers.ResolveModelConfig(cfg, modelName)
	if err != nil {
		return nil
	}
	return modelCfg
}

func computeConfigSignature(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	var parts []string
	parts = append(parts, "default_provider:"+defaultChainProvider(cfg))
	defaultModel := strings.TrimSpace(cfg.Agents.Defaults.GetModelName())
	if defaultModel != "" {
		parts = append(parts, "model:"+defaultModel)
	}
	modelStreamingSignatures := computeModelStreamingSignatures(cfg)
	if len(modelStreamingSignatures) > 0 {
		parts = append(parts, "model_streaming:"+strings.Join(modelStreamingSignatures, ","))
	}
	toolSignatures := []string{}
	if cfg.Tools.ReadFile.Enabled {
		toolSignatures = append(toolSignatures, "read_file")
	}
	if cfg.Tools.WriteFile.Enabled {
		toolSignatures = append(toolSignatures, "write_file")
	}
	if cfg.Tools.ListDir.Enabled {
		toolSignatures = append(toolSignatures, "list_dir")
	}
	if cfg.Tools.EditFile.Enabled {
		toolSignatures = append(toolSignatures, "edit_file")
	}
	if cfg.Tools.AppendFile.Enabled {
		toolSignatures = append(toolSignatures, "append_file")
	}
	if cfg.Tools.Exec.Enabled {
		toolSignatures = append(toolSignatures, "exec")
	}
	if cfg.Tools.Cron.Enabled {
		toolSignatures = append(toolSignatures, "cron")
	}
	if cfg.Tools.Web.Enabled {
		toolSignatures = append(toolSignatures, "web")
		webConfig, err := json.Marshal(canonicalizeSignatureValue(reflect.ValueOf(cfg.Tools.Web)))
		if err == nil {
			parts = append(parts, "webcfg:"+string(webConfig))
		}
	}
	if cfg.Tools.WebFetch.Enabled {
		toolSignatures = append(toolSignatures, "web_fetch")
	}
	if cfg.Tools.Message.Enabled {
		toolSignatures = append(toolSignatures, "message")
	}
	if cfg.Tools.SendFile.Enabled {
		toolSignatures = append(toolSignatures, "send_file")
	}
	if cfg.Tools.FindSkills.Enabled {
		toolSignatures = append(toolSignatures, "find_skills")
	}
	if cfg.Tools.InstallSkill.Enabled {
		toolSignatures = append(toolSignatures, "install_skill")
	}
	if cfg.Tools.Spawn.Enabled {
		toolSignatures = append(toolSignatures, "spawn")
	}
	if cfg.Tools.SpawnStatus.Enabled {
		toolSignatures = append(toolSignatures, "spawn_status")
	}
	if cfg.Tools.I2C.Enabled {
		toolSignatures = append(toolSignatures, "i2c")
	}
	if cfg.Tools.SPI.Enabled {
		toolSignatures = append(toolSignatures, "spi")
	}
	if cfg.Tools.MCP.Enabled {
		toolSignatures = append(toolSignatures, "mcp")
	}
	if len(toolSignatures) > 0 {
		parts = append(parts, "tools:"+strings.Join(toolSignatures, ","))
	}
	channelSignatures := computeChannelSignatures(cfg.Channels)
	if len(channelSignatures) > 0 {
		parts = append(parts, "channels:"+strings.Join(channelSignatures, ","))
	}
	return strings.Join(parts, ";")
}

func computeModelStreamingSignatures(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	defaultProvider := strings.TrimSpace(cfg.Agents.Defaults.Provider)
	if defaultProvider == "" {
		defaultProvider = "openai"
	}
	type streamingRef struct {
		scope string
		name  string
	}

	refs := []streamingRef{{
		scope: "defaults.primary",
		name:  strings.TrimSpace(cfg.Agents.Defaults.GetModelName()),
	}}
	for i, name := range cfg.Agents.Defaults.ModelFallbacks {
		refs = append(refs, streamingRef{
			scope: "defaults.fallback:" + strconv.Itoa(i),
			name:  name,
		})
	}
	if cfg.Agents.Defaults.Routing != nil {
		refs = append(refs, streamingRef{
			scope: "defaults.routing.light",
			name:  cfg.Agents.Defaults.Routing.LightModel,
		})
	}
	for agentIndex, agent := range cfg.Agents.List {
		if agent.Model == nil {
			continue
		}
		refs = append(refs, streamingRef{
			scope: "agent:" + strconv.Itoa(agentIndex) + ".primary",
			name:  agent.Model.Primary,
		})
		for i, name := range agent.Model.Fallbacks {
			refs = append(refs, streamingRef{
				scope: "agent:" + strconv.Itoa(agentIndex) + ".fallback:" + strconv.Itoa(i),
				name:  name,
			})
		}
	}

	seenEntries := make(map[string]bool)
	signatures := make([]string, 0, len(refs))
	for _, ref := range refs {
		ref.name = strings.TrimSpace(ref.name)
		if ref.name == "" {
			continue
		}
		referenceEntry := strings.Join([]string{ref.scope, ref.name}, ":")
		if !seenEntries[referenceEntry] {
			seenEntries[referenceEntry] = true
			signatures = append(signatures, referenceEntry)
		}
		for _, match := range modelConfigsMatchingSignatureRef(cfg.ModelList, ref.name, defaultProvider) {
			mc := match.model
			entry := strings.Join([]string{
				ref.scope,
				ref.name,
				strconv.Itoa(match.index),
				strings.TrimSpace(mc.Provider),
				strings.TrimSpace(mc.Model),
				strconv.FormatBool(mc.Streaming.Enabled),
			}, ":")
			if seenEntries[entry] {
				continue
			}
			seenEntries[entry] = true
			signatures = append(signatures, entry)
		}
	}
	sort.Strings(signatures)
	return signatures
}

type signatureModelConfigMatch struct {
	index int
	model *config.ModelConfig
}

func modelConfigsMatchingSignatureRef(
	modelList []*config.ModelConfig,
	raw string,
	defaultProvider string,
) []signatureModelConfigMatch {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	matches := make([]signatureModelConfigMatch, 0, 1)
	for i, mc := range modelList {
		if mc == nil || !mc.IsEnabled() || strings.TrimSpace(mc.ModelName) != raw {
			continue
		}
		matches = append(matches, signatureModelConfigMatch{index: i, model: mc})
	}
	if len(matches) > 0 {
		return matches
	}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Provider: defaultProvider},
		},
		ModelList: modelList,
	}
	resolved, err := providers.ResolveModelConfig(cfg, raw)
	if err != nil {
		return nil
	}
	for i, modelCfg := range modelList {
		if modelConfigsMatchResolvedEntry(modelCfg, resolved, defaultProvider) {
			return []signatureModelConfigMatch{{index: i, model: modelCfg}}
		}
	}
	return nil
}

func modelConfigsMatchResolvedEntry(
	left,
	right *config.ModelConfig,
	defaultProvider string,
) bool {
	if left == nil || right == nil || left.IsVirtual() || !left.IsEnabled() {
		return false
	}
	leftProvider, leftModel := modelConfigProviderAndID(left, defaultProvider)
	rightProvider, rightModel := providers.ExtractProtocol(right)
	return left.ModelName == right.ModelName &&
		providers.ModelKey(leftProvider, leftModel) == providers.ModelKey(rightProvider, rightModel) &&
		left.APIBase == right.APIBase &&
		left.APIKey() == right.APIKey() &&
		left.IsEnabled() == right.IsEnabled() &&
		left.AuthMethod == right.AuthMethod &&
		left.ConnectMode == right.ConnectMode
}

func modelConfigProviderAndID(mc *config.ModelConfig, defaultProvider string) (string, string) {
	if mc == nil {
		return "", ""
	}
	if strings.TrimSpace(mc.Provider) != "" {
		return providers.ExtractProtocol(mc)
	}
	return providers.SplitModelProviderAndID(mc.Model, defaultProvider)
}

func computeChannelSignatures(channels config.ChannelsConfig) []string {
	if len(channels) == 0 {
		return nil
	}

	keys := make([]string, 0, len(channels))
	for name := range channels {
		keys = append(keys, name)
	}
	sort.Strings(keys)

	signatures := make([]string, 0, len(keys))
	for _, name := range keys {
		channel := channels[name]
		if channel == nil {
			signatures = append(signatures, name+":<nil>")
			continue
		}

		payload := struct {
			Enabled            bool                       `json:"enabled"`
			Type               string                     `json:"type"`
			AllowFrom          config.FlexibleStringSlice `json:"allow_from,omitempty"`
			ReasoningChannelID string                     `json:"reasoning_channel_id,omitempty"`
			GroupTrigger       config.GroupTriggerConfig  `json:"group_trigger,omitempty"`
			Typing             config.TypingConfig        `json:"typing,omitempty"`
			Placeholder        config.PlaceholderConfig   `json:"placeholder,omitempty"`
			Settings           json.RawMessage            `json:"settings,omitempty"`
		}{
			Enabled:            channel.Enabled,
			Type:               channel.Type,
			AllowFrom:          channel.AllowFrom,
			ReasoningChannelID: channel.ReasoningChannelID,
			GroupTrigger:       channel.GroupTrigger,
			Typing:             channel.Typing,
			Placeholder:        channel.Placeholder,
			Settings:           normalizeChannelSettings(channel),
		}

		encoded, err := json.Marshal(payload)
		if err != nil {
			signatures = append(signatures, name+":<invalid>")
			continue
		}
		signatures = append(signatures, name+":"+string(encoded))
	}

	return signatures
}

func normalizeChannelSettings(channel *config.Channel) json.RawMessage {
	if channel == nil {
		return nil
	}

	decoded, err := channel.GetDecoded()
	if err == nil && decoded != nil {
		normalized, err := json.Marshal(canonicalizeSignatureValue(reflect.ValueOf(decoded)))
		if err == nil {
			return normalized
		}
	}

	return normalizeRawJSON(channel.Settings)
}

func normalizeRawJSON(raw config.RawNode) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}

	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return bytes.TrimSpace(raw)
	}

	normalized, err := json.Marshal(value)
	if err != nil {
		return bytes.TrimSpace(raw)
	}
	return normalized
}

func canonicalizeSignatureValue(value reflect.Value) any {
	if !value.IsValid() {
		return nil
	}

	if value.CanInterface() {
		switch typed := value.Interface().(type) {
		case config.SecureString:
			return typed.String()
		case *config.SecureString:
			if typed == nil {
				return ""
			}
			return typed.String()
		case config.SecureStrings:
			return typed.Values()
		case *config.SecureStrings:
			if typed == nil {
				return nil
			}
			return typed.Values()
		}
	}

	switch value.Kind() {
	case reflect.Interface, reflect.Pointer:
		if value.IsNil() {
			return nil
		}
		return canonicalizeSignatureValue(value.Elem())
	case reflect.Struct:
		result := make(map[string]any)
		valueType := value.Type()
		for i := 0; i < value.NumField(); i++ {
			field := valueType.Field(i)
			if field.PkgPath != "" {
				continue
			}
			tag := field.Tag.Get("json")
			name := field.Name
			if tag != "" {
				if comma := strings.Index(tag, ","); comma >= 0 {
					tag = tag[:comma]
				}
				if tag == "-" {
					continue
				}
				if tag != "" {
					name = tag
				}
			}
			result[name] = canonicalizeSignatureValue(value.Field(i))
		}
		return result
	case reflect.Slice, reflect.Array:
		length := value.Len()
		result := make([]any, 0, length)
		for i := 0; i < length; i++ {
			result = append(result, canonicalizeSignatureValue(value.Index(i)))
		}
		return result
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return value.Interface()
		}
		result := make(map[string]any, value.Len())
		iter := value.MapRange()
		for iter.Next() {
			result[iter.Key().String()] = canonicalizeSignatureValue(iter.Value())
		}
		return result
	default:
		if value.CanInterface() {
			return value.Interface()
		}
		return nil
	}
}

func gatewayRestartRequiredBySignature(bootSignature, currentSignature, gatewayStatus string) bool {
	if gatewayStatus != "running" {
		return false
	}
	if bootSignature == "" || currentSignature == "" {
		return false
	}
	return bootSignature != currentSignature
}

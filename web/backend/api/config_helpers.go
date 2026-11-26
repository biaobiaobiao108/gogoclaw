package api

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
)

// validateConfig validates the configuration struct and returns a list of
// human-readable error strings; empty means valid.
func validateConfig(cfg *config.Config) []string {
	var errs []string

	if err := cfg.ValidateAgentWorkspaces(); err != nil {
		errs = append(errs, err.Error())
	}

	// Validate model_list entries
	if err := cfg.ValidateModelList(); err != nil {
		errs = append(errs, err.Error())
	}

	if err := cfg.ValidateTurnProfile(); err != nil {
		errs = append(errs, err.Error())
	}

	// Gateway port range
	if cfg.Gateway.Port != 0 && (cfg.Gateway.Port < 1 || cfg.Gateway.Port > 65535) {
		errs = append(errs, fmt.Sprintf("gateway.port %d is out of valid range (1-65535)", cfg.Gateway.Port))
	}

	for name, bc := range cfg.Channels {
		streaming, ok := channelStreamingConfig(bc)
		if !ok {
			continue
		}
		if streaming.ThrottleSeconds < 0 {
			errs = append(errs, fmt.Sprintf("channel %q streaming.throttle_seconds must be >= 0", name))
		}
		if streaming.MinGrowthChars < 0 {
			errs = append(errs, fmt.Sprintf("channel %q streaming.min_growth_chars must be >= 0", name))
		}
	}

	// Pico channel: token required when enabled
	{
		bc := cfg.Channels.GetByType(config.ChannelPico)
		if bc != nil && bc.Enabled {
			if decoded, err := bc.GetDecoded(); err == nil && decoded != nil {
				if c, ok := decoded.(*config.PicoSettings); ok && c.Token.String() == "" {
					errs = append(errs, "channels.pico.token is required when pico channel is enabled")
				}
			}
		}
	}

	// Telegram: token required when enabled
	{
		bc := cfg.Channels.GetByType(config.ChannelTelegram)
		if bc != nil && bc.Enabled {
			if decoded, err := bc.GetDecoded(); err == nil && decoded != nil {
				if c, ok := decoded.(*config.TelegramSettings); ok && c.Token.String() == "" {
					errs = append(errs, "channels.telegram.token is required when telegram channel is enabled")
				}
			}
		}
	}

	// Discord: token required when enabled
	{
		bc := cfg.Channels.GetByType(config.ChannelDiscord)
		if bc != nil && bc.Enabled {
			if decoded, err := bc.GetDecoded(); err == nil && decoded != nil {
				if c, ok := decoded.(*config.DiscordSettings); ok && c.Token.String() == "" {
					errs = append(errs, "channels.discord.token is required when discord channel is enabled")
				}
			}
		}
	}

	if cfg.Tools.Exec.Enabled {
		if cfg.Tools.Exec.EnableDenyPatterns {
			errs = append(
				errs,
				validateRegexPatterns("tools.exec.custom_deny_patterns", cfg.Tools.Exec.CustomDenyPatterns)...)
		}
		errs = append(
			errs,
			validateRegexPatterns("tools.exec.custom_allow_patterns", cfg.Tools.Exec.CustomAllowPatterns)...)
	}

	return errs
}

func validateRegexPatterns(field string, patterns []string) []string {
	var errs []string
	for index, pattern := range patterns {
		if _, err := regexp.Compile(pattern); err != nil {
			errs = append(errs, fmt.Sprintf("%s[%d] is not a valid regular expression: %v", field, index, err))
		}
	}
	return errs
}

// mergeMap recursively merges src into dst (JSON Merge Patch semantics).
// - If a key in src has a null value, it is deleted from dst.
// - If both dst and src have a nested object for the same key, merge recursively.
// - Otherwise the value from src overwrites dst.
func mergeMap(dst, src map[string]any) {
	for key, srcVal := range src {
		if srcVal == nil {
			delete(dst, key)
			continue
		}
		srcMap, srcIsMap := srcVal.(map[string]any)
		dstMap, dstIsMap := dst[key].(map[string]any)
		if srcIsMap && dstIsMap {
			mergeMap(dstMap, srcMap)
		} else {
			dst[key] = srcVal
		}
	}
}

func asMapField(value map[string]any, key string) (map[string]any, bool) {
	raw, exists := value[key]
	if !exists {
		return nil, false
	}
	m, isMap := raw.(map[string]any)
	return m, isMap
}

var (
	allowFromHiddenCharsRe = regexp.MustCompile("[\u200B\u200C\u200D\u200E\u200F\u202A-\u202E\u2060-\u2069\uFEFF]")
	allowFromSplitRe       = regexp.MustCompile("[,\uFF0C、;；\r\n\t]+")
	conservativeSplitRe    = regexp.MustCompile("[,\uFF0C\r\n\t]+")
)

type stringArrayParserOptions struct {
	stripHiddenChars bool
}

func normalizeChannelArrayFields(raw map[string]any) error {
	channelsMap, hasChannels := asMapField(raw, "channel_list")
	if !hasChannels {
		return nil
	}

	defaultCfg := config.DefaultConfig()
	for channelName, rawChannel := range channelsMap {
		chMap, ok := rawChannel.(map[string]any)
		if !ok {
			continue
		}

		if rawAllowFrom, exists := chMap["allow_from"]; exists {
			normalized, err := normalizeStringArrayValue(rawAllowFrom, stringArrayParserOptions{
				stripHiddenChars: true,
			})
			if err != nil {
				return fmt.Errorf("channel_list.%s.allow_from: %w", channelName, err)
			}
			chMap["allow_from"] = normalized
		}

		if groupTrigger, ok := asMapField(chMap, "group_trigger"); ok {
			if rawPrefixes, exists := groupTrigger["prefixes"]; exists {
				normalized, err := normalizeStringArrayValue(rawPrefixes, stringArrayParserOptions{})
				if err != nil {
					return fmt.Errorf("channel_list.%s.group_trigger.prefixes: %w", channelName, err)
				}
				groupTrigger["prefixes"] = normalized
			}
		}

		settingsMap, hasSettings := asMapField(chMap, "settings")
		if !hasSettings {
			continue
		}

		settingsType := channelSettingsType(defaultCfg, channelName, chMap)
		if settingsType == nil {
			continue
		}

		for i := range settingsType.NumField() {
			field := settingsType.Field(i)
			if !field.IsExported() || !isStringSliceType(field.Type) {
				continue
			}
			jsonKey := strings.Split(field.Tag.Get("json"), ",")[0]
			if jsonKey == "" || jsonKey == "-" {
				continue
			}
			rawValue, exists := settingsMap[jsonKey]
			if !exists {
				continue
			}

			options := stringArrayParserOptions{}
			if jsonKey == "allow_from" {
				options.stripHiddenChars = true
			}
			normalized, err := normalizeStringArrayValue(rawValue, options)
			if err != nil {
				return fmt.Errorf("channel_list.%s.settings.%s: %w", channelName, jsonKey, err)
			}
			settingsMap[jsonKey] = normalized
		}
	}
	return nil
}

func channelSettingsType(
	defaultCfg *config.Config,
	channelName string,
	channelMap map[string]any,
) reflect.Type {
	if channelType, _ := channelMap["type"].(string); channelType != "" {
		if bc := defaultCfg.Channels.GetByType(channelType); bc != nil {
			if decoded, err := bc.GetDecoded(); err == nil && decoded != nil {
				return derefType(reflect.TypeOf(decoded))
			}
		}
	}

	if bc := defaultCfg.Channels.Get(channelName); bc != nil {
		if decoded, err := bc.GetDecoded(); err == nil && decoded != nil {
			return derefType(reflect.TypeOf(decoded))
		}
	}

	return nil
}

func derefType(typ reflect.Type) reflect.Type {
	for typ != nil && typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}
	return typ
}

func isStringSliceType(typ reflect.Type) bool {
	typ = derefType(typ)
	return typ != nil && typ.Kind() == reflect.Slice && typ.Elem().Kind() == reflect.String
}

func normalizeStringArrayValue(value any, options stringArrayParserOptions) ([]string, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case string:
		return parseStringArrayValue(typed, options), nil
	case float64:
		return normalizeStringArrayItems([]string{fmt.Sprintf("%.0f", typed)}, options), nil
	case []string:
		return normalizeStringArrayItems(typed, options), nil
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			switch raw := item.(type) {
			case string:
				items = append(items, raw)
			case float64:
				items = append(items, fmt.Sprintf("%.0f", raw))
			default:
				return nil, fmt.Errorf("unsupported list item type %T", item)
			}
		}
		return normalizeStringArrayItems(items, options), nil
	default:
		return nil, fmt.Errorf("unsupported list field type %T", value)
	}
}

func parseStringArrayValue(raw string, options stringArrayParserOptions) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	splitRe := conservativeSplitRe
	if options.stripHiddenChars {
		splitRe = allowFromSplitRe
	}
	return normalizeStringArrayItems(splitRe.Split(raw, -1), options)
}

func normalizeStringArrayItems(items []string, options stringArrayParserOptions) []string {
	result := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		normalized := item
		if options.stripHiddenChars {
			normalized = allowFromHiddenCharsRe.ReplaceAllString(normalized, "")
		}
		normalized = strings.TrimSpace(normalized)
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	if len(result) == 0 {
		return []string{}
	}
	return result
}

func getSecretString(m map[string]any, key string) (string, bool) {
	if raw, exists := m[key]; exists {
		s, isString := raw.(string)
		if isString {
			return s, true
		}
	}
	if raw, exists := m["_"+key]; exists {
		s, isString := raw.(string)
		if isString {
			return s, true
		}
	}
	return "", false
}

func applyConfigSecretsFromMap(cfg *config.Config, raw map[string]any) {
	channelsMap, hasChannels := asMapField(raw, "channel_list")
	if !hasChannels {
		return
	}

	for chName, chData := range channelsMap {
		chMap, ok := chData.(map[string]any)
		if !ok {
			continue
		}
		bc := cfg.Channels.Get(chName)
		if bc == nil {
			continue
		}
		decoded, err := bc.GetDecoded()
		if err != nil || decoded == nil {
			continue
		}
		rv := reflect.ValueOf(decoded)
		if rv.Kind() == reflect.Ptr {
			rv = rv.Elem()
		}
		if rv.Kind() != reflect.Struct {
			continue
		}
		// Channel-specific settings live under the "settings" key in the raw map
		settingsMap := chMap
		if sm, hasSettings := asMapField(chMap, "settings"); hasSettings {
			settingsMap = sm
		}
		applySecureStringsToStruct(rv, settingsMap)
	}

	// Handle tools secrets
	tools, hasTools := asMapField(raw, "tools")
	if !hasTools {
		return
	}
	skills, hasSkills := asMapField(tools, "skills")
	if !hasSkills {
		return
	}
	if github, hasGithub := asMapField(skills, "github"); hasGithub {
		if token, hasToken := getSecretString(github, "token"); hasToken {
			cfg.Tools.Skills.Github.Token.Set(token)
		}
	}
	if registries, hasRegistries := asMapField(skills, "registries"); hasRegistries {
		for registryName, rawRegistry := range registries {
			registryMap, ok := rawRegistry.(map[string]any)
			if !ok {
				continue
			}
			if authToken, hasAuthToken := getSecretString(registryMap, "auth_token"); hasAuthToken {
				registryCfg, _ := cfg.Tools.Skills.Registries.Get(registryName)
				registryCfg.AuthToken.Set(authToken)
				cfg.Tools.Skills.Registries.Set(registryName, registryCfg)
			}
		}
		return
	}

	registriesList, hasRegistries := skills["registries"].([]any)
	if !hasRegistries {
		return
	}
	for _, rawRegistry := range registriesList {
		registryMap, ok := rawRegistry.(map[string]any)
		if !ok {
			continue
		}
		name, _ := registryMap["name"].(string)
		if name == "" {
			continue
		}
		if authToken, hasAuthToken := getSecretString(registryMap, "auth_token"); hasAuthToken {
			registryCfg, _ := cfg.Tools.Skills.Registries.Get(name)
			registryCfg.AuthToken.Set(authToken)
			cfg.Tools.Skills.Registries.Set(name, registryCfg)
		}
	}
}

// applySecureStringsToStruct walks a struct and applies SecureString fields
// from the matching keys in rawMap. It recurses into nested maps and slices.
func applySecureStringsToStruct(rv reflect.Value, rawMap map[string]any) {
	rt := rv.Type()
	for jsonKey, rawVal := range rawMap {
		for i := range rt.NumField() {
			f := rt.Field(i)
			if !f.IsExported() {
				continue
			}
			tag := f.Tag.Get("json")
			name := strings.Split(tag, ",")[0]
			if name != jsonKey {
				continue
			}
			sf := rv.Field(i)
			if !sf.CanSet() {
				continue
			}
			// Direct SecureString field
			if s, ok := rawVal.(string); ok {
				if f.Type == reflect.TypeOf(config.SecureString{}) {
					sf.Set(reflect.ValueOf(*config.NewSecureString(s)))
				} else if f.Type == reflect.TypeOf(&config.SecureString{}) {
					sf.Set(reflect.ValueOf(config.NewSecureString(s)))
				}
				continue
			}
			// Recurse into nested struct
			if sf.Kind() == reflect.Struct {
				if nested, ok := rawVal.(map[string]any); ok {
					applySecureStringsToStruct(sf, nested)
				}
				continue
			}
			// Recurse into map fields (e.g., map[string]SomeStruct)
			if sf.Kind() == reflect.Map && sf.Type().Elem().Kind() == reflect.Struct {
				if nestedMap, ok := rawVal.(map[string]any); ok {
					for mapKey, mapVal := range nestedMap {
						nested, ok := mapVal.(map[string]any)
						if !ok {
							continue
						}
						elemType := sf.Type().Elem()
						// Get existing element or create a new zero value
						var elem reflect.Value
						existing := sf.MapIndex(reflect.ValueOf(mapKey))
						if existing.IsValid() {
							if existing.Kind() == reflect.Interface {
								existing = existing.Elem()
							}
							if existing.Kind() == reflect.Ptr && !existing.IsNil() {
								elem = reflect.New(elemType)
								elem.Elem().Set(existing.Elem())
							} else if existing.Kind() == reflect.Struct {
								elem = reflect.New(elemType)
								elem.Elem().Set(existing)
							}
						}
						if !elem.IsValid() {
							elem = reflect.New(elemType)
						}
						applySecureStringsToStruct(elem.Elem(), nested)
						sf.SetMapIndex(reflect.ValueOf(mapKey), elem.Elem())
					}
				}
				continue
			}
			// Recurse into slice elements that are structs
			if sf.Kind() == reflect.Slice && sf.Type().Elem().Kind() == reflect.Struct {
				if sliceRaw, ok := rawVal.([]any); ok {
					for idx, elemRaw := range sliceRaw {
						if nested, ok := elemRaw.(map[string]any); ok {
							if idx < sf.Len() {
								applySecureStringsToStruct(sf.Index(idx), nested)
							}
						}
					}
				}
			}
		}
	}
}

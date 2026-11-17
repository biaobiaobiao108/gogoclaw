package api

import (
	"fmt"
	"strings"

	"github.com/biaobiaobiao108/gogoclaw/pkg/audio/asr"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

func normalizeStoredModelConfig(mc *config.ModelConfig, defaultProvider string) bool {
	if mc == nil {
		return false
	}

	changed := false
	modelName := strings.TrimSpace(mc.ModelName)
	if modelName != mc.ModelName {
		mc.ModelName = modelName
		changed = true
	}
	model := strings.TrimSpace(mc.Model)
	if model != mc.Model {
		mc.Model = model
		changed = true
	}
	provider := strings.TrimSpace(mc.Provider)
	if provider != mc.Provider {
		mc.Provider = provider
		changed = true
	}
	authMethod := strings.ToLower(strings.TrimSpace(mc.AuthMethod))
	if authMethod != mc.AuthMethod {
		mc.AuthMethod = authMethod
		changed = true
	}

	if provider != "" {
		normalizedProvider := providers.NormalizeProvider(provider)
		if providers.IsSupportedModelProvider(normalizedProvider) && normalizedProvider != provider {
			mc.Provider = normalizedProvider
			changed = true
		}
		if mc.Provider == "elevenlabs" {
			if _, strippedModel, found := strings.Cut(
				model,
				"/",
			); found &&
				providers.NormalizeProvider(strings.TrimSpace(provider)) == "elevenlabs" {
				strippedModel = strings.TrimSpace(strippedModel)
				if strippedModel != "" && strippedModel != mc.Model {
					mc.Model = strippedModel
					changed = true
				}
			}
			if strings.TrimSpace(mc.Model) != asr.ElevenLabsSupportedModelID() {
				mc.Model = asr.ElevenLabsSupportedModelID()
				changed = true
			}
		}
		return changed
	}

	effectiveProvider, modelID := providers.SplitModelProviderAndID(model, defaultProvider)
	if effectiveProvider == "" {
		return changed
	}
	if mc.Provider != effectiveProvider {
		mc.Provider = effectiveProvider
		changed = true
	}
	if mc.Model != modelID {
		mc.Model = modelID
		changed = true
	}
	return changed
}

func normalizeIncomingModelConfig(mc *config.ModelConfig, defaultProvider string) {
	if mc == nil {
		return
	}

	mc.ModelName = strings.TrimSpace(mc.ModelName)
	mc.Model = strings.TrimSpace(mc.Model)
	mc.Provider = strings.TrimSpace(mc.Provider)
	mc.AuthMethod = strings.ToLower(strings.TrimSpace(mc.AuthMethod))
	if mc.Provider == "" {
		mc.Provider, mc.Model = providers.SplitModelProviderAndID(mc.Model, defaultProvider)
	} else {
		mc.Provider = providers.NormalizeProvider(mc.Provider)
		if mc.Provider == "elevenlabs" {
			if _, strippedModel, found := strings.Cut(mc.Model, "/"); found {
				strippedModel = strings.TrimSpace(strippedModel)
				if strippedModel != "" {
					mc.Model = strippedModel
				}
			}
		}
	}
	if mc.Provider == "antigravity" && mc.AuthMethod == "" {
		mc.AuthMethod = "oauth"
	}
}

func createAllowedForProvider(provider string) bool {
	normalized := providers.NormalizeProvider(provider)
	switch normalized {
	case "bedrock":
		// Bedrock currently authenticates through the AWS SDK credential chain
		// (env vars, shared profiles, IAM roles, etc.), and this Web layer does
		// not yet have a reliable preflight check for those credential sources.
		// Keep it creatable in the catalog and let provider construction/runtime
		// return the concrete AWS error when the environment is incomplete.
		return true
	case "claude-cli", "codex-cli":
		return cliProviderCreateAllowedFromCurrentStatus(normalized)
	default:
		return providers.IsCreatableModelProvider(normalized)
	}
}

// cliProviderCreateAllowedFromCurrentStatus intentionally reuses the existing
// local model status pipeline so provider catalog gating follows the same CLI
// executable probe used by launcher readiness.
func cliProviderCreateAllowedFromCurrentStatus(provider string) bool {
	status := modelConfigurationStatus(&config.ModelConfig{
		Provider: provider,
		Model:    provider,
	})
	return status.Available
}

func modelProviderOptionsForResponse() []providers.ModelProviderOption {
	options := providers.ModelProviderOptions()
	for i := range options {
		options[i].CreateAllowed = createAllowedForProvider(options[i].ID)
	}
	return options
}

func defaultModelAllowedForModelConfig(mc *config.ModelConfig, defaultProvider string) bool {
	provider, _ := modelConfigProviderAndID(mc, defaultProvider)
	return providers.IsDefaultModelProvider(provider)
}

func validateIncomingModelConfig(mc *config.ModelConfig, existing *config.ModelConfig) error {
	if mc == nil {
		return fmt.Errorf("model config is required")
	}
	if err := mc.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(mc.Provider) == "" {
		return fmt.Errorf("provider is required")
	}
	if !providers.IsSupportedModelProvider(mc.Provider) {
		return fmt.Errorf("provider %q is not supported", mc.Provider)
	}
	if mc.Provider == "elevenlabs" && strings.TrimSpace(mc.Model) != asr.ElevenLabsSupportedModelID() {
		return fmt.Errorf("provider %q only supports model %q", mc.Provider, asr.ElevenLabsSupportedModelID())
	}
	if !createAllowedForProvider(mc.Provider) {
		if existing == nil {
			return fmt.Errorf("provider %q is not available for new models", mc.Provider)
		}
		existingProvider, _ := providers.ExtractProtocol(existing)
		if providers.NormalizeProvider(existingProvider) != mc.Provider {
			return fmt.Errorf("provider %q is not available for selection", mc.Provider)
		}
	}
	return nil
}

func normalizeStoredModelProviders(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}

	changed := false
	defaultProvider := defaultChainProvider(cfg)
	for _, model := range cfg.ModelList {
		if normalizeStoredModelConfig(model, defaultProvider) {
			changed = true
		}
	}
	return changed
}

func normalizeStoredModelNames(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	changed := false
	for _, model := range cfg.ModelList {
		if model == nil {
			continue
		}
		modelName := strings.TrimSpace(model.ModelName)
		if modelName != model.ModelName {
			model.ModelName = modelName
			changed = true
		}
	}
	return changed
}

func trimModelNameList(names []string) []string {
	trimmed := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		trimmed = append(trimmed, name)
	}
	return trimmed
}

func dedupeModelNameList(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(names))
	deduped := make([]string, 0, len(names))
	for _, name := range names {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		deduped = append(deduped, name)
	}
	return deduped
}

func modelConfigsByName(cfg *config.Config) map[string][]*config.ModelConfig {
	models := make(map[string][]*config.ModelConfig, len(cfg.ModelList))
	for _, model := range cfg.ModelList {
		if model == nil || !model.IsEnabled() {
			continue
		}
		name := strings.TrimSpace(model.ModelName)
		if name == "" {
			continue
		}
		models[name] = append(models[name], model)
	}
	return models
}

func modelConfigsByNameExceptIndex(
	cfg *config.Config,
	modelName string,
	exceptIndex int,
) []*config.ModelConfig {
	modelName = strings.TrimSpace(modelName)
	if cfg == nil || modelName == "" {
		return nil
	}
	models := make([]*config.ModelConfig, 0, 1)
	for i, model := range cfg.ModelList {
		if i == exceptIndex || model == nil || !model.IsEnabled() {
			continue
		}
		if strings.TrimSpace(model.ModelName) == modelName {
			models = append(models, model)
		}
	}
	return models
}

func validateDefaultChainModelSet(
	modelName string,
	models []*config.ModelConfig,
	defaultContext bool,
	defaultProvider string,
) error {
	if len(models) == 0 {
		if defaultContext {
			return fmt.Errorf("default model %q not found in model_list", modelName)
		}
		return fmt.Errorf("fallback model %q not found in model_list", modelName)
	}
	for _, modelCfg := range models {
		if modelCfg.IsVirtual() {
			if defaultContext {
				return fmt.Errorf("cannot set virtual model %q as default", modelName)
			}
			return fmt.Errorf("cannot add virtual model %q to fallback_chain", modelName)
		}
		if !defaultModelAllowedForModelConfig(modelCfg, defaultProvider) {
			if defaultContext {
				return fmt.Errorf("model %q cannot be used as the default chat model", modelName)
			}
			return fmt.Errorf("model %q cannot be used in the fallback chain", modelName)
		}
		effectiveModelCfg := modelCfg
		if strings.TrimSpace(modelCfg.Provider) == "" {
			clone := *modelCfg
			normalizeStoredModelConfig(&clone, defaultProvider)
			effectiveModelCfg = &clone
		}
		if !hasModelConfiguration(effectiveModelCfg) {
			return rawModelConfigurationError(modelName, defaultContext)
		}
	}
	return nil
}

func validateDefaultChainReference(
	cfg *config.Config,
	modelName string,
	models []*config.ModelConfig,
	defaultContext bool,
) error {
	if len(models) > 0 {
		return validateDefaultChainModelSet(
			modelName,
			models,
			defaultContext,
			defaultChainProvider(cfg),
		)
	}

	resolved, err := providers.ResolveModelConfig(cfg, modelName)
	if err != nil || resolved == nil {
		ref := providers.ParseModelRef(modelName, defaultChainProvider(cfg))
		if ref != nil &&
			strings.TrimSpace(ref.Provider) != "" &&
			strings.TrimSpace(ref.Model) != "" &&
			!providers.IsDefaultModelProvider(ref.Provider) {
			if defaultContext {
				return fmt.Errorf("model %q cannot be used as the default chat model", modelName)
			}
			return fmt.Errorf("model %q cannot be used in the fallback chain", modelName)
		}
		if ref != nil &&
			strings.TrimSpace(ref.Provider) != "" &&
			strings.TrimSpace(ref.Model) != "" &&
			providers.IsDefaultModelProvider(ref.Provider) {
			return rawModelConfigurationError(modelName, defaultContext)
		}
		return validateDefaultChainModelSet(
			modelName,
			nil,
			defaultContext,
			defaultChainProvider(cfg),
		)
	}
	if err := validateDefaultChainModelSet(
		modelName,
		[]*config.ModelConfig{resolved},
		defaultContext,
		defaultChainProvider(cfg),
	); err != nil {
		return err
	}
	if !rawTemplateHasConfiguration(resolved) {
		return rawModelConfigurationError(modelName, defaultContext)
	}
	return nil
}

func rawTemplateHasConfiguration(modelCfg *config.ModelConfig) bool {
	return hasModelConfiguration(modelCfg)
}

func rawModelConfigurationError(modelName string, defaultContext bool) error {
	if defaultContext {
		return fmt.Errorf("default model %q has no configured provider", modelName)
	}
	return fmt.Errorf("fallback model %q has no configured provider", modelName)
}

func defaultChainProvider(cfg *config.Config) string {
	if cfg != nil && strings.TrimSpace(cfg.Agents.Defaults.Provider) != "" {
		return providers.NormalizeProvider(cfg.Agents.Defaults.Provider)
	}
	return "openai"
}

func defaultChainReferenceIdentity(cfg *config.Config, modelName string) string {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return ""
	}

	defaultProvider := defaultChainProvider(cfg)
	var modelList []*config.ModelConfig
	if cfg != nil {
		modelList = cfg.ModelList
	}
	matches := modelConfigsMatchingSignatureRef(modelList, modelName, defaultProvider)
	if len(matches) > 0 {
		modelCfg := matches[0].model
		if alias := strings.TrimSpace(modelCfg.ModelName); alias != "" {
			return "model_name:" + alias
		}
		provider, modelID := providers.ExtractProtocol(modelCfg)
		return providers.ModelKey(provider, modelID)
	}

	ref := providers.ParseModelRef(modelName, defaultProvider)
	if ref == nil {
		return ""
	}
	return providers.ModelKey(ref.Provider, ref.Model)
}

func validateDefaultModelChain(
	cfg *config.Config,
	defaultModel string,
	fallbacks []string,
) ([]string, error) {
	defaultModel = strings.TrimSpace(defaultModel)
	fallbacks = dedupeModelNameList(trimModelNameList(fallbacks))

	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if defaultModel == "" && len(fallbacks) > 0 {
		return nil, fmt.Errorf("default_model is required when fallback_chain is not empty")
	}

	modelsByName := modelConfigsByName(cfg)
	defaultIdentity := ""
	if defaultModel != "" {
		if err := validateDefaultChainReference(
			cfg,
			defaultModel,
			modelsByName[defaultModel],
			true,
		); err != nil {
			return nil, err
		}
		defaultIdentity = defaultChainReferenceIdentity(cfg, defaultModel)
	}

	seenIdentities := make(map[string]struct{}, len(fallbacks))
	validated := make([]string, 0, len(fallbacks))
	for _, fallback := range fallbacks {
		if fallback == defaultModel {
			return nil, fmt.Errorf("default model %q cannot also appear in fallback_chain", defaultModel)
		}
		if err := validateDefaultChainReference(cfg, fallback, modelsByName[fallback], false); err != nil {
			return nil, err
		}
		identity := defaultChainReferenceIdentity(cfg, fallback)
		if identity != "" && identity == defaultIdentity {
			return nil, fmt.Errorf(
				"fallback model %q resolves to the same runtime model as default model %q",
				fallback,
				defaultModel,
			)
		}
		if _, exists := seenIdentities[identity]; identity != "" && exists {
			continue
		}
		if identity != "" {
			seenIdentities[identity] = struct{}{}
		}
		validated = append(validated, fallback)
	}

	return validated, nil
}

func normalizeDefaultChainReferences(cfg *config.Config) {
	if cfg == nil {
		return
	}

	defaultModel := strings.TrimSpace(cfg.Agents.Defaults.ModelName)
	fallbacks := dedupeModelNameList(trimModelNameList(cfg.Agents.Defaults.ModelFallbacks))
	modelsByName := modelConfigsByName(cfg)
	defaultIdentity := ""

	if defaultModel != "" {
		if err := validateDefaultChainReference(
			cfg,
			defaultModel,
			modelsByName[defaultModel],
			true,
		); err != nil {
			defaultModel = ""
		}
		defaultIdentity = defaultChainReferenceIdentity(cfg, defaultModel)
	}
	if defaultModel == "" {
		cfg.Agents.Defaults.ModelName = ""
		cfg.Agents.Defaults.ModelFallbacks = nil
		return
	}

	seenIdentities := make(map[string]struct{}, len(fallbacks))
	validated := make([]string, 0, len(fallbacks))
	for _, fallback := range fallbacks {
		if fallback == defaultModel {
			continue
		}
		if err := validateDefaultChainReference(cfg, fallback, modelsByName[fallback], false); err != nil {
			continue
		}
		identity := defaultChainReferenceIdentity(cfg, fallback)
		if identity != "" && identity == defaultIdentity {
			continue
		}
		if _, exists := seenIdentities[identity]; identity != "" && exists {
			continue
		}
		if identity != "" {
			seenIdentities[identity] = struct{}{}
		}
		validated = append(validated, fallback)
	}

	cfg.Agents.Defaults.ModelName = defaultModel
	cfg.Agents.Defaults.ModelFallbacks = validated
}

// handleListModels returns all model_list entries with masked API keys.
//
//	GET /api/models

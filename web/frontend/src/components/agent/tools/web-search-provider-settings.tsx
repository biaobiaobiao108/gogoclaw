import { IconChevronDown } from "@tabler/icons-react"
import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"

import type { WebSearchProviderConfig } from "@/api/tools"
import { maskedSecretPlaceholder } from "@/components/secret-placeholder"
import { KeyInput } from "@/components/shared-form"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { cn } from "@/lib/utils"

import type { WebSearchDraftUpdater } from "./types"

interface WebSearchProviderSettingsProps {
  providerLabelMap: Map<string, string>
  settings: Record<string, WebSearchProviderConfig>
  expandedProvider: string | null
  onToggleProviderExpand: (providerId: string) => void
  onUpdateDraft: WebSearchDraftUpdater
}

const baseUrlProviders = new Set([
  "tavily",
  "kagi",
  "searxng",
  "glm_search",
  "baidu_search",
])

const apiKeyProviders = new Set([
  "brave",
  "tavily",
  "kagi",
  "perplexity",
  "gemini",
  "glm_search",
  "baidu_search",
])

const modelProviders = new Set(["gemini"])

export function WebSearchProviderSettings({
  providerLabelMap,
  settings,
  expandedProvider,
  onToggleProviderExpand,
  onUpdateDraft,
}: WebSearchProviderSettingsProps) {
  const { t } = useTranslation()

  return (
    <div className="space-y-4">
      <h3 className="text-foreground text-sm font-semibold">
        {t("pages.agent.tools.web_search.providers_config", "Integrations")}
      </h3>

      <div className="bg-card border-border divide-border divide-y overflow-hidden rounded-lg border">
        {Object.entries(settings).map(([providerId, providerSettings]) => (
          <ProviderCard
            key={providerId}
            providerId={providerId}
            providerLabel={providerLabelMap.get(providerId) ?? providerId}
            settings={providerSettings}
            isExpanded={expandedProvider === providerId}
            onToggleExpand={onToggleProviderExpand}
            onUpdateDraft={onUpdateDraft}
          />
        ))}
      </div>
    </div>
  )
}

function ProviderCard({
  providerId,
  providerLabel,
  settings,
  isExpanded,
  onToggleExpand,
  onUpdateDraft,
}: {
  providerId: string
  providerLabel: string
  settings: WebSearchProviderConfig
  isExpanded: boolean
  onToggleExpand: (providerId: string) => void
  onUpdateDraft: WebSearchDraftUpdater
}) {
  const { t } = useTranslation()
  const apiKeyPlaceholder = maskedSecretPlaceholder(
    settings.api_key_set ? `${providerId}-configured` : "",
    t("pages.agent.tools.web_search.api_key_placeholder", "Enter API key..."),
  )

  const updateSettings = (
    updater: (current: WebSearchProviderConfig) => WebSearchProviderConfig,
  ) => {
    onUpdateDraft((current) => {
      const nextSettings = current.settings[providerId] ?? settings
      return {
        ...current,
        settings: {
          ...current.settings,
          [providerId]: updater(nextSettings),
        },
      }
    })
  }

  return (
    <div
      className={cn(
        "group flex flex-col transition-colors",
        isExpanded ? "bg-muted/5" : "hover:bg-muted/20",
      )}
    >
      <div className="flex items-center justify-between gap-4 p-5">
        <button
          type="button"
          className="flex min-w-0 flex-1 cursor-pointer items-center gap-4 text-left select-none"
          aria-expanded={isExpanded}
          aria-controls={`web-search-provider-${providerId}`}
          onClick={() => onToggleExpand(providerId)}
        >
          <div
            className={cn(
              "text-muted-foreground flex items-center justify-center transition-transform duration-[var(--motion-state)]",
              isExpanded && "rotate-180",
            )}
          >
            <IconChevronDown className="size-[18px]" />
          </div>
          <div className="flex items-center gap-3">
            <span className="text-foreground/90 text-[15px] font-semibold tracking-tight">
              {providerLabel}
            </span>
            {settings.enabled ? (
              <span className="bg-success/10 text-success inline-block rounded-sm px-2 py-0.5 text-[11px] font-medium">
                {t("pages.agent.tools.filter.enabled", "Enabled")}
              </span>
            ) : (
              <span className="bg-muted text-muted-foreground inline-block rounded-sm px-2 py-0.5 text-[11px] font-medium">
                {t("pages.agent.tools.filter.disabled", "Disabled")}
              </span>
            )}
          </div>
        </button>

        <div
          className="flex items-center gap-4"
          onClick={(event) => event.stopPropagation()}
        >
          <Switch
            checked={settings.enabled}
            onCheckedChange={(checked) =>
              updateSettings((current) => ({
                ...current,
                enabled: checked,
              }))
            }
            aria-label={t(
              "pages.agent.tools.web_search.enable_provider",
              "Enable {{name}}",
              { name: providerLabel },
            )}
          />
        </div>
      </div>

      {isExpanded && (
        <div
          id={`web-search-provider-${providerId}`}
          className="animate-in fade-in slide-in-from-top-1 border-border/10 border-t px-6 pt-1 pb-6 duration-200"
        >
          <div className="ml-8 flex max-w-xl flex-col gap-5">
            <ProviderField
              label={t(
                "pages.agent.tools.web_search.max_results",
                "Max Results",
              )}
            >
              <Input
                type="number"
                min={1}
                max={10}
                value={settings.max_results || 5}
                onChange={(event) =>
                  updateSettings((current) => ({
                    ...current,
                    max_results: Number(event.target.value) || 0,
                  }))
                }
                className="bg-muted/40 hover:bg-muted/60 focus:bg-background h-10 rounded-md border-transparent"
              />
            </ProviderField>

            {baseUrlProviders.has(providerId) && (
              <ProviderField
                label={t("pages.agent.tools.web_search.base_url", "Base URL")}
              >
                <Input
                  value={settings.base_url ?? ""}
                  onChange={(event) =>
                    updateSettings((current) => ({
                      ...current,
                      base_url: event.target.value,
                    }))
                  }
                  placeholder={t(
                    "pages.agent.tools.web_search.base_url_placeholder",
                    "Optional endpoint override",
                  )}
                  className="bg-muted/40 hover:bg-muted/60 focus:bg-background h-10 rounded-md border-transparent"
                />
              </ProviderField>
            )}

            {apiKeyProviders.has(providerId) && (
              <ProviderField
                label={t(
                  "pages.agent.tools.web_search.api_key",
                  "API Key / Token",
                )}
                className="pt-1"
              >
                <KeyInput
                  value={settings.api_key ?? ""}
                  onChange={(value) =>
                    updateSettings((current) => ({
                      ...current,
                      api_key: value,
                    }))
                  }
                  placeholder={apiKeyPlaceholder}
                  className="bg-muted/40 hover:bg-muted/60 focus:bg-background h-10 rounded-md border-transparent"
                />
              </ProviderField>
            )}

            {modelProviders.has(providerId) && (
              <ProviderField
                label={t("pages.agent.tools.web_search.model", "Model")}
              >
                <Input
                  value={settings.model ?? ""}
                  onChange={(event) =>
                    updateSettings((current) => ({
                      ...current,
                      model: event.target.value,
                    }))
                  }
                  placeholder={t(
                    "pages.agent.tools.web_search.model_placeholder",
                    "Optional model override",
                  )}
                  className="bg-muted/40 hover:bg-muted/60 focus:bg-background h-10 rounded-md border-transparent"
                />
              </ProviderField>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

function ProviderField({
  label,
  className,
  children,
}: {
  label: string
  className?: string
  children: ReactNode
}) {
  return (
    <div className={cn("space-y-1.5", className)}>
      <label className="text-foreground/80 text-[13px] font-semibold">
        {label}
      </label>
      {children}
    </div>
  )
}

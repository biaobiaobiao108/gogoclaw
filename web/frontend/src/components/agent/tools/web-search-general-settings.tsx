import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"

import type { WebSearchConfigResponse } from "@/api/tools"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"

import type { WebSearchDraftUpdater } from "./types"

interface WebSearchGeneralSettingsProps {
  draft: WebSearchConfigResponse
  onUpdateDraft: WebSearchDraftUpdater
}

export function WebSearchGeneralSettings({
  draft,
  onUpdateDraft,
}: WebSearchGeneralSettingsProps) {
  const { t } = useTranslation()

  return (
    <div className="space-y-4">
      <h3 className="text-foreground text-sm font-semibold">
        {t("pages.agent.tools.web_search.global_settings", "General")}
      </h3>

      <div className="bg-card border-border divide-border divide-y overflow-hidden rounded-lg border">
        <SettingRow
          label={t("pages.agent.tools.web_search.provider", "Primary Provider")}
          description={t(
            "pages.agent.tools.web_search.provider_description",
            "Select the default provider to use when the web search tool handles a request.",
          )}
        >
          <Select
            value={draft.provider}
            onValueChange={(value) =>
              onUpdateDraft((current) => ({
                ...current,
                provider: value,
              }))
            }
          >
            <SelectTrigger className="bg-muted/40 hover:bg-muted/60 focus:border-border w-full rounded-md border-transparent sm:w-64">
              <SelectValue />
            </SelectTrigger>
            <SelectContent className="border-border rounded-md">
              {draft.providers.map((provider) => (
                <SelectItem
                  key={provider.id}
                  value={provider.id}
                  className="rounded-sm"
                >
                  {provider.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </SettingRow>

        <SettingRow
          label={t("pages.agent.tools.web_search.proxy", "Proxy Configuration")}
          description={t(
            "pages.agent.tools.web_search.proxy_description",
            "Optional global HTTP/S proxy for underlying web requests.",
          )}
        >
          <Input
            className="bg-muted/40 hover:bg-muted/60 focus-visible:bg-background focus-visible:border-border w-full rounded-md border-transparent sm:w-64"
            value={draft.proxy ?? ""}
            onChange={(event) =>
              onUpdateDraft((current) => ({
                ...current,
                proxy: event.target.value,
              }))
            }
            placeholder="http://127.0.0.1:7890"
          />
        </SettingRow>

        <SettingRow
          label={t(
            "pages.agent.tools.web_search.prefer_native",
            "Prefer Native Search",
          )}
          description={t(
            "pages.agent.tools.web_search.prefer_native_hint",
            "When enabled, the model may use its built-in search capability instead of the configured provider list.",
          )}
        >
          <Switch
            checked={draft.prefer_native}
            onCheckedChange={(checked) =>
              onUpdateDraft((current) => ({
                ...current,
                prefer_native: checked,
              }))
            }
            aria-label={t(
              "pages.agent.tools.web_search.prefer_native",
              "Prefer Native Search",
            )}
            className="data-[state=checked]:shadow-xs"
          />
        </SettingRow>
      </div>
    </div>
  )
}

function SettingRow({
  label,
  description,
  children,
}: {
  label: string
  description: string
  children: ReactNode
}) {
  return (
    <div className="hover:bg-muted/10 flex flex-col justify-between gap-4 p-5 transition-colors sm:flex-row sm:items-center">
      <div className="w-full space-y-1 sm:max-w-md">
        <label className="text-foreground/90 text-[15px] font-semibold tracking-tight">
          {label}
        </label>
        <p className="text-muted-foreground/80 text-[13px] leading-relaxed">
          {description}
        </p>
      </div>
      {children}
    </div>
  )
}

import { IconLoader2, IconSearch } from "@tabler/icons-react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

import type { UnavailableToolMessage } from "./tool-support"

export function SearchPanel({
  marketQuery,
  canSearchMarketplace,
  isMarketSearchInitialLoading,
  unavailableToolMessages,
  onMarketQueryChange,
  onSearchSubmit,
}: {
  marketQuery: string
  canSearchMarketplace: boolean
  isMarketSearchInitialLoading: boolean
  unavailableToolMessages: UnavailableToolMessage[]
  onMarketQueryChange: (value: string) => void
  onSearchSubmit: () => void
}) {
  const { t } = useTranslation()

  return (
    <section className="space-y-4" aria-labelledby="hub-search-title">
      <div className="space-y-1">
        <h2 id="hub-search-title" className="text-sm font-semibold">
          {t("pages.agent.skills.marketplace_title", {
            defaultValue: "Discover Skills",
          })}
        </h2>
        <p className="text-muted-foreground max-w-2xl text-xs leading-5">
          {t("pages.agent.skills.marketplace_description")}
        </p>
      </div>

      <form
        className="flex w-full items-center gap-2"
        onSubmit={(event) => {
          event.preventDefault()
          onSearchSubmit()
        }}
      >
        <div className="relative min-w-0 flex-1">
          <IconSearch
            className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2"
            aria-hidden="true"
          />
          <Input
            id="hub-skill-search"
            value={marketQuery}
            onChange={(event) => onMarketQueryChange(event.target.value)}
            aria-label={t("pages.agent.skills.marketplace_search_placeholder")}
            placeholder={t("pages.agent.skills.marketplace_search_placeholder")}
            className="pl-8"
            disabled={!canSearchMarketplace}
          />
        </div>
        <Button
          type="submit"
          className="min-w-16"
          aria-label={t("pages.agent.skills.marketplace_search_action", {
            defaultValue: "Search",
          })}
          aria-busy={isMarketSearchInitialLoading}
          disabled={
            !canSearchMarketplace ||
            isMarketSearchInitialLoading ||
            marketQuery.trim() === ""
          }
        >
          {isMarketSearchInitialLoading ? (
            <IconLoader2 className="size-4 animate-spin" />
          ) : (
            <span>
              {t("pages.agent.skills.marketplace_search_action", {
                defaultValue: "Search",
              })}
            </span>
          )}
        </Button>
      </form>

      {unavailableToolMessages.length ? (
        <div className="flex w-full flex-col gap-3">
          {unavailableToolMessages.map((item) => (
            <div
              key={item.key}
              className="border-warning/30 bg-warning/10 text-foreground rounded-lg border px-4 py-3 text-left text-sm"
            >
              <div className="font-semibold">{item.label}</div>
              <div className="mt-1 leading-6">{item.message}</div>
            </div>
          ))}
        </div>
      ) : null}
    </section>
  )
}

import { useTranslation } from "react-i18next"

import { PageHeader } from "@/components/page-header"

import { ResultsPanel } from "./results-panel"
import { SearchPanel } from "./search-panel"
import { useHubMarketplace } from "./use-hub-marketplace"

export function HubPage() {
  const { t } = useTranslation()
  const hub = useHubMarketplace()

  return (
    <div className="flex h-full flex-col">
      <PageHeader title={t("navigation.hub")} />

      <div
        className="flex-1 overflow-auto px-4 py-6 sm:px-6"
        onScroll={hub.handleScroll}
      >
        <div className="mx-auto w-full max-w-[960px] space-y-6">
          <SearchPanel
            marketQuery={hub.marketQuery}
            canSearchMarketplace={hub.canSearchMarketplace}
            isMarketSearchInitialLoading={hub.isMarketSearchInitialLoading}
            unavailableToolMessages={hub.unavailableToolMessages}
            onMarketQueryChange={hub.setMarketQuery}
            onSearchSubmit={hub.handleSearchSubmit}
          />

          <ResultsPanel
            canSearchMarketplace={hub.canSearchMarketplace}
            hasSubmittedQuery={hub.hasSubmittedQuery}
            submittedQuery={hub.submittedMarketQuery}
            marketResults={hub.marketResults}
            marketSearchError={hub.marketSearchError}
            isMarketSearchInitialLoading={hub.isMarketSearchInitialLoading}
            isMarketSearchLoadingMore={hub.isMarketSearchLoadingMore}
            canInstallFromMarketplace={hub.canInstallFromMarketplace}
            getInstalledSkill={hub.getInstalledSkill}
            isInstallPending={hub.isInstallPending}
            onInstall={hub.handleInstall}
            onViewInstalled={hub.handleViewInstalled}
          />
        </div>
      </div>
    </div>
  )
}

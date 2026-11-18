import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { createRouter, RouterProvider } from "@tanstack/react-router"
import { StrictMode } from "react"
import ReactDOM from "react-dom/client"

import { AppProviders } from "./app-providers"
import "./i18n"
import "../.cache/tailwind.css"
import { installInteractionFeedback } from "./lib/interaction-feedback"
import { routeTree } from "./routeTree.gen"

const queryClient = new QueryClient()
installInteractionFeedback()

if (process.env.NODE_ENV !== "production") {
  const reloadSource = new EventSource("/__gogoclaw_reload")
  reloadSource.addEventListener("reload", () => globalThis.location.reload())
}

const router = createRouter({
  routeTree,
  context: {
    queryClient,
  },
})

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router
  }
}

const rootElement = document.getElementById("root")!
if (!rootElement.innerHTML) {
  const root = ReactDOM.createRoot(rootElement)
  root.render(
    <StrictMode>
      <AppProviders>
        <QueryClientProvider client={queryClient}>
          <RouterProvider router={router} />
        </QueryClientProvider>
      </AppProviders>
    </StrictMode>,
  )
}

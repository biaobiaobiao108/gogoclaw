import { createRootRoute, Outlet, useRouterState } from "@tanstack/react-router"
import { lazy, Suspense, useEffect, useState } from "react"
import { useTranslation } from "react-i18next"

import { getLauncherAuthStatus } from "@/api/launcher-auth"
import { isLauncherAuthPathname } from "@/lib/launcher-login-path"

const AuthenticatedLayout = lazy(
  () => import("@/components/authenticated-layout"),
)

const RootLayout = () => {
  const { t } = useTranslation()
  // Prefer the real address bar path: stale embedded bundles may not register
  // /launcher-login or /launcher-setup in the route tree, which would otherwise
  // keep AppLayout + gateway polling → 401 → launcherFetch redirect loop.
  const routerState = useRouterState({
    select: (s) => ({
      pathname: s.location.pathname,
      matches: s.matches,
    }),
  })

  const windowPath =
    typeof globalThis.location !== "undefined"
      ? globalThis.location.pathname || "/"
      : routerState.pathname

  const isAuthPage =
    isLauncherAuthPathname(windowPath) ||
    isLauncherAuthPathname(routerState.pathname) ||
    routerState.matches.some(
      (m) => m.routeId === "/launcher-login" || m.routeId === "/launcher-setup",
    )

  const [authError, setAuthError] = useState<string | null>(null)
  const [authChecked, setAuthChecked] = useState(false)

  // Session guard: proactively check auth status on every page load.
  useEffect(() => {
    if (isAuthPage) return
    let cancelled = false
    void getLauncherAuthStatus()
      .then((s) => {
        if (cancelled) return
        if (!s.initialized) {
          globalThis.location.assign("/launcher-setup")
        } else if (!s.authenticated) {
          globalThis.location.assign("/launcher-login")
        } else {
          setAuthChecked(true)
        }
      })
      .catch((err: unknown) => {
        if (cancelled) return
        // On 401/403, redirect to login — the session is invalid.
        // On 5xx (e.g. 503 when the auth store is unavailable) or network errors,
        // do NOT redirect: a subsequent successful login would loop straight back here.
        // launcherFetch handles 401 on real API calls regardless.
        if (err instanceof Error && /^status 40[13]$/.test(err.message)) {
          globalThis.location.assign("/launcher-login")
        } else {
          setAuthError(err instanceof Error ? err.message : "")
          setAuthChecked(true)
        }
      })
    return () => {
      cancelled = true
    }
  }, [isAuthPage])

  if (isAuthPage) {
    return <Outlet />
  }

  return (
    <>
      {authError !== null && (
        <div className="bg-destructive text-destructive-foreground fixed inset-x-0 top-0 z-[100] flex items-center justify-between px-4 py-2 text-sm shadow-md">
          <span>
            {t("auth.serviceError")}:{" "}
            {authError || t("auth.serviceUnavailable")}
          </span>
          <button
            className="ml-4 opacity-70 hover:opacity-100"
            onClick={() => setAuthError(null)}
            aria-label={t("common.dismiss")}
          >
            ✕
          </button>
        </div>
      )}
      {authChecked && (
        <Suspense>
          <AuthenticatedLayout>
            <Outlet />
          </AuthenticatedLayout>
        </Suspense>
      )}
    </>
  )
}

export const Route = createRootRoute({ component: RootLayout })

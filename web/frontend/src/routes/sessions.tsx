import { createFileRoute } from "@tanstack/react-router"

import { SessionsPage } from "@/components/session-management/sessions-page"
export const Route = createFileRoute("/sessions")({ component: SessionsPage })

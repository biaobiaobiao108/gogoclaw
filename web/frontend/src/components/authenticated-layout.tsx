import { type PropsWithChildren, useEffect } from "react"

import { AppLayout } from "@/components/app-layout"
import { initializeChatStore } from "@/features/chat/controller"

export default function AuthenticatedLayout({ children }: PropsWithChildren) {
  useEffect(() => {
    initializeChatStore()
  }, [])

  return <AppLayout>{children}</AppLayout>
}

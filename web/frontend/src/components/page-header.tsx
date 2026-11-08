import { IconMenu2 } from "@tabler/icons-react"
import type { ReactNode } from "react"

import { SidebarTrigger } from "@/components/ui/sidebar"
import { cn } from "@/lib/utils"

interface PageHeaderProps {
  title: string
  titleExtra?: ReactNode
  children?: ReactNode
  className?: string
}

export function PageHeader({
  title,
  titleExtra,
  children,
  className,
}: PageHeaderProps) {
  return (
    <div
      className={cn(
        "border-border/50 bg-background/80 z-30 flex h-14 shrink-0 items-center justify-between border-b px-6 backdrop-blur-xs",
        className,
      )}
    >
      <div className="flex items-center gap-3">
        <SidebarTrigger className="border-border/60 bg-card text-muted-foreground hover:bg-muted hover:text-foreground hidden h-8 w-8 rounded-md border sm:flex [&>svg]:size-4">
          <IconMenu2 />
        </SidebarTrigger>
        <h1 className="text-foreground text-base font-semibold tracking-tight">
          {title}
        </h1>
        {titleExtra}
      </div>
      {children && <div className="flex items-center gap-2">{children}</div>}
    </div>
  )
}

import type { ReactNode } from "react"

import { cn } from "@/lib/utils"

interface PageHeaderProps {
  leading?: ReactNode
  title: string
  titleExtra?: ReactNode
  children?: ReactNode
  className?: string
}

export function PageHeader({
  leading,
  title,
  titleExtra,
  children,
  className,
}: PageHeaderProps) {
  return (
    <div
      className={cn(
        "border-border/30 bg-background/50 z-30 flex h-12 shrink-0 items-center justify-between border-b px-4 backdrop-blur-xs",
        className,
      )}
    >
      <div className="flex min-w-0 items-center gap-3">
        {leading}
        <h1 className="text-foreground truncate text-sm font-semibold tracking-tight whitespace-nowrap">
          {title}
        </h1>
        {titleExtra}
      </div>
      {children && (
        <div className="flex shrink-0 items-center gap-2">{children}</div>
      )}
    </div>
  )
}

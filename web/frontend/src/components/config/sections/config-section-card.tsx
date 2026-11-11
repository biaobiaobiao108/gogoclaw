import type { ReactNode } from "react"

import type {
  CoreConfigForm,
  LauncherForm,
} from "@/components/config/form-model"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"

export type UpdateCoreField = <K extends keyof CoreConfigForm>(
  key: K,
  value: CoreConfigForm[K],
) => void

export type UpdateLauncherField = <K extends keyof LauncherForm>(
  key: K,
  value: LauncherForm[K],
) => void

interface ConfigSectionCardProps {
  title: string
  description?: string
  children: ReactNode
}

export function ConfigSectionCard({
  title,
  description,
  children,
}: ConfigSectionCardProps) {
  return (
    <Card size="sm">
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        {description && (
          <CardDescription className="text-xs leading-5">
            {description}
          </CardDescription>
        )}
      </CardHeader>
      <CardContent className="pt-0">
        <div className="space-y-2">{children}</div>
      </CardContent>
    </Card>
  )
}

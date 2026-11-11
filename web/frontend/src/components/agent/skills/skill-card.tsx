import { IconFileInfo, IconTrash } from "@tabler/icons-react"
import { useTranslation } from "react-i18next"

import type { SkillSupportItem } from "@/api/skills"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"

interface SkillCardProps {
  skill: SkillSupportItem
  onView: () => void
  onDelete: () => void
}

export function SkillCard({ skill, onView, onDelete }: SkillCardProps) {
  const { t } = useTranslation()

  return (
    <Card
      className="group border-border bg-card hover:bg-muted/15 hover:border-border/80 relative overflow-hidden rounded-lg border transition-colors"
      size="sm"
    >
      <CardHeader className="pb-3">
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0 flex-1 space-y-2">
            <div className="flex flex-wrap items-center gap-2">
              <CardTitle className="text-base font-semibold tracking-tight">
                {skill.name}
              </CardTitle>
              {skill.registry_name ? (
                <span className="bg-muted text-muted-foreground inline-flex items-center rounded-sm px-2 py-0.5 text-[11px] font-medium">
                  {skill.registry_name}
                </span>
              ) : null}
            </div>
            <CardDescription className="line-clamp-2 text-sm leading-relaxed">
              {skill.description || t("pages.agent.skills.no_description")}
            </CardDescription>
          </div>
          <div className="flex items-center gap-1 opacity-80 transition-opacity group-hover:opacity-100">
            <Button
              variant="ghost"
              size="icon-sm"
              className="text-muted-foreground hover:bg-muted hover:text-foreground"
              onClick={onView}
              aria-label={t("pages.agent.skills.view")}
            >
              <IconFileInfo className="size-4" />
            </Button>
            {skill.source === "workspace" ? (
              <Button
                variant="ghost"
                size="icon-sm"
                className="text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
                onClick={onDelete}
                aria-label={t("pages.agent.skills.delete")}
              >
                <IconTrash className="size-4" />
              </Button>
            ) : null}
          </div>
        </div>
      </CardHeader>
      <CardContent>
        {skill.registry_url ? (
          <a
            href={skill.registry_url}
            target="_blank"
            rel="noreferrer"
            className="text-primary/80 hover:text-primary inline-flex items-center text-xs transition-colors hover:underline hover:underline-offset-4"
          >
            {skill.registry_url}
          </a>
        ) : null}
      </CardContent>
    </Card>
  )
}

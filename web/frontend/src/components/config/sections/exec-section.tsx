import { useState } from "react"
import { useTranslation } from "react-i18next"

import type { CoreConfigForm } from "@/components/config/form-model"
import { Field, SwitchCardField } from "@/components/shared-form"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"

import { ConfigSectionCard, type UpdateCoreField } from "./config-section-card"

interface ExecSectionProps {
  form: CoreConfigForm
  onFieldChange: UpdateCoreField
}

export function ExecSection({ form, onFieldChange }: ExecSectionProps) {
  const { t } = useTranslation()
  const [testCommand, setTestCommand] = useState("")
  const [testResult, setTestResult] = useState<{
    allowed: boolean
    blocked: boolean
    matchedWhitelist: string | null
    matchedBlacklist: string | null
  } | null>(null)
  const [isLoading, setIsLoading] = useState(false)

  const testPatterns = async () => {
    if (!testCommand.trim()) {
      setTestResult(null)
      return
    }

    const allowPatterns = form.customAllowPatternsText
      .split("\n")
      .map((p) => p.trim())
      .filter((p) => p.length > 0)
    const denyPatterns = form.enableDenyPatterns
      ? form.customDenyPatternsText
          .split("\n")
          .map((p) => p.trim())
          .filter((p) => p.length > 0)
      : []

    setIsLoading(true)
    try {
      const res = await fetch("/api/config/test-command-patterns", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          allow_patterns: allowPatterns,
          deny_patterns: denyPatterns,
          command: testCommand,
        }),
      })
      const data = await res.json()
      setTestResult({
        allowed: data.allowed,
        blocked: data.blocked,
        matchedWhitelist: data.matched_whitelist ?? null,
        matchedBlacklist: data.matched_blacklist ?? null,
      })
    } catch {
      setTestResult(null)
    } finally {
      setIsLoading(false)
    }
  }

  return (
    <ConfigSectionCard title={t("pages.config.sections.exec")}>
      <SwitchCardField
        label={t("pages.config.exec_enabled")}
        hint={t("pages.config.exec_enabled_hint")}
        layout="setting-row"
        checked={form.execEnabled}
        onCheckedChange={(checked) => onFieldChange("execEnabled", checked)}
      />

      {form.execEnabled && (
        <>
          <SwitchCardField
            label={t("pages.config.allow_remote")}
            hint={t("pages.config.allow_remote_hint")}
            layout="setting-row"
            checked={form.allowRemote}
            onCheckedChange={(checked) => onFieldChange("allowRemote", checked)}
          />

          <SwitchCardField
            label={t("pages.config.enable_deny_patterns")}
            hint={t("pages.config.enable_deny_patterns_hint")}
            layout="setting-row"
            checked={form.enableDenyPatterns}
            onCheckedChange={(checked) =>
              onFieldChange("enableDenyPatterns", checked)
            }
          />

          {form.enableDenyPatterns && (
            <Field
              label={t("pages.config.custom_deny_patterns")}
              hint={t("pages.config.custom_deny_patterns_hint")}
              layout="setting-row"
              controlClassName="md:max-w-md"
            >
              <Textarea
                value={form.customDenyPatternsText}
                placeholder={t("pages.config.custom_patterns_placeholder")}
                className="min-h-[88px]"
                onChange={(e) =>
                  onFieldChange("customDenyPatternsText", e.target.value)
                }
              />
            </Field>
          )}

          <Field
            label={t("pages.config.custom_allow_patterns")}
            hint={t("pages.config.custom_allow_patterns_hint")}
            layout="setting-row"
            controlClassName="md:max-w-md"
          >
            <Textarea
              value={form.customAllowPatternsText}
              placeholder={t("pages.config.custom_patterns_placeholder")}
              className="min-h-[88px]"
              onChange={(e) =>
                onFieldChange("customAllowPatternsText", e.target.value)
              }
            />
          </Field>

          <Field
            label={t("pages.config.pattern_detector_title")}
            hint={t("pages.config.pattern_detector_hint")}
            layout="setting-row"
            controlClassName="md:max-w-md"
          >
            <div className="flex w-full flex-col gap-2">
              <div className="flex gap-2">
                <Input
                  value={testCommand}
                  aria-label={t(
                    "pages.config.pattern_detector_input_placeholder",
                  )}
                  placeholder={t(
                    "pages.config.pattern_detector_input_placeholder",
                  )}
                  onChange={(e) => setTestCommand(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      testPatterns()
                    }
                  }}
                />
                <Button onClick={testPatterns} disabled={isLoading}>
                  {t("pages.config.pattern_detector_test_button")}
                </Button>
              </div>
              {testResult && (
                <div
                  className={`rounded-md p-2 text-sm ${
                    testResult.blocked
                      ? "bg-destructive/10 text-destructive"
                      : testResult.allowed
                        ? "bg-success/10 text-success"
                        : "bg-muted text-muted-foreground"
                  }`}
                >
                  {testResult.blocked
                    ? `${t("pages.config.pattern_detector_result_blocked")}${testResult.matchedBlacklist ? ` (${testResult.matchedBlacklist})` : ""}`
                    : testResult.allowed
                      ? `${t("pages.config.pattern_detector_result_allowed")}${testResult.matchedWhitelist ? ` (${testResult.matchedWhitelist})` : ""}`
                      : t("pages.config.pattern_detector_result_no_match")}
                </div>
              )}
            </div>
          </Field>

          <Field
            label={t("pages.config.exec_timeout_seconds")}
            hint={t("pages.config.exec_timeout_seconds_hint")}
            layout="setting-row"
          >
            <Input
              type="number"
              min={0}
              value={form.execTimeoutSeconds}
              onChange={(e) =>
                onFieldChange("execTimeoutSeconds", e.target.value)
              }
            />
          </Field>
        </>
      )}
    </ConfigSectionCard>
  )
}

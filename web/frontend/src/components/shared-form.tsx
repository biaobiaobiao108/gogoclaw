import { IconChevronDown, IconEye, IconEyeOff } from "@tabler/icons-react"
import {
  cloneElement,
  isValidElement,
  type ReactElement,
  type ReactNode,
  useId,
  useState,
} from "react"
import { useTranslation } from "react-i18next"

import {
  Field as UiField,
  FieldDescription,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { cn } from "@/lib/utils"

type FieldLayout = "default" | "setting-row"

interface FieldProps {
  label: string
  hint?: string
  error?: string
  required?: boolean
  children: ReactNode
  layout?: FieldLayout
  controlClassName?: string
}

export function Field({
  label,
  hint,
  error,
  required,
  children,
  layout = "default",
  controlClassName,
}: FieldProps) {
  const generatedId = useId()
  const labelId = `${generatedId}-label`
  const hintId = hint ? `${generatedId}-hint` : undefined
  const errorId = error ? `${generatedId}-error` : undefined

  const describedBy = [hintId, errorId].filter(Boolean).join(" ") || undefined
  const control = isValidElement(children)
    ? cloneElement(children as ReactElement<Record<string, unknown>>, {
        id:
          (children.props as Record<string, unknown>).id ??
          `${generatedId}-control`,
        "aria-labelledby":
          (children.props as Record<string, unknown>)["aria-labelledby"] ??
          labelId,
        "aria-describedby":
          [
            (children.props as Record<string, unknown>)["aria-describedby"],
            describedBy,
          ]
            .filter(Boolean)
            .join(" ") || undefined,
        "aria-required": required || undefined,
        "aria-invalid": error ? true : undefined,
      })
    : children

  if (layout === "setting-row") {
    return (
      <div className="grid grid-cols-1 gap-3 py-3 md:grid-cols-[minmax(0,220px)_minmax(0,1fr)] md:items-start md:gap-x-6">
        <div className="w-full min-w-0">
          <FieldLabel
            id={labelId}
            htmlFor={`${generatedId}-control`}
            className="leading-5 break-words whitespace-normal"
          >
            {label}
            {required && <span className="text-destructive ml-1">*</span>}
          </FieldLabel>
          {hint && (
            <FieldDescription
              id={hintId}
              className="mt-1 text-xs leading-relaxed break-words whitespace-normal"
            >
              {hint}
            </FieldDescription>
          )}
        </div>
        <div
          className={cn("w-full min-w-0 md:max-w-[28rem]", controlClassName)}
        >
          {control}
        </div>
        {error && (
          <FieldDescription
            id={errorId}
            className="text-destructive text-xs leading-normal md:col-start-2 md:justify-self-start"
          >
            {error}
          </FieldDescription>
        )}
      </div>
    )
  }

  return (
    <UiField className="gap-2.5" aria-labelledby={labelId}>
      <div className="space-y-1">
        <FieldLabel id={labelId} htmlFor={`${generatedId}-control`}>
          {label}
          {required && <span className="text-destructive ml-1">*</span>}
        </FieldLabel>
        {hint && (
          <FieldDescription id={hintId} className="text-xs leading-normal">
            {hint}
          </FieldDescription>
        )}
      </div>
      {control}
      {error && (
        <FieldDescription
          id={errorId}
          className="text-destructive text-xs leading-normal"
        >
          {error}
        </FieldDescription>
      )}
    </UiField>
  )
}

interface KeyInputProps {
  value: string
  onChange: (v: string) => void
  placeholder?: string
  className?: string
  id?: string
  "aria-labelledby"?: string
  "aria-describedby"?: string
  "aria-invalid"?: boolean
  "aria-required"?: boolean
}

export function KeyInput({
  value,
  onChange,
  placeholder,
  className,
  id,
  "aria-labelledby": ariaLabelledBy,
  "aria-describedby": ariaDescribedBy,
  "aria-invalid": ariaInvalid,
  "aria-required": ariaRequired,
}: KeyInputProps) {
  const [show, setShow] = useState(false)
  const { t } = useTranslation()

  return (
    <div className="relative w-full">
      <Input
        id={id}
        type={show ? "text" : "password"}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        aria-labelledby={ariaLabelledBy}
        aria-describedby={ariaDescribedBy}
        aria-invalid={ariaInvalid}
        aria-required={ariaRequired}
        className={cn("pr-10", className)}
      />
      <button
        type="button"
        onClick={() => setShow((v) => !v)}
        aria-label={t(
          show ? "common.hidePassword" : "common.showPassword",
          show ? "Hide password" : "Show password",
        )}
        aria-pressed={show}
        className="text-muted-foreground hover:bg-muted/70 hover:text-foreground absolute top-1/2 right-1 flex size-8 -translate-y-1/2 items-center justify-center rounded-md transition-colors"
      >
        {show ? (
          <IconEyeOff className="size-4" />
        ) : (
          <IconEye className="size-4" />
        )}
      </button>
    </div>
  )
}

interface SwitchCardFieldProps {
  label: string
  hint?: string
  error?: string
  checked: boolean
  onCheckedChange: (checked: boolean) => void
  ariaLabel?: string
  disabled?: boolean
  children?: ReactNode
  layout?: FieldLayout
  transparent?: boolean
}

export function SwitchCardField({
  label,
  hint,
  error,
  checked,
  onCheckedChange,
  ariaLabel,
  disabled,
  children,
  layout = "default",
  transparent,
}: SwitchCardFieldProps) {
  if (layout === "setting-row") {
    return (
      <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-3 py-3 md:grid-cols-[minmax(0,220px)_minmax(0,1fr)] md:items-start md:gap-x-6">
        <div className="w-full min-w-0">
          <p className="text-sm leading-relaxed font-medium break-words whitespace-normal">
            {label}
          </p>
          {hint && (
            <p className="text-muted-foreground mt-1 text-xs leading-relaxed break-words whitespace-normal">
              {hint}
            </p>
          )}
        </div>
        <div className="flex items-center md:min-h-9">
          <Switch
            checked={checked}
            onCheckedChange={onCheckedChange}
            disabled={disabled}
            aria-label={ariaLabel ?? label}
          />
        </div>
        {children && (
          <div className="col-span-2 mt-1 flex w-full md:col-span-1 md:col-start-2">
            <div className="w-full min-w-0 md:max-w-[28rem]">{children}</div>
          </div>
        )}
        {error && (
          <p className="text-destructive col-span-2 text-xs leading-normal md:col-span-1 md:col-start-2 md:justify-self-start">
            {error}
          </p>
        )}
      </div>
    )
  }

  return (
    <div
      className={cn(
        transparent ? "py-1" : "border-border/60 rounded-lg border px-4 py-3",
      )}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-sm font-medium">{label}</p>
          {hint && (
            <p className="text-muted-foreground mt-0.5 text-xs leading-normal">
              {hint}
            </p>
          )}
        </div>
        <Switch
          checked={checked}
          onCheckedChange={onCheckedChange}
          disabled={disabled}
          aria-label={ariaLabel ?? label}
        />
      </div>
      {children && <div className="mt-4">{children}</div>}
      {error && (
        <p className="text-destructive mt-2 text-xs leading-normal">{error}</p>
      )}
    </div>
  )
}

interface AdvancedSectionProps {
  children: ReactNode
}

export function AdvancedSection({ children }: AdvancedSectionProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)

  return (
    <div className="border-border/50 rounded-lg border">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="hover:bg-muted/40 flex w-full items-center justify-between rounded-lg px-4 py-3 transition-colors"
      >
        <span className="text-muted-foreground text-sm">
          {t("models.advanced.toggle")}
        </span>
        <IconChevronDown
          className={[
            "text-muted-foreground size-4 transition-transform duration-200",
            open ? "rotate-180" : "",
          ].join(" ")}
        />
      </button>
      {open && (
        <div className="border-border/30 space-y-5 border-t px-4 pt-4 pb-4">
          {children}
        </div>
      )}
    </div>
  )
}

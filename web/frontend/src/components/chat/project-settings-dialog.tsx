import {
  IconAlertTriangle,
  IconCheck,
  IconLoader2,
  IconRefresh,
  IconTrash,
  IconUpload,
} from "@tabler/icons-react"
import { useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import {
  type ChatProject,
  type ChatProjectFile,
  deleteProjectFile,
  getProject,
  projectFileContentUrl,
  reindexProjectFile,
  uploadProjectFile,
} from "@/api/projects"
import { getUploadConfig } from "@/api/sessions"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { cn } from "@/lib/utils"

const ACCEPTED_FILES =
  ".pdf,.docx,.xlsx,.pptx,.md,.markdown,.txt,.csv,.json,.go,.js,.jsx,.ts,.tsx,.py,.rs,.java,.kt,.kts,.c,.h,.cc,.cpp,.hpp,.cs,.rb,.php,.swift,.scala,.sh,.ps1,.sql,.html,.htm,.css,.scss,.yaml,.yml,.toml,.xml,.ini,.conf,.log"
const ACCEPTED_EXTENSIONS = new Set(ACCEPTED_FILES.split(","))

interface PendingUpload {
  key: string
  file: File
  status: "uploading" | "failed"
  error?: string
}

interface ProjectSettingsDialogProps {
  open: boolean
  project: ChatProject | null
  onOpenChange: (open: boolean) => void
  onCreate: (name: string, instructions: string) => Promise<ChatProject>
  onUpdate: (
    id: string,
    input: { name: string; instructions: string },
  ) => Promise<ChatProject>
  onDelete: (id: string) => Promise<void>
}

function isAcceptedFile(file: File) {
  const extension = file.name.includes(".")
    ? file.name.slice(file.name.lastIndexOf(".")).toLowerCase()
    : ""
  return ACCEPTED_EXTENSIONS.has(extension)
}

function formatBytes(value: number) {
  if (value >= 1024 * 1024) return `${(value / (1024 * 1024)).toFixed(1)} MB`
  if (value >= 1024) return `${Math.round(value / 1024)} KB`
  return `${value} B`
}

export function ProjectSettingsDialog({
  open,
  project,
  onOpenChange,
  onCreate,
  onUpdate,
  onDelete,
}: ProjectSettingsDialogProps) {
  const { t } = useTranslation()
  const isCreating = project === null
  const fileInputRef = useRef<HTMLInputElement>(null)
  const dragDepth = useRef(0)
  const currentProjectIdRef = useRef<string | null>(null)
  const [name, setName] = useState("")
  const [instructions, setInstructions] = useState("")
  const [files, setFiles] = useState<ChatProjectFile[]>([])
  const [pendingUploads, setPendingUploads] = useState<PendingUpload[]>([])
  const [loading, setLoading] = useState(false)
  const [loadFailed, setLoadFailed] = useState(false)
  const [reloadVersion, setReloadVersion] = useState(0)
  const [saving, setSaving] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [error, setError] = useState("")
  const [uploadError, setUploadError] = useState("")
  const [dragActive, setDragActive] = useState(false)
  const [maxFileSize, setMaxFileSize] = useState(20 * 1024 * 1024)

  useEffect(() => {
    if (!open) {
      currentProjectIdRef.current = null
      return
    }
    currentProjectIdRef.current = project?.id ?? null
    setError("")
    setLoadFailed(false)
    setUploadError("")
    setPendingUploads([])
    setDragActive(false)
    setName(project?.name ?? "")
    setInstructions(project?.instructions ?? "")
    setFiles([])
    if (isCreating) {
      setLoading(false)
      return
    }
    let cancelled = false
    setLoading(true)
    void Promise.all([getProject(project.id), getUploadConfig()])
      .then(([detail, config]) => {
        if (cancelled) return
        setName(detail.name)
        setInstructions(detail.instructions)
        setFiles(detail.files)
        setMaxFileSize(config.max_media_size || 20 * 1024 * 1024)
      })
      .catch((loadError) => {
        if (!cancelled) {
          setLoadFailed(true)
          setError(
            loadError instanceof Error ? loadError.message : String(loadError),
          )
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [
    isCreating,
    open,
    project?.id,
    project?.name,
    project?.instructions,
    reloadVersion,
  ])

  useEffect(() => {
    if (!open) return
    const blockWindowDrop = (event: DragEvent) => {
      if (event.dataTransfer?.types.includes("Files")) event.preventDefault()
    }
    window.addEventListener("dragover", blockWindowDrop)
    window.addEventListener("drop", blockWindowDrop)
    return () => {
      window.removeEventListener("dragover", blockWindowDrop)
      window.removeEventListener("drop", blockWindowDrop)
    }
  }, [open])

  const save = async () => {
    const trimmedName = name.trim()
    if (!trimmedName || saving) return
    setSaving(true)
    setError("")
    try {
      if (isCreating) {
        await onCreate(trimmedName, instructions)
      } else {
        await onUpdate(project.id, { name: trimmedName, instructions })
      }
      onOpenChange(false)
    } catch (saveError) {
      setError(
        saveError instanceof Error ? saveError.message : String(saveError),
      )
    } finally {
      setSaving(false)
    }
  }

  const uploadOne = async (pending: PendingUpload) => {
    if (!project) return
    const projectID = project.id
    setPendingUploads((previous) =>
      previous.map((item) =>
        item.key === pending.key
          ? { ...item, status: "uploading", error: undefined }
          : item,
      ),
    )
    try {
      const uploaded = await uploadProjectFile(project.id, pending.file)
      if (currentProjectIdRef.current !== projectID) return
      setFiles((previous) => [
        ...previous.filter((item) => item.id !== uploaded.id),
        uploaded,
      ])
      setPendingUploads((previous) =>
        previous.filter((item) => item.key !== pending.key),
      )
    } catch (uploadFailure) {
      if (currentProjectIdRef.current !== projectID) return
      setPendingUploads((previous) =>
        previous.map((item) =>
          item.key === pending.key
            ? {
                ...item,
                status: "failed",
                error:
                  uploadFailure instanceof Error
                    ? uploadFailure.message
                    : String(uploadFailure),
              }
            : item,
        ),
      )
    }
  }

  const addFiles = (incoming: FileList | File[]) => {
    if (!project) return
    const accepted: File[] = []
    const rejected: string[] = []
    for (const file of Array.from(incoming)) {
      if (!isAcceptedFile(file)) {
        rejected.push(t("chat.projectFileUnsupported", { name: file.name }))
      } else if (file.size > maxFileSize) {
        rejected.push(
          t("chat.projectFileTooLarge", {
            name: file.name,
            limit: formatBytes(maxFileSize),
          }),
        )
      } else {
        accepted.push(file)
      }
    }
    setUploadError(rejected.join(" "))
    const pending = accepted.map((file, index) => ({
      key: `${Date.now()}-${index}-${file.name}`,
      file,
      status: "uploading" as const,
    }))
    setPendingUploads((previous) => [...previous, ...pending])
    for (const item of pending) void uploadOne(item)
  }

  const retryFile = async (file: ChatProjectFile) => {
    if (!project) return
    const projectID = project.id
    setFiles((previous) =>
      previous.map((item) =>
        item.id === file.id ? { ...item, status: "processing" } : item,
      ),
    )
    try {
      const updated = await reindexProjectFile(project.id, file.id)
      if (currentProjectIdRef.current !== projectID) return
      setFiles((previous) =>
        previous.map((item) => (item.id === updated.id ? updated : item)),
      )
    } catch (retryError) {
      if (currentProjectIdRef.current !== projectID) return
      setFiles((previous) =>
        previous.map((item) =>
          item.id === file.id
            ? {
                ...item,
                status: "failed",
                error:
                  retryError instanceof Error
                    ? retryError.message
                    : String(retryError),
              }
            : item,
        ),
      )
    }
  }

  const removeFile = async (fileID: string) => {
    if (!project) return
    const projectID = project.id
    try {
      await deleteProjectFile(project.id, fileID)
      if (currentProjectIdRef.current !== projectID) return
      setFiles((previous) => previous.filter((file) => file.id !== fileID))
    } catch (removeError) {
      if (currentProjectIdRef.current !== projectID) return
      setUploadError(
        removeError instanceof Error
          ? removeError.message
          : String(removeError),
      )
    }
  }

  const confirmDelete = async () => {
    if (!project || deleting) return
    setDeleting(true)
    setError("")
    try {
      await onDelete(project.id)
      setDeleteOpen(false)
      onOpenChange(false)
    } catch (deleteFailure) {
      setError(
        deleteFailure instanceof Error
          ? deleteFailure.message
          : String(deleteFailure),
      )
    } finally {
      setDeleting(false)
    }
  }

  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="max-h-[calc(100svh-2rem)] overflow-y-auto sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>
              {isCreating ? t("chat.newProject") : t("chat.projectSettings")}
            </DialogTitle>
            <DialogDescription>
              {t("chat.projectSettingsDescription")}
            </DialogDescription>
          </DialogHeader>

          {loading ? (
            <div className="space-y-4" aria-busy="true">
              <div className="bg-muted/50 h-9 animate-pulse rounded-md motion-reduce:animate-none" />
              <div className="bg-muted/50 h-28 animate-pulse rounded-md motion-reduce:animate-none" />
              <div className="bg-muted/50 h-24 animate-pulse rounded-md motion-reduce:animate-none" />
            </div>
          ) : (
            <div className="space-y-6">
              <div className="grid gap-2 sm:grid-cols-[220px_minmax(0,1fr)] sm:gap-6">
                <Label htmlFor="project-name">{t("chat.projectName")}</Label>
                <div className="min-w-0">
                  <Input
                    id="project-name"
                    value={name}
                    maxLength={64}
                    onChange={(event) => setName(event.target.value)}
                    autoFocus
                    className="max-w-[448px]"
                  />
                </div>
              </div>

              <div className="grid gap-2 sm:grid-cols-[220px_minmax(0,1fr)] sm:gap-6">
                <Label htmlFor="project-instructions">
                  {t("chat.projectInstructions")}
                </Label>
                <div className="min-w-0">
                  <Textarea
                    id="project-instructions"
                    value={instructions}
                    maxLength={16000}
                    rows={6}
                    onChange={(event) => setInstructions(event.target.value)}
                    placeholder={t("chat.projectInstructionsPlaceholder")}
                    className="min-h-28 max-w-[448px] resize-y"
                  />
                </div>
              </div>

              {!isCreating && project && (
                <section
                  className="space-y-2"
                  aria-labelledby="project-files-title"
                >
                  <div className="flex items-center justify-between gap-3">
                    <h3
                      id="project-files-title"
                      className="text-sm font-medium"
                    >
                      {t("chat.projectFiles")}
                    </h3>
                    <span className="text-muted-foreground text-xs">
                      {t("chat.projectFileLimit", {
                        size: formatBytes(maxFileSize),
                      })}
                    </span>
                  </div>
                  <div
                    className={cn(
                      "border-border bg-muted/15 flex min-h-32 flex-col items-center justify-center gap-3 rounded-lg border border-dashed p-4 text-center transition-[background-color,border-color]",
                      dragActive && "border-accent bg-accent/5",
                    )}
                    onDragEnter={(event) => {
                      event.preventDefault()
                      dragDepth.current += 1
                      setDragActive(true)
                    }}
                    onDragOver={(event) => {
                      event.preventDefault()
                      event.dataTransfer.dropEffect = "copy"
                    }}
                    onDragLeave={(event) => {
                      event.preventDefault()
                      dragDepth.current = Math.max(0, dragDepth.current - 1)
                      if (dragDepth.current === 0) setDragActive(false)
                    }}
                    onDrop={(event) => {
                      event.preventDefault()
                      dragDepth.current = 0
                      setDragActive(false)
                      addFiles(event.dataTransfer.files)
                    }}
                  >
                    <IconUpload className="text-muted-foreground size-5" />
                    <div className="space-y-1">
                      <p className="text-sm">
                        {dragActive
                          ? t("chat.projectDropActive")
                          : t("chat.projectDropIdle")}
                      </p>
                      <p className="text-muted-foreground text-xs">
                        {t("chat.projectFileTypes")}
                      </p>
                    </div>
                    <input
                      ref={fileInputRef}
                      type="file"
                      multiple
                      accept={ACCEPTED_FILES}
                      className="sr-only"
                      onChange={(event) => {
                        if (event.target.files) addFiles(event.target.files)
                        event.target.value = ""
                      }}
                    />
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      onClick={() => fileInputRef.current?.click()}
                    >
                      {t("chat.chooseProjectFiles")}
                    </Button>
                  </div>
                  {uploadError && (
                    <p role="alert" className="text-destructive text-xs">
                      {uploadError}
                    </p>
                  )}
                  <div className="space-y-1.5" aria-live="polite">
                    {pendingUploads.map((pending) => (
                      <div
                        key={pending.key}
                        className="border-border flex min-h-12 items-center gap-3 rounded-md border px-3 py-2"
                      >
                        {pending.status === "uploading" ? (
                          <IconLoader2 className="text-muted-foreground size-4 animate-spin motion-reduce:animate-none" />
                        ) : (
                          <IconAlertTriangle className="text-destructive size-4" />
                        )}
                        <div className="min-w-0 flex-1">
                          <p className="truncate text-sm">
                            {pending.file.name}
                          </p>
                          <p className="text-muted-foreground text-xs">
                            {pending.status === "uploading"
                              ? t("chat.projectFileProcessing")
                              : pending.error || t("chat.projectFileFailed")}
                          </p>
                        </div>
                        {pending.status === "failed" && (
                          <Button
                            type="button"
                            variant="ghost"
                            size="icon-sm"
                            aria-label={t("chat.retryProjectFile")}
                            onClick={() => void uploadOne(pending)}
                          >
                            <IconRefresh />
                          </Button>
                        )}
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon-sm"
                          aria-label={t("chat.removeProjectFile")}
                          onClick={() =>
                            setPendingUploads((previous) =>
                              previous.filter(
                                (item) => item.key !== pending.key,
                              ),
                            )
                          }
                        >
                          <IconTrash />
                        </Button>
                      </div>
                    ))}
                    {files.map((file) => (
                      <div
                        key={file.id}
                        className="border-border flex min-h-12 items-center gap-3 rounded-md border px-3 py-2"
                      >
                        {file.status === "ready" ? (
                          <IconCheck className="text-success size-4" />
                        ) : file.status === "failed" ? (
                          <IconAlertTriangle className="text-destructive size-4" />
                        ) : (
                          <IconLoader2 className="text-muted-foreground size-4 animate-spin motion-reduce:animate-none" />
                        )}
                        <div className="min-w-0 flex-1">
                          <p className="truncate text-sm">{file.filename}</p>
                          <p className="text-muted-foreground text-xs">
                            {file.status === "ready"
                              ? t("chat.projectFileReady")
                              : file.status === "failed"
                                ? file.error || t("chat.projectFileFailed")
                                : t("chat.projectFileProcessing")}
                          </p>
                        </div>
                        {file.status === "ready" && (
                          <a
                            href={projectFileContentUrl(
                              file.project_id,
                              file.id,
                            )}
                            target="_blank"
                            rel="noreferrer"
                            className="text-muted-foreground hover:text-foreground focus-visible:ring-ring inline-flex min-h-8 items-center rounded-sm px-1 text-xs hover:underline focus-visible:ring-2 focus-visible:outline-none"
                          >
                            {t("chat.openProjectFile")}
                          </a>
                        )}
                        {file.status === "failed" && (
                          <Button
                            type="button"
                            variant="ghost"
                            size="icon-sm"
                            aria-label={t("chat.retryProjectFile")}
                            onClick={() => void retryFile(file)}
                          >
                            <IconRefresh />
                          </Button>
                        )}
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon-sm"
                          aria-label={t("chat.removeProjectFile")}
                          onClick={() => void removeFile(file.id)}
                        >
                          <IconTrash />
                        </Button>
                      </div>
                    ))}
                    {pendingUploads.length === 0 && files.length === 0 && (
                      <p className="text-muted-foreground py-2 text-xs">
                        {t("chat.projectFilesEmpty")}
                      </p>
                    )}
                  </div>
                </section>
              )}

              {error && (
                <div
                  role="alert"
                  className="text-destructive flex flex-wrap items-center justify-between gap-3 text-sm"
                >
                  <span>{error}</span>
                  {loadFailed && (
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      onClick={() => setReloadVersion((value) => value + 1)}
                    >
                      {t("common.retry")}
                    </Button>
                  )}
                </div>
              )}
            </div>
          )}

          <DialogFooter className="items-center sm:justify-between">
            <div>
              {!isCreating && project && (
                <Button
                  type="button"
                  variant="destructive"
                  onClick={() => setDeleteOpen(true)}
                >
                  {t("chat.deleteProject")}
                </Button>
              )}
            </div>
            <div className="flex flex-col-reverse gap-2 sm:flex-row">
              <Button
                type="button"
                variant="outline"
                onClick={() => onOpenChange(false)}
              >
                {t("common.cancel")}
              </Button>
              <Button
                type="button"
                disabled={!name.trim() || saving || loading || loadFailed}
                onClick={() => void save()}
              >
                {saving
                  ? t("chat.savingProject")
                  : isCreating
                    ? t("chat.createProject")
                    : t("common.save")}
              </Button>
            </div>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <AlertDialogContent size="sm">
          <AlertDialogHeader>
            <AlertDialogTitle>{t("chat.deleteProjectTitle")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("chat.deleteProjectDescription", {
                name: project?.name ?? "",
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleting}>
              {t("common.cancel")}
            </AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              disabled={deleting}
              onClick={(event) => {
                event.preventDefault()
                void confirmDelete()
              }}
            >
              {deleting ? t("chat.deletingProject") : t("chat.deleteProject")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

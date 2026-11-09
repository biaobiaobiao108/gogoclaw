import type { TFunction } from "i18next"
import { toast } from "sonner"

import { uploadSessionAttachment } from "@/api/sessions"
import type { ChatAttachment } from "@/store/chat"

export const CHAT_ATTACHMENT_ACCEPT = ""

function formatFileSize(bytes: number): string {
  if (bytes >= 1024 * 1024) {
    const megabytes = bytes / (1024 * 1024)
    return `${Number.isInteger(megabytes) ? megabytes : megabytes.toFixed(1)} MB`
  }
  if (bytes >= 1024) return `${Math.round(bytes / 1024)} KB`
  return `${bytes} B`
}

function getTransferItemFiles(dataTransfer: DataTransfer | null): File[] {
  if (!dataTransfer) {
    return []
  }

  const files = Array.from(dataTransfer.files)
  if (files.length > 0) {
    return files
  }

  return Array.from(dataTransfer.items)
    .filter((item) => item.kind === "file")
    .map((item) => item.getAsFile())
    .filter((file): file is File => file !== null)
}

export function hasFileTransfer(dataTransfer: DataTransfer | null): boolean {
  if (!dataTransfer) {
    return false
  }

  if (dataTransfer.files.length > 0) {
    return true
  }

  return Array.from(dataTransfer.items).some((item) => item.kind === "file")
}

export function getTransferredFiles(dataTransfer: DataTransfer | null) {
  return getTransferItemFiles(dataTransfer)
}

function attachmentType(file: File): ChatAttachment["type"] {
  const type = file.type.toLowerCase()
  if (type.startsWith("image/")) return "image"
  if (type.startsWith("audio/")) return "audio"
  if (type.startsWith("video/")) return "video"
  return "file"
}

function attachmentMime(file: File): string | undefined {
  return file.type.trim() || undefined
}

function attachmentPreview(file: File): string {
  if (typeof URL !== "undefined" && typeof URL.createObjectURL === "function") {
    return URL.createObjectURL(file)
  }
  return ""
}

export function releaseAttachmentPreview(attachment: ChatAttachment) {
  if (attachment.url.startsWith("blob:")) {
    URL.revokeObjectURL(attachment.url)
  }
}

export async function uploadChatAttachments({
  files,
  sessionId,
  maxAttachmentSize,
  t,
  onAttachment,
}: {
  files: readonly File[]
  sessionId: string
  maxAttachmentSize: number
  t: TFunction
  onAttachment: (attachment: ChatAttachment) => void
}) {
  for (const [index, file] of files.entries()) {
    const filename = file.name.trim() || `attachment-${index + 1}`
    const clientId = `${Date.now()}-${index}-${filename}`
    const sizeError =
      maxAttachmentSize > 0 && file.size > maxAttachmentSize
        ? t("chat.attachmentTooLarge", {
            name: filename,
            size: formatFileSize(maxAttachmentSize),
          })
        : ""
    if (sizeError) {
      toast.error(sizeError)
      onAttachment({
        clientId,
        type: attachmentType(file),
        url: attachmentPreview(file),
        filename,
        contentType: attachmentMime(file),
        status: "failed",
        persisted: false,
        error: sizeError,
        file,
      })
      continue
    }

    const pending: ChatAttachment = {
      clientId,
      type: attachmentType(file),
      url: attachmentPreview(file),
      filename,
      contentType: attachmentMime(file),
      status: "uploading",
      persisted: false,
      file,
    }
    onAttachment(pending)
    try {
      const uploaded = await uploadSessionAttachment(sessionId, file)
      releaseAttachmentPreview(pending)
      onAttachment({
        clientId,
        id: uploaded.id,
        type: uploaded.type,
        url: uploaded.url,
        filename: uploaded.filename || filename,
        contentType: uploaded.content_type || attachmentMime(file),
        status: "ready",
        persisted: false,
        file,
      })
    } catch (error) {
      const message = error instanceof Error ? error.message : ""
      onAttachment({
        ...pending,
        status: "failed",
        error: message || t("chat.attachmentUploadFailed", { name: filename }),
      })
      toast.error(t("chat.attachmentUploadFailed", { name: filename }))
    }
  }
}

export async function retryChatAttachment({
  attachment,
  sessionId,
  maxAttachmentSize,
  t,
  onAttachment,
}: {
  attachment: ChatAttachment
  sessionId: string
  maxAttachmentSize: number
  t: TFunction
  onAttachment: (attachment: ChatAttachment) => void
}) {
  if (!attachment.file) {
    toast.error(t("chat.attachmentRetryUnavailable"))
    return
  }
  if (maxAttachmentSize > 0 && attachment.file.size > maxAttachmentSize) {
    const message = t("chat.attachmentTooLarge", {
      name: attachment.filename || attachment.file.name,
      size: formatFileSize(maxAttachmentSize),
    })
    onAttachment({ ...attachment, status: "failed", error: message })
    toast.error(message)
    return
  }
  const pending = {
    ...attachment,
    status: "uploading" as const,
    error: undefined,
  }
  onAttachment(pending)
  try {
    const uploaded = await uploadSessionAttachment(sessionId, attachment.file)
    releaseAttachmentPreview(pending)
    onAttachment({
      ...pending,
      id: uploaded.id,
      type: uploaded.type,
      url: uploaded.url,
      filename: uploaded.filename || attachment.filename,
      contentType: uploaded.content_type || attachment.contentType,
      status: "ready",
    })
  } catch (error) {
    const message = error instanceof Error ? error.message : ""
    onAttachment({
      ...pending,
      status: "failed",
      error:
        message ||
        t("chat.attachmentUploadFailed", { name: attachment.filename || "" }),
    })
    toast.error(
      t("chat.attachmentUploadFailed", { name: attachment.filename || "" }),
    )
  }
}

import type { TFunction } from "i18next"
import { toast } from "sonner"

import { uploadSessionAttachment } from "@/api/sessions"
import type { ChatAttachment } from "@/store/chat"

export const CHAT_ATTACHMENT_ACCEPT = ""

const MAX_CHAT_ATTACHMENT_SIZE_BYTES = 20 * 1024 * 1024
const MAX_CHAT_ATTACHMENT_SIZE_LABEL = "20 MB"

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
  t,
  onAttachment,
}: {
  files: readonly File[]
  sessionId: string
  t: TFunction
  onAttachment: (attachment: ChatAttachment) => void
}) {
  for (const [index, file] of files.entries()) {
    const filename = file.name.trim() || `attachment-${index + 1}`
    const clientId = `${Date.now()}-${index}-${filename}`
    const sizeError =
      file.size > MAX_CHAT_ATTACHMENT_SIZE_BYTES
        ? t("chat.attachmentTooLarge", {
            name: filename,
            size: MAX_CHAT_ATTACHMENT_SIZE_LABEL,
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
      file,
    }
    onAttachment(pending)
    try {
      const uploaded = await uploadSessionAttachment(sessionId, file)
      releaseAttachmentPreview(pending)
      onAttachment({
        clientId,
        type: uploaded.type,
        url: uploaded.url,
        path: uploaded.path,
        filename: uploaded.filename || filename,
        contentType: uploaded.content_type || attachmentMime(file),
        status: "ready",
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
  t,
  onAttachment,
}: {
  attachment: ChatAttachment
  sessionId: string
  t: TFunction
  onAttachment: (attachment: ChatAttachment) => void
}) {
  if (!attachment.file) {
    toast.error(t("chat.attachmentRetryUnavailable"))
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
      type: uploaded.type,
      url: uploaded.url,
      path: uploaded.path,
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

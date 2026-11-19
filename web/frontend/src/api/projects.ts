import { launcherFetch } from "@/api/http"

export interface ChatProject {
  id: string
  name: string
  instructions: string
  created_at: string
  updated_at: string
  session_count?: number
  file_count?: number
}

export type ChatProjectFileStatus = "processing" | "ready" | "failed"

export interface ChatProjectFile {
  id: string
  project_id: string
  filename: string
  stored_name: string
  content_type?: string
  size_bytes: number
  status: ChatProjectFileStatus
  error?: string
  created_at: string
  indexed_at?: string
}

export interface ChatProjectDetail extends ChatProject {
  files: ChatProjectFile[]
}

async function responseError(response: Response, fallback: string) {
  const detail = await response.text().catch(() => "")
  return new Error(detail.trim() || `${fallback}: ${response.status}`)
}

export async function getProjects(): Promise<ChatProject[]> {
  const response = await launcherFetch("/api/projects")
  if (!response.ok)
    throw await responseError(response, "Failed to load projects")
  return response.json()
}

export async function getProject(id: string): Promise<ChatProjectDetail> {
  const response = await launcherFetch(
    `/api/projects/${encodeURIComponent(id)}`,
  )
  if (!response.ok)
    throw await responseError(response, "Failed to load project")
  return response.json()
}

export async function createProject(input: {
  name: string
  instructions?: string
}): Promise<ChatProject> {
  const response = await launcherFetch("/api/projects", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  })
  if (!response.ok)
    throw await responseError(response, "Failed to create project")
  return response.json()
}

export async function updateProject(
  id: string,
  input: { name?: string; instructions?: string },
): Promise<ChatProject> {
  const response = await launcherFetch(
    `/api/projects/${encodeURIComponent(id)}`,
    {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    },
  )
  if (!response.ok)
    throw await responseError(response, "Failed to update project")
  return response.json()
}

export async function deleteProject(id: string): Promise<void> {
  const response = await launcherFetch(
    `/api/projects/${encodeURIComponent(id)}`,
    {
      method: "DELETE",
    },
  )
  if (!response.ok)
    throw await responseError(response, "Failed to delete project")
}

export async function uploadProjectFile(
  projectId: string,
  file: File,
): Promise<ChatProjectFile> {
  const body = new FormData()
  body.append("file", file, file.name)
  const response = await launcherFetch(
    `/api/projects/${encodeURIComponent(projectId)}/files`,
    { method: "POST", body },
  )
  if (!response.ok) throw await responseError(response, "Failed to upload file")
  return response.json()
}

export async function reindexProjectFile(
  projectId: string,
  fileId: string,
): Promise<ChatProjectFile> {
  const response = await launcherFetch(
    `/api/projects/${encodeURIComponent(projectId)}/files/${encodeURIComponent(fileId)}/reindex`,
    { method: "POST" },
  )
  if (!response.ok)
    throw await responseError(response, "Failed to reindex file")
  return response.json()
}

export async function deleteProjectFile(
  projectId: string,
  fileId: string,
): Promise<void> {
  const response = await launcherFetch(
    `/api/projects/${encodeURIComponent(projectId)}/files/${encodeURIComponent(fileId)}`,
    { method: "DELETE" },
  )
  if (!response.ok) throw await responseError(response, "Failed to delete file")
}

export function projectFileContentUrl(projectId: string, fileId: string) {
  return `/api/projects/${encodeURIComponent(projectId)}/files/${encodeURIComponent(fileId)}/content`
}

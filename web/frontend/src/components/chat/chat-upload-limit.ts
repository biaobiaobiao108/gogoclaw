import { getUploadConfig } from "@/api/sessions"

const fallbackUploadLimit = 20 * 1024 * 1024
let cachedUploadLimit: number | null = null
let uploadLimitPromise: Promise<number> | null = null

export async function getCachedUploadLimit(): Promise<number> {
  if (cachedUploadLimit !== null) return cachedUploadLimit
  if (!uploadLimitPromise) {
    uploadLimitPromise = getUploadConfig()
      .then((config) => {
        const limit =
          config.max_media_size > 0
            ? config.max_media_size
            : fallbackUploadLimit
        cachedUploadLimit = limit
        return limit
      })
      .catch((error) => {
        console.warn("Failed to load attachment limit:", error)
        return fallbackUploadLimit
      })
      .finally(() => {
        uploadLimitPromise = null
      })
  }
  return uploadLimitPromise
}

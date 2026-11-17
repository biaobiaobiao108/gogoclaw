import { describe, expect, test } from "bun:test"
import { readdirSync, readFileSync, statSync } from "node:fs"
import { dirname, extname, relative, resolve } from "node:path"
import { fileURLToPath } from "node:url"

import en from "./locales/en.json"
import zh from "./locales/zh.json"

const sourceRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..")
const sourceFilePattern = /\bt\(\s*["']([^"'`]+)["']/g

describe("i18n coverage", () => {
  test("English and Chinese locale files expose the same keys", () => {
    const enKeys = collectLeafKeys(en)
    const zhKeys = collectLeafKeys(zh)

    expect([...enKeys].filter((key) => !zhKeys.has(key)).sort()).toEqual([])
    expect([...zhKeys].filter((key) => !enKeys.has(key)).sort()).toEqual([])
  })

  test("every statically referenced translation key exists in both locales", () => {
    const enKeys = collectLeafKeys(en)
    const zhKeys = collectLeafKeys(zh)
    const missing: Array<{ key: string; file: string }> = []

    for (const file of sourceFiles(sourceRoot)) {
      const source = readFileSync(file, "utf8")
      for (const match of source.matchAll(sourceFilePattern)) {
        const key = match[1]
        if (!hasTranslation(enKeys, key) || !hasTranslation(zhKeys, key)) {
          missing.push({ key, file: relative(sourceRoot, file) })
        }
      }
    }

    expect(missing).toEqual([])
  })
})

function collectLeafKeys(value: unknown, prefix = ""): Set<string> {
  const keys = new Set<string>()
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    if (prefix) keys.add(prefix)
    return keys
  }
  for (const [key, child] of Object.entries(value)) {
    for (const childKey of collectLeafKeys(
      child,
      prefix ? `${prefix}.${key}` : key,
    )) {
      keys.add(childKey)
    }
  }
  return keys
}

function hasTranslation(keys: Set<string>, key: string): boolean {
  return keys.has(key) || (keys.has(`${key}_one`) && keys.has(`${key}_other`))
}

function sourceFiles(directory: string): string[] {
  const files: string[] = []
  for (const entry of readdirSync(directory)) {
    const path = resolve(directory, entry)
    if (statSync(path).isDirectory()) {
      files.push(...sourceFiles(path))
      continue
    }
    if ([".ts", ".tsx"].includes(extname(path))) files.push(path)
  }
  return files
}

import { describe, expect, test } from "bun:test"

import type { ModelInfo, ModelProviderOption } from "@/api/models"

import {
  applyReferenceRename,
  applyReferenceRenames,
  deriveModelUpdateRenames,
  modelEntryAllowedInDefaultChain,
  modelNameAllowedInDefaultChain,
  modelReferenceAllowedAfterModelChange,
  rawModelTemplateScore,
  rebaseDefaultChainDraft,
  selectBestRawModelMatch,
} from "./model-list-logic"

function providerOption(
  option: Partial<ModelProviderOption> & Pick<ModelProviderOption, "id">,
): ModelProviderOption {
  return {
    display_name: option.id,
    default_api_base: "https://api.openai.com/v1",
    empty_api_key_allowed: false,
    create_allowed: true,
    default_model_allowed: true,
    ...option,
  }
}

function model(
  fields: Partial<ModelInfo> &
    Pick<ModelInfo, "index" | "model_name" | "model">,
): ModelInfo {
  return {
    provider: "openai",
    api_key: "",
    enabled: true,
    available: true,
    status: "available",
    is_default: false,
    is_virtual: false,
    default_model_allowed: true,
    ...fields,
  }
}

const providerOptions = [
  providerOption({ id: "openai", aliases: ["oa"] }),
  providerOption({
    id: "local",
    default_api_base: "",
    default_model_allowed: false,
  }),
]

describe("default chain constraints", () => {
  test("rejects virtual, unconfigured, and disallowed model entries", () => {
    expect(
      modelEntryAllowedInDefaultChain(
        model({ index: 1, model_name: "virtual", model: "virtual" }),
      ),
    ).toBe(true)
    expect(
      modelEntryAllowedInDefaultChain(
        model({
          index: 2,
          model_name: "virtual",
          model: "virtual",
          is_virtual: true,
        }),
      ),
    ).toBe(false)
    expect(
      modelEntryAllowedInDefaultChain(
        model({
          index: 3,
          model_name: "pending",
          model: "pending",
          status: "unconfigured",
        }),
      ),
    ).toBe(false)
    expect(
      modelEntryAllowedInDefaultChain(
        model({
          index: 4,
          model_name: "blocked",
          model: "blocked",
          default_model_allowed: false,
        }),
      ),
    ).toBe(false)
  })

  test("requires every duplicate alias to remain eligible", () => {
    const models = [
      model({ index: 1, model_name: "shared", model: "gpt-4o" }),
      model({
        index: 2,
        model_name: "shared",
        model: "gpt-4o-mini",
        status: "unconfigured",
      }),
    ]

    expect(modelNameAllowedInDefaultChain("shared", models)).toBe(false)
    expect(modelNameAllowedInDefaultChain("missing", models)).toBe(false)
  })

  test("accepts new raw references only when their provider and template are usable", () => {
    const models = [
      model({
        index: 1,
        model_name: "oa/gpt-4o",
        provider: "openai",
        model: "gpt-4o",
        api_key: "sk-test",
      }),
      model({
        index: 2,
        model_name: "local/llama",
        provider: "local",
        model: "llama",
      }),
      model({ index: 3, model_name: "removed", model: "removed" }),
    ]

    expect(
      modelReferenceAllowedAfterModelChange(
        "oa/gpt-4o",
        [],
        models,
        providerOptions,
        "openai",
      ),
    ).toBe(true)
    expect(
      modelReferenceAllowedAfterModelChange(
        "local/new-model",
        [],
        models,
        providerOptions,
        "openai",
      ),
    ).toBe(false)
    expect(
      modelReferenceAllowedAfterModelChange(
        "removed",
        [models[2]],
        models.slice(0, 2),
        providerOptions,
        "openai",
      ),
    ).toBe(false)
  })
})

describe("raw template matching", () => {
  test("scores credentials, non-default endpoints, auth, and availability", () => {
    const keyed = model({
      index: 1,
      model_name: "keyed",
      model: "keyed",
      api_key: "sk-test",
    })
    const customEndpoint = model({
      index: 2,
      model_name: "custom",
      model: "custom",
      api_base: "https://proxy.example.com/v1",
      auth_method: "api_key",
    })

    expect(rawModelTemplateScore(keyed, providerOptions)).toBe(33)
    expect(rawModelTemplateScore(customEndpoint, providerOptions)).toBe(25)
  })

  test("selects the strongest template and uses list order as the tie-breaker", () => {
    const models = [
      model({
        index: 7,
        model_name: "keyed-later",
        model: "keyed-later",
        api_key: "sk-test",
      }),
      model({ index: 3, model_name: "plain", model: "plain" }),
      model({
        index: 4,
        model_name: "keyed-first",
        model: "keyed-first",
        api_key: "sk-test",
      }),
    ]

    expect(selectBestRawModelMatch(models, providerOptions).model_name).toBe(
      "keyed-first",
    )
  })
})

describe("reference rename propagation", () => {
  const renames = [
    { from: "old-default", to: "new-default", default: true, fallback: false },
    {
      from: "old-fallback",
      to: "new-fallback",
      default: false,
      fallback: true,
    },
  ]

  test("applies only the roles covered by each rename and de-duplicates chains", () => {
    expect(applyReferenceRename("old-default", renames, "default")).toBe(
      "new-default",
    )
    expect(applyReferenceRename("old-default", renames, "fallback")).toBe(
      "old-default",
    )
    expect(
      applyReferenceRenames(
        ["old-fallback", "old-fallback", "stable"],
        renames,
      ),
    ).toEqual(["new-fallback", "stable"])
  })

  test("derives an update rename only while the updated index and aliases stay stable", () => {
    const updatedModels = [
      model({ index: 1, model_name: "new-name", model: "gpt-4o" }),
      model({ index: 2, model_name: "kept-name", model: "gpt-4o-mini" }),
    ]
    const updates = [
      {
        index: 1,
        from: "old-name",
        stableAliases: [{ index: 2, modelName: "kept-name" }],
      },
    ]

    expect(deriveModelUpdateRenames(updates, updatedModels)).toEqual([
      {
        from: "old-name",
        to: "new-name",
        default: true,
        fallback: true,
      },
    ])
    expect(
      deriveModelUpdateRenames(
        [
          {
            ...updates[0],
            stableAliases: [{ index: 2, modelName: "old-kept-name" }],
          },
        ],
        updatedModels,
      ),
    ).toEqual([])
  })
})

describe("draft rebase", () => {
  const models = [
    model({ index: 1, model_name: "primary", model: "gpt-4o" }),
    model({ index: 2, model_name: "second", model: "gpt-4o-mini" }),
    model({ index: 3, model_name: "old", model: "gpt-4o-old" }),
    model({ index: 4, model_name: "new", model: "gpt-4o-new" }),
  ]

  test("applies default renames before deciding whether a draft default changed", () => {
    expect(
      rebaseDefaultChainDraft(
        { defaultModelName: "legacy", fallbackChain: [] },
        { defaultModelName: "legacy", fallbackChain: [] },
        { defaultModelName: "renamed", fallbackChain: [] },
        [model({ index: 1, model_name: "legacy", model: "legacy" })],
        [model({ index: 1, model_name: "renamed", model: "renamed" })],
        [
          {
            from: "legacy",
            to: "renamed",
            default: true,
            fallback: true,
          },
        ],
        providerOptions,
        "openai",
      ),
    ).toEqual({ defaultModelName: "renamed", fallbackChain: [] })
  })

  test("keeps a user removal while inserting newer persisted fallbacks in order", () => {
    expect(
      rebaseDefaultChainDraft(
        {
          defaultModelName: "primary",
          fallbackChain: ["old", "second"],
        },
        { defaultModelName: "primary", fallbackChain: ["second"] },
        {
          defaultModelName: "primary",
          fallbackChain: ["old", "new", "second"],
        },
        models,
        models,
        [],
        providerOptions,
        "openai",
      ),
    ).toEqual({
      defaultModelName: "primary",
      fallbackChain: ["new", "second"],
    })
  })

  test("rejects a draft default that disappeared and restores persisted fallbacks", () => {
    const removed = model({ index: 9, model_name: "removed", model: "removed" })

    expect(
      rebaseDefaultChainDraft(
        { defaultModelName: "removed", fallbackChain: [] },
        { defaultModelName: "removed", fallbackChain: ["removed"] },
        { defaultModelName: "primary", fallbackChain: ["second"] },
        [...models, removed],
        models,
        [],
        providerOptions,
        "openai",
      ),
    ).toEqual({ defaultModelName: "primary", fallbackChain: ["second"] })
  })

  test("removes duplicate identities and returns an empty chain without a default", () => {
    expect(
      rebaseDefaultChainDraft(
        { defaultModelName: "primary", fallbackChain: [] },
        {
          defaultModelName: "primary",
          fallbackChain: ["primary", "second", "oa/gpt-4o-mini", "second"],
        },
        { defaultModelName: "primary", fallbackChain: [] },
        models,
        models,
        [],
        providerOptions,
        "openai",
      ),
    ).toEqual({ defaultModelName: "primary", fallbackChain: ["second"] })

    expect(
      rebaseDefaultChainDraft(
        { defaultModelName: "", fallbackChain: [] },
        { defaultModelName: "", fallbackChain: [] },
        { defaultModelName: "", fallbackChain: [] },
        [],
        [],
        [],
        providerOptions,
        "openai",
      ),
    ).toEqual({ defaultModelName: "", fallbackChain: [] })
  })
})

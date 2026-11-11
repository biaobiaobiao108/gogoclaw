# Issue #783 Investigation and Fix Execution Document

## 1. Problem Clarification (Confirmed)

- Symptom: When `agents.*.model.primary/fallbacks` use a `model_name` alias (such as `step-3.5-flash`), the fallback chain treats the alias as a real `provider/model` for parsing, which can result in an empty `provider` and an incorrect `model`.
- Root cause: `ResolveCandidates` only applies `ParseModelRef` to strings and does not first map the alias to the real `model` field through `model_list`.
- Impact:
  - Fallback execution may send the alias directly to an OpenAI-compatible provider, triggering `Unknown Model`.
  - When `defaults.provider` is empty, logs show `provider=` with an empty value.

## 2. Goals of This Change

- Fix fallback candidate resolution: resolve aliases through `model_list` first.
- Preserve legacy behavior: if there is no match in `model_list`, continue using the original `ParseModelRef` fallback.
- Add tests: cover aliases, nested-path models (such as `openrouter/stepfun/...`), and an empty default provider.
- Verify code style: keep it consistent with the current repository style (naming, error handling, and test structure).

## 3. Online Best-Practice Research Conclusions (Completed)

- [x] Reviewed recommended handling of the `model` field by OpenAI-compatible gateways (such as OpenRouter).
- [x] Reviewed best practices for multi-provider/fallback design (candidate resolution and log observability).
- [x] Mapped external recommendations to executable constraints for this repository.

Key points from external references (from official documentation for OpenRouter/LiteLLM/Cloudflare AI Gateway and others):

- Prefer explicit configuration and do not rely on string splitting to infer the provider.
- Preserve the full path semantics of gateway model identifiers to avoid truncation causing Unknown Model.
- Fallback and primary should reuse the same resolution strategy, avoiding a situation where "the primary path is correct but the degraded path is wrong".

Reference links:

- OpenRouter Provider Routing: https://openrouter.ai/docs/guides/routing/provider-selection
- OpenRouter Model Fallbacks: https://openrouter.ai/docs/guides/routing/model-fallbacks
- OpenRouter Chat Completion API: https://openrouter.ai/docs/api-reference/chat-completion
- LiteLLM Router Architecture: https://docs.litellm.ai/docs/router_architecture
- Cloudflare AI Gateway Chat Completion: https://developers.cloudflare.com/ai-gateway/usage/chat-completion/

Executable constraints corresponding to this repository:

- Perform the `model_name -> model_list.model` mapping first during fallback candidate construction.
- Preserve the legacy parsing behavior when no mapping matches, ensuring compatibility.
- Use new tests to lock down the "alias + nested model path + empty default provider" scenarios.

## 4. Implementation Steps (Executed in Order)

- [x] Step 1: Align with existing code patterns and locate the minimal change points (`pkg/agent` + `pkg/providers`).
- [x] Step 2: Implement "fallback candidate resolution based on model_list".
- [x] Step 3: Add/update unit tests covering the issue scenarios.
- [x] Step 4: Recheck code style consistency (against existing file styles).
- [x] Step 5: Run quality gates (LSP + `make check`).

## 5. Execution Record

- Status: Completed
- Completed changes:
  - `pkg/providers/fallback.go`: Added `ResolveCandidatesWithLookup` while keeping `ResolveCandidates` backward compatible.
  - `pkg/agent/instance.go`: Before constructing fallback candidates, resolve aliases through `model_list` first, and for models without a protocol, add the default `openai/` prefix before parsing.
  - `pkg/providers/fallback_test.go`: Added alias resolution and deduplication tests.
  - `pkg/agent/instance_test.go`: Added agent-side tests for resolving aliases to nested model paths and for resolving models without a protocol.
- Style alignment check (completed): Consistent with existing patterns in `pkg/providers/fallback_test.go` and `pkg/providers/model_ref_test.go`.
- Quality verification (completed): Ran `make generate` first, then the full `make check` passed.

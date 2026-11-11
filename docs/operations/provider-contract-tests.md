# Optional Live Model Service Contract Checks

Normal CI uses local mock services and does not depend on accounts or keys. To verify a real OpenAI-compatible service, you can manually run the `Live provider contract` workflow in GitHub Actions; it sends only a single very short chat request to check whether authentication, the request path, and response parsing can interoperate correctly with the current service.

Before first use, configure the following in the repository settings:

| Type | Name | Value |
| --- | --- | --- |
| Repository variable | `GOGOCLAW_LIVE_LLM_API_BASE` | OpenAI-compatible API base URL, for example an address containing `/v1` |
| Repository variable | `GOGOCLAW_LIVE_LLM_MODEL` | Model identifier accepted by the server |
| Repository secret | `GOGOCLAW_LIVE_LLM_API_KEY` | API key |

Then manually run `Live provider contract` in Actions and explicitly check `run_live_contract`. If it is not checked, no request is sent to the model service. Real requests may incur a small cost; use a test key with a low quota limit. The workflow is fixed to use the address in the repository variable, avoiding sending the key to an arbitrary manually entered host.

This check covers the OpenAI-compatible chat completion interface. It does not replace end-to-end tests for channels such as Telegram and Discord, nor does it guarantee that all extended parameters of compatible APIs are available.

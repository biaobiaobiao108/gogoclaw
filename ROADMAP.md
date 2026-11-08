
# 🦐 GoGoClaw Roadmap

> **Vision**: To build the ultimate lightweight, secure, and fully autonomous AI Agent infrastructure.automate the mundane, unleash your creativity

---

## 🚀 1. Core Optimization: Lean Containers

*Our defining characteristic. We keep the application process small while production runs in a Linux container.*

* [**Memory Footprint Reduction**](https://github.com/biaobiaobiao108/gogoclaw/issues/346)
  * **Goal**: Keep the core gateway process below 20MB where practical and keep the production container usable on compact 64-bit Linux hosts.
  * **Context**: Production capacity is determined by the container image and runtime, not by native binaries on bare embedded boards. The documented host baseline remains 512MB RAM.
  * **Action**: Analyze memory growth between releases, remove redundant dependencies, and optimize data structures.


## 🛡️ 2. Security Hardening: Defense in Depth

*Paying off early technical debt. We invite security experts to help build a "Secure-by-Default" agent.*

* **Input Defense & Permission Control**
  * **Prompt Injection Defense**: Harden JSON extraction logic to prevent LLM manipulation.
  * **Tool Abuse Prevention**: Strict parameter validation to ensure generated commands stay within safe boundaries.
  * **SSRF Protection**: Built-in blocklists for network tools to prevent accessing internal IPs (LAN/Metadata services).


* **Sandboxing & Isolation**
  * **Filesystem Sandbox**: Restrict file R/W operations to specific directories only.
  * **Context Isolation**: Prevent data leakage between different user sessions or channels.
  * **Privacy Redaction**: Auto-redact sensitive info (API Keys, PII) from logs and standard outputs.


* **Authentication & Secrets**
  * **Crypto Upgrade**: Adopt modern algorithms like `ChaCha20-Poly1305` for secret storage.
  * **OAuth 2.0 Flow**: Deprecate hardcoded API keys in the CLI; move to secure OAuth flows.



## 🔌 3. Connectivity: Protocol-First Architecture

*Connect every model, reach every platform.*

* **Provider**
  * [**Architecture Upgrade**](https://github.com/biaobiaobiao108/gogoclaw/issues/283): Refactor from "Vendor-based" to "Protocol-based" classification (e.g., OpenAI-compatible, Ollama-compatible). *(Status: In progress by @Daming, ETA 5 days)*
  * **Local Models**: Deep integration with **Ollama**, **vLLM**, **LM Studio**, and **Mistral** (local inference).
  * **Online Models**: Continued support for frontier closed-source models.


* **Channel**
  * **IM Matrix**: QQ, WeChat (Work), DingTalk, Feishu (Lark), Telegram, Discord, WhatsApp, LINE, Slack, Email, KOOK, Signal, ...
  * **Standards**: Support for the **OneBot** protocol.
  * [**attachment**](https://github.com/biaobiaobiao108/gogoclaw/issues/348): Native handling of images, audio, and video attachments.


* **Skill Marketplace**
  * [**Discovery skills**](https://github.com/biaobiaobiao108/gogoclaw/issues/287): Implement `find_skill` to automatically discover and install skills from the [GitHub Skills Repo] or other registries.



## 🧠 4. Advanced Capabilities: From Chatbot to Agentic AI

*Beyond conversation—focusing on action and collaboration.*

* **Operations**
  * [**MCP Support**](https://github.com/biaobiaobiao108/gogoclaw/issues/290): Native support for the **Model Context Protocol (MCP)**.
  * [**Browser Automation**](https://github.com/biaobiaobiao108/gogoclaw/issues/293): Headless browser control via CDP (Chrome DevTools Protocol) or ActionBook.


* **Multi-Agent Collaboration**
  * [**Basic Multi-Agent**](https://github.com/biaobiaobiao108/gogoclaw/issues/294) implement
  * [**Model Routing**](https://github.com/biaobiaobiao108/gogoclaw/issues/295): "Smart Routing" — dispatch simple tasks to small/local models (fast/cheap) and complex tasks to SOTA models (smart).
  * [**Swarm Mode**](https://github.com/biaobiaobiao108/gogoclaw/issues/284): Collaboration between multiple GoGoClaw instances on the same network.
  * [**AIEOS**](https://github.com/biaobiaobiao108/gogoclaw/issues/296): Exploring AI-Native Operating System interaction paradigms.



## 📚 5. Developer Experience (DevEx) & Documentation

*Lowering the barrier to entry so anyone can deploy in minutes.*

* [**QuickGuide (Zero-Config Start)**](https://github.com/biaobiaobiao108/gogoclaw/issues/350)
  * Interactive CLI Wizard: If launched without config, automatically detect the environment and guide the user through Token/Network setup step-by-step.


* **Comprehensive Documentation**
  * **Platform Guides**: Container deployment plus local development on Windows, macOS, and Linux.
  * **Step-by-Step Tutorials**: "Babysitter-level" guides for configuring Providers and Channels.
  * **AI-Assisted Docs**: Using AI to auto-generate API references and code comments (with human verification to prevent hallucinations).



## 🤖 6. Engineering: AI-Powered Open Source

*Born from Vibe Coding, we continue to use AI to accelerate development.*

* **AI-Enhanced CI/CD**
  * Integrate AI for automated code review and PR labeling.
  * **Bot Noise Reduction**: Optimize bot interactions to keep PR timelines clean.
  * **Issue Triage**: AI agents to analyze incoming issues and suggest preliminary fixes.



## 🎨 7. Brand & Community

* [**Logo Design**](https://github.com/biaobiaobiao108/gogoclaw/issues/297): We are looking for a **Mantis Shrimp (Stomatopoda)** logo design!
  * *Concept*: Needs to reflect "Small but Mighty" and "Lightning Fast Strikes."



---

### 🤝 Call for Contributions

We welcome community contributions to any item on this roadmap! Please comment on the relevant Issue or submit a PR. Let's build the best Edge AI Agent together!

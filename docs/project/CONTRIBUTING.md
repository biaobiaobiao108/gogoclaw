# Contributing to GoGoClaw

Thank you for your interest in GoGoClaw! This project is a community-driven open-source project whose goal is to build a lightweight, flexible, and accessible personal AI assistant. We welcome contributions of all forms: bug fixes, new features, documentation, translations, and tests.

GoGoClaw itself is developed to a large extent with AI assistance—we embrace this approach and have built our contribution process around it.

## Table of Contents

- [Code of Conduct](#行为准则)
- [Ways to Contribute](#贡献方式)
- [Quick Start](#快速开始)
- [Development Environment Setup](#开发环境配置)
- [Submitting Changes](#提交修改)
- [AI-Assisted Contributions](#ai-辅助贡献)
- [Pull Request Process](#pull-request-流程)
- [Branch Strategy](#分支策略)
- [Code Review](#代码审查)
- [Communication Channels](#沟通渠道)

---

## Code of Conduct

We are committed to maintaining a friendly and mutually respectful community environment. Please be kind, constructive, and interpret others in good faith. Harassment or discrimination of any kind is not acceptable.

---

## Ways to Contribute

- **Bug reports** — Submit an Issue using the bug report template.
- **Feature suggestions** — Submit an Issue using the feature request template; we recommend discussing it before starting implementation.
- **Code contributions** — Fix bugs or implement new features; see the workflow below.
- **Documentation improvements** — Improve the README, documentation, code comments, or translations.
- **Testing and validation** — Run GoGoClaw on new hardware, a new channel, or a new LLM provider and report the results.

For larger new features, please submit an Issue to discuss the design before writing code. This avoids wasted effort and ensures alignment with the project direction.

---

## Quick Start

1. **Fork** this repository on GitHub.
2. **Clone** your fork locally:
   ```bash
   git clone https://github.com/<你的用户名>/gogoclaw.git
   cd gogoclaw
   ```
3. Add the upstream remote repository:
   ```bash
   git remote add upstream https://github.com/biaobiaobiao108/gogoclaw.git
   ```

---

## Development Environment Setup

### Prerequisites

- Go 1.27.4 or later
- Bun 1.4.4 or later for Web console development
- `make`

### Build

```bash
make build       # 构建二进制文件（会先执行 go generate）
make generate    # 仅执行 go generate
make check       # 完整的提交前检查：依赖、测试和开发构建
```

### Run Tests

```bash
make test                                    # 运行所有测试
go test -run TestName -v ./pkg/session/      # 运行单个测试
go test -bench=. -benchmem -run='^$' ./...  # 运行基准测试
```

Go code uses `gofmt`; TypeScript should remain consistent with the existing code. Run `make check` locally before pushing code.

---

## Submitting Changes

### Branch Management

Always branch from `main`, and target `main` in pull requests. Do not push directly to `main` or any `release/*` branch:

```bash
git checkout main
git pull upstream main
git checkout -b 你的功能分支名
```

Use descriptive branch names, such as `fix/telegram-timeout`, `feat/ollama-provider`, or `docs/contributing-guide`.

### Commit Convention

- Write clear, concise commit messages in Chinese.
- Use the Conventional Commits format, such as `fix(channels): 修复 Telegram 超时重试`.
- Reference related Issues in the PR description when applicable.
- Keep commits focused; each commit should do one thing.
- For small cleanups or spelling fixes, merge them into a single commit before submitting the PR.
- Follow the [Conventional Commits](https://www.conventionalcommits.org/zh-hans/v1.0.0/) specification.

### Stay in Sync with Upstream

Before submitting a PR, rebase your branch onto upstream `main`:

```bash
git fetch upstream
git rebase upstream/main
```

---

## AI-Assisted Contributions

GoGoClaw is developed to a large extent with AI assistance, and we fully embrace this development approach. However, contributors must clearly understand their responsibilities when using AI tools.

### AI Usage Must Be Disclosed

Every PR must disclose AI involvement through the **🤖 AI code generation** section of the PR template, in one of three levels:

| Level | Description |
|---|---|
| 🤖 Fully AI-generated | AI wrote the code; the contributor is responsible for review and verification |
| 🛠️ Mostly AI-generated | AI drafted it and the contributor made substantial changes |
| 👨‍💻 Mostly human-written | The contributor led the work and AI provided only assistance or was not used |

We expect you to fill this in honestly. All three levels are acceptable, without discrimination—what matters is the quality of the contribution.

### You Are Fully Responsible for Submitted Code

Using AI-generated code does not reduce your responsibility as a contributor. Before submitting a PR containing AI-generated code, you must:

- **Read and understand** the generated code line by line.
- **Test it in a real environment** (see the test environment section in the PR template).
- **Check for security issues** — AI models may generate code with security risks (such as path traversal, injection attacks, or credential leaks); review it carefully.
- **Verify correctness** — AI-generated logic may sound plausible but be wrong; verify behavior, not just syntax.

If it is obvious that the contributor has not read or tested AI-generated code, the PR will be closed directly without review.

### Quality Standards for AI-Generated Code

AI-generated code follows the **same quality requirements** as human-written code:

- It must pass all CI checks (`make check`).
- It must follow idiomatic Go and remain consistent with the existing codebase style.
- It must not introduce unnecessary abstractions, dead code, or overengineering.
- It must include or update tests where appropriate.

### Security Review

AI-generated code requires especially careful security review. Pay particular attention to the following:

- File path handling and sandbox escapes (commit `244eb0b` in the project's history is a real example)
- Validation of external input in channel handlers and tool implementations
- Handling of credentials or keys
- Command execution (`exec.Command`, shell invocations, etc.)

If you are unsure whether a piece of AI-generated code is safe, say so in the PR—reviewers will help assess it.

---

## Pull Request Process

### Pre-PR Checklist

- [ ] Run `make check` locally and confirm it passes.
- [ ] Complete the PR template in full, including the AI disclosure section.
- [ ] Link the relevant Issue in the PR description.
- [ ] Keep the PR focused and avoid mixing unrelated changes.

### PR Template Sections

The PR template requires:

- **Description** — What does this change do, and why?
- **Change type** — Bug fix, new feature, documentation, or refactor.
- **AI code generation** — Disclosure of AI involvement (required).
- **Related Issue** — Link to the Issue this PR resolves.
- **Technical background** — Reference links and design rationale (can be skipped for documentation-only PRs).
- **Test environment** — Hardware, operating system, model/provider, and channel used for testing.
- **Verification evidence** — Optional logs or screenshots proving the change works.
- **Checklist** — Self-review confirmation.

### PR Size

Please submit small, easy-to-review PRs. A PR touching 5 files with 200 lines of changes is far easier to review than one touching 30 files with 2,000 lines of changes. If your feature is large, consider splitting it into a series of logically complete, smaller PRs.

---

## Branch Strategy

### Long-Lived Branches

- **`main`** — The active development branch. All feature PRs target `main`. This branch is protected: direct pushes are prohibited, and at least one maintainer approval is required before merging.
- **`release/x.y`** — Stable release branches, cut from `main` when a version is ready for release. These branches have a higher level of protection than `main`.

### Requirements for Merging into `main`

A PR must satisfy all of the following conditions before it can be merged:

1. **All CI passes** — All GitHub Actions workflows (test, security, build) are green.
2. **Reviewer approval** — At least one maintainer has approved the PR.
3. **No unresolved review comments** — All review discussion threads are resolved.
4. **PR template completed** — Including AI disclosure and test environment information.

### Who Can Merge

Only maintainers can merge PRs. Contributors cannot merge their own PRs, even if they have write permission.

### Merge Strategy

To keep `main` history clear and readable, we use **Squash Merge** for most PRs. Each merged PR becomes a single commit containing the PR number, for example:

```
feat: Add Ollama provider support (#491)
```

If a PR contains multiple independent, clearly structured commits that tell a complete story, maintainers may choose a regular merge at their discretion.

### Release Branches

When a version is ready, maintainers cut a `release/x.y` branch from `main`. After that:

- **New features are not backported.** Once a release branch is cut, it no longer receives any new features.
- **Security fixes and critical bug fixes are cherry-picked in.** If a fix on `main` addresses a security vulnerability, data loss, or crash, maintainers will cherry-pick the relevant commit to the affected `release/x.y` branch and publish a patch release.

If you believe a fix on `main` should be backported to a release branch, note it in the PR description or open a separate Issue explaining it. The final decision is made by maintainers.

Release branches have a higher level of protection than `main`, and direct pushes are never allowed under any circumstances.

---

## Code Review

### Advice for Contributors

- Respond to review comments within a reasonable time. If you need more time, let reviewers know.
- When updating a PR in response to feedback, briefly explain the changes (for example: "Switched to `sync.RWMutex` as suggested").
- If you disagree with a comment, explain your reasoning politely—reviewers can also make mistakes.
- Do not force push after review has started—it makes changes difficult for reviewers to track. Use additional commits instead; maintainers will squash when merging.

### Advice for Reviewers

Review priorities:

1. **Correctness** — Does the code implement what it claims? Are there edge cases?
2. **Security** — Pay particular attention to AI-generated code, tool implementations, and channel handlers.
3. **Architecture** — Is the implementation consistent with the existing design?
4. **Simplicity** — Is there a simpler approach? Has unnecessary complexity been introduced?
5. **Tests** — Does the change have test coverage? Are existing tests still meaningful?

Please give constructive and specific feedback. "If two goroutines call this function concurrently there may be a race condition; consider adding a mutex here" is far more helpful than "this looks wrong".

### Reviewer List

After submitting the corresponding PR, you can refer to the table below to contact the appropriate reviewer.

|Function| Reviewer|
|---     |---      |
|Provider|@yinwm   |
|Channel |@yinwm/@alexhoshina   |
|Agent   |@lxowalle/@Zhaoyikaiii|
|Tools   |@lxowalle|
|Skill   ||
|MCP     ||
|Optimization|@lxowalle|
|Security||
|AI CI   |@imguoguo|
|UX      ||
|Document||



---

## Communication Channels

- **GitHub Issues** — Bug reports, feature suggestions, and design discussions.
- **GitHub Discussions** — General questions, idea exchange, and community discussions.
- **Pull Request comments** — Feedback related to specific code.
- **Wechat&Discord** — Once you have at least one merged PR, we will invite you to join the developer discussion group.

If you have questions, open an Issue for discussion before writing code. It costs almost nothing and can avoid a great deal of wasted effort.

---

## About This Project's AI-Driven Origins

GoGoClaw's architecture has undergone a large amount of AI-assisted design and implementation work under human supervision. If something looks strange or overengineered, it may be a trace left by that process—you are welcome to open an Issue to discuss it.

We believe that responsible use of AI-assisted development can produce excellent results. We also believe that humans must be accountable for what they submit. These two points are not contradictory.

Thank you for your contribution!

# GoGoClaw Documentation

GoGoClaw documentation is organized by document type. English documents use the base filename and Chinese translations use the `.zh.md` suffix beside the source file. The repository root provides a Chinese README by default and an English README at `README.en.md`.

## Reader Navigation

- [Guides](guides/README.md): setup, configuration, provider, and workflow guides.
- [Reference](reference/README.md): precise configuration and behavior reference.
- [Operations](operations/README.md): debugging and troubleshooting material.
- [Security](security/README.md): security-focused guides and controls.
- [Architecture](architecture/README.md): implementation notes and internal design docs.
- [Migration](migration/README.md): upgrade and migration notes.

For channel-specific setup, start with [Chat Apps Configuration](guides/chat-apps.md) and then use `docs/channels/<name>/README.md` as needed.

## Translation Rules

- English documents use the base filename, such as `README.md` or `configuration.md`.
- Chinese translations stay beside the English source and use `.zh.md`, such as `configuration.zh.md`.
- Other locale suffixes are not maintained.
- Project entry documentation lives in the repository root; code-adjacent translations stay beside the code they describe.

## Directory Layout

- `docs/guides/`: setup and usage guides.
- `docs/reference/`: detailed configuration and behavior references.
- `docs/operations/`: debugging and troubleshooting.
- `docs/security/`: security documentation.
- `docs/architecture/`: internal design notes.
- `docs/channels/`: channel-specific integration guides.
- `docs/design/`: design proposals and investigations.
- `docs/migration/`: migration notes.

# PicoClaw Documentation

PicoClaw documentation is organized by document type. English is the source language; Chinese translations use the `.zh.md` suffix beside the source file.

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
- Project entry translations live in `docs/project/`; code-adjacent translations stay beside the code they describe.

## Directory Layout

- `docs/guides/`: setup and usage guides.
- `docs/reference/`: detailed configuration and behavior references.
- `docs/operations/`: debugging and troubleshooting.
- `docs/security/`: security documentation.
- `docs/architecture/`: internal design notes.
- `docs/channels/`: channel-specific integration guides.
- `docs/design/`: design proposals and investigations.
- `docs/migration/`: migration notes.

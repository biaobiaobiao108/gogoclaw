> Back to [README](../../../README.md)

# Photon iMessage

The Photon channel connects GoGoClaw to iMessage through [Photon Spectrum](https://photon.codes/). Photon supplies the Apple messaging line; GoGoClaw connects directly to Photon's public gRPC API in Go and does not need a Mac, public webhook, tunnel, Bun sidecar, or `ffmpeg`.

## Configuration

Get the `Project ID` and `Project Secret` from the Photon Dashboard, then configure:

```json
{
  "channel_list": {
    "photon": {
      "enabled": true,
      "type": "photon",
      "allow_from": ["+15551234567"],
      "typing": { "enabled": true },
      "placeholder": { "enabled": true, "text": ["Thinking..."] },
      "settings": {
        "project_id": "sp-...",
        "project_secret": "YOUR_PROJECT_SECRET",
        "disable_read_receipts": false
      }
    }
  }
}
```

| Field | Required | Description |
| --- | --- | --- |
| `enabled` | Yes | Enable the Photon channel. |
| `allow_from` | No | E.164 phone numbers, email addresses, or `*`. Empty denies every sender. |
| `project_id` | Yes | Spectrum Project ID from the Photon Dashboard. |
| `project_secret` | Yes | Project Secret generated for the same project. It is stored as a secure field. |
| `disable_read_receipts` | No | Do not mark received messages as read. |
| `group_trigger` | No | The shared mention-only or prefix rules used by other channels. |
| `typing.enabled` | No | Show the native typing indicator. |
| `placeholder.enabled` | No | Send an editable waiting placeholder. |
| `streaming.enabled` | No | Use native iMessage edits for best-effort streaming. |

`project_secret` is removed from channel configuration responses and is not written to `config.json`; the Web console stores it in `.security.yml`. `cloud_url` exists only as a hidden development override and should normally be left unset.

## Setup

1. Sign in to [Photon Dashboard](https://app.photon.codes/).
2. Create a project, enable Spectrum, and bind the phone number that will own the iMessage line.
3. Generate or rotate the Project Secret.
4. Open **iMessage** in the GoGoClaw channel page and fill in the Project ID and Project Secret.
5. Add authorized senders to `allow_from` in E.164 format, for example `+15551234567`, or use `*` only for an intentionally public channel.
6. Enable the channel and save.

The gateway fetches a short-lived line token from Photon, keeps the gRPC connection open, and refreshes the token automatically. It persists the event cursor and the chat-to-line mapping in `sessions/photon-state.json` so a dedicated-line project resumes without losing its route.

## Features

- Inbound and outbound iMessage text with native rich links.
- Direct and group conversations using the shared `allow_from` and `group_trigger` behavior.
- Inbound images, voice, audio, video, and document attachments through the normal media store and media tools.
- Outbound images, voice/audio, video, and documents, including captions.
- Native typing indicators, editable waiting placeholders, message edits, unsend, and 👀 reactions.
- Best-effort streaming through iMessage edits with conservative throttling.
- Read receipts by default, with an opt-out.
- Durable text delivery tracking and the same restart/retry handling as the other message channels.

The channel uses the public Photon iMessage protocol pinned in `pkg/channels/photon/proto/` and generated Go clients in `pkg/channels/photon/internal/`. The Go implementation is the production path; no runtime sidecar is started.

## Limits

- Photon's shared-line tier has a daily message quota and cannot start a new conversation with a number until that number has messaged the line first.
- iMessage edit and unsend operations are subject to Apple's time window and edit-count limits. Streaming is disabled by default; keep a conservative update interval if you enable it.
- Inbound and outbound attachments use the configured agent media limit (20 MiB by default). Larger inbound attachments are passed as metadata only.
- Photon availability still depends on outbound HTTPS/gRPC access from the GoGoClaw host. No public callback port is required.

## Troubleshooting

- **Token request failed**: confirm the Project ID and Project Secret belong to the same Photon project and that Spectrum is enabled.
- **No reply to a new number**: on a shared line, ask the recipient to message the assigned line first.
- **Messages are received but ignored**: check `allow_from`; an empty list rejects every sender. The configured value must match the sender's phone number or email address.
- **Group messages are ignored**: when `group_trigger.mention_only` is enabled, mention the configured bot name or address. Prefix triggers are also matched by the shared channel runtime.
- **Streaming is not appearing**: enable `settings.streaming.enabled` explicitly. The final reply is always delivered even when intermediate edits are throttled or rejected.
- **Messages remain Delivered**: clear `disable_read_receipts` to allow automatic read receipts.

## Live verification

The optional Photon integration tests use `GOGOCLAW_LIVE_PHOTON_PROJECT_ID` and `GOGOCLAW_LIVE_PHOTON_PROJECT_SECRET`. Set `GOGOCLAW_LIVE_PHOTON_CHAT_ID` only when testing a disposable message, edit, unsend, and image against a chat you control:

```sh
go test -tags integration -run '^TestLiveContract_Photon' -v ./pkg/channels/photon
```

Without `GOGOCLAW_LIVE_PHOTON_CHAT_ID`, the send tests are skipped and the read-only token, line discovery, catch-up, and subscription checks still run.

# Safe Web Note

A tiny WebSocket chat with client-side AES-GCM encryption and a shared password. The server stores and relays only encrypted payloads, keeping plaintext on the client.

## Features
- Client-side encryption (AES-GCM via Web Crypto)
- Shared password login (small group use)
- Message history persisted to `messages.txt`
- On connect, server sends the last 25 messages
- Image sharing: images are downscaled to fit 512px and sent as encrypted JPEG data URLs
- Markdown-lite rendering for links and images
- Ephemeral presence: online-user bar plus join/leave events with the sender's browser user agent
- Unlinked read-only WebSocket endpoint for observers
- Simple single-page UI served from `/`
- Easter-egg message effects: `{shine}`, `{flash}`, and `{runaround}`

## Dependencies

Server/runtime:
- Go `1.26.1+`
- Go module: `github.com/gorilla/websocket v1.5.3`
- Writable repo directory (server creates/appends `messages.txt`)
- `password_check.txt` in repo root (required at startup, served by `/check`)
- Available listen port `8080`

Browser/client:
- Secure context (`https://` or `localhost`)
- `Web Crypto` (`crypto.subtle`)
- `TextEncoder` / `TextDecoder`
- `WebSocket`
- `fetch`

Optional (service management):
- `bash`
- `systemd` / `systemctl`
- `sudo` (only for system-wide service install/uninstall)

## Quick Start

```bash
# from the repo root
printf 'change-me\n' > password_check.txt
go run .
```

Open:
- `http://localhost:8080/`

## Systemd (User Service)

Install and start the service (run from the repo root):

```bash
bash install-service.sh
```

To install as a system service (requires sudo):

```bash
sudo bash install-service.sh
```

Useful commands:
- `systemctl --user status safe-web-note.service`
- `systemctl --user restart safe-web-note.service`
- `systemctl --user stop safe-web-note.service`

Uninstall:

```bash
bash uninstall-service.sh
```

Uninstall system service:

```bash
sudo bash uninstall-service.sh
```

## How It Works
- The browser derives a key from the password (PBKDF2) and encrypts each message using AES-GCM.
- Each payload is `{sender, message}` JSON; `message` may contain text, a markdown link, or an embedded image.
- The server writes encrypted messages to `messages.txt` through one ordered writer and flushes the queue during normal shutdown.
- The server retains only the most recent 25 messages in memory and sends them to new connections.
- Join/leave events and the online-user list are ephemeral: they are broadcast live but never persisted or replayed.
- On plain HTTP to a non-`localhost` host, the page redirects to HTTPS because Web Crypto requires a secure context.

## Endpoints

| Path | Purpose |
| --- | --- |
| `/` | Single-page chat UI |
| `/ws?username=<name>&ua=<ua>` | Read-write WebSocket |
| `/ws-readonly` | Read-only WebSocket (see below) |
| `/status` | Plain-text client count and total message count |
| `/check` | Serves `password_check.txt` for the client-side password check |

## Image Sharing

Selecting an image attaches it to the next message. The client shrinks it to at
most 512px on the long edge, re-encodes it as JPEG (quality 0.8), embeds it as a
`data:` URL, and encrypts the whole payload like any other message. Only
`data:image/*;base64` and `http(s)` image URLs are rendered.

## Easter Eggs

Messages whose text is wrapped in a command are rendered with a special effect:

- `{shine}[text]` renders the text with an animated shimmer.
- `{flash}[text]` alternates the text and background colors.
- `{runaround}[text]` or `{runaround}(n)[text]` also spawns up to 20 text clones that bounce around the viewport, avoiding the UI controls.

Commands are matched only when the whole message body is wrapped, and text
containing a markdown image is ignored.

## Read-Only WebSocket

An unlinked read-only connection is available at:

```text
ws://localhost:8080/ws-readonly
```

Use `wss://` when the site is served over HTTPS. The endpoint receives the same
encrypted history, live messages, and system events as `/ws`. It does not decrypt
content, so the client still needs the shared password and the same client-side
decryption logic.

Read-only clients are incognito: the endpoint ignores any `username` parameter,
does not appear in online-user lists, and emits no join or leave events.

The server closes a read-only connection with WebSocket policy-violation code
`1008` if it sends a data message. The endpoint is intentionally not linked or
shown in the web UI. Its unlinked URL is not an authentication mechanism; message
confidentiality still depends on the shared encryption password.

## Files
- `main.go`: WebSocket server, persistence, history replay
- `index.html`: UI + client-side crypto + WebSocket client
- `messages.txt`: line-delimited encrypted messages
- `password_check.txt`: encrypted token used to verify the shared password (gitignored)
- `main_test.go`: server tests

## Notes / Limitations
- The server is intentionally dumb and untrusted; it does not validate or decrypt content.
- Anyone with the password can read all messages.
- No per-user accounts or access control beyond the shared password.

## TODO
- Integrity (per-sender)

## Development
- Edit `index.html` for UI/crypto behavior.
- Edit `main.go` for server behavior.
- Restart the server after changes.
- Run `go test ./...` to run the test suite; the WebSocket test binds a local port.

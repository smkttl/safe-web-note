# Safe Web Note

A tiny WebSocket chat with client-side AES-GCM encryption and a shared password. The server stores and relays only encrypted payloads, keeping plaintext on the client.

## Features
- Client-side encryption (AES-GCM via Web Crypto)
- Shared password login (small group use)
- Message history persisted to `messages.txt`
- On connect, server sends the last 25 messages
- Simple single-page UI served from `/`

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
- The server writes each encrypted message as one JSON line in `messages.txt`.
- On new connections, the server sends the most recent 25 messages.

## Read-Only WebSocket

An unlinked read-only connection is available at:

```text
ws://localhost:8080/ws-readonly?username=viewer
```

Use `wss://` when the site is served over HTTPS. The endpoint receives the same
encrypted history, live messages, and system events as `/ws`. It does not decrypt
content, so the client still needs the shared password and the same client-side
decryption logic.

The server closes a read-only connection with WebSocket policy-violation code
`1008` if it sends a data message. The endpoint is intentionally not linked or
shown in the web UI. Its unlinked URL is not an authentication mechanism; message
confidentiality still depends on the shared encryption password.

## Files
- `main.go`: WebSocket server, persistence, history replay
- `index.html`: UI + client-side crypto + WebSocket client
- `messages.txt`: line-delimited encrypted messages

## Notes / Limitations
- The server is intentionally dumb and untrusted; it does not validate or decrypt content.
- Anyone with the password can read all messages.
- No per-user accounts or access control beyond the shared password.

## TODO
- Integrity (per-sender)
- Image upload

## Development
- Edit `index.html` for UI/crypto behavior.
- Restart the server after changes.

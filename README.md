# Safe Web Note

A tiny WebSocket chat with client-side AES-GCM encryption and a shared password. The server stores and relays only encrypted payloads, keeping plaintext on the client.

## Features
- Client-side encryption (AES-GCM via Web Crypto)
- Shared password login (small group use)
- Message history persisted to `messages.txt`
- On connect, server sends the last 25 messages
- Simple single-page UI served from `/`

## Quick Start

```bash
# from the repo root
go run .
```

Open:
- `http://localhost:8080/`

## How It Works
- The browser derives a key from the password (PBKDF2) and encrypts each message using AES-GCM.
- The server writes each encrypted message as one JSON line in `messages.txt`.
- On new connections, the server sends the most recent 25 messages.

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

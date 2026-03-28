# Letter Sealing

This note summarizes what issue [#42](https://github.com/highesttt/matrix-line-messenger/issues/42) and issue [#54](https://github.com/highesttt/matrix-line-messenger/issues/54) currently tell us about Letter Sealing support in this bridge, and how that lines up with the current implementation.

## TL;DR

- The bridge supports both `LSON` and `LSOFF` accounts for login and messaging.
- Text messages work in all combinations: `LSON`/`LSOFF` direct messages, mixed groups, and business/bot accounts.
- Image, video, and file sending works for all user types. E2EE media uses encrypted OBS upload; plain media uses the `r/talk/m` post-send upload path.
- Transparent PNGs are composited onto a white background before upload, matching LINE native client behavior.

## Terms

- `LSON`: LINE user with Letter Sealing enabled.
- `LSOFF`: LINE user with Letter Sealing disabled.

## Current behavior matrix

| Scenario | Current status | Notes |
| --- | --- | --- |
| Bridge account is `LSOFF` and tries to log in | `WORKS` | Uses non-E2EE login flow: fresh RSA key, type 0 retry, JQ polling. |
| Bridge account is `LSON` and tries to log in | `WORKS` | Uses E2EE login flow: LF1 polling, ConfirmE2EELogin, key chain export. |
| Bridge account is `LSON`, receives direct message from `LSOFF` | `WORKS` | Confirmed in issue testing. |
| Bridge account is `LSON`, receives group message in a chat that includes `LSOFF` | `WORKS` | Confirmed in issue testing. |
| Bridge account is `LSON`, sends direct message to `LSOFF` | `WORKS` | Detects missing peer E2EE key and falls back to plain text send. |
| Bridge account is `LSON`, sends group message to a room that includes `LSOFF` | `WORKS` | Detects missing group shared key and falls back to plain text send. |
| Bridge account is `LSON`, sends to `LSON` only chats | `WORKS` | Uses full E2EE encryption path. |
| Bridge account is `LSOFF`, sends direct message to `LSON` | `WORKS` | Sends as plain text (E2EE not initialized). |
| Bridge account is `LSOFF`, sends to mixed groups | `WORKS` | Sends as plain text. |
| Bridge account is `LSOFF`, sends to bot/business account | `WORKS` | Sends as plain text. |
| Bridge account sends images/media to `LSON` | `WORKS` | E2EE encrypted media with keyMaterial in payload. |
| Bridge account sends images/media to `LSOFF` or plain chats | `WORKS` | Plain media uploaded via `r/talk/m/{msgId}` after sending. |

## How it works

### Login path

The bridge supports two login flows, selected automatically based on whether the LINE account has Letter Sealing enabled:

**LSON accounts (E2EE login):**
1. `loginV2` with `type: 2` and E2EE `secret` returns a verifier and PIN.
2. Background goroutine polls `LF1` endpoint until the user confirms on their phone.
3. LF1 returns `EncryptedKeyChain` and `PublicKey`.
4. `confirmE2EELogin` completes the E2EE handshake.
5. `loginV2WithVerifier` finalizes and returns access tokens + E2EE key material.

**LSOFF accounts (non-E2EE login):**
1. `loginV2` with `type: 2` and E2EE `secret` fails with code 89 "not supported".
2. Bridge fetches a fresh RSA key (LINE invalidates the previous one after the failed attempt).
3. Retry `loginV2` with `type: 0`, no secret, and the new RSA key returns a verifier and PIN.
4. Background goroutine polls `JQ` endpoint (not LF1) until the user confirms on their phone.
5. JQ returns `authPhase: "QRCODE_VERIFIED"`.
6. `loginV2WithVerifier` finalizes and returns access tokens (no E2EE data).

### Send path

The bridge determines E2EE capability per-chat before sending:

- If `lc.E2EE == nil` (LSOFF bridge account): all messages sent as plain text.
- For 1:1 chats: `ensurePeerKey` probes the peer's E2EE public key. If the peer is `LSOFF`, falls back to plain text.
- For groups: `fetchAndUnwrapGroupKey` attempts to get the group shared key. If not available (mixed group), falls back to plain text.
- E2EE capability is cached per-peer and per-group with a 1-hour TTL.

**Plain text media** uses a different upload flow than E2EE media:
- E2EE: encrypt data, upload to `r/talk/emi/{id}` (OBS), send message with OID in metadata.
- Plain: send message first, then upload raw data to `r/talk/m/{serverMessageId}`.

### Receive path

- `pkg/connector/handle_message.go` lazily fetches peer keys or group keys based on key IDs in incoming message chunks.
- Incoming messages from `LSOFF` users arrive as plain text and are handled without decryption.

## Test matrix

| Test case | Status |
| --- | --- |
| `LSOFF` bridge account login | Verified |
| `LSON` bridge account login | Verified |
| `LSON` -> `LSOFF` direct text send | Verified |
| `LSON` -> `LSON` direct text send | Verified |
| `LSOFF` -> `LSON` direct text send | Verified |
| `LSOFF` -> mixed group send | Verified |
| `LSOFF` -> bot/business account send | Verified |
| `LSON` -> mixed group send | Verified |
| `LSON` -> `LSON` group send | Verified |
| Incoming messages from `LSOFF` users | Verified |
| Incoming messages from `LSON` users | Verified |
| Image send to `LSON` (E2EE) | Verified |
| Image send to `LSOFF` / plain chats | Verified |
| Transparent PNG handling | Verified (composited onto white background) |

## References

- Issue [#42: login fails for accounts with letter sealing OFF](https://github.com/highesttt/matrix-line-messenger/issues/42)
- Issue [#54: failure to decrypt when sending message to user with letter sealing off](https://github.com/highesttt/matrix-line-messenger/issues/54)
- [About Letter Sealing | LINE Help Center](https://help.line.me/line/?contentId=50000087)
- [October, 2015 LINE Introduces Letter Sealing Feature for Advanced Security](https://www.linecorp.com/en/pr/news/en/2015/1107)
- [LINE Encryption Report (2024)](https://www.lycorp.co.jp/en/privacy-security/security/transparency/encryption-report/2024/)

# LINE Call Events — Protocol Analysis

Captured via CDP network interception of the LINE Chrome Extension (v3.7.2).

## How calls arrive

Calls are **not** a separate operation type. They arrive as regular `OpSendMessage` (type 25) or `OpReceiveMessage` (type 26) operations via the SSE stream (`/api/operation/receive`), with `contentType: 0` (text).

The call is identified by `contentMetadata["ORGCONTP"] == "CALL"`.

## Captured call event (incoming audio call, unanswered)

```json
{
  "revision": "953142",
  "createdTime": "1775456870419",
  "type": 25,
  "message": {
    "from": "<caller-mid>",
    "to": "<recipient-mid>",
    "toType": 0,
    "contentType": 0,
    "text": "Your OS version doesn't support this feature.",
    "contentMetadata": {
      "ORGCONTP": "CALL",
      "TYPE": "A",
      "RESULT": "CANCELED",
      "DURATION": "0",
      "CAUSE": "77",
      "VERSION": "M",
      "SESSION_ID": "EE410AB4-DED1-43DD-B84A-88C6E629447D"
    }
  }
}
```

## contentMetadata fields

| Field | Values | Meaning |
|-------|--------|---------|
| `ORGCONTP` | `"CALL"` | Identifies this message as a call event (original content type) |
| `TYPE` | `"A"` = audio, presumably `"V"` = video | Call type |
| `RESULT` | `"CANCELED"`, likely also `"ANSWERED"`, `"DECLINED"`, `"BUSY"` etc. | Call outcome |
| `DURATION` | `"0"` | Call duration in seconds (string) |
| `CAUSE` | `"77"` | Numeric cause code (77 = unsupported client) |
| `VERSION` | `"M"` | Protocol version indicator |
| `SESSION_ID` | UUID string | Unique call session identifier |

Note: Only `ORGCONTP`, `TYPE`, `RESULT`, and `DURATION` are confirmed from this single capture. The `RESULT` values other than `"CANCELED"` and `TYPE: "V"` are educated guesses — capture more call scenarios to confirm.

## Current bridge behavior

The bridge (`pkg/connector/handle_message.go:27`) switches on `contentType`. Since calls have `contentType: 0` (text), they pass through as regular text messages, delivering the fallback text ("Your OS version doesn't support this feature.") to Matrix.

## What to implement

1. In `queueIncomingMessage`, before processing text content, check `contentMetadata["ORGCONTP"] == "CALL"`
2. Convert to a Matrix notice with call info (e.g. "Audio call (missed)" / "Audio call (3:42)")
3. Use `TYPE` for audio vs video, `RESULT` for missed/answered, `DURATION` for length
4. Drop the fake `text` field — it's just a fallback for clients that can't render calls

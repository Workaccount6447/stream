# fsb-stream — streaming / file-serving service

Half 2 of the split project: reads files from Telegram and serves them over HTTP.
No bot commands live here — those are in **fsb-bot**.

## Routes
| Route | Purpose |
|---|---|
| `GET/HEAD /stream/<linkID>?hash=…` (`&d=true` to download) | serve a file, full Range support |
| `GET/HEAD /stream/<messageID>?hash=…` | legacy links from before the split (owner channel) |
| `GET /`, `HEAD /uptime` | health |

Responses for bad / expired links are proper **HTML pages**:
- `410` **Link expired** page (file past its time limit, revoked, or deleted) — `internal/web/templates/expired.html`
- `400/403/502` error page — `error.html`

## Setup
1. `fsb.sample.env` → `fsb.env`. `DATABASE_URL`, `HASH_SECRET`, `BOT_TOKEN`, `LOG_CHANNEL` **must equal fsb-bot's**.
2. **`BOT_TOKEN` must be the same bot as fsb-bot.** Files in a user's own channel can only be read by the bot the
   user made admin, so that bot is always used for them. Extra `MULTI_TOKEN*` workers are used only for owner-channel files
   (add them as admins of `LOG_CHANNEL`, or set `USER_SESSION` to auto-add them).
3. `go run ./cmd/fsb run` or `docker build -t fsb-stream .`
4. Put it behind HTTPS and point fsb-bot's `STREAM_HOST` at it.

## "Invalid file / page doesn't open" — what was wrong and is fixed
1. **Headers were written after `WriteHeader`** → `Content-Type`, `Content-Length` and `Content-Disposition` were silently dropped on most requests, so browsers couldn't open/play the file. Headers now go first.
2. **No `HEAD` route** → players/browsers/Telegram previews that probe with HEAD got 404. Added.
3. `Content-Disposition` broke on names with quotes, newlines or non-ASCII → now RFC 6266/5987 encoded; files without a filename (voice, video notes…) get a name from their MIME type.
4. Bad/unsatisfiable `Range` could panic or return garbage → proper `416`; multi-range falls back to the full file.
5. Message fetch could panic on unexpected Telegram response types / empty results → handled; deleted files show the expired page instead of a raw error.
6. Telegram file references expire → cache TTL cut from 1 h to 5 min.
7. Photos: the largest real size is picked (the last entry isn't always the biggest).
8. User-uploaded HTML/SVG served inline now gets `Content-Security-Policy: sandbox` + `nosniff`.
9. Errors are friendly HTML instead of plain text.

## Expiry behaviour
- `expires_at` passed → expired page immediately (does not wait for the bot's cleaner).
- Record gone but hash valid (cleaner already removed it) → expired page. The hash signs only the link ID, so it stays verifiable after deletion.

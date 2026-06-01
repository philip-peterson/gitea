# Gitea Git HTTP Receive-Pack Investigation (Interceptors branch)

**Date:** 2026-05-31 (ongoing)
**Branch:** `interceptor` (user's working branch, diverged from `pp/interceptor`)
**Reporter:** ironmagma
**Main file:** `routers/web/repo/githttp.go` (`serviceRPC`)

## Executive Summary

A change on the `interceptor` branch to support `MAX_PUSH_BLOB_SIZE` (zero-byte failure detection + nice error injection) introduced a regression in normal `git receive-pack` over HTTP.

The root cause of the initial symptom ("first receive-pack POST after successful `info/refs` immediately dies with 'remote end hung up unexpectedly'") was wrapping `ctx.Resp` with a `byteCountWriter` for the stdout of `git receive-pack` in the **normal path** (when `MaxPushBlobSize == 0`).

Removing that wrapper in the normal path fixed the "immediate first-POST death" symptom.

A secondary, deeper symptom remains for large pushes: the server-side `git receive-pack` dies mid-transfer ("unexpected disconnect while reading sideband packet" on client, "remote end hung up unexpectedly" on server). This is believed to be unrelated to the wrapper (or only indirectly related) and is likely caused by other changes on the interceptor branch (hooks, command execution, context handling, etc.).

`MAX_PUSH_BLOB_SIZE` is **not active** in the test environment (setting is 0 / default).

## Timeline of Symptoms & Fixes

### Original Symptom (pre-any of our changes)
- First `POST /git-receive-pack` after successful `info/refs` → immediate server "hung up".
- Retries produced "bad line length character: ???" on server-side git stderr.
- Client saw protocol errors and retry storms.

### Key Debugging Steps & Discoveries
1. Added heavy `[git-http-debug]` logging (later removed).
2. Ruled out:
   - Gzip on request body
   - Response compression (gzhttp) — we added `Content-Encoding: identity` defensively anyway
   - Panic recovery injecting error pages (added guard in `errpage.go`)
   - Sideband flag hardcoded to `true` in error injection (changed to `false`)
3. The `byteCountWriter` wrapper (added to detect `n==0` and inject `WriteReceivePackError`) was the direct cause of the first-POST regression.
   - It sat in the live stdout path for *every* receive-pack in the normal path.
   - Even with `Flush()` delegation added, it interfered with timely protocol responses / HTTP streaming.
4. Removing the wrapper in the normal path made the first POST succeed cleanly.
5. Current client symptom (large push, ~5.7 MiB, 10k objects):
   - Gets to 100% "Writing objects"
   - Then: `send-pack: unexpected disconnect while reading sideband packet`
   - Server: `git receive-pack` exits 128 with "remote end hung up unexpectedly"
   - Happens on a subsequent POST (after a successful first one in the latest run)

## Code Changes Made (kept in tree)

- Removed `byteCountWriter` stdout wrapping + related zero-byte injection logic from the **normal path** in `serviceRPC` (the source of the regression).
- Kept the wrapper + injection logic inside the `if MaxPushBlobSize > 0` interceptor path (where it belongs).
- Added early `WriteHeader(200) + Content-Encoding: identity` for all git smart protocol responses (defensive).
- Added `http.Flusher` support to `byteCountWriter` (still used in interceptor path).
- Added guard in `routers/common/errpage.go` so panic recovery never injects text/HTML into active `application/x-git-*` responses.
- Cleaned up all the temporary `[git-http-debug]` noise (code is quiet again).
- Minor: fixed an `undefined: err` scoping issue early in the investigation.

All real robustness improvements were retained; only the problematic wrapper usage was reverted in the normal path.

## Current Code State (normal receive-pack path)

```go
stdout := ctx.Resp   // direct, no wrapper

cmdErr := gitrepo.RunCmdWithStderr(ctx, ..., 
    WithStdinCopy(reqBody),
    WithStdoutCopy(stdout),
)

if cmdErr != nil && !gitcmd.IsErrorCanceledOrKilled(cmdErr) {
    log.Error(...)
}
```

Simple and reliable for the protocol stream. No post-mortem injection in this path anymore (that was the trade-off for correctness).

The interceptor path (`MaxPushBlobSize > 0`) still has its own (different) use of the wrapper inside `gitprotocol.ReceivePack`.

## Remaining Hypothesis for Current Failure

The server-side `git receive-pack` process is exiting abruptly during/after receiving a large pack (while the client is reading sideband status).

Likely causes (in rough order):
- Hook failure (pre-receive / update) causing git to abort the connection.
- Something in hook environment or push option handling on the interceptor branch.
- Context cancellation or command timeout on long-running receive-pack.
- Unrelated regression introduced elsewhere on the interceptor branch (git command execution, process management, etc.).

The fact that the client reaches 100% object writing but then dies while reading the response points to a problem *after* the pack is fully received (hook execution or post-unpack phase).

"Everything up-to-date" at the end of the client output is also suspicious and may indicate partial ref state or client-side retry confusion.

## Next Steps / Experiments (when user can test)

1. **Hook bypass test** (highest value):
   - Temporarily disable/rename hooks in the `templ` repo (or the whole instance).
   - Reproduce the push.
   - If it succeeds → problem is in hook execution / new hook-related code on this branch.

2. Check server logs (not just githttp) during the failing push for pre-receive output, errors from hooks, or internal Gitea push error messages.

3. Confirm exact config value of `MAX_PUSH_BLOB_SIZE` (even if "not set", double-check the rendered value at runtime).

4. If hooks are not the cause, look for changes on the interceptor branch in:
   - `modules/git/gitcmd` (WithStdinCopy / WithStdoutCopy behavior for large bodies)
   - `services/repository` (push / hook execution)
   - Any new code that runs for *every* receive-pack (not just the size-limit path)

5. (Optional) Re-introduce zero-byte error injection in a safer way in the normal path (using `MakeStdoutPipe` + separate reading instead of wrapping the live writer). Only do this after the current regression is fully understood.

## Useful Log Patterns to Watch

- First receive-pack POST after successful `info/refs`:
  - Should now succeed (no immediate "Fail to serve" error).
- Later POSTs during large push:
  - `cmdErr` value + whether `IsErrorCanceledOrKilled`.
  - Timing of "HTTPRequest completed" vs. the error log.
  - Whether the error happens before or after the client reaches "Writing objects: 100%".

## Files Changed During Investigation (high level)

- `routers/web/repo/githttp.go` — main battleground (wrapper removal + robustness headers)
- `routers/common/errpage.go` — panic recovery guard
- Temporary heavy debug logging (added then removed)

## Notes for Future Work on the Interceptor Feature

- The `byteCountWriter` + post-`RunCmd` injection pattern is fragile when placed in the live stdout path of `git receive-pack`.
- Prefer designs that let the real git process write its own protocol data whenever possible.
- The interceptor (`gitprotocol.ReceivePack`) already has its own sophisticated error handling — try to keep normal-path behavior as close to upstream as possible.

---

**Status as of last log (deeper root cause from Git source):** The two-POST behavior is *intentional* in Git's remote-curl.c (see `probe_rpc` + `post_rpc` around lines 879-956 in the Git tree at ~/Code/git):

- In `post_rpc`, the helper first tries to buffer the entire request from send-pack until the first flush pkt.
- If it doesn't fit in `http_post_buffer` (i.e. any real push with a packfile), `large_request=1`.
- Then it does a *synchronous probe* `POST` with a hardcoded `CURLOPT_POSTFIELDS "0000"` (Content-Length:4) **solely** to:
  - Force any auth handshake / 401 re-challenge to happen early.
  - Possibly negotiate 100-continue (for GSS/negotiate or explicit authtype).
  - "Prime" the keep-alive connection before libcurl starts the real (chunked, unknown-size) POST via a READFUNCTION callback.
- The probe response body is completely ignored (simple fwrite_buffer + discard). Git's own receive-pack, when fed exactly "0000" in stateless-rpc mode, takes the fast path in `read_head_info` (first packet_reader_read sees FLUSH → returns NULL commands), skips all work, writes **zero bytes** of status (use_sideband is never set because there was no cap line), and exits 0. Empty body is the expected and correct server response for the probe.

Previous peek code broke the *second* request's chunked body reader for exactly this flow. Removing it was necessary but not sufficient for long-term robustness.

**Additional improvement made:** Added explicit `http.Flusher.Flush()` immediately after `WriteHeader(200)` + the "identity" header in `serviceRPC` (the common RPC result path for both upload-pack and receive-pack, covering both the probe and the real POST). This ensures the 200 + headers are on the wire promptly over keep-alive *before* the client starts pumping the second request body or before a long-running receive-pack blocks. Matches the intent of the existing `byteCountWriter` (which already delegates Flush for the MAX_PUSH_BLOB_SIZE path) and the comments in Git's rpc_state about flush boundaries being request boundaries.

A short comment was left at the flush site (and the previous body-purity comment was tightened) explaining the Git client probe + keep-alive requirement.

This is the correct "something else to do" once the peek was gone: make header delivery timely for the degenerate probe case that Git deliberately issues on non-trivial pushes.

The change is small, targeted, and directly derived from reading the Git C sources for `probe_rpc` / `large_request` / `read_head_info`. No behavior change for small requests or when MaxPushBlobSize is active.

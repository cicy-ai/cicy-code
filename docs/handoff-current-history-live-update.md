# Current History Live Update Handoff

## Goal

Fix the agent inspector current-history experience for `w-20005`:

1. `history_id` must match the content shown in UI
2. live updates must be timely
3. UI must not jump around
4. spacer height behavior must be stable

## What Is Already Done

### 1. `history-ids?limit=5` was removed from live update flow

Current intent in frontend:

- `history-ids` only for:
  - first screen load
  - `loadMore`
- live update should not rely on `history-ids`

Main file:

- `app/src/components/chat/CurrentHistoryView.tsx`

### 2. Backend `current_updated` now includes `item`

Backend was changed so ws `current_updated` tries to include full turn item:

- file: `api/mgr/ai_gateway_audit.go`

Both start snapshot path and complete path now build:

- `history_id`
- `item`

using:

- `agentHistoryLoadItemByID(...)`
- `agentInspectorAttachCurrentHistoryToolMeta(...)`

### 3. Frontend was changed to prefer ws `item`

`CurrentHistoryView.tsx` now has a branch:

- when `msg.type === "current_updated"`
- if `msg.data.item` exists
- write to IndexedDB
- merge directly into `items`

This is the right direction.

### 4. Duplicate sqlite open/close was reduced

Backend history sqlite access was partially improved:

- `api/mgr/agent_history_sqlite.go`
- `api/mgr/agent_inspector.go`

Changes made:

- cache DB handle by `agentID`
- stop `defer db.Close()` on hot paths

Reason:

- there were `SQLITE_BUSY` / `database is locked` errors

## What Is Still Not Finished

### 1. Same logical turn still becomes multiple `history_id`s

This is the biggest unresolved backend problem.

Observed behavior:

- same question, for example `hi1`
- gets multiple history rows / ids
- one early row may be `thinking`
- later row may be `text`

Example observed:

- one id with `q="hi1", a="", status="thinking"`
- another id with `q="hi1", a="hi1", status="text"`

This means history storage is still snapshot-like, not stable-turn-like.

Correct target:

- one logical turn => one stable `history_id`
- later updates should update that row, not append a new row

Likely files to fix:

- `api/mgr/agent_history_sqlite.go`
- `api/mgr/ai_gateway_audit.go`

Likely functions to inspect:

- `aiGatewayAppendMessageRecord`
- `agentHistoryUpsertRecordWithItem`
- merge target / turn key logic

### 2. Live update path is not fully reduced to ws-only

The intended direction is ws-only for live updates, but frontend file still contains leftover code and dead state from older approaches.

In `CurrentHistoryView.tsx`, inspect and simplify:

- `getHistoryTurn(...)`
- `getCurrentHistory(...)`
- `refreshTimersRef`
- `stalledRefreshRef`
- `pendingHistoryTurnRequestsRef`
- `recentHistoryTurnFetchAtRef`

Even when some of this is no longer active, the file still has too much leftover logic and should be cut down hard.

### 3. Scroll / anchor / spacer logic is still too complicated

User wanted:

1. new Q appears immediately
2. with `Thinking...`
3. new turn anchors near top
4. spacer decreases as answer grows
5. UI should not jump around

What happened during debugging:

- scroll-bottom and top-anchor logic fought each other
- spacer got reset
- UI jumped

Current state:

- some scroll logic was already reduced
- but file still has too much state and too many refs

Main refs to review:

- `pendingAnchorTurnKeyRef`
- `lastAnchoredHistoryIDRef`
- `activeSpacerTurnKeyRef`
- `anchorSpacerHeightRef`
- `forceScrollBottomRef`
- `shouldStickBottomRef`

Recommendation:

- keep only one simple rule:
  - only scroll/anchor when a new turn id appears
  - plain content updates for current turn should not trigger scroll policy changes

### 4. UI still showed repeated same-question rows

Frontend currently tries to dedupe by question text in `normalizeHistoryTurns(...)`.

This is only a band-aid.

It can hide some duplicates in UI, but the real bug is backend storage creating multiple ids for one logical turn.

## Concrete Findings From Debugging

### A. `history-turn` returned valid data, but UI still did not update

One concrete bug found:

- frontend called `/history-turn`
- response returned correct `steps`
- but UI still did not change

Reason found at that time:

- `refreshTurnByID(...)` used shared `requestSeqRef`
- response could be dropped as "stale"

That specific bug was adjusted, but the larger conclusion is:

- live updates should not depend on this pull path

### B. Multiple requests for same `history_id`

Observed:

- same `history_id` requested repeatedly

Contributing causes during the debugging process:

- ws `current_updated` repeated
- frontend dedupe used `updated_at`
- fallback refresh paths retriggered

Direction already chosen:

- do not rely on repeated `history-turn` pulls for live updates

### C. `SQLITE_BUSY` happened on history endpoints

Observed:

- `/api/agents/history-ids/...`
- `/api/agents/history-turn/...`

could return:

- `database is locked (5) (SQLITE_BUSY)`

Reason:

- history sqlite file was being opened/closed repeatedly on hot paths

Partial mitigation already applied:

- reuse DB per agent

Still worth retesting after storage model changes.

## Files Most Relevant For Next Person

- `app/src/components/chat/CurrentHistoryView.tsx`
- `app/src/components/Workspace.tsx`
- `api/mgr/ai_gateway_audit.go`
- `api/mgr/agent_history_sqlite.go`
- `api/mgr/agent_inspector.go`

## Recommended Next Steps

1. Fix backend data model first
   - make one logical turn map to one stable `history_id`
   - stop creating multiple rows for one question lifecycle

2. Then simplify frontend live flow
   - ws `current_updated` with full `item`
   - merge directly into UI
   - no live `/history-turn` pulls

3. Then simplify scroll behavior
   - only react to new turn id
   - do not re-run scroll policy on plain content growth

4. Only after that, tune spacer behavior
   - otherwise spacer debugging is polluted by duplicate ids / duplicate rows / stale updates

## Important Context About User Expectation

User explicitly wants:

- simple real-time push
- not a pile of fallback code
- not repeated polling
- not duplicate same-turn ids
- not UI jumping during live updates

The main complaint was valid: this feature became overcomplicated and drifted away from the simple push model.

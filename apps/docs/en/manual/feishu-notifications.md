---
title: Feishu Account Event Notifications
description: Configure a Feishu custom bot in Manager Server for reauthentication, quota exhaustion, and explicit recovery events.
---

# Feishu Account Event Notifications

Manager Server can send account events through a Feishu custom bot Webhook. Notifications are disabled by default. Administrators can configure the bot under **System Information → Feishu Account Event Notifications**, or manage configuration through environment variables and Secret files. The lightweight static panel cannot send background notifications independently. Bot credentials are not persisted in browser storage.

## Events And Evidence

- **Reauthentication required:** request collection or server inspection explicitly identifies expired, invalid, or revoked credentials. Generic `401/403`, regional restrictions, and network failures do not qualify.
- **Authentication recovered:** a later successful request or explicitly healthy server inspection for the same Provider and `auth_index`.
- **Quota exhausted:** a supported normalized window reaches `100%`, or requests contain explicit Codex `usage_limit_reached` or xAI free-quota exhaustion evidence. A generic `429` is insufficient.
- **Quota recovered:** a newer valid observation for the same account, window, and model scope is below `100%`. A successful request or the expected reset time alone does not clear a known exhausted window.

Local Codex/xAI inspections and manual or scheduled server inspections can produce events. Local inspections require an administrator session connected to Manager Server and upload normalized results without raw response bodies. Other providers depend on normalized evidence from request collection or active quota lookup. xAI inspection uses stable weekly, monthly, and pay-as-you-go windows, excluding product rows identified only by position.

Missing or invalid percentages, expired windows, observations older than 24 hours, excessive clock skew, and evidence older than the current state are ignored. A 45-minute inspection remains eligible. Within a batch, only the latest valid observation per account and dimension is used; separate batches still evaluate transitions independently. The first healthy result establishes a baseline; a fresh explicit initial failure can notify. Events require a stable `auth_index`; a changed index after login is treated as a new credential.

Codex exhaustion without a specific window is labeled `unknown`. Recovery of another window does not clear it. Unknown-window failures have a 24-hour cooldown per account and scope; fresh failures after that period may notify again. Repeated evidence does not extend the cooldown, and time alone does not imply recovery. The notification threshold is always `100%`, independent of inspection thresholds such as `90%`, automatic disable settings, or the account action queue.

## Page Configuration

Log in with an administrator key and open the notification card in System Information:

1. Enable notifications and enter the full bot Webhook URL and optional signing secret.
2. Select reauthentication, authentication recovery, quota exhaustion, and quota recovery events.
3. Save to apply immediately without restarting.
4. Send a test message. Tests are at least five seconds apart and require saved settings.
5. Review the latest 20 delivery records, including event, status, attempts, and fixed error categories.

![Administrator notification settings](/img/feishu-notifications-desktop.png)

The Webhook and signing secret are encrypted in SQLite with the existing `data.key`; APIs return configured flags rather than credentials. Empty fields preserve saved values. Explicit removal controls clear the signing secret or Webhook; removing the Webhook also clears the secret and disables notifications. Read-only sessions cannot read settings, change them, submit local inspections, or send tests, and do not see this card.

Any configured Feishu environment variable or Secret file path makes the entire group environment-managed and locks page editing. Administrators can still test delivery and view history. Environment configuration takes precedence over database settings.

## Bot And Environment Configuration

Add a custom bot to a Feishu group and obtain its Webhook. Configure the signing secret when signature verification is enabled. For keyword verification, every message contains `CPA Manager Plus`.

```dotenv
CPA_MANAGER_FEISHU_ENABLED=true
CPA_MANAGER_FEISHU_WEBHOOK_URL_FILE=/run/secrets/feishu_webhook_url
# Optional: only when signature verification is enabled.
CPA_MANAGER_FEISHU_SIGNING_SECRET_FILE=/run/secrets/feishu_signing_secret
```

The two files contain the complete URL and signing secret respectively. Mount them read-only and restrict access. Direct `CPA_MANAGER_FEISHU_WEBHOOK_URL` and `CPA_MANAGER_FEISHU_SIGNING_SECRET` values are also supported; nonempty direct values take precedence over files. Explicitly configured files that are unreadable or empty prevent startup.

Only mainland Feishu URLs matching `https://open.feishu.cn/open-apis/bot/v2/hook/<token>` are supported. International Lark is unsupported, and redirects are not followed. Environment changes require restarting or recreating Manager Server through the normal deployment process.

## Testing And Delivery

With the same binary version and database/data-key configuration as the service, run:

```sh
cpa-manager-plus test-feishu-notification
```

This command sends a real test message. It prefers environment settings, otherwise reading saved page settings from the existing database and data key in read-only mode. It does not create or migrate a database, create keys, record delivery history, or start collectors. Success requires both a successful HTTP status and a Feishu JSON business code of `0`. Starting the service does not send a test message.

Messages include the event, Provider, full account email, account index, source, confirmed failure HTTP status, quota window, observation time, and known reset time. Authentication failures include the classified reason and reauthentication advice. Emails come from existing account snapshots and are lowercased. Values identical to credential filenames are excluded; missing emails are shown as unknown and are never inferred from filenames. Older queued messages without these fields remain compatible. Times use Beijing time (UTC+08:00). Raw upstream errors, tokens, Management Keys, Webhook URLs, and signing secrets are excluded.

State and queued messages are stored in `account_health_notification_state` and `account_health_notification_outbox`. Shared state deduplicates repeated failures across local/server inspections, request collection, and restarts. Disabled event types still update the baseline while notifications are enabled, but do not send; corresponding pending events are skipped. Each stream retains delivery order. A separate worker attempts at most one message per second, with a ten-second timeout and at most five attempts. Temporary failures retry after 60, 120, 240, and 480 seconds; explicit configuration or business rejections stop retries.

Completed records are retained for 30 days from creation and cleaned after worker processing; pending records are not removed by that retention rule. **Outbox payloads store full email addresses in plaintext**, so protect the database and backups as account data. The recent-history API returns delivery metadata without payloads or emails. Logs contain only message IDs, attempt counts, and fixed error categories. Delivery is not exactly once: a crash before acknowledgment or an ambiguous timeout can cause duplicates.

Notifications do not add upstream polling. Without requests, active quota queries, or inspections, changes cannot be discovered promptly. Delivery failures do not change account enablement or inspection results.

## Disable And Roll Back

For page-managed settings, disable and save to stop observation and delivery immediately. For environment-managed settings, set `CPA_MANAGER_FEISHU_ENABLED=false` and restart through the normal deployment process. Existing records remain; re-enabling resumes pending delivery. Changing the Webhook sends pending messages to the new bot. Older application versions can leave the additional tables in place.

## Observation And Troubleshooting

Inspection evidence uses each account's probe completion time and a 24-hour freshness window. Local browser clocks may be up to five minutes ahead; other observations allow one minute. Accepted future local observations are clamped to server receipt time for health state and quota snapshots, preventing normal later evidence from appearing stale. Original timestamps are retained only for local retry deduplication. Inspection `created_at_ms` remains the database insertion time.

The local reporting endpoint selects the latest result per account in each batch. Expired results do not enter quota history. Responses retain `accepted` and include `acceptedCount`, `skippedCount`, and `skippedReasons` (`expired`, `clockAhead`, `superseded`, `missingIdentity`). Accepted counts describe processed results, not delivered notifications. The page aggregates warnings across batches and does not re-upload results completed before the page opened. Fresh local Codex quota snapshots are synchronized independently of the notification switch.

The shared cache holds up to 16384 event streams. Repeated states advance only the in-memory observation time. State transitions and queued messages persist in the same transaction, with at most one transaction per request-event batch. Streams beyond cache capacity use the database path; restarts restore the latest persisted state.

If database notification settings cannot be decrypted or the Webhook is invalid, Manager Server keeps running with notifications disabled and displays a configuration fault. Readable but invalid URLs can be corrected or removed on the page. For unreadable settings, restore the original data key and restart, or explicitly select full replacement and enter complete new settings. An empty signing secret during replacement removes the old secret. Explicit replacement plus Webhook removal clears and disables notifications. Invalid explicit environment settings still prevent startup and cannot be changed on the page. This fallback applies only to optional notification settings.

# Fallback notification to Microsoft Teams and Zoom

Besides e-mail and Telegram, the fallback notification (and the notice about an incident being acknowledged or
resolved) can go to a Microsoft Teams channel and to a Zoom Team Chat chat. Both channels
work through incoming webhooks, so they have no personal addresses: the message is received by a
channel or chat, not by an individual person.

Where messages go:

- **Team channel.** In the team dialog ("Teams" → team), the "Microsoft Teams webhook" and
  "Zoom chat endpoint" fields. A team that has any channel of its own (mailbox, Telegram chat, Teams
  webhook or Zoom) gets the notification in that channel and to its lead, not to every member —
  including a team that only has a Teams or Zoom webhook.
- **"Always also notify".** In the "Settings → Alerting" card — the "Teams webhooks"
  and "Zoom endpoints" lists, one URL per line (up to 50).

Both channels are turned on with switches in the "Channels" card of the same settings; a test can be
sent to any webhook with the "Teams" and "Zoom" buttons in the "Send a test" section (after
saving).

## Microsoft Teams

The message is an Adaptive Card 1.4: the subject in bold, the message text line by line, and the
"Acknowledge", "Open in Umbrella" and "Grafana" buttons (if the links exist: the Umbrella address and
a Grafana dashboard are set). The text and subject come from the same templates as the e-mail ("Subject" and "Text" in
"Message templates"). Any 2xx response counts as successful delivery.

How to get the URL (the recommended way is Workflows; Microsoft is retiring the old Office 365
connectors):

1. In Teams, open the team channel → "…" on the channel → **Workflows**.
2. Choose the **"Post to a channel when a webhook request is received"** template (trigger
   "When a Teams webhook request is received").
3. Name the flow (for example "Umbrella"), choose the team and channel, press **Add workflow**.
4. Copy the URL shown (`https://….logic.azure.com/workflows/…/triggers/manual/paths/invoke?…&sig=…`
   or `https://….environment.api.powerplatform.com/…`) and paste it into the team field or
   into "Teams webhooks".

The old **Incoming Webhook** (`https://<tenant>.webhook.office.com/webhookb2/…`) also works
as long as Microsoft supports it.

If the Workflows flow posts on behalf of a user, check that this user
has access to the channel. For private channels, use a flow with the "Post card in a chat
or channel" action.

## Zoom Team Chat

The message is sent in the `full` format: the header is the subject, the body is the message text (links in
the text). The address gets the `format=full` parameter appended, and the verification token goes in the
`Authorization` header.

How to get the address and token:

1. A Zoom administrator installs the **Incoming Webhook** app from the Zoom App Marketplace.
2. In the desired Team Chat channel, type `/inc connect umbrella` (any connection name).
3. The Incoming Webhook bot replies in the chat with the **Endpoint**
   (`https://integrations.zoom.us/chat/webhooks/incomingwebhook/…`) and the **Verification Token**.
4. Paste the Endpoint into the team's "Zoom chat endpoint" field or into "Zoom endpoints", and the Verification
   Token into the "Verification token" field of the Zoom card in "Settings → Alerting". There is one token
   for all connections of the app; it is saved in OpenBao and is not shown in the interface
   (`has_zoom_token`); leaving the field empty when saving keeps the saved token.

## Address security

A webhook URL by itself grants the right to post to the channel, so it is a secret:

- The API and the interface accept only `https://` addresses.
- In the incident timeline, the application log, the audit log, the "Where an incident
  goes" block, the incident card and the test response, the address is shown shortened:
  `https://host/…abcd` (scheme, host and the last four characters).
- Full addresses are stored in the configuration (like the other addresses) and in the incident record —
  so that the acknowledgement notice can be sent to the same place. `GET /api/notifications`
  (`settings.alerting:view`) returns them in full so that an administrator can edit them;
  `GET /api/teams` returns team addresses in full only to those with `teams:edit`,
  to everyone else — shortened.
- The Zoom token is stored only in OpenBao (`notify/zoom_token`).

## API

- `PUT /api/notifications`: `teams: {enabled}`, `zoom: {enabled, token}`, `extra_teams: []`,
  `extra_zoom: []`. Errors: `teams_invalid`, `zoom_invalid` (not an https URL),
  `zoom_token_required` (Zoom is on but there is no token). The response contains `has_zoom_token`.
- `POST /api/notifications/test`: `{channel: "teams" | "zoom", to: "<https URL>"}` — the address
  is required (the user has no address of their own).
- `POST/PUT /api/teams[/{id}]`: the `teams` and `zoom` fields (https URL, an empty string removes it).
  Errors `invalid_teams`, `invalid_zoom`.

Old saved settings are read unchanged: the new channels are off and the lists are empty.

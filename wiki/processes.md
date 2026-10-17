# Beacon: business processes

Beacon turns alerts from monitoring systems into incidents, finds who owns them, pages those people and runs the
response. How Beacon works with Keystone, Ledger and Current is in [family.md](https://onedev.evcsbrw.ru/Monitoring/Current/~files/main/docs/family.md); this page is about the
processes inside Beacon.

## Roles in the processes

| Who | Beacon role (given in Keystone) | Does |
| --- | --- | --- |
| Administrator | Administrator | installs, connects sources and other applications, defines roles |
| Integration administrator | Integration administrator | connectors, credentials, RED/USE rules, monitoring systems, inventory sources |
| NOC lead | NOC lead | teams, people's teams and scope, wallboards, alerting checks |
| On-call engineer | On-call engineer | acknowledges, resolves and comments on incidents, maintenance windows |
| Service owner | Service owner, scope "services of my teams" | watches and acknowledges incidents of their services |
| Monitoring system, Current | service account token or connector token | sends events |

## B1. Install and first steps

1. The administrator starts the container; the setup code is printed to the log.
2. The setup wizard asks for the language, theme and time zone, OpenBao, PostgreSQL, the public address and the
   Keystone connection key, and writes `umbrella.json`.
3. Whoever Keystone gives the `admin` role signs in and is the administrator.
4. The "First steps" checklist leads through: Keystone, a source, the first event, a team, the catalogue, delivery
   and the public address.

## B2. Connect a monitoring system

1. The integration administrator picks a template (Alertmanager, Grafana, Graylog, Zabbix, Current, webhook) or
   builds a connector in the low-code builder.
2. Beacon creates the intake token, the connector and its address in one step.
3. The administrator points the monitoring system at the address (for Zabbix: imports the media type).
4. A test event shows how the connector normalizes it and which CI it lands on.

## B3. Event to incident

1. A monitoring system or Current sends an event to `/api/ingest/<slug>`.
2. Beacon checks the network, rate, size and token, stores the request and answers the source.
3. The connector normalizes the event: severity P1–P5, signal, CI hints.
4. Beacon finds the CI: source reference, ID, name, short name, IP, DNS name, aliases.
5. Events with the same CI and signal collapse into one incident; a resolve event resolves it.
6. An event for an unknown CI waits; it is bound as soon as the CI appears (from Ledger or by hand).

## B4. Route and page

1. The route is CI → business service → owning team → people (or the CI's responsible people).
2. Incidents in a maintenance window or below the severity threshold are recorded, not paged.
3. Beacon sends the incident to PagerDuty with the team's or service's routing.
4. If PagerDuty does not accept it in time (or is not connected), Beacon sends fallback notifications by e-mail,
   Telegram, Teams and Zoom with a signed acknowledge link.
5. Acknowledge and resolve, in Beacon or in PagerDuty, update the timeline and notify the same addresses.

## B5. Respond

1. The response policy runs for each open incident: escalation steps, then a war room in Teams, a Zoom bridge, a
   Jira task, a voice call, and a postmortem with a due date.
2. People can run any of these actions by hand from the incident.
3. From the incident, "Traffic" opens Current and "Grafana" opens the context dashboard.

## B6. Keep the catalogue right

1. Physical CIs come from Ledger every 15 minutes (or from a real NetBox where Ledger is not used, never both:
   Beacon refuses to turn one on while the other is on, and pauses NetBox at start if both were on);
   computers can come from LDAP / Active Directory and hosts from Zabbix / Prometheus. A new device is created
   in Ledger ("Create the device in Ledger" on the incident and CI cards), not as a second record in Beacon;
   Beacon writes nothing to NetBox while Ledger is the source ([ledger.md](ledger.md#one-inventory-source)).
2. People build business services from CIs and give each service an owning team.
3. Current proposes dependencies seen in traffic; people accept or reject them on the CMDB map.
4. Beacon sends the alert state of Ledger's devices back to Ledger every 30 seconds.

## B7. Teams, scope and service accounts

1. People and their roles come from Keystone; the NOC lead adds people to teams and sets their visibility scope
   (all services, services of their teams, chosen services).
2. Teams get a mailbox and a Telegram chat for fallback notifications.
3. Other systems call Beacon with service-account tokens; every account has a person as owner, and its tokens stop
   working while Keystone has the owner disabled.

## B8. Plan maintenance

1. An on-call engineer or service owner creates a maintenance window for CIs or services in their scope.
2. During the window events are recorded but do not page; when it ends, open incidents page as usual.

## B9. Operate

1. Settings → Status shows the health of every dependency.
2. The state can be moved to another PostgreSQL and the secrets to another OpenBao from the settings pages.
3. Backups are PostgreSQL backups plus the data folder (Beacon has no backup job of its own).

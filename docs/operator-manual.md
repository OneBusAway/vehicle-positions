# Operator Manual

This manual is for the person who runs the service day to day: an operations
manager or dispatcher at a transit agency. You do not need to write code, use a
terminal, or log into a server. Everything here happens in a web browser or on a
driver's phone.

If you are the person who has to *install* the software, read
[`deployment.md`](deployment.md) instead — it covers servers, databases,
certificates and the settings named in this manual. Where something is a
settings change rather than a click, this manual says so and points you at your
IT contact.

Throughout, **bold** text is exactly what you will see on the screen.

---

## 1. What the system does

Drivers carry an Android phone running the Vehicle Tracker app. When a driver
starts a trip, the phone sends its location to your server every few seconds.
The server keeps the current position of every vehicle and publishes it as a
standard GTFS Realtime **Vehicle Positions** feed — the same format OneBusAway
and other rider apps already understand. You manage the fleet, the accounts and
the assignments from a web page at `/admin` on the same server.

```
  ┌──────────────────┐      ┌──────────────────┐      ┌──────────────────┐
  │  Driver's phone  │      │   Your server    │      │  OneBusAway or   │
  │                  │─────▶│                  │─────▶│  any GTFS-RT     │
  │  Vehicle Tracker │  GPS │  Admin UI /admin │ Feed │  rider app       │
  │  app, on a trip  │      │  + GTFS-RT feed  │      │                  │
  └──────────────────┘      └──────────────────┘      └──────────────────┘
```

Two things are worth knowing up front:

- A vehicle appears in the feed only while a phone is reporting for it. When
  reports stop, the vehicle drops out of the feed after the **staleness
  threshold** (5 minutes unless your IT contact changed it).
- Positions are not stored on the phone when the network is down. Fixes taken
  during an outage are lost, not sent later.

---

## 2. Signing in

1. In a browser, go to `https://your-server/admin`.
2. You land on the sign-in page, headed **Transit Tracker** / **Fleet
   operations sign in**.
3. Enter your **Email** and **Password** and click **Sign in**.

The very first admin account is created during installation, from the
`ADMIN_BOOTSTRAP_EMAIL` and `ADMIN_BOOTSTRAP_PASSWORD` settings — see
[`deployment.md` §4.4](deployment.md#44-after-the-first-sign-in). If nobody has
signed in yet, ask your IT contact for those credentials. After you sign in,
create a real account for yourself and every other person who needs one
(section 5).

**If sign-in fails.** A wrong password, an unknown email address, and a
deactivated account all show the same message, **Invalid email or password.** —
this is deliberate, so an outsider cannot learn which email addresses exist. If
you see **Too many attempts, try again shortly.**, wait a minute: the server
allows only a handful of sign-in attempts per email address and per network
address in any one minute, and then makes you pause.

**Changing your own password.** There is no separate profile page. Go to
**Users**, click **Edit** on your own row, type a new password into **New
password (leave blank to keep current)**, and click **Save changes**. Passwords
must be at least 8 characters.

Once signed in, the left sidebar takes you to **Live Map**, **Dashboard**,
**Vehicles**, **Users** and **Trips**. **Sign out** is at the top right of every
page.

---

## 3. Dashboard

**Dashboard** is the one-screen health check. Four numbers across the top:

| Card | What it counts |
|---|---|
| **Active Fleet** | Vehicles you have registered and not deactivated. It does not mean they are moving. |
| **Active Now** | Vehicles that have reported a position within the staleness threshold. These are the ones in the feed right now. |
| **Drivers** | Accounts with the driver role that are not deactivated. |
| **Active Trips** | Trips that have been started and not yet ended. |

Below them is a thin strip with two more:

| Field | What it means |
|---|---|
| **Feed last updated** | How long ago *any* vehicle the server is still tracking last reported — "just now", "3 min ago". It is **never** whenever the server is tracking no vehicles at all, which in practice means nothing has reported for roughly the last 5 to 10 minutes: the server forgets a vehicle once it passes the staleness threshold. **never** is also what you see after a restart if nothing has reported in the last few minutes. Treat it as "recently" or "not recently", not as a long-term record. |
| **Staleness threshold** | How long a vehicle may go without reporting before it drops out of the feed and out of **Active Now**. Default 5 min; changing it is a server setting (`STALENESS_THRESHOLD`), not a UI control. |

The **Recent Activity** table lists the ten most recently reporting vehicles
with their **Vehicle** label, **Route** (the route of their current trip, or a
dash if they have none) and **Last Update**. **View all →** goes to the vehicle
list. When nothing has reported lately it says **No vehicles have reported
recently.**

The healthy picture during service hours is: **Active Now** roughly equal to the
number of buses you expect out, **Active Trips** matching it, and **Feed last
updated** saying "just now".

---

## 4. Vehicles

**Vehicles** lists every vehicle under the heading **All Vehicles**, with
columns **ID**, **Label**, **Agency tag**, **Status**, **Last seen**, **Driver**
and **Actions**. By default only active vehicles are shown; **Show deactivated**
adds the deactivated ones and turns into **Hide deactivated**. Long lists are
paged with **← Previous** / **Next →** at the bottom.

### Add a vehicle

1. Click **New vehicle**.
2. Fill in **Vehicle ID**. This is the permanent identifier that goes into the
   feed, so use the id your agency already uses for the bus — a fleet number,
   for example.
   - Allowed characters: letters, digits, dots, hyphens and underscores.
   - Maximum 50 characters.
   - No spaces, no accented letters, no slashes. If you use anything else the
     form comes back with **vehicle id must contain only alphanumeric
     characters, dots, hyphens, and underscores**.
   - Ids must be unique. Reusing one gives **vehicle id already exists**.
3. Fill in **Label** — the human name shown in the admin screens and on the map
   ("Bus 12", "Minibus 7").
4. Fill in **Agency tag** if you run more than one operator or depot through one
   server. It is a free-text grouping label; leave it blank if you do not need
   it.
5. Click **Create vehicle**. You return to the list with **Vehicle created.**

### Edit a vehicle

Click **Edit** on the row. You can change **Label** and **Agency tag** and click
**Save changes**. **Vehicle ID** is greyed out and cannot be changed — it is
baked into the feed and into every location record already stored. If a vehicle
genuinely needs a different id, create a new one and deactivate the old one.

### Deactivate and reactivate

There is no delete button, by design. Use **Deactivate** on the row; a
confirmation asks **Deactivate this vehicle?** The vehicle disappears from the
default list, can no longer be assigned to a driver, and stops being offered to
drivers in the app. Its history is kept. To bring it back, click **Show
deactivated**, then **Activate** on the row (**Reactivate this vehicle?**).

Deactivating does not stop a trip that is already running.

### Export location history

The **CSV** link on each row downloads that vehicle's recent location points as
a spreadsheet file named `<vehicle-id>_locations.csv`. It contains the last
**24 hours**, up to **1,000 points**, with one row per position:

| Column | Meaning |
|---|---|
| `timestamp` | When the phone recorded the fix (Unix seconds). |
| `latitude`, `longitude` | Where it was. |
| `bearing` | Direction of travel in degrees, if the phone reported one. |
| `speed` | Metres per second, if the phone reported one. |
| `accuracy` | The phone's own estimate of how far off the fix may be, in metres. |
| `trip_id` | The GTFS trip id the driver entered, if any. |
| `received_at` | When your server received the point. |

Empty cells mean the phone did not supply that value. For anything older or
larger than that — a full month of one vehicle, for instance — ask your IT
contact; it is a database query, not a button.

---

## 5. Drivers and admins

**Users** lists every account under **All Users**, with **Name**, **Email**,
**Role**, **Status**, **Vehicles** (how many are assigned) and **Actions**.

There are exactly two roles:

| Role | Can do |
|---|---|
| **Driver** | Sign in to the Android app, see their assigned vehicles, start and end trips, report locations. Cannot open `/admin` at all. |
| **Admin** | Everything in the admin web UI: vehicles, users, assignments, map, trips, CSV export. |

### Create an account

1. Click **New user**.
2. Fill in **Name**, **Email** and **Password**. The password must be at least
   8 characters, or you get **password must be at least 8 characters**.
3. Choose **Role** — **Driver** or **Admin**. New accounts default to
   **Driver**.
4. Click **Create user**. A duplicate address gives **email already exists**.

Give the driver their email address and password in person or by whatever
channel your agency uses; there is no invitation email and no self-service
password reset.

### Reset a password

1. **Users** → **Edit** on the row.
2. Type the new password into **New password (leave blank to keep current)**.
   Leave the field empty if you only want to change the name, email or role.
3. Click **Save changes**.

### Deactivate an account

Click **Deactivate** on the row and confirm **Deactivate this user?** The person
can no longer sign in — to the admin UI or the app. **Activate** puts them back.

**Important:** deactivating blocks *new* sign-ins immediately, but it does not
cut off a session that is already open. A phone or browser that signed in
earlier keeps working until its session expires, which can be up to 24 hours.
Changing someone's password has the same limit. If you need someone locked out
right now — a phone was stolen, a driver was dismissed — deactivate the account
*and* tell your IT contact — signing everyone out at once is a server-side
change (rotating the token-signing key and restarting), not something the admin
UI can do.

### The last-admin protection

The system will not let you lock everyone out of the admin UI. If exactly one
active admin account is left:

- Deactivating it is refused with **cannot deactivate the last active admin**
  (shown as a plain error page — use your browser's Back button).
- Changing its **Role** from **Admin** to **Driver** is refused with **cannot
  demote the last active admin**, shown on the form.

Create the second admin first, then make the change. Keeping at least two admin
accounts is good practice anyway.

---

## 6. Assigning vehicles to drivers

A driver can only start a trip on a vehicle assigned to them. Assignments live
on the user's edit page.

1. **Users** → **Edit** on the driver's row.
2. Scroll to **Assigned vehicles**. It shows what they have now, or **No
   vehicles assigned.**
3. Pick a vehicle from the dropdown and click **Assign**. You get **Vehicle
   assigned.** Only active vehicles appear in the dropdown; if there are none,
   you see **No active vehicles available to assign.**
4. To take one away, click **Remove** beside it and confirm **Remove this
   vehicle assignment?** You get **Vehicle unassigned.**

A driver may hold several assignments — useful when the same person drives
whichever bus is free — and a vehicle may be assigned to several drivers.

If a driver tries to start a trip on a vehicle that is not assigned to them, the
server refuses (`403 driver is not assigned to this vehicle`) and the app shows
**You are not assigned to this vehicle.** Note the one thing this does *not*
cover: the assignment is checked when a trip is started, not on each individual
location report. In normal use that is the same thing, because the app only
reports while a trip is running.

---

## 7. Onboarding a driver

Do this once per driver, with the driver and their phone in front of you. It
takes about ten minutes. Before you start, make sure the driver has an account
(section 5) and at least one assigned vehicle (section 6).

**On your side, first:**

1. Get the app file (an APK) from your IT contact, along with the exact server
   web address the drivers should use. Both come out of
   [`deployment.md` §12](deployment.md#12-distributing-the-android-app).
2. Have the driver's email address, their password, and the **route id** they
   will be driving ready.

**On the driver's phone:**

1. Install the app. The driver will have to allow installation from your source
   the first time; your IT contact's distribution instructions cover this.
2. Open **Vehicle Tracker**. The first screen is **Driver Login**.
3. Enter the **Server URL** you were given. It is stored on the phone, so the
   driver only ever types it once. Enter **Email** and **Password**, then tap
   **Log In**.
   - **Invalid email or password.** means the account or password is wrong, or
     the account is deactivated.
   - **Could not reach the server. Check your connection.** means the phone
     cannot reach the server — check the network and the server URL.
4. Next is **Select a Vehicle**, listing exactly the vehicles you assigned. If
   there is only one, the app skips this screen and selects it automatically. If
   it says **No vehicles are assigned to you.**, go back to section 6.
5. Next is **Start a Trip**:
   - **Route ID** — required. Type the route id exactly as it appears in the
     `route_id` column of your agency's GTFS `routes.txt`. This is often *not*
     the number painted on the bus. If your published route "12" has the GTFS
     `route_id` `RT-012`, the driver must type `RT-012`. Get this wrong and the
     vehicle still shows up in the feed, but OneBusAway cannot match it to a
     route, so riders will not see it on the route they are waiting for.
   - **GTFS Trip ID (optional)** — leave it blank unless your agency works from
     a schedule and knows the exact `trip_id` for this run. A route id alone is
     enough for the feed. Filling it in lets OneBusAway tie the bus to a
     specific scheduled trip.
   - **Recent routes** — after the first few trips, the last route ids used on
     that phone appear as buttons here. Tapping one fills in **Route ID**. This
     is what most drivers will use day to day.
   - Tap **Start Trip**.
6. The permission prompts appear next, once per phone. Work through them with
   the driver — this is the step drivers get wrong on their own:
   - Location: choose the **precise** option. If only approximate location is
     granted, the app warns **Approximate Location Only** and offers **Grant
     Precise Location**; take it.
   - Background location: the app shows **Keep Tracking in the Background**.
     Tap **Continue**, then choose **"Allow all the time"** on the Android
     screen that appears. Anything less — or tapping **Not Now** — and tracking
     stops the moment the screen locks.
   - Notifications: allow them. If they are refused the app says so and carries
     on, but the driver loses the ongoing tracking notification.
   - Battery: the app shows **Prevent Battery Optimization**. Tap **Continue**
     and allow it. This is what keeps long trips reporting reliably.
   - If the phone's location services are switched off entirely, the app says
     **Turn On Location Services** and will not start until they are on.
7. Tracking starts. The screen fills with a large status banner, plus **Route
   …**, **Trip duration: …** and **Location updates sent: …**, and an **End
   Trip** button. A notification, **OBA Tracker is active**, stays in the status
   bar for the whole trip.

**What the status banner means.** The banner is green when all is well and red
when something needs attention:

| Banner | Colour | What is happening | What the driver should do |
|---|---|---|---|
| **Tracking – Connected** | green | Fixes are reaching the server. | Nothing. |
| **No connection** | red | The phone cannot reach the server. Positions taken now are dropped, not queued. | Keep driving; it goes green again by itself when signal returns. |
| **GPS unavailable** | red | The phone is not producing location fixes. | Check that location services are still on and the phone has a view of the sky. |
| **Check device clock** | red | The server has rejected several reports because the phone's clock is wrong. | Turn on automatic date and time in Android settings. |
| **Session expired – log in again** | red | The phone's sign-in has expired. Sign-ins last 24 hours, so this normally appears on a phone that has been running since the previous day. | Tap **Log in** on the same screen and sign in again with the same email and password. |

**Ending a trip.** Tap **End Trip** and confirm **End this trip?** ("This will
stop location tracking and mark the trip as complete."). Tracking stops, the
notification disappears, and the vehicle drops out of the feed once its last
report ages past the staleness threshold. If the server cannot be reached at
that moment the app offers **Could not end trip** with **Retry** and **End
Locally** — **End Locally** stops the phone but leaves the trip showing as
**Active** on your **Trips** page, so ask the driver to reopen the app and end
it properly when they are back on the network.

Teach every driver the same three habits: start the trip before pulling out,
check the banner is green, and end the trip when the run finishes.

---

## 8. Watching the fleet

### Live Map

**Live Map** shows every vehicle that has reported within the staleness
threshold, refreshing about every 10 seconds on its own. It shows
driver-reported vehicles only.

- The overlay at the top left counts **Active** vehicles and how many distinct
  **Routes** they are on.
- Clicking a bus marker opens a card with **Route**, **Driver**, **Speed** and
  **Updated** ("how long ago").
- **Fleet Status** on the right lists the same vehicles as rows.
- If nothing is reporting you get the banner **No vehicles to show right now.**
  and **No vehicles reporting.** in the sidebar.

### Trips and trails

**Trips** is the history, headed **Trip History**. Each row shows **Trip**,
**Vehicle**, **Driver**, **Route**, **GTFS trip**, **Start**, **End**,
**Status** and **Duration**. Filter with the status dropdown (**All statuses**,
**Active**, **Completed**), the vehicle dropdown (**All vehicles**), and the
search box ("Search driver, route, trip id") — then click **Apply**.

Click **View trail** on any trip to open the map on that trip alone: the route
actually driven as a line, with a **Start** and an **End** marker, and the
sidebar replaced by **Trip Detail** showing driver, route, status and times.
This is the tool for answering "where did this bus actually go?".

### The feed itself

Hand this address to whoever runs your OneBusAway instance, or to any other
GTFS-Realtime consumer:

```
https://your-server/gtfs-rt/vehicle-positions
```

That is the whole integration. OneBusAway reads GTFS-RT Vehicle Positions
natively; see [`deployment.md` §11](deployment.md#11-connecting-the-feed-to-onebusaway)
for the details your IT contact needs.

To check the feed with your own eyes, open the same address with `?format=json`
on the end:

```
https://your-server/gtfs-rt/vehicle-positions?format=json
```

The browser shows a readable list instead of the machine format. Each vehicle
appears once under an `id` — the **Vehicle ID** you gave it on the vehicle form
— with its position, and with the `routeId` and `startDate` its driver entered
(plus `tripId` if the driver typed a GTFS trip id). An empty list means no
vehicle has reported inside the staleness window.

---

## 9. Daily operations checklist

**Before service**

1. Open **Dashboard**. Confirm **Active Fleet** matches the number of buses you
   expect to be available and **Drivers** matches your roster.
2. Check **Trips** filtered to **Active**. It should be empty. Anything left
   over is yesterday's trip that was never ended — see section 12.
3. Before the first bus reports, **Feed last updated** will read **never** and
   **Active Now** will be 0. That is normal, not a fault.
4. As the first buses pull out, confirm **Active Now** and **Active Trips**
   start climbing and **Feed last updated** changes to "just now".

**During service**

1. Keep **Live Map** open. Every bus that should be out should be a marker.
2. A bus that vanishes from the map has stopped reporting for longer than the
   staleness threshold — call the driver before assuming a fault.
3. Spot-check the feed with `?format=json` if a rider or OneBusAway reports
   something missing.

**After service**

1. **Trips** → **Completed**: every run of the day should be there, with a
   sensible **Duration**.
2. **Trips** → **Active**: should be empty. Every trip still listed as
   **Active** is a driver who did not tap **End Trip**. Ask them to reopen the
   app and end it; the app remembers the trip and will take them straight back
   to the tracking screen.
3. **Dashboard**: **Active Trips** back to 0, **Active Now** back to 0 a few
   minutes after the last bus.

---

## 10. Rider mode (optional)

Rider mode is an optional extra where ordinary riders' phones, running a
separate anonymous rider app, help fill in positions for trips no driver phone
is covering. The server checks each reported position against your GTFS
schedule and route shape, and only publishes one when it is convincing. It is
**off** unless your IT contact deliberately turned it on, and when it is off it
changes nothing at all about the driver-reported feed.

It has no screens in the admin UI. Two addresses report on it, and both answer
with raw data meant for a technical reader rather than a page:

- `https://your-server/api/v1/admin/rider/status` — whether it is enabled, plus
  schedule and live-ride counts. When rider mode is off it simply answers
  `{"enabled":false}`, which is the quickest way to tell.
- `https://your-server/api/v1/admin/rider/rides` — recent rider rides, newest
  first.

Both require an admin account, so while you are signed in to the admin UI you
can open them in the same browser. If the answer means nothing to you, that is
expected — hand it to your IT contact. The settings behind it are in
[`deployment.md` §3](deployment.md#3-configuration-reference) and the README's
rider-mode section.

What you *can* see for yourself is the effect on the feed. Rider-derived
vehicles are labelled **Rider-reported** and their ids look like
`rider:<trip id>:<date>`, so they are never confused with your own buses. They
never appear on the **Live Map**, which shows driver-reported vehicles only.
A trip your own drivers are already reporting is never published from rider
data. If your OneBusAway instance should receive agency positions only, your IT
contact can point it at the feed address with `?source=driver` on the end.

---

## 11. Data retention and privacy

**What is stored.** For every location report: the vehicle, the position
(latitude, longitude, and where available bearing, speed and accuracy), the
time the phone recorded it, the time the server received it, and the trip it
belonged to. For every trip: the driver, the vehicle, the route id, the
optional GTFS trip id, and the start and end times. For every account: name,
email address, role and a scrambled form of the password — never the password
itself.

Taken together this is a **per-driver movement history**, and it should be
treated like one. Anyone with an admin account can see any vehicle's positions:
the **Live Map**, the trip trails, and the **CSV** export on the vehicle list.
Drivers can see none of it — the app has no history screen, and driver accounts
cannot open `/admin`. So the real access control here is how many admin
accounts you hand out. Keep the number small, and deactivate accounts when
people change jobs.

**How long it is kept.** Location history is kept **forever** unless your agency
turns on retention. Turning it on is a server setting
(`LOCATION_RETENTION_PERIOD`), not a control in the admin UI — ask your IT
contact, and see [`deployment.md` §6.5](deployment.md#65-retention) and the
README's retention notes. Three things to know before you ask for it:

- Deletion is permanent. There is no archive and no undo. Export anything you
  need to keep first.
- The clock runs from when the server received the point, not from the time the
  phone claimed — so a phone with a wrong clock cannot dodge the rule.
- Pick a period consistent with local law and your own driver-privacy policy.
  Ninety days is a common starting point.

Trip records and accounts are not deleted by retention; only the location
points are.

---

## 12. Troubleshooting

| Symptom | Likely cause | What to do |
|---|---|---|
| Driver sees **You are not assigned to this vehicle.** when tapping **Start Trip** | The vehicle is not assigned to that driver, or the assignment was removed. | **Users** → **Edit** the driver → **Assigned vehicles** → pick the vehicle → **Assign**. Then have them tap **Start Trip** again. |
| Driver sees **You already have an active trip.** | An earlier trip was never ended — usually **End Locally** after a network problem, or the app was reinstalled. | Have the driver reopen the app; it returns to the tracking screen for the old trip, where **End Trip** ends it. Confirm on **Trips** → **Active** that it is gone. If the app no longer knows about the trip, ask your IT contact — there is no button in the admin UI to end another person's trip. |
| Driver sees **No vehicles are assigned to you.** | No active vehicle is assigned to that account. | Check the vehicle is not deactivated (**Vehicles** → **Show deactivated**), then assign it (section 6). |
| One vehicle missing from the feed and the map, others fine | That phone has stopped reporting for longer than the staleness threshold: no signal, app closed, trip never started, phone battery dead, or Android killed the app in the background. | Ask the driver what the status banner says. **No connection** is a coverage problem and clears itself. If the app is not on the tracking screen at all, the trip was never started or was ended. If it keeps dying when the screen locks, the background-location permission is not **"Allow all the time"** — redo step 6 of section 7. |
| Feed completely empty during service hours | Nothing is reporting, or the server lost its database. | Check **Dashboard**: if **Feed last updated** says **never** (nothing has reported for several minutes) and **Active Trips** is 0, it is the phones — start with two or three drivers. If **Active Trips** is healthy but nothing is in the feed, contact IT: see the monitoring checks in [`deployment.md` §9](deployment.md#9-monitoring). |
| Driver phone shows **Session expired – log in again** | The 24-hour sign-in expired. | Have the driver tap **Log in** on that screen and sign in again. If you have since deactivated the account, sign-in is refused — which is the intended outcome. |
| A driver or admin cannot sign in and insists the password is right | The account is deactivated. Deactivation is deliberately indistinguishable from a wrong password on the sign-in screen. | **Users** → find the row → if **Status** reads **Deactivated**, click **Activate** and confirm **Reactivate this user?** |
| Someone you deactivated is still reporting, or their phone still works | Deactivation blocks new sign-ins; it does not end a session already in progress, and that session can last up to 24 hours. | Confirm the row shows **Deactivated** on **Users** — that is enough to stop them signing in again. If they must be cut off this minute, tell your IT contact; only a server-side change ends live sessions. |
| Driver phone shows **Check device clock** | The phone's clock is wrong, so the server is rejecting its reports. | On the phone, turn on automatic date and time in Android settings, then restart the app. The banner goes green once reports are accepted again. |
| Vehicles show on your **Live Map** but not in OneBusAway | Almost always the route id. The driver typed the public route number instead of the GTFS `route_id`, so OBA cannot match it to a route. | Check **Trips** → the **Route** column against your GTFS `routes.txt`. Correct it with the driver (section 7, step 5) and have them end and restart the trip. If the ids are right, check with IT that OBA is pointed at the feed address and using the same GTFS static file. |
| **Deactivate** on a user returns a plain page saying **cannot deactivate the last active admin** | It is the only active admin account left. | Use your browser's Back button, create or reactivate another admin, then try again. |
| Vehicle form rejects an id | The id has spaces or other characters, or is over 50 characters, or already exists. | Use only letters, digits, dots, hyphens and underscores; keep it to at most 50 characters; check **Show deactivated** in case the id belongs to a deactivated vehicle. |
| `/admin` returns "not found" | The admin UI was switched off at install time (`ADMIN_UI_ENABLED=false`). | Ask your IT contact to enable it — see [`deployment.md` §3](deployment.md#3-configuration-reference). |

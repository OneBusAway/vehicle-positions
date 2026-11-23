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
2. You land on the sign-in page, headed **Sign in**. If a page sent you there
   because you were signed out, it says so and takes you back to that page
   after.
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
**Drivers and users**, click **Edit** on your own row, type a new password into
**New password**, and click **Save changes**. Passwords
must be at least 8 characters and no more than 72 bytes — in practice 72
letters, or fewer if you use accented letters or other non-English characters.

Signing in opens **Live map**, or the welcome on **Overview** while the server
has no vehicle or no driver yet. The menu down the left side has three groups:
**Live map**, **Alerts** and **Trips**; **Overview**, **Analytics**, **Rider
mode** and **Feed health**; and **Vehicles**, **Drivers and users**, **Driver
setup**, **API keys** and **Audit log**. **Fold menu** at its foot shrinks it
to icons. On a
phone the menu is a bar along the bottom, with the rest of the pages under
**More**.

The bar across the top is on every page:

- The search box finds vehicles, drivers, trips and routes from anywhere.
  Press **/** to jump to it.
- Beside it, **9 reporting** counts the vehicles reporting now and **2 quiet**
  those on a trip that have gone quiet. The last part says how long ago the
  page heard from the server, such as **updated 4 s ago**. It keeps itself
  current, and reads **no answer for 40 s** when the server stops answering.
  With several admin tabs open, one of them asks the server and shares the
  answer with the others.
- The bell shows how many alerts are open and opens **Alerts**.
- The circle with your initials opens a small menu with **Light or dark**,
  which switches the pages' colours, and **Sign out**. On a phone both are on
  **More**. In a browser too old for that menu, the circle opens **More**
  instead. If you are signed in as a viewer, **Viewer** sits beside the circle
  ([Read-only accounts](#read-only-accounts)).

---

## 3. Overview

**Overview** is the one-screen health check. Until you have added a vehicle
and a driver it shows a welcome in its place, with the four steps to your
first vehicle on the map.

The headline says how many of the vehicles on a trip are reporting, such as
**9 of 11 vehicles are reporting**, and the line under it how the stops passed
today stood against the timetable, set against yesterday. When something needs
a look, a dark line above the tiles says what, with a way to it. Four tiles
follow, and each opens the page behind it:

| Tile | What it counts |
|---|---|
| **On a trip now** | Vehicles with a trip started and not ended, of the vehicles you have registered and not deactivated. Under it is a mark for each: green for one that is reporting, amber for one that has gone quiet. |
| **On time today** | The share of the stops passed today that were on time, with the shares early and late under it. [Analytics](#analytics) has the rule. |
| **Trips today** | Scheduled trips a driver has run today, of those due to have left by now, with how many are running and how many vehicles have not started their next trip. |
| **Open alerts** | How many alerts are open, and how long the oldest has been. |

Below the tiles:

- **Routes right now** draws each route with a trip running as a line, its
  stops as ticks and each vehicle where it is along the route. A vehicle that
  has gone quiet is grey. Beside each line is the route's on-time share today.
- **Needs attention** lists the three alerts open longest.
- **Location reports, last hour** is a bar for each minute: the reports the
  server accepted, with any it rejected stacked on them in amber.
- The last card is three lines. **Feed** says how many times the feed was
  fetched in the last hour and when it last was. **Schedule** says how many
  routes and trips your GTFS schedule has and when it was loaded. **Rider
  mode** says whether it is on. Each opens its page.

The page brings itself up to date every 10 seconds, and each vehicle slides to
where it now is.

The healthy picture during service hours is: the headline counting every
vehicle you expect out, **Open alerts** at 0, and the top bar saying
**updated** a few seconds ago.

---

## 4. Vehicles

**Vehicles** lists every vehicle under a headline that counts them, such as
**14 vehicles, 9 reporting now**. Its columns are **Vehicle** (the label, with
the id under it), **Agency tag**, **Status**, **Last report**, **Driver now**
and **Actions**. Search by id or label with the box above the list and
**Apply**. By default only active vehicles are shown; **Show deactivated** adds
the deactivated ones and reads **Showing deactivated** while it is on. Long
lists are paged with **Previous** and **Next** at the bottom.

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
dialog asks **Deactivate this vehicle?** The vehicle disappears from the
default list, can no longer be assigned to a driver, and stops being offered to
drivers in the app. Its history is kept. To bring it back, click **Show
deactivated**, then **Activate** on the row.

Deactivating does not stop a trip that is already running.

### Export location history

**Reports** on each row downloads that vehicle's recent location points as
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

**Drivers and users** lists every account under a headline that counts them,
such as **11 drivers and 1 admin**, or **11 drivers, 1 admin and 2 viewers**
once there are viewers. Its columns are **Name** (with the email
under it), **Role**, **Status**, **May drive** (how many vehicles are
assigned) and **Actions**. Search by name or email, or show one role, with the
controls above the list.

There are three roles:

| Role | Can do |
|---|---|
| **Driver** | Sign in to the Android app, see their assigned vehicles, start and end trips, report locations. Cannot open `/admin` at all. |
| **Admin** | Everything in the admin web UI: vehicles, users, assignments, map, trips, CSV export. |
| **Viewer** | Open every page of the admin web UI and change nothing. Cannot drive. See [Read-only accounts](#read-only-accounts). |

### Create an account

1. Click **New user**.
2. Fill in **Name**, **Email** and **Password**. The password must be at least
   8 characters, or you get **password must be at least 8 characters**. The
   maximum is 72 bytes (**password must be at most 72 bytes**).
3. Choose **Role** — **Driver**, **Admin** or **Viewer**. New accounts
   default to **Driver**.
4. Click **Create user**. A duplicate address gives **email already exists**.

Give the driver their email address and password in person or by whatever
channel your agency uses; there is no invitation email and no self-service
password reset.

### Reset a password

1. **Drivers and users** → **Edit** on the row.
2. Type the new password into **New password**. Leave the field empty if you
   only want to change the name, email or role.
3. Click **Save changes**.

### Deactivate an account

Click **Deactivate** on the row and confirm **Deactivate this account?** The
person can no longer sign in, to the admin UI or the app. **Activate** puts
them back.

**Important:** deactivating cuts off sessions that are already open, as well
as blocking new sign-ins. A phone or browser that signed in earlier is refused
on its next request, so this is the right response when a phone was stolen or
a driver was dismissed. Changing someone's **Role** takes effect at once too:
an admin changed to a driver loses the admin UI straight away, and one changed
to a viewer can no longer change anything.

Changing someone's password is different: it only applies from the next
sign-in. A session that is already open keeps working until it expires, which
is 15 minutes for the app unless your IT contact has changed it, and can be up
to 24 hours for the admin UI in a browser. To lock someone out right now,
deactivate the account; a new password alone does not do it.

### Read-only accounts

A **Viewer** signs in at `/admin` as an admin does and sees every page: the map,
alerts, trips, Analytics, the vehicles and accounts, API keys and the Audit
log. It cannot change anything. The pages leave out every button that would,
such as **New vehicle**, **Deactivate**, **End trip**, **Import drivers** and
**Create key**; **Edit** reads **View**, and a vehicle's or an account's page
shows its fields greyed out with no **Save changes**. **Viewer** sits beside
the initials in the top bar. A change sent anyway, from a tab left open since
the account was an admin's or from a script, is refused with **Your account
can look but not change** and nothing is saved. Through the admin API a
viewer's token reads what an admin's does and gets
`403 admin access required` for any change.

Make one for anyone who only needs to watch: a supervisor, a planner, someone
from the city. Three things to know:

- A viewer cannot drive. The app signs it in, but the server refuses the
  vehicle list and every trip and report it sends.
- A viewer sees what an admin sees of where drivers have been: the trip trails
  and each vehicle's **Reports** download (see
  [section 11](#11-data-retention-and-privacy)). Hand the role out as carefully
  as an admin's.
- On the **Audit log** a viewer sees who made each change but not the address
  it came from.

A viewer can't be given a vehicle: its page has no **Vehicles this driver may
pick**, and the admin API refuses with **a viewer can't drive, so no vehicles
can be assigned to one**. Making an account a viewer takes away the vehicles
it had in the same change, and is refused with **end this account's running
trip before making it a viewer** while it is on a trip: end the trip on
**Trips** first. The role change and each vehicle taken away are recorded on
the Audit log, as is changing the account back.

### The last-admin protection

The system will not let you lock everyone out of the admin UI. If exactly one
active admin account is left:

- Deactivating it is refused with **cannot deactivate the last active admin**
  (shown as a plain error page — use your browser's Back button).
- Changing its **Role** from **Admin** to **Driver** or **Viewer** is refused
  with **cannot demote the last active admin**, shown on the form.

Create the second admin first, then make the change. Keeping at least two admin
accounts is good practice anyway.

### Locked out: no admin can sign in

If the only admin has lost their password, or there is no active admin at all
(the server logs **no active admin account** at startup), IT can restore access
from the server host. `ADMIN_BOOTSTRAP_EMAIL` and `ADMIN_BOOTSTRAP_PASSWORD` do
not help here: they only act on a database with no admin in it.

Put the new password on the first line of a file, then run `reset-admin` with
the email of the account to restore. With Docker Compose:

```bash
docker compose exec -T server vehicle-positions reset-admin -email ops@example.org < new-password.txt
```

On a host install, run the binary the same way, with the server's
`DATABASE_URL` set. The account is created if it does not exist; otherwise it
becomes an active admin with the new password. Delete the password file
afterwards.

The account can no longer renew a session it already had, so it has to sign in
with the new password once that session expires. But a session that is already
open, in a browser or an app, keeps working until then, which can be up to 24
hours: the same limit as [any password change](#deactivate-an-account). If
the reset is because someone else had the account and they must be out right
now, also have your IT contact rotate the token-signing key and restart the
server, which signs everyone out.

### Who changed what: the Audit log

**Audit log** lists every change made to a vehicle, an account, a trip or an
API key, newest first, whether it was made on these pages or through the admin
API. Each row says what was done and to what, for example **Edited vehicle**
and the vehicle's label; when; who did it, with the address their browser or
script connected from; and what the change did to each field, such as a label
from its old value to its new one or an account from **Active** to
**Deactivated**.

- The chips above the list show one kind of object: **Vehicles**,
  **Accounts**, **Trips** or **API keys**.
- Click the object in a row to see its whole history, or an admin's email to
  see every change they made.
- **Server** in place of an email is a change the server made itself: the first
  admin, created from `ADMIN_BOOTSTRAP_EMAIL`, or an account restored with
  `reset-admin` (see [Locked out](#locked-out-no-admin-can-sign-in)).
- A new password shows as **Set a new password for** and the account, never the
  password itself.
- A CSV import adds a row for each vehicle or account it writes and each
  vehicle it assigns. A save that changed nothing adds no row.
- A viewer sees this page too, without the addresses.

Each row is saved together with the change it records. If the row cannot be
saved, the change is not made either and you see an error, so a change that
went through always has its row. Rows cannot be edited afterwards. They are
kept forever unless your IT contact sets how long to keep them
(`AUDIT_LOG_RETENTION_PERIOD`); then, once an hour, the rows older than that
are deleted for good.

Your IT contact can read the same list as JSON from `GET /api/v1/admin/audit`,
with the same filters. A viewer's copy leaves out the addresses there too.

It does not record sign-ins, what anyone looked at, or what drivers do in the
app, such as starting and ending their own trips. The server's log has those.

Behind a reverse proxy the address is right only when your IT contact has set
`TRUST_PROXY_HEADERS`, the same setting the sign-in limit relies on; without
it, every row shows the proxy's address.

---

## 6. Assigning vehicles to drivers

A driver can only start a trip on a vehicle assigned to them. Assignments live
on the user's edit page. A viewer's page has none, since a viewer can't drive
([Read-only accounts](#read-only-accounts)).

1. **Drivers and users** → **Edit** on the driver's row.
2. Scroll to **Vehicles this driver may pick**. It shows what they have now,
   or **No vehicles assigned.**
3. Pick a vehicle from the dropdown and click **Assign**. You get **Vehicle
   assigned.** Only active vehicles the driver does not have yet appear in the
   dropdown; if there are none, you see **No more active vehicles to assign.**
4. To take one away, click **Remove** beside it and confirm **Remove this
   vehicle?** You get **Vehicle unassigned.** If the driver is on a
   trip on that vehicle, it stays assigned and you get **The driver is on a
   trip on this vehicle. End it on Trips, then remove the vehicle.**

A driver may hold several assignments — useful when the same person drives
whichever bus is free — and a vehicle may be assigned to several drivers.

If a driver tries to start a trip on a vehicle that is not assigned to them, the
server refuses (`403 driver is not assigned to this vehicle`) and the app shows
**You're not assigned to vehicle 4317.**, with the vehicle's number, and a **Change
vehicle** button. The server checks the assignment on every location report
too, which is why **Remove** refuses while the driver is on a trip on that
vehicle: the trip would keep running with every report refused.

### Many at once: import a CSV file

**Import from CSV** on **Vehicles** and **Import drivers** on **Drivers and
users** set up a whole fleet and its drivers from a spreadsheet. Import the
vehicles first, so the drivers file can assign them. The line across the top of
the page shows which of the three steps you are on.

1. Click **Import from CSV** or **Import drivers**, then **Download the
   template**. Fill it in and save it as **CSV UTF-8**. The first line names
   the columns, in any order:
   - Vehicles: `id`, `label` and `agency_tag`, checked as on **New vehicle**.
   - Drivers: `email`, `name` and `vehicle_ids`, the vehicles to assign,
     separated by spaces (`101 102`). Each must exist and be active. Emails are
     saved in lower case.
2. Choose the file and click **Check the file**. Each line is marked **New**,
   **Update**, **Unchanged** or **Fix this row**, with the reason in the words
   the forms use. A file holds up to 200 rows and 256 KB.
3. If any line has an error, nothing can be imported: fix the file and upload
   it again. Otherwise click **Confirm import** within an hour. Every line is
   saved, or none is.
4. The result lists what was created and updated. Each new driver has a
   password, with **Copy** beside it, and a **Setup sheet** link. The
   passwords are shown only on this page, so give each driver theirs with
   their sheet; if one is lost, set a new one with **Edit**.

An existing vehicle takes the file's label and agency tag. An existing driver
gets only the new name and the listed vehicles added: an import never changes a
password or a role, never removes an assignment, never activates or deactivates
anything, and refuses a line whose email belongs to an admin or a viewer.

---

## 7. Onboarding a driver

Do this once per driver, with the driver and their phone in front of you. It
takes about ten minutes. Before you start, make sure the driver has an account
(section 5) and at least one assigned vehicle (section 6).

**On your side, first:**

1. Get the app file (an APK) from your IT contact, along with the exact server
   web address the drivers should use. Both come out of
   [`deployment.md` §12](deployment.md#12-distributing-the-android-app).
2. Have the driver's email address and password ready, and know which route
   and run they will be driving.
3. If the driver's phone is not in English, ask your IT contact whether its
   language has been checked: the app follows the phone, and each language but
   English is a draft until a fluent speaker has read it
   ([`deployment.md` §15](deployment.md#15-languages-on-drivers-phones)).

**On the driver's phone:**

1. Install the app. The driver will have to allow installation from your source
   the first time; your IT contact's distribution instructions cover this.
2. Open **Vehicle Tracker**. The first screen is **Sign in**.
3. Enter the **Server address** you were given, starting with `https://`. It is
   stored on the phone, so the driver only ever types it once: after the first
   sign-in it shows as one line, with **Change** beside it. Enter **Email** and
   **Password**, then tap **Sign in**. Each problem shows under the field it is
   about:
   - **Wrong email or password. Try again.** means the account or password is
     wrong, or the account is deactivated.
   - **Check the address**, with **Can't reach** the server above the button,
     means the phone cannot reach the server: check the network and the server
     address.
   - **Couldn't sign in. Try again.** means the server answered with an error.
4. Next is **Choose your vehicle**, showing exactly the vehicles you assigned
   as tiles, with the ones the driver starred under **Favorites** and the ones
   they drove last under **Recent**. If there is only one, the app skips this
   screen and selects it automatically. If it says **No vehicles are assigned
   to you**, go back to section 6, then have the driver tap **Check again**.
5. Next the driver picks the route, then the run, from your agency's GTFS
   schedule. There is nothing to type:
   - **Choose a route** lists the routes in the schedule, each with its number
     on its colour and its name; a schedule that names none of its routes shows
     just the numbers, as a grid. Typing in **Route number or name** narrows the
     list. After
     the first trip, the routes that phone has driven appear at the top under
     **Recent**; this is what most drivers will use day to day. If a route the
     driver needs is missing, tell your IT contact: a route is listed only if
     the GTFS feed gives at least one of its trips a shape (the line drawn on
     the map).
   - **Choose a run** shows that route's runs for today as a departure board:
     each run's start time over where it is headed, with a line at the current
     time. When the schedule gives the route two directions, a switch at the top
     picks one, named after where its runs go. The times are your agency's local
     time, whatever time zone the phone is set to. Of the runs not over yet, the
     one starting nearest the current time is marked: **Now** once it has
     started, **Next** before that. Tap the run the driver is about to drive.
     The app asks first, for example **Start the 6:43 AM run?** over the route,
     where the run goes and the vehicle; check them and tap **Start trip**.
     **No runs on route 7 today** means the schedule has no service on that
     route today.

   The route and trip the app reports both come from the schedule, so as long
   as OneBusAway uses the same GTFS feed (your IT contact sets this up), it can
   place the bus on the exact scheduled trip.
6. If the phone has not yet allowed everything the app needs, **Start trip**
   opens **Before you start**: six rows, each saying why the app needs it. Work
   through it with the driver; this is the step drivers get wrong on their
   own. Tap **Continue** and Android asks about each in turn, and each row then
   says **Allowed** or **Skipped**:
   - Location: choose **Precise** and **While using the app**. If only
     approximate location is allowed, the row says so and offers **Try
     again**; take it. Location cannot be skipped: a refusal shows **Try
     again**, and a second refusal **Open settings**, where it has to be
     allowed.
   - All the time: Android opens its location settings for the app. **Allow all
     the time** lets the app restart reporting by itself if Android closes it.
     Reports carry on with the screen locked either way.
   - Notifications: allow them, so the driver can see that reporting is on.
     Refused, reports still work.
   - Battery: allow it. This is what keeps long trips reporting reliably.
   - If the phone's location services are switched off, Android offers to turn
     them on, and the trip does not start until they are on.

   The checklist comes back before a later trip only while something on it is
   still missing.
7. Tracking starts. A map of the trip fills the screen, with the status card
   over its top and the trip bar along its bottom: the route and where the run
   is headed, how long the trip has run and how many reports have been sent,
   and an **End trip** button. Swipe the bar up, or tap it, to see the run, how
   reporting is going, the street map, and the stops from the last one passed
   to the end of the line. A notification stays in the status bar for the whole
   trip, saying what the card says: the route and its status, such as **Route 7
   · On time**, over the next stop or what is wrong.

**What the status card means.** The card at the top says how the trip is
keeping to its schedule, judged against the next stop: green **On time**, blue
**N min late**, red **N min early**, and grey while the bus is off the route, has
no position yet, or has no timetable on the phone. **Off schedule · Is this the
right run?** under the answer means the bus is so far from this run's timetable
that the driver has probably picked the wrong run. When reports are not getting
through, a dark line with an amber icon joins the bottom of the card:

| Line under the card | What is happening | What the driver should do |
|---|---|---|
| **No connection** · Riders can't see you | The phone cannot reach the server. Positions taken now are dropped, not queued. | Keep driving; the line goes away by itself when signal returns. |
| **No GPS signal** · Position paused | The phone is not producing location fixes. | Check that location services are still on and the phone has a view of the sky. |
| **Phone clock is wrong** · Set automatic time | The server has rejected several reports because the phone's clock is wrong. | Turn on automatic date and time in Android settings. |

The phone renews its sign-in by itself while the app is in use. When it can't,
a **Signed out** card takes the status card's place: "Reports have stopped. Your
trip is still open." That happens when the account has been deactivated or
deleted, or its password changed, when the phone has gone a week without using
the app, or when the driver signed in on another phone and then signed out
there. Tap **Sign in** on the card and sign in again with the same email and
password; the trip carries on.

**Ending a trip.** Tap **End trip** on the trip bar and confirm **End this
trip?**, which names the route and run and says how many reports were sent.
The button says **Ending…** while the end is on its way. Tracking stops, the
notification disappears, and the vehicle drops out of the feed once its last
report ages past the staleness threshold. If the server cannot be reached at
that moment the app offers **Trip not ended yet** with **Try again** and **End
on this phone**. **End on this phone** stops the phone at once, but the trip stays
**Running** on your **Trips** page until the driver starts their next trip: the
app sends the end just before that start, so the trip's end time shows when the
next one began, not when the run finished.

Teach every driver the same three habits: start the trip before pulling out,
check there is no dark line under the status card, and end the trip when the
run finishes.

---

## 8. Watching the fleet

### Live map

**Live map** shows every vehicle that has reported within the staleness
threshold, refreshing about every 10 seconds on its own. It shows
driver-reported vehicles only. The map fills the page; everything else floats
on it.

- Each vehicle is a dark marker with an arrow for the way it is heading (a dot
  when the phone does not say) and a small tag with its route's short name. A
  vehicle that needs a look has an amber edge and is drawn on top: one that
  has gone quiet is grey, and one with an open alert (**Not moving**, **Off
  route**, **Not started**, **Bunched**) keeps its colour. Vehicles glide between updates,
  and a ring leaves a vehicle when its new report arrives (only while 80 or
  fewer are on the map). With more than 60 vehicles, zoomed out, each vehicle
  that needs no look is a small dot; zoom in and they are markers again.
- The buttons along the top show **All**, then **Needs attention** when
  anything does, then each route, each with how many vehicles it has. Pick one
  to show only those.
- **Vehicles** lists them by route, what needs attention first, each with
  where it is heading, its speed in km/h and how old its last report is (or
  why it needs a look: **Quiet 4 min**, **Off route 7 min**). With more than 40
  vehicles each route is folded; open it to see its vehicles. A folded route
  says how many of its vehicles need attention.
- Picking a vehicle, on the map or in the list, opens its card: its route and
  where it is heading, whether it is reporting, the stop it is heading to
  (**Next stop**) or standing at (**At stop**) with that stop's scheduled time
  (just the name on a trip run from the GTFS frequencies, which has no
  timetable), how far it is from its timetable where it is now
  (**Schedule**: **6:10 late**, **1:05 early**, or **On time** with the
  difference beside it, by the same on-time window as **Analytics**), for a
  bunched vehicle how far behind the vehicle ahead it is running (**Gap**:
  **1 min behind 12, every 10 min**), its
  driver, GTFS trip, speed and heading, with **Follow** (keep it in view),
  **Open trip** and **Vehicle**. **Schedule** is left out until the vehicle
  has left its first stop, while it is off its route, past its last stop, or
  quiet, and on a trip run from the GTFS frequencies. It can differ from
  **Trips** and the trip's page, which say how it stood at the last stop it
  passed. It also draws its trip's route
  and stops, with a larger, bolder ring on the stop the card names. **Esc**
  closes it.
- The buttons on the right open the list, make the map full screen, fit every
  vehicle in view, and zoom in and out.
- If nothing is reporting the map says **No vehicles are reporting**, with
  **Set up a driver**, and the list closes. On a computer it opens again by
  itself when vehicles report, unless you have opened or closed it in the
  meantime. If the server stops answering, the vehicles stay where
  they last were, dimmed, and the map says what time they are from; until it
  has answered once, the list says **Waiting for the server**.
- With the keyboard, the buttons along the top, the list and the card are the
  way in: the vehicles on the map itself cannot be reached with Tab. A screen
  reader hears which vehicle was picked.
- `/admin/map?trip_id=N`, which an older version used for a trip's trail,
  now opens that trip's page.

### Alerts

**Alerts** lists the vehicles that need a look. There are five kinds:

- **Gone quiet**: the vehicle has sent no report for over 3 minutes. The phone
  may have lost signal or been switched off, or the app may have stopped. At 5
  minutes the vehicle also drops off the Live map and out of the feed.
- **Not moving**: the vehicle is still reporting but has moved under 50 m in 10
  minutes. It may have broken down or be stuck in traffic. A driver waiting at
  the first stop before departure shows here too.
- **Off route**: the vehicle has been more than 60 m from its trip's route for
  over 5 minutes. The driver may have taken a wrong turn or a detour, or be
  heading back to the depot with the trip still running. A vehicle standing
  still off its route shows here rather than as **Not moving**. It counts only
  once the vehicle has been on its route during the trip, so a driver who
  starts the trip at a depot or a layover spot shows as **Not moving** if they
  wait there. Only trips whose route has a shape in your GTFS feed are
  checked, so a trip started with only a route, or on a feed without shapes,
  never shows as off route.
- **Not started**: the vehicle has finished a trip on a block, the chain of
  trips your GTFS feed gives one vehicle for the day, and the block's next
  trip has not started 5 minutes after it was due to leave. The driver may
  have forgotten to start it in the app, or not be back from a break. The
  alert lasts until that trip is due to arrive, and clears when the vehicle
  starts a trip or another vehicle starts that one. A trip started with only
  a route doesn't take the vehicle off the block, though: the alert stays away
  while that trip runs and comes back when it ends, if the block's next trip
  is still due. Only a block a vehicle has run a trip on today is watched, so
  blocks nobody with the app is driving never show here, and neither does a
  feed whose trips have no `block_id`.
- **Bunched**: the vehicle has been running under 25% of the scheduled gap
  behind the vehicle ahead of it for 3 minutes: on a route every 10 minutes,
  under 2.5 minutes behind. Riders at the stops ahead get two vehicles at once
  and then a long wait. Only frequent service is checked, the same service
  **Analytics** measures gaps on (see
  [Gaps between vehicles](#gaps-between-vehicles)), with the same scheduled
  gap: trips run from your GTFS frequencies, and a timetable every 12 minutes
  or more often at the stop the vehicle is heading to. The vehicle ahead is
  the nearest one on the same route, going the same way along the same shape
  in your GTFS feed, that stops there too. The
  alert goes on the vehicle behind, the one to hold back. A vehicle that has
  not left its first stop, is heading to its last, is off its route or has
  gone quiet is not checked, and a vehicle without the app is never the one
  ahead.

Each row names the vehicle, driver, route and GTFS trip, and says how long:
**No report for 7 min**, **Under 50 m in 12 min**, **Over 60 m from its route
for 6 min**, **Next on block 12, due 7 min ago** or **1 min behind 12, every
10 min**, and when the alert opened.
The longest-running alert is at the top. The buttons above the list each name
a kind and how many alerts of it are open; pick one to show only those. A kind
with none open says so, and **All** shows every alert again.
**Last position** opens the trip's page (see [A trip's page](#a-trips-page)),
whose map starts where the vehicle last reported, even after it has dropped
off the Live map. **Trip** opens **Trips** on that trip, where **End trip**
is.

For an off-route vehicle, **Last position** shows the way it has gone since
the trip started. Ask the driver whether a road is closed. If they have
finished the trip, end it from **Trip**. If vehicles on one route keep showing as
off route at the same place, the route's shape in your GTFS feed is probably
wrong there: tell whoever maintains the feed.

For a vehicle that has not started its next trip, the row shows the trip it
should be on, with the vehicle and driver of the block's last trip. Call the
driver. **Trip** opens all of that vehicle's trips, since none is running.

For a bunched vehicle, the row names the vehicle ahead and how often the stop
is served. Holding the vehicle at a stop for a minute or two lets the gap open
up again, and the alert clears once it has. On a timetabled route the vehicle
ahead may be the one running late: its card on **Live map** says so under
**Schedule**.

An alert clears by itself when the vehicle reports again, moves on, comes
back to its route or drops back from the vehicle ahead, and when its trip ends. Once an alert clears it moves to
**Cleared**, below the open ones, newest first, with when it was raised, when
it cleared and how long it lasted. Pick a kind above that list to see only
those, and **Next** for older ones. A position the phone is unsure of (a poor
GPS fix) neither raises nor clears **Off route**. The page reloads
every 30 seconds. The count beside **Alerts** in the menu, and on the bell at
the top, is on every page and keeps itself current.
Unless your server posts them to a chat channel (see
[Alerts on your phone](#alerts-on-your-phone)), keep the page open during
service. Your server
administrator can change these times and the 60 m (`ALERT_QUIET_AFTER`,
`ALERT_STOPPED_AFTER`, `ALERT_OFF_ROUTE_AFTER`, `ALERT_NOT_STARTED_AFTER`,
`ALERT_BUNCHED_AFTER` and `ALERT_OFF_ROUTE_DISTANCE`, see
[`configuration.md`](configuration.md)). After
the server restarts, a vehicle is counted as quiet from the restart.

#### Alerts on your phone

Your server can post each alert to a chat channel, so the dispatcher on duty
hears of it on their phone without keeping **Alerts** open. An alert is posted
once when it starts and once when it clears, as one sentence:

> Bus 7 on route 1 has gone quiet: no report since 09:02. https://tracker.example.org/admin/trips/42
>
> Cleared: Bus 7 on route 1, gone quiet since 09:02.

The link opens the trip's page. A post names the vehicle, the route and the
trip, never the driver, and never says where the vehicle is. Alerts are checked
every 30 seconds, as often as the page reloads.

To set it up in Slack:

1. Make a channel for the alerts, such as `#fleet-alerts`, and add the
   dispatchers.
2. At [api.slack.com/apps](https://api.slack.com/apps), click **Create New
   App**, give the app a name such as "Fleet alerts" and pick your workspace.
3. On the app's settings page, open **Incoming Webhooks** and turn on
   **Activate Incoming Webhooks**.
4. Click **Add New Webhook to Workspace**, pick the channel and click
   **Authorize**.
5. Copy the new URL under **Webhook URLs for Your Workspace**. It starts with
   `https://hooks.slack.com/services/`. Treat it like a password: anyone who
   has it can post to the channel.
6. Send it to your IT contact and ask them to set `ALERT_WEBHOOK_URL` to it and
   `PUBLIC_URL` to the address you open this site at, and restart the server
   (see [`configuration.md`](configuration.md#alert-webhook)).

In the Slack app on each dispatcher's phone, set the channel's notifications to
all new messages, so every alert rings. Google Chat works the same way with a
space's webhook. In Microsoft Teams, your IT contact sets up a Workflows flow
that posts the alert's text to a channel.

### Trips and trails

**Trips** is the history, under a headline that counts the trips running now.
Each row shows **Trip**, **Vehicle**, **Driver**, **Route**, **Started**,
**Ended**, **Status** and **Actions**. Show **All**, **Running** or
**Completed** trips with the buttons above the list, pick one vehicle from the
dropdown (**All vehicles**), or search by driver, route or trip id, then click
**Apply**.

Click a trip to open its page, below, with the route it drove on a map.

An active trip also has **End trip**. It ends the trip for good, the same as
the driver ending it. Use it when a driver sees **You already have a trip
running. End it before starting another.** and their phone no longer shows
that trip. The dialog asks **End this trip?** and says to do it only when the
driver's phone cannot. Confirm with **End trip**, the page says **Trip ended.
The driver can start a new trip.**, and the driver taps the run again. If a phone does still show the trip, ending it
there is safe: on Android **End trip** finishes as usual, and on an iPhone
**End locally anyway** is right when the app says **Could not end the trip**,
because the server has already ended it.

### A trip's page

One trip has a page of its own, opened from its row on **Trips**, **Last
position** on **Alerts**, or **Replay** in **Analytics**. Its times are on your
agency's clock, which the page names (UTC if the schedule is not loaded).

- The top shows the vehicle, the route in its colour, whether the trip is
  **Running** or **Completed**, and how it stood at its last stop. Under it
  are the driver, the vehicle's ID when its name is something else, the GTFS
  trip and block, when it started and ended, how long it
  took, how many of its stops were on time and how many reports the phone
  sent. An active trip has **End trip** here too; it works as on **Trips** and
  takes you back there.
- The map draws the trail in the route's colour over the trip's route in
  grey, with its stops. A dashed line is a gap: more than 5 minutes (the
  staleness threshold) with no report. The marker starts on the last report.
- Under the map is the trip drawn as a line from its first report to its
  last, with a tick for each stop, a hatched stretch for each gap and the
  vehicle's speed above it. **Play** replays the trip at **1x**, **10x** or
  **60x**. Drag along the line, or use the arrow keys, to step through the
  reports one at a time. The replay needs JavaScript; the rest of the page
  does not.
- **Stops** starts with the trip's scheduled times and, once the trip is over,
  how it left the first stop and reached the last. Then it lists each stop
  with when it was due, when the vehicle left it
  (reached it, for the last stop) and how early or late that was, by the same
  rule as **Analytics**, so the two pages agree. A stop inside the on-time
  window says **On time** in green with the difference beside it. An early
  stop is red and a late one is blue, as in the OneBusAway apps. A stop your
  schedule does not mark as a timepoint shows its time but is not counted. On
  a running trip, a stop the vehicle has not got to yet says **not yet**. A
  trip run by headway shows only when it passed each stop, and a trip started
  with only a route has no stop list.
- **Gaps in reports** lists each gap's times, and **Show** goes to it on the
  map.
- **Download CSV** saves the trip's reports. One file holds at most 1,000, so
  a longer trip comes in parts, each named by its times.
- An open page of an active trip adds new reports every 10 seconds.

A trip run twice, say by a second vehicle after a breakdown, shows each run on
its own page, while **Analytics** takes each stop from whichever run passed it
first.

### Analytics

**Analytics** answers two questions about your service: did the scheduled
trips run, and did they run on time. Its headline is the answer in a sentence,
such as **On time was 86% over the last 7 days**, and the line under it names
the route pulling the share down, when one is. When no stop was judged against
a timetable but there were gaps between vehicles, as on a schedule run wholly
from GTFS frequencies, the headline is about the gaps instead, such as **6% of
gaps were bunched and 18% were long over the last 7 days**, and the line under
it names the route bunched most often, when one is above the rest (see
[Gaps between vehicles](#gaps-between-vehicles)). Frequent timetabled service
is judged on time as well as by its gaps, so it keeps the On time headline.

It opens on the seven days that end yesterday. **Day** shows one day,
**7 days** a week and **28 days** four weeks. The date box picks the last day
shown: choose a date and click **Show**, or step back and forward with the
arrows beside it. Every choice is in the page's address, so a bookmark is a
saved report.

Over 7 or 28 days:

- Five figures cross the top: **On time**, **Trips run**, **Early**, **Late**
  and **Stops with no report**, and **Bunched** and **Long gaps** after them
  when a route has frequent service, from its GTFS frequencies or a
  timetable every 12 minutes or more often at a stop. Click one to draw
  it day by day in the chart under them. With **Compare with the 7 days
  before** (or the 28 days before) on, the period before is drawn behind as a
  dashed line and each figure says how many points it moved.
- **By route** has a row for each route with service, added up over the days.
- **Early and late by hour of day** shows, for the trips leaving in each hour,
  the share of their stops that were late, with the share that were early
  stacked on it. On time is the space above.
- **How early and how late** counts trips by the minute they reached their
  last stop against the timetable, with the on-time window shaded.
- **Gaps between vehicles** and its two charts follow, when a route has
  frequent service.

A single day has tiles in place of the figures and their chart: four, and
**Bunched** and **Long gaps** too on a day with frequent service.
**Show these numbers as a table** under each chart opens the same numbers as a
table.

The columns of **By route**:

- **Trips run**: how many of the trips the schedule has, holidays included, a
  driver ran. A driver's trip counts for the run of its GTFS trip nearest to
  when the driver started it, so a trip started a few minutes before
  midnight, or run after midnight, lands on the right day. A trip your
  schedule runs from its GTFS frequencies, every few minutes rather than to a
  timetable, counts once for each run its frequencies schedule: every 10
  minutes from 06:00 to 22:00 is 96 runs. Each trip a driver starts fills the
  scheduled run nearest it, so a run that left a little late still counts in
  the hour it was due, and the day it belongs to is the one whose frequencies
  cover when it started.
- **On time**: the share of stops the vehicles passed from 1 minute early to 5
  minutes late, with a bar that splits early, on time and late. A stop counts
  when the vehicle leaves it, the last stop when the vehicle reaches it, so a
  driver waiting at the first stop before departure is not early. When the
  agency's schedule marks timepoints, only those stops are counted. A trip
  run from the GTFS frequencies has no timetable to be early or late against
  (its headway is the minutes between vehicles), so its stops are not
  counted here; [Gaps between vehicles](#gaps-between-vehicles) measures it
  instead.
- **Early** and **Late**: the shares of the stops judged that were outside
  that window. Early is red and late is blue, as in the OneBusAway apps.
- **No report**: the share of stops the report cannot judge, because the
  phone sent nothing within 2 minutes either side of the vehicle passing the
  stop. They are left out of **On time** rather than counted late. A high
  number usually means phones losing signal or the app being stopped. A trip
  whose schedule has no route shape has no report everywhere.

Click a route to open its day, the last day shown. Its timetabled trips are
listed by departure, each with when it was scheduled, the vehicle and driver
that ran it (**Not run** if none), how it left the first stop and reached the
last, and its own on-time share. Inside the on-time window a stop says **On
time** with the difference beside it; outside it, how early or late.
**Replay** opens the trip's page, with its time at every stop. **All routes**
goes back.

A route with frequent service also gets **Bunched** and **Long gaps** tiles
and a card for each direction, such as **Runs to Downtown**, listing its runs
in the order they left the first stop. Each run shows when it left, its
vehicle and driver, and the gap to the run before it there, such as **2 min
after Vehicle 12, every 10 min**, marked **Bunched** or **Long** when it was.
**Bunched at** is how many stops the run's gap to the vehicle ahead was
bunched at, of those it was judged at (see [Gaps between
vehicles](#gaps-between-vehicles)). A run whose phone sent nothing near the
first stop left at a time **not known**, and one that ended before leaving it
**did not leave**. When gaps are measured at none of the stops of a trip run
from the GTFS frequencies, as when its only timepoint is its last stop, its
runs are listed by when they started, each left at a time **not known**, or
**did not leave** when it ended before leaving the trip's first stop.
A trip run from the GTFS frequencies has no timetable, so
it is listed only here: the card says how often it runs and how many of its
runs ran, such as **every 10 min, 06:00 to 22:00: 41 of 96 runs**. A
timetabled trip is listed in both places when it is part of the frequent
service.

**Download CSV**, on a single day, saves that day's route rows as a
spreadsheet. Today's figures are worked out afresh at most once a minute, so
they fill in as the day goes on. A past day is worked out once its service has
ended and then kept in memory until the server next reloads the schedule,
daily by default, so points a phone uploads late from its offline queue count
only from that reload on. A day older than yesterday is also kept in the
database the first time it is worked out after that, and read from there, so a
period opens quickly however long it is. A kept day does not change: points
uploaded later still show on the trip's page but not here, and a schedule
reload leaves it judged against the timetable it ran to. After the server
starts, and after each schedule reload or new day, it works out in the
background any of the last 56 days it has not kept yet, one at a time. Just
after an upgrade that takes a few minutes, during which a long period can be
slow to open; a period further back is worked out the first time it is
opened, which can take several seconds. Kept days are deleted with the
location history they came from, when `LOCATION_RETENTION_PERIOD` is set, and
the background work then stops at the first day the history is still whole
for. Your server
administrator can change the 1 and 5 minutes (`REPORT_ON_TIME_EARLY` and
`REPORT_ON_TIME_LATE`, see [`configuration.md`](configuration.md)); the days
kept are worked out again under the new window. A kept day is also worked
out again once after an upgrade that changes what Analytics counts.

#### Gaps between vehicles

A rider on a route run from GTFS frequencies has no timetable to time their
trip by, and one at a stop served every few minutes doesn't need one, so what
they notice is how long they wait for the next vehicle. Analytics measures
that as the gaps between vehicles, for each route and direction with frequent
service: trips your schedule runs from its GTFS frequencies, however often
they run, and a timetable every 12 minutes or more often at a stop.

A timetable runs that often where two departures in a row are no more than 12
minutes apart, and two trips due at the same time are one departure, as a
rider sees them. Five or more an hour is the line Transport for London draws
for high-frequency bus routes: riders turn up at random at a stop served that
often. Served less often, they time their trips by the timetable, and **On
time** already says how well it was kept. A timetabled trip is still judged on
time, whether it is measured here or not, while a trip run from GTFS
frequencies is measured only here.

- A **gap** is the time between two vehicles leaving a stop, whichever
  vehicles they were, so a vehicle that overtakes another counts where it
  passed. Gaps are measured at the stops **On time** would judge, except a
  trip's last stop, and count in the hour the later vehicle left.
- The **scheduled gap** is what the schedule promises at that stop then. For
  trips run from GTFS frequencies it is the headway they set. For a
  timetable it is the spacing between two departures in a row, which applies
  from halfway after the earlier departure to halfway to the next, so a
  vehicle is judged against the spacing up to the departure nearest when it
  left. With departures at 08:00, 08:08 and 08:20, a vehicle leaving at 08:13
  is judged against 8 minutes and one leaving at 08:15 against 12. That way a
  vehicle on time, or a little early or late, is judged against the gap the
  riders waiting for it were promised.
- All the trips of the route and direction at a stop add up there: two
  patterns every 10 minutes through one stop make every 5 minutes at it, and
  so does a timetable every 10 minutes beside trips run from GTFS frequencies
  every 10 minutes.
- A gap is **bunched** under 25% of the scheduled gap and a **long gap** over
  150%: on a route every 10 minutes, under 2.5 minutes and over 15.
- **Extra wait** is how much longer than the schedule promises a rider who
  turned up at random waited, on average, in minutes. It is below zero when
  the vehicles came more often than scheduled.
- A gap is **unknown**, and not judged, when a vehicle whose phone sent
  nothing within 2 minutes either side of the stop could have left between
  the two. The first vehicle after a break in service has no gap, so the
  evening's first vehicle is not a long gap after the morning's last. On a
  timetable, a spacing of more than 12 minutes is a break in service.

Only drivers' trips count, not Rider mode, and only vehicles running the app
are seen. Two of them close together are bunched whatever else ran, but a
vehicle without the app looks like a long gap. So each row shows the route's
**Trips run** beside its gaps: when many runs are missing, many long gaps and
a high extra wait may be vehicles the page could not see. **Trips run** counts
all of the route's trips, measured here or not: both directions, and the trips
outside its frequent service, such as an evening every 20 minutes.

The section has a row for each route and direction, with **Gaps judged** (and
how many were unknown), **Bunched**, **Long gaps**, **Extra wait (min)** and
the whole route's **Trips run**. Under it:

- **Bunched and long gaps by hour of day** shows, by the hour the later
  vehicle left, the share of gaps that were long, with the share bunched
  stacked on it.
- **How even the gaps were** counts gaps by each tenth of their scheduled gap,
  from under 10% to 300% and over, with the bunched and long bands shaded.

A timetabled trip whose departure from a stop has no other within 12 minutes
of it, such as an early trip half an hour before the rest, is left out of the
gaps at that stop: it adds nothing to the scheduled gap, and its vehicle is
not counted in any gap there. **On time** still judges it.

### The feed itself

Hand this address to whoever runs your OneBusAway instance, or to any other
GTFS-Realtime consumer:

```
https://your-server/gtfs-rt/vehicle-positions
```

Maglev, the Go OneBusAway server, reads it as-is and shows your vehicles. The
Java OneBusAway server needs a second address as well, the trip updates feed,
which says how late each vehicle is:

```
https://your-server/gtfs-rt/trip-updates
```

It shows a vehicle only while the server knows how late it is, so not one on a
headway route, one whose driver gave only the route, or one off its route. See
[`deployment.md` §11](deployment.md#11-connecting-the-feed-to-onebusaway) for
the details your IT contact needs.

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

If your server administrator has turned on feed authentication, opening either
address in a browser shows `missing API key` instead — a browser cannot send the
key the feed now requires. That is expected, and it does not mean the feed is
down. Make yourself a key (see [API keys](#api-keys) below) and check
the feed with
`curl -H "X-API-Key: <your key>" "https://your-server/gtfs-rt/vehicle-positions?format=json"`.

### API keys

When feed authentication is on, every consumer of the feed needs a key, and
you make and revoke those keys on the **API keys** page. With it off the
page still works, but the feed does not ask for a key.

To give a consumer a key, type who it is for into **New key for** and click
**Create key**. The name is only a label for you, for example "OneBusAway". The
page then shows the key under **Copy it now. It cannot be shown again.** Copy
it, with **Copy key** or by hand, before you leave the page and pass it to the
consumer privately; they send it in
an `X-API-Key` header on every request. The server keeps no readable copy, so a
lost key cannot be looked up: make a new one and deactivate the old one.

Give each consumer its own key. The list shows each key's name, when it was
created, when it was last used and whether it is **Active**, so separate keys
tell you who is still reading the feed and let you cut one consumer off without
touching the others. **Last used** says **never** until the key's first request
and is updated at most once a minute after that.

To cut a consumer off, click **Deactivate** on its row and confirm **Deactivate
this key?** The dialog says it cannot be turned back on. The feed refuses that
key from its next request. There is no reactivate: if the consumer should have access again, make
it a new key.

### Feed health

**Feed health** shows what became of every position report the driver phones
sent and every fetch of the feed, minute by minute for the last hour, plus
totals. Its headline says in a sentence whether anything is wrong. Five tiles
give how many vehicles reported in the last full minute and the last hour's
counts, and three charts draw the vehicles reporting, the reports and the
fetches a minute. A vehicle counts once in each minute the server accepted a
live report from it; a phone uploading positions it saved offline does not
count. The counts are kept in memory:
a server restart starts them again, and the page says when counting began. It
reloads every minute. Your IT contact can read the report and fetch counts,
and the vehicles reporting each minute, from `GET /api/v1/admin/health`.

The **Outcomes** table:

| Row | What it means |
|---|---|
| **Reports accepted** | The server took the position. |
| **Rejected as invalid** | The phone sent a report the server would not take: malformed (400), for a trip the driver is not running (409), or in the wrong format (415). |
| **Rejected, signed out or not allowed** | The phone was not signed in, or the driver is not assigned to the vehicle, or the vehicle is deactivated (401, 403). |
| **Rejected, rate limited** | The phone reported more often than once every 5 seconds on average (429). |
| **Rejected, server error** | The server failed to handle the report, usually because the database did (5xx). |
| **Feed fetches served** | A consumer got the feed. |
| **Refused, missing or bad API key** | Only with feed authentication on: the consumer sent no key, an unknown key or a deactivated one. |
| **Bad request** | The consumer asked for an address the feed does not understand. |
| **Server error** | The server failed to build the feed. |

**Show the last hour minute by minute** opens the same counts for each minute,
newest first, with any rejection in amber. Use it to find when a problem
started.

**When the rejected share climbs**, look at the most common reason:

- **invalid**: often one phone. A 409 means the driver is reporting for a trip
  that is no longer running, for example one ended from **Trips**. Ask them to
  start their trip again. If it began right after an app update, tell your IT
  contact.
- **signed out or not allowed**: check **Drivers and users** and **Vehicles** for an account
  or vehicle you deactivated, or an assignment you removed. A short burst right
  after you do that is expected; it stops once the driver signs in again.
- **rate limited**: a phone sending too often, usually one running an old or
  modified app.
- **server error**: tell your IT contact the minute it started. The server's
  logs and its `/ready` check will say whether the database is reachable.

**When refused fetches climb**, a consumer is using a missing or deactivated
key. Steady refusals every minute are a consumer polling with a key you revoked:
give them a new one or ask them to stop.

A phone that cannot reach the server sends nothing, so it shows up as fewer
reports, not as rejections. **Alerts** tells you which vehicle has gone quiet.

---

## 9. Daily operations checklist

**Before service**

1. Open **Vehicles** and **Drivers and users**. Confirm the headlines count
   the vehicles you expect to be available and the drivers on your roster.
2. Check **Trips** with **Running** chosen. It should be empty. Anything left
   over is yesterday's trip that was never ended; see section 12.
3. Before the first vehicle reports, **Overview** says **No vehicles are on a
   trip** and the top bar says **0 reporting**. That is normal, not a fault.
4. As the first vehicles pull out, confirm the headline on **Overview** starts
   counting them and the top bar's count climbs.

**During service**

1. Keep **Live map** or **Overview** open. Every vehicle that should be out
   should be a marker on the map and on its route's line on **Overview**.
2. A vehicle that vanishes from the map has stopped reporting for longer than
   the staleness threshold. It shows on **Alerts** first, as **Gone quiet**;
   call the driver before assuming a fault.
3. Spot-check the feed with `?format=json` if a rider or OneBusAway reports
   something missing.

**After service**

1. **Trips** → **Completed**: every run of the day should be there, with a
   sensible length under **Ended**.
2. **Trips** → **Running**: should be empty. Every trip still listed as
   **Running** is a driver who did not tap **End trip**. Ask them to reopen
   the app and end it; the app remembers the trip and will take them straight
   back to the tracking screen.
3. **Overview**: **On a trip now** back to 0, and the top bar back to **0
   reporting** a few minutes after the last vehicle.

---

## 10. Rider mode (optional)

Rider mode is an optional extra where ordinary riders' phones, running a
separate anonymous rider app, help fill in positions for trips no driver phone
is covering. The server checks each reported position against your GTFS
schedule and route shape, and only publishes one when it is convincing. It is
**off** unless your IT contact deliberately turned it on, and when it is off it
changes nothing at all about the driver-reported feed.

### The Rider mode page

**Rider mode** in the menu shows what it is doing. When rider mode is off,
the page says so and names the setting that turns it on. When it is on:

- **Riders**, **Rides in progress**, **In the feed** and **Trusted feeds**: how many rider
  phones have registered (and how many are trusted or blocked), how many rides
  are in progress, how many vehicles riders are placing in the feed right now,
  and how many trusted feeds are set.
- **Rides per service day**: a bar for each of the last 28 service days,
  today's last, counting the rides on that day's trips. Point at a bar for how
  many trips those rides covered and how many of their positions matched; the
  line above the chart adds the 28 days up.
- **Schedule riders are checked against**: the GTFS schedule the server loaded,
  and when.
- **Trusted feeds**: each GTFS-realtime feed of vehicle positions your IT
  contact told the server to believe, and whether its last poll worked. A
  rider whose position a trusted feed agrees with is **Confirmed**. Your own
  drivers' positions always count as trusted.
- **Rides**: every ride, newest first, 50 to a page; **In progress** and
  **Ended** show only those. Each shows the trip and route the rider picked,
  whether the ride is **Pending** (not enough matching positions yet),
  **Verified** or **Rejected**, and how many of its positions matched. For a ride in progress, **Riders on trip** counts the
  riders whose recent positions count for that trip, **In the feed** marks a
  trip the riders are placing, and **Driver trip** opens the page of the one
  driver trip running the same scheduled trip. An ended ride shows why it ended.

A vehicle goes in the feed from riders only when one trusted rider, or two
riders who agree, are on it. Your IT contact can read the same data as JSON
from `/api/v1/admin/rider/status` and `/api/v1/admin/rider/rides`. The settings
behind it are in [`deployment.md` §3](deployment.md#3-configuration-reference)
and the README's rider-mode section.

### Rider positions in the feed

Rider-derived vehicles are labelled **Rider-reported** and their ids look like
`rider:<trip id>:<date>`, so they are never confused with your own vehicles. They
never appear on the **Live map**, which shows driver-reported vehicles only.
A trip your own drivers are already reporting is never published from rider
data — but only when the driver picked the specific scheduled trip, not just a
route. If a driver only entered a route, the server has no scheduled trip to
match against, so rider coverage for that trip is not suppressed. If your
OneBusAway instance should receive agency positions only, your IT contact can
point it at the feed address with `?source=driver` on the end.

---

## 11. Data retention and privacy

**What is stored.** For every location report: the vehicle, the position
(latitude, longitude, and where available bearing, speed and accuracy), the
time the phone recorded it, the time the server received it, and the trip it
belonged to. For every trip: the driver, the vehicle, the route id, the
optional GTFS trip id, and the start and end times. For every account: name,
email address, role and a scrambled form of the password — never the password
itself. For every change made in the admin or its API, the
[Audit log](#who-changed-what-the-audit-log) keeps who made it, by account and
email, the address they connected from, and the fields it changed.

Taken together this is a **per-driver movement history**, and it should be
treated like one. Anyone with an admin or viewer account can see any vehicle's
positions: the **Live map**, the trip trails, and the **CSV** export on the
vehicle list. Drivers can see none of it — the app has no history screen, and
driver accounts cannot open `/admin`. So the real access control here is how
many admin and viewer accounts you hand out. Keep the number small, and
deactivate accounts when people change jobs.

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

Trip records, accounts and the Audit log are not deleted by this setting; only
the location points are. The Audit log has a setting of its own,
`AUDIT_LOG_RETENTION_PERIOD`, and is kept forever until it is set (see
[Who changed what](#who-changed-what-the-audit-log)).

---

## 12. Troubleshooting

| Symptom | Likely cause | What to do |
|---|---|---|
| Driver sees **You're not assigned to vehicle 4317.** after tapping a run on **Choose a run** | The vehicle is not assigned to that driver, or the assignment was removed. | **Drivers and users** → **Edit** the driver → **Vehicles this driver may pick** → pick the vehicle → **Assign**. Then have them tap the run again. |
| Driver sees **You already have a trip running. End it before starting another.** | An earlier trip was never ended, usually because it was started on another phone or the app was reinstalled. | Have the driver open the app on the phone that started the trip; it returns to the tracking screen for the old trip, where **End trip** ends it. Confirm on **Trips** → **Running** that it is gone. If the app no longer knows about the trip, end it yourself: **Trips** → **Running**, then **End trip** on the driver's row and confirm. Then have them tap the run again. |
| Driver sees **No vehicles are assigned to you** | No active vehicle is assigned to that account. | Check the vehicle is not deactivated (**Vehicles** → **Show deactivated**), then assign it (section 6) and have the driver tap **Check again**. |
| One vehicle missing from the feed and the map, others fine | That phone has stopped reporting for longer than the staleness threshold: no signal, app closed, trip never started, phone battery dead, or Android killed the app in the background. | Ask the driver what the line under the status card says. **No connection** is a coverage problem and clears itself. If the app is not on the tracking screen at all, the trip was never started or was ended. If it keeps dying when the screen locks, the background-location permission is not **"Allow all the time"**: redo step 6 of section 7. After the phone restarts or the app updates, reporting starts again by itself once the driver unlocks the phone, but only with **"Allow all the time"** and for a trip started less than 12 hours ago; otherwise a **Reports paused** notification asks the driver to open the app and tap **Resume trip**. A phone that was force-stopped, by the driver or by an aggressive battery saver (some Xiaomi, Samsung and Huawei phones), reports nothing until the driver opens the app, which also shows **Resume trip**: see the battery note in the [README's section 3.2](../README.md#32-android-driver-app-kotlin) and [dontkillmyapp.com](https://dontkillmyapp.com) for that phone's settings. |
| Feed completely empty during service hours | Nothing is reporting, or the server lost its database. | Check **Overview**: if the top bar says **0 reporting** and **On a trip now** is 0, it is the phones, so start with two or three drivers. If **On a trip now** is healthy but nothing is in the feed, contact IT: see the monitoring checks in [`deployment.md` §9](deployment.md#9-monitoring). |
| Driver phone shows **Signed out** | The sign-in could not be renewed: the account was deactivated or deleted, or its password changed, the phone went a week without using the app, or the driver signed out on another phone. | Have the driver tap **Sign in** on that card and sign in again. If you have since deactivated the account, sign-in is refused, which is the intended outcome. |
| A driver, admin or viewer cannot sign in and insists the password is right | The account is deactivated. Deactivation is deliberately indistinguishable from a wrong password on the sign-in screen. | **Drivers and users** → **Show deactivated** → find the row → if **Status** reads **Deactivated**, click **Activate**. |
| Someone you deactivated is still reporting, or their phone still works | Deactivating cuts off open sessions at once, so either the account is not deactivated or the phone is signed in with another account. | Confirm the row shows **Deactivated** on **Drivers and users**. Then find the vehicle's trip on **Trips** and read its **Driver** column to see which account is reporting. |
| Driver phone shows **Phone clock is wrong** | The phone's clock is wrong, so the server is rejecting its reports. | On the phone, turn on automatic date and time in Android settings, then restart the app. The line under the status card goes away once reports are accepted again. |
| Vehicles show on your **Live map** but not in OneBusAway | The server and OneBusAway are using different GTFS schedules. Drivers pick their route and run from the server's schedule, so the ids they send are that schedule's, and OBA can only match ids from its own. | Ask your IT contact to check that the server's `GTFS_STATIC_URL` and OneBusAway use the same GTFS static feed, and that OBA is pointed at the feed address. |
| **Deactivate** on a user returns a plain page saying **cannot deactivate the last active admin** | It is the only active admin account left. | Use your browser's Back button, create or reactivate another admin, then try again. |
| Vehicle form rejects an id | The id has spaces or other characters, or is over 50 characters, or already exists. | Use only letters, digits, dots, hyphens and underscores; keep it to at most 50 characters; check **Show deactivated** in case the id belongs to a deactivated vehicle. |
| `/admin` returns "not found" | The admin UI was switched off at install time (`ADMIN_UI_ENABLED=false`). | Ask your IT contact to enable it — see [`deployment.md` §3](deployment.md#3-configuration-reference). |

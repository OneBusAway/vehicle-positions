# Demo Environment

One command starts the whole system on your machine with nothing to configure:
the server, a sample agency's GTFS schedule, nine simulated vehicles driving
its routes, the admin map showing them, and the GTFS-RT feed a OneBusAway
server can read.

It is for showing the project and for trying a change against moving vehicles.
It is not a deployment: for that see [deployment.md](deployment.md).

## Run it

You need Docker with Compose v2, `make` and `openssl`, plus an internet
connection for the image downloads and the map tiles.

```bash
git clone https://github.com/OneBusAway/vehicle-positions.git
cd vehicle-positions
make demo
```

The first run builds two images and takes a few minutes; later runs reuse
them. When everything is up it prints where to look:

```
The demo is running. Sign in as admin@demo.example with password 3f9c0a...
  Admin map:    http://localhost:18080/admin/map
  GTFS-RT feed: http://localhost:18080/gtfs-rt/vehicle-positions?format=json
Stop it and remove everything it made with: make demo-down
```

The password is generated on your machine the first time, together with the
server's `JWT_SECRET`, and kept in `.env.demo.local` (git-ignored), so no
secret is checked in. Run `make demo` again at any time to see the same lines.

The server is published on `127.0.0.1` only, on port 18080 so that it does not
collide with the development stack or a OneBusAway server on 8080. To use
another port: `DEMO_PORT=9000 make demo`. The demo's database is not published
at all.

## What you will see

**The admin map.** Open the map link and sign in. Nine vehicles are on the
road within a few seconds, three on each route. Each reports its position
every six seconds and the map refreshes every ten. **Trips** lists each
vehicle's trips, and **View trail** on one draws where that trip has been.

**The feed.** `/gtfs-rt/vehicle-positions` is the GTFS Realtime feed, as
protobuf. Add `?format=json` to read it:

```bash
curl -s 'http://localhost:18080/gtfs-rt/vehicle-positions?format=json' | jq '.entity[0]'
```

```json
{
  "id": "sim-fe4aa6e4-001",
  "vehicle": {
    "trip": { "tripId": "R1-0-1615", "startDate": "20261113", "routeId": "R1" },
    "position": { "latitude": -1.30023, "longitude": 36.77566, "bearing": 267.3, "speed": 6.99 },
    "timestamp": "1794576649",
    "vehicle": { "id": "sim-fe4aa6e4-001" }
  }
}
```

(Numbers shortened here.) Every entity names its trip, route and service date
from the sample schedule, which is what lets a consumer match the vehicle to a
scheduled trip.

**The sample agency.** `demo/gtfs.zip` is the schedule of a made-up agency,
Demo Transit (agency id `DEMO`, timezone `Africa/Nairobi`). It has three routes
out of central Nairobi and back:

| Route | Id | Runs along | Outer terminal | Length |
|---|---|---|---|---|
| 1 | `R1` | Kenyatta Avenue, Valley Road, Ngong Road | Dagoretti Corner | 7.6 km |
| 2 | `R2` | Haile Selassie Avenue, Landhies Road, Jogoo Road | Donholm | 7.6 km |
| 3 | `R3` | Murang'a Road, Thika Road | Roysambu | 10.6 km |

A trip leaves each end of each route every 15 minutes, around the clock, every
day, so there is always something running whenever you start the demo. The
roads are real, so the vehicles follow streets on the map; the agency, its
stops and its timetable are not. The route shapes were traced from
OpenStreetMap data, © OpenStreetMap contributors, available under the Open
Database License.

**The vehicles.** The simulator (`cmd/simulator`) signs in as the admin, creates
nine vehicles with a driver account each, and then does what nine driver apps
would: each starts a trip from the schedule, reports its position along that
trip's shape, and ends the trip at the last stop. A vehicle joins a trip that is
already on the road at the point the timetable has it, keeps to the timetable
(it stands still for the 20 seconds scheduled at each stop), then waits at the
terminal for the next trip back and drives that. So the vehicles are on time,
and stay on their routes for as long as the demo runs.

## The simulator on any schedule

`make simulate` and `make smoke` run the same simulator against whatever
schedule your own server has loaded, so the rules it follows are worth knowing.
It reads the schedule from the server's catalog (`/api/v1/gtfs/...`), which
means it needs no copy of the feed and its trip and route ids are always ones
the server accepts. Each vehicle takes a route in turn and then:

- joins the trip that left most recently, at the point the timetable has it,
  and keeps to that timetable;
- at the last stop, ends the trip and waits for the next one leaving from that
  stop, if one does within 15 minutes; otherwise it joins a trip already on the
  road;
- when the schedule has nothing running, drives the next trip to leave from its
  first stop straight away, at the timetable's pace. The test feed
  `rider/testdata/fixture.zip` is like this at most hours: its trips are at
  08:00 and 25:00. A route with no service today is searched up to six days
  ahead for a trip;
- takes a different trip from the other vehicles where it can. A route with
  fewer trips than vehicles has them share one, and they report the same
  positions.

A vehicle that cannot go on, because its route has no trip to drive or the
server refuses to start one, logs why and stops. The simulator then exits `1`
when the run ends.

## How it is put together

`make demo` runs `docker-compose.demo.yml`, a Compose project of its own
(`vehicle-positions-demo`) that shares nothing with the development stack in
`docker-compose.yml`:

| Service | What it is |
|---|---|
| `db` | PostgreSQL, reachable only from the server |
| `server` | The server image from the repository's `Dockerfile`, with `GTFS_STATIC_URL` pointing at `demo/gtfs.zip` and the admin account created at first boot |
| `simulator` | `cmd/simulator`, built by `demo/simulator.Dockerfile`, running until the stack stops |

To watch the simulator work:

```bash
docker logs -f vehicle-positions-demo-simulator-1
```

## Stop and clean up

```bash
make demo-down
```

This stops the three containers and removes them, the demo's network, its
database volume, the two images it built and `.env.demo.local`. Nothing of the
demo is left running or stored; the next `make demo` starts from scratch with
a new password. Docker keeps the `postgres:17-alpine` image and its build
cache, which `docker image rm postgres:17-alpine` and `docker builder prune`
remove if you want the disk space back.

## Add the feed to a OneBusAway server

A OneBusAway server needs two things from the demo, and they must go together:

- the static schedule, `demo/gtfs.zip`, because the feed's trip and route ids
  only mean something next to it;
- the realtime feed, `http://localhost:18080/gtfs-rt/vehicle-positions`
  (protobuf, no `?format=json`, no API key), for agency `DEMO`.

Leave the demo running and point the server at both.

### Maglev

[Maglev](https://github.com/OneBusAway/maglev) is the OneBusAway server written
in Go. In its checkout, save this as `config.json`, with the path to your clone
of this repository:

```json
{
  "port": 4000,
  "env": "development",
  "api-keys": ["test"],
  "gtfs-static-feed": {
    "url": "/path/to/vehicle-positions/demo/gtfs.zip"
  },
  "gtfs-rt-feeds": [
    {
      "id": "vehicle-positions-demo",
      "agency-ids": ["DEMO"],
      "vehicle-positions-url": "http://localhost:18080/gtfs-rt/vehicle-positions",
      "refresh-interval": 10
    }
  ],
  "data-path": "./demo-gtfs.db"
}
```

Start it with `make run`, which builds Maglev and runs it with `config.json`,
then ask it for the agency's vehicles:

```bash
curl -s 'http://localhost:4000/api/where/vehicles-for-agency/DEMO.json?key=test' | jq '.data.list'
```

### The OneBusAway application suite

The Java server is easiest to run from
[onebusaway-docker](https://github.com/OneBusAway/onebusaway-docker). Its
README describes the variables it reads. Build the bundle from the URL of
`demo/gtfs.zip`, then give the `oba_app` service the same URL and the feed:

```yaml
    environment:
      - GTFS_URL=https://raw.githubusercontent.com/OneBusAway/vehicle-positions/main/demo/gtfs.zip
      - TZ=Africa/Nairobi
      - VEHICLE_POSITIONS_URL=http://host.docker.internal:18080/gtfs-rt/vehicle-positions
      - AGENCY_ID=DEMO
      - REFRESH_INTERVAL=10
```

`TZ` has to be the timezone in the feed's `agency.txt`. Inside a container
`localhost` is the container itself, which is why the feed's address is not the
one above. `host.docker.internal` is your machine as Docker
Desktop (macOS and Windows) names it. On Linux, attach the OneBusAway container
to the demo's network, `vehicle-positions-demo_default`, and use
`http://server:8080/gtfs-rt/vehicle-positions` instead.

For a deployed server rather than this demo, see
[Connecting the feed to OneBusAway](deployment.md#11-connecting-the-feed-to-onebusaway).

## Limits

- **It stops moving after 24 hours.** The simulator's driver tokens last for
  `ACCESS_TOKEN_TTL` (24 hours by default) and it does not renew them. After
  that its reports are refused with `401`. `make demo-down` and `make demo`
  start a fresh one.
- **Restarting it leaves accounts behind.** Every start of the simulator
  creates nine new vehicles and nine new driver accounts, named
  `sim-<run id>-NNN` and `sim-driver-NNN-<run id>@simulator.invalid`. If the
  containers are stopped and started again (a reboot, say) without
  `make demo-down`, the old ones stay in the admin lists, off the map.
- **The map needs the internet.** The admin map draws OpenStreetMap tiles,
  which it downloads as you look at it.
- **It is not secured for a network.** The database password is `postgres` and
  the feed has no API key, which is why the server is bound to `127.0.0.1`.

## Changing the sample feed

`demo/gtfs.zip` is generated by `demo/feed_test.go`, which holds the routes,
stop names, headway and speeds. After editing it, write the feed again:

```bash
WRITE_DEMO_FEED=1 go test ./demo -run TestWriteFeed
```

`go test ./demo` fails if the committed zip no longer matches the generator, and
checks the property the demo depends on: every route has a trip running in each
direction at every hour of the week.

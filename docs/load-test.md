# Load test

How the server held up with 50, 100 and 200 vehicles reporting every 10
seconds, each level for 10 minutes, on the resources of a small agency VM: one
CPU and 1 GB of memory. Every number on this page was measured in the runs
described here.

## Setup

- **When and what:** 2026-11-13, `main` at commit `592972f`.
- **Server:** built with the Dockerfile's build command,
  `CGO_ENABLED=0 GOOS=linux go build` (Go 1.26.1), and run in an `alpine:3.21`
  container with `--cpus 1 --memory 1g`. Settings were at their defaults apart
  from the database address, `JWT_SECRET`, the bootstrap admin account and the
  GTFS schedule, which was the demo feed (`demo/gtfs.zip`, see
  [demo.md](demo.md)). So location retention, rider mode and feed API keys were
  off.
- **Postgres:** `postgres:17-alpine` with its default configuration, in its own
  container, also given `--cpus 1 --memory 1g`: a second small VM, or a small
  managed database. On one VM running both, add the two CPU figures together.
- **Load:** `cmd/simulator` as it is on `main`, with
  `-vehicles N -interval 10s -duration 10m`, in a container sharing the server's
  network the way the demo runs it. Reports went over loopback, with no TLS and
  no proxy. Each vehicle signs in as its own driver, starts a trip from the demo
  schedule and reports its position along that trip.
- **Each level** started from an empty database and a new server. The simulator
  first creates its drivers, vehicles and assignments, which took between 25
  seconds (50 vehicles) and 1 minute 41 seconds (200). The numbers below are
  from the 10 minutes of driving after that.
- **Measured:** report latency from the simulator's own log lines
  (`POST 201 (12ms)`); `docker stats` about every 2 seconds; each container's
  cgroup `cpu.stat`, for average CPU and for time held back by the CPU limit;
  the feed, fetched from the host every 10 seconds through the published port;
  the database and `location_points` sizes at the start and end of the window.
- **Host:** Docker Desktop on an Apple M1 laptop, its VM given 8 CPUs and
  7.7 GiB, with other projects' containers running at the same time. Expect
  different numbers on a cloud VM.

The 50-vehicle level was run twice, because the first run's slow reports all
fell within five minutes in a row (see below).

## Results

Each item lists the runs in the same order: 50 vehicles (first run), 50
(rerun), 100, 200.

- **Errors:** none. 2,950, 2,950, 5,900 and 11,800 reports, every one answered
  `201`, and no request failed.
- **Report latency, p50 / p95 / p99:** 13 / 473 / 615 ms, 9 / 21 / 83 ms,
  10 / 67 / 82 ms, 4 / 23 / 98 ms. The slowest single report in each run took
  724, 93, 239 and 138 ms.
- **Server CPU, `docker stats` mean and highest sample:** 1.1% and 51.5%,
  0.6% and 5.8%, 0.9% and 9.3%, 1.5% and 19.0% of its one CPU.
- **Postgres CPU, same:** 1.4% and 62.4%, 1.0% and 12.1%, 1.4% and 20.2%,
  2.3% and 28.2%.
- **Memory, highest `docker stats` sample:** server 33, 31, 37 and 44 MiB;
  Postgres 51, 53, 61 and 56 MiB, out of 1 GB each.
- **Feed size:** 6,015, 12,015 and 24,015 bytes of protobuf for 50, 100 and 200
  vehicles, 120 bytes a vehicle. With `?format=json`: 14,777, 29,437 and 58,808
  bytes.
- **Feed fetch time from the host, p50 / p95:** 4.0 / 45.4 ms, 2.4 / 3.5 ms,
  3.1 / 9.7 ms, 3.7 / 8.1 ms.
- **Postgres growth per hour** (each 10-minute window's growth times six): the
  database grew 7.2, 7.1, 13.7 and 24.8 MiB an hour, almost all of it
  `location_points`, at 17,730, 17,700, 35,400 and 70,918 rows an hour and 356
  to 392 bytes a row including its indexes.

## What limits first

Nothing ran out. At 200 vehicles every report was accepted, neither container
averaged more than 2.3% of its CPU, and neither used more than 56 MiB of its
1 GB. In the order they would matter:

1. **Postgres CPU, during bursts.** The simulator starts every vehicle at the
   same moment, so each vehicle's report lands in the same second of every
   10-second cycle: in every run, all 50, 100 or 200 reports of a cycle were
   logged within one second of each other. During those bursts Postgres used
   up its one CPU and the kernel held it back, for 0.18, 1.88 and 0.35 seconds
   over the windows of the 50-vehicle rerun, the 100 and the 200. The server
   was never held back in those runs. So the p95 and p99 above are what a
   report sees when it arrives together with every other vehicle's. Real
   phones start their trips at different times, so their reports spread out
   over the 10 seconds.
2. **Disk, over time.** Growth follows the number of reports: 356 to 392 bytes
   of `location_points` each, which was 24.8 MiB an hour at 200 vehicles. The
   server also logs one line for every request, 149 bytes for a report, which
   was 10.1 MiB an hour for the reports alone at 200 vehicles. Neither stops
   on its own. `LOCATION_RETENTION_PERIOD` bounds the table (see
   [configuration.md](configuration.md#location-retention)), and the log needs
   rotation in whatever collects it.
3. **The host, more than the load.** The first 50-vehicle run had a p95 of
   473 ms, and every report slower than 200 ms fell within five minutes in a
   row, while the rerun had 21 ms and the 200-vehicle run 23 ms. These runs
   shared a laptop with other work. One run's tail on a shared machine says
   little, so repeat the test on the VM you will deploy to.

Not covered: more than 200 vehicles, so the point where something does run out
is still unknown; TLS, a reverse proxy and phones on mobile networks; admin UI
users, rider mode and feed API keys.

## How to repeat it

From a clone of this repository, with Docker and Go. `GOOS=linux` builds for
your machine's CPU, which is what Docker runs. Port 8086 is only an example.

```bash
OUT=$(mktemp -d)
CGO_ENABLED=0 GOOS=linux go build -o "$OUT/vehicle-positions" .
CGO_ENABLED=0 GOOS=linux go build -o "$OUT/simulator" ./cmd/simulator
PASSWORD=$(openssl rand -hex 16)

docker network create vp-load
docker run -d --name vp-load-db --network vp-load --cpus 1 --memory 1g \
  -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=vehicle_positions \
  postgres:17-alpine
until docker exec vp-load-db pg_isready -q -h 127.0.0.1 -U postgres; do sleep 1; done

docker run -d --name vp-load-server --network vp-load --cpus 1 --memory 1g \
  -p 127.0.0.1:8086:8080 \
  -v "$OUT/vehicle-positions:/usr/local/bin/vehicle-positions:ro" \
  -v "$PWD/demo/gtfs.zip:/demo/gtfs.zip:ro" \
  -e 'DATABASE_URL=postgres://postgres:postgres@vp-load-db:5432/vehicle_positions?sslmode=disable' \
  -e JWT_SECRET="$(openssl rand -hex 32)" -e GTFS_STATIC_URL=/demo/gtfs.zip \
  -e ADMIN_BOOTSTRAP_EMAIL=admin@load.example -e ADMIN_BOOTSTRAP_PASSWORD="$PASSWORD" \
  alpine:3.21 vehicle-positions
until curl -sf http://127.0.0.1:8086/health; do sleep 1; done

docker run --rm --network container:vp-load-server \
  -v "$OUT/simulator:/usr/local/bin/simulator:ro" \
  -e ADMIN_BOOTSTRAP_EMAIL=admin@load.example -e ADMIN_BOOTSTRAP_PASSWORD="$PASSWORD" \
  alpine:3.21 simulator -url http://localhost:8080 -vehicles 200 -interval 10s -duration 10m \
  >"$OUT/sim.log" 2>&1
```

The 10 minutes start when `starting simulator` appears in `$OUT/sim.log`. From
then until the run ends, in a second terminal:

```bash
docker stats vp-load-server vp-load-db
curl -s -o /dev/null -w '%{time_total}s %{size_download} bytes\n' \
  http://127.0.0.1:8086/gtfs-rt/vehicle-positions
```

At the start and at the end of the window, note the database size and how long
each container has been held back by its CPU limit (`throttled_usec`):

```bash
docker exec vp-load-db psql -U postgres -d vehicle_positions -Atc \
  "select pg_database_size(current_database()), pg_total_relation_size('location_points'), count(*) from location_points"
docker exec vp-load-db grep throttled_usec /sys/fs/cgroup/cpu.stat
docker exec vp-load-server grep throttled_usec /sys/fs/cgroup/cpu.stat
```

When the simulator exits, count the reports, list any that were not `201`, and
work out the latency percentiles from its log:

```bash
grep -c 'POST 201 (' "$OUT/sim.log"
grep -E 'POST [0-9]+ \(|POST failed' "$OUT/sim.log" | grep -v 'POST 201 ('
sed -n 's/.*POST 201 (\([0-9]*\)ms).*/\1/p' "$OUT/sim.log" | sort -n | awk '
  { v[NR] = $1 }
  END {
    split("50 95 99", p, " ")
    for (i = 1; i <= 3; i++) {
      k = NR * p[i] / 100
      if (k > int(k)) k = int(k) + 1
      printf "p%s %d ms\n", p[i], v[k]
    }
  }'
```

Then remove everything the run made:

```bash
docker rm -fv vp-load-server vp-load-db
docker network rm vp-load
rm -r "$OUT"
```

`docker rm -v` also removes the database's volume. For the next level, run it
all again with a different `-vehicles`.

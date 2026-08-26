# JobHawk

JobHawk is a single-user Go Telegram bot for saving and manually running job searches. It uses [Telego](https://github.com/mymmrac/telego), PostgreSQL 18, pgx v5, and sqlc.

## What is included

- Typed Ashby and Greenhouse filters for board name/token, exact location, and
  title words
- Workday CXS searches with tenant/site discovery from a job URL, partial
  location matching, and title-word filters
- Persistent search queries with common columns and source-specific JSONB filters
- Inline-keyboard menus for creating, listing, running, and deleting searches
- Non-blocking search execution with immediate Telegram progress feedback
- `/ashby`, `/greenhouse`, `/workday`, `/queries`, and one-shot `/search`
  command fallbacks
- Access restricted to one configured Telegram chat ID
- PostgreSQL 18 in Compose, pgx v5 pooling, and sqlc-generated query code
- A provider-independent `jobs.Job` result model
- Daily 09:00 subscriptions for every saved query, with one Telegram digest
- Date-scoped 15, 30, or 60 minute alerts for individual saved queries
- Durable first-seen job deduplication across queries and application restarts

Saving a query automatically includes it in the daily subscription. `/search`
still lets you run it once without affecting the daily schedule.

## Run locally

Requirements: Docker Compose, or Go 1.25 plus PostgreSQL 18. Create a Telegram bot token with [@BotFather](https://t.me/BotFather), message the bot once, and obtain your numeric chat ID from Telegram's `getUpdates` API.

```sh
cp .env.example .env
# Edit .env and set TELEGRAM_BOT_TOKEN.
# Also set TELEGRAM_CHAT_ID.
docker compose up --build
```

Compose starts PostgreSQL, applies the SQL files under `db/migrations` to a new
data volume, waits for database health, and then starts the bot. The bot loads
`.env` automatically for direct local runs; exported environment variables take
precedence.

The daily report runs at 09:00 in `DAILY_TIMEZONE` (default:
`America/Chicago`). Both the hour and timezone can be changed in `.env`.

To run the app directly instead:

```sh
make db-up
make run
```

## Ashby, Greenhouse, and Workday searches

Send `/start` or `/menu` to open the button interface:

1. Choose **Add search query**.
2. Choose Ashby, Greenhouse, or Workday, then enter the provider details and
   filters in the guided form. For Ashby and Workday, paste any public job URL
   from the target site.
3. Review the typed filters and choose **Save search**.

Use **Run daily report now** on the main menu to trigger the full scheduled
workflow for debugging. It runs every saved query, updates the `jobs` table,
and sends the same single digest as the 09:00 scheduler.

Choose **Search queries** to see saved searches. Selecting one opens its details with **Run query** and **Delete** buttons. Deletion requires confirmation and permanently removes the row from PostgreSQL.

From a query's details, choose **Create hourly alert**, enter the date as
`YYYY-MM-DD`, and select a 15, 30, or 60 minute interval. On that local date,
JobHawk runs the selected query at the chosen interval. Empty searches are
silent; any matching results produce a query-scoped Telegram notification.
The same query screen shows the active schedule and lets you delete it.

The creation flow is kept in memory while it is in progress; only a completed search is persisted. `/cancel` abandons the current draft.

The compact command form remains available as a fallback.

Save an Ashby search using any job URL from the target board:

```text
/ashby Snowflake Software | https://jobs.ashbyhq.com/snowflake/fc1923c1-b151-4458-a792-40d58331a5be | Warsaw, Poland | Software, Engineer
```

JobHawk derives the board name (`snowflake`) and fetches its currently published
jobs from `https://api.ashbyhq.com/posting-api/job-board/snowflake`. Location
comparison checks Ashby's readable structured address and raw location label,
including secondary locations; it is exact after trimming and case-insensitive.
Every title word must be present, also case-insensitively. You can enter the
board name instead of a full job URL in the command or guided flow.

Save the Point72 example with this Telegram command:

```text
/greenhouse Point72 SWE Internship 2027 | point72 | Warsaw, Poland | 2027, Internship, Software
```

The four pipe-separated fields are the query name, Greenhouse board token, location, and comma-separated title words. Location comparison is exact after trimming and is case-insensitive. Every title word must be present, also case-insensitively.

Save a Workday search with a public job URL from the target tenant and recruiting
site:

```text
/workday State Street Poland | https://statestreet.wd1.myworkdayjobs.com/Global/job/Munich-Germany/Working-Student_R-795614-1/apply | Poland | Working, Student
```

For Workday, the location is a case-insensitive partial match, so `Poland`
matches `Krakow, Poland`. Every title word must be present. JobHawk derives the
CXS endpoint from the URL, posts pages of 20 jobs to
`/wday/cxs/{tenant}/{site}/jobs`, and searches all returned pages.

Run or inspect saved queries:

```text
/queries
/search Point72 SWE Internship 2027
```

The one-shot search calls the selected provider, normalizes matching results into
`jobs.Job`, and returns at most ten jobs in Telegram.

## Daily subscriptions

Every saved query runs once per day. The runner executes all queries, normalizes
and upserts every match into `jobs`, and considers a job new only when its
provider identity has never been stored before. Jobs matched by multiple
queries appear once. After all queries finish, the bot sends one aggregate
Telegram report: either **No new jobs** or a list of newly discovered jobs.
Individual query failures do not prevent the other queries or the digest from
completing; the report includes a failure count.

## Hourly subscriptions

Hourly subscriptions are stored separately from saved queries and are scoped to
one query and one calendar date in `DAILY_TIMEZONE`. A schedule created for
today is eligible to run immediately; a future schedule begins at midnight on
its selected date. The scheduler checks for due work once per minute and keeps
the configured cadence based on the persisted next-run time, including across
application restarts. Expired schedules are removed automatically. Deleting a
saved query also deletes its hourly subscription.

For an existing PostgreSQL volume, apply any migrations that it predates once
before restarting the bot:

```sh
docker compose exec -T postgres psql -U jobhawk -d jobhawk < db/migrations/002_create_jobs.sql
docker compose exec -T postgres psql -U jobhawk -d jobhawk < db/migrations/003_create_hourly_search_queries.sql
```

## Database model

`search_queries` keeps common fields (`name`, `source_type`, `enabled`, and
timestamps) as SQL columns. `filters` is JSONB and is decoded into the
source-specific Go type `ashby.Filters`, `greenhouse.Filters`, or
`workday.Filters`. Query names
are unique; saving a query with an existing name updates it, including its
source type.

`jobs` stores one row per provider opening using the unique identity
`(source_type, source_key, external_id)`. Its normalized display fields are
refreshed on later sightings while `first_seen_at` remains unchanged.

`hourly_search_queries` stores one date-scoped schedule per saved query, with a
validated 15, 30, or 60 minute interval and its durable next run time. Its
foreign key uses `ON DELETE CASCADE` so schedules cannot outlive their query.

Change SQL under `db/` and regenerate the pgx v5 data layer with:

```sh
make generate
```

## Development

```sh
make test
make lint
make build
```

## Next steps

1. Add enable/disable controls for individual queries.
2. Add more source types with their own typed JSONB filter payloads.

Do not commit `.env`; it is ignored because it contains the bot token.

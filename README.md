# JobHawk

JobHawk is a single-user Go Telegram bot for saving and manually running job searches. It uses [Telego](https://github.com/mymmrac/telego), PostgreSQL 18, pgx v5, and sqlc.

## What is included

- Typed Greenhouse filters for board token, exact location, and title words
- Workday CXS searches with tenant/site discovery from a job URL, partial
  location matching, and title-word filters
- Persistent search queries with common columns and source-specific JSONB filters
- Inline-keyboard menus for creating, listing, running, and deleting searches
- Non-blocking search execution with immediate Telegram progress feedback
- `/greenhouse`, `/workday`, `/queries`, and one-shot `/search` command fallbacks
- Access restricted to one configured Telegram chat ID
- PostgreSQL 18 in Compose, pgx v5 pooling, and sqlc-generated query code
- A provider-independent `jobs.Job` result model

The alert polling/subscription loop is intentionally not implemented yet. Saving a query prepares it for that worker; `/search` lets you run it once now.

## Run locally

Requirements: Docker Compose, or Go 1.25 plus PostgreSQL 18. Create a Telegram bot token with [@BotFather](https://t.me/BotFather), message the bot once, and obtain your numeric chat ID from Telegram's `getUpdates` API.

```sh
cp .env.example .env
# Edit .env and set TELEGRAM_BOT_TOKEN.
# Also set TELEGRAM_CHAT_ID.
docker compose up --build
```

Compose starts PostgreSQL, applies `db/migrations/001_create_search_queries.sql` to a new data volume, waits for database health, and then starts the bot. The bot loads `.env` automatically for direct local runs; exported environment variables take precedence.

To run the app directly instead:

```sh
make db-up
make run
```

## Greenhouse and Workday searches

Send `/start` or `/menu` to open the button interface:

1. Choose **Add search query**.
2. Choose Greenhouse or Workday, then enter the provider details and filters in
   the guided form. For Workday, paste any public job URL from the target site.
3. Review the typed filters and choose **Save search**.

Choose **Search queries** to see saved searches. Selecting one opens its details with **Run query** and **Delete** buttons. Deletion requires confirmation and permanently removes the row from PostgreSQL.

The creation flow is kept in memory while it is in progress; only a completed search is persisted. `/cancel` abandons the current draft.

The compact command form remains available as a fallback.

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

## Database model

`search_queries` keeps common fields (`name`, `source_type`, `enabled`, and
timestamps) as SQL columns. `filters` is JSONB and is decoded into the
source-specific Go type `greenhouse.Filters` or `workday.Filters`. Query names
are unique; saving a query with an existing name updates it, including its
source type.

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

1. Add a polling service that loads enabled queries, runs each source, and records seen job IDs.
2. Deliver unseen matches through `telegram.Bot.Notify`.
3. Add enable/disable and delete commands for individual queries.
4. Add more source types with their own typed JSONB filter payloads.

Do not commit `.env`; it is ignored because it contains the bot token.

# JobHawk

JobHawk is a single-user Go Telegram bot for saving and manually running job searches. It uses [Telego](https://github.com/mymmrac/telego), PostgreSQL 18, pgx v5, and sqlc.
Currently, bot supports companies that use **Ashby, Greenhouse, or Workday**. 
Bot also supports generic text searches that fetch any filtered job-board URL and detect its configured empty-results message. 

Available subscriptions include daily subscriptions for every saved query, with one Telegram digest. And, optional date-scoped 15, 30, or 60 minute alerts for individual saved queries.

## Screenshots
<img width="356" height="340" alt="Screenshot 2026-08-31 at 22 24 21" src="https://github.com/user-attachments/assets/644f496d-dde9-4b4f-abda-479e2f9a0f83" />
<img width="445" height="442" alt="Screenshot 2026-08-31 at 22 37 52" src="https://github.com/user-attachments/assets/e9248b1c-11a9-41c8-a283-57f4a81c35a7" />


<img width="398" height="448" alt="Screenshot 2026-08-31 at 22 26 09" src="https://github.com/user-attachments/assets/eacbbb58-136f-4759-8dc8-f97fb53757ec" />
<img width="496" height="503" alt="Screenshot 2026-08-31 at 22 26 31" src="https://github.com/user-attachments/assets/b9f290f1-d25e-4136-bf48-cb44e653be6a" />
<img width="565" height="481" alt="Screenshot 2026-08-31 at 22 23 25" src="https://github.com/user-attachments/assets/26620f4f-0de8-46ec-8fd8-c4e91fcc6b9f" />


## Run locally

Requirements: Docker Compose, or Go 1.26.7 plus PostgreSQL 18. Create a
Telegram bot token with [@BotFather](https://t.me/BotFather) and message the bot
once. Its access-restricted reply includes the `TELEGRAM_CHAT_ID=<id>` setting
for that chat.

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

## Job board searches

Send `/start` or `/menu` to open the button interface:

1. Choose **Add search query**.
2. Choose Ashby, Greenhouse, Workday, or Text search, then enter the provider
   details and filters in the guided form. Text search accepts a complete job
   board results URL with its filters already applied.
3. Review the typed filters and choose **Save search**.

Use **Run daily report now** on the main menu to trigger the full scheduled
workflow for debugging. It runs every saved query, updates the `jobs` table,
and sends the same single digest as the 09:00 scheduler.



# tg-podcastotron
A Telegram bot that allows you to create your own podcast feeds from magnet links.
Please note that it is intended strictly for legal content and is in no way intended to be used for piracy.

## How to use
- You send the bot a magnet link (or paste it in the app)
- The bot gives you a button that opens the Mini App
- In the Mini App, you choose the files and the podcast feeds for the new episodes
- The app downloads the files, converts them to mp3 (it can join them into one episode), and uploads them to S3
- The app shows the progress of each episode. The bot sends you one message when all episodes are ready
- You add the feed link to your podcast app of choice

All management (episodes, feeds, invites, storage cleanup) is in the Mini App.
The bot only gets links, opens the app, accepts invites, and tells you when episodes are ready.

## What is needed to run this bot
- [mediary](https://github.com/dir01/mediary) instance
- S3 bucket (minio won't work out of the box, but PRs are welcome)
- Telegram bot token
- A public HTTPS URL for the Mini App (Telegram opens Mini Apps only over HTTPS)

Storage and background jobs are kept in a local SQLite database, so no external database is required.

## Configuration
Bot is configured via environment variables, here is a table:

| Variable                | Description                                                                                               |
| ----------------------- |:--------------------------------------------------------------------------------------------------------- |
| `MEDIARY_URL`           | Root endpoint of [mediary](https://github.com/dir01/mediary), my media downloader-encoder-uploader        |
| `DB_PATH`               | Path to the SQLite database file (storage + background jobs). Optional, defaults to `./db/sqlite.db`       |
| `BOT_TOKEN`             | Telegram bot token obtained from [@BotFather](https://t.me/BotFather)                                     |
| `ADMIN_USERNAME`        | Telegram username of a person who will be considered admin. This person can grant access to another users |
| `AWS_BUCKET_NAME`       | S3 bucket to store media files and actual podcast feeds                                                   |
| `AWS_REGION`            | AWS region for S3 bucket                                                                                  |
| `AWS_ACCESS_KEY_ID`     | AWS access key id which has access to configured bucket                                                   |
| `AWS_SECRET_ACCESS_KEY` | AWS secret access key for provided `AWS_ACCESS_KEY_ID`                                                    |
| `USER_PATH_SECRET`      | Just some secret string. We will use it to make user directories unguessable                              |
| `TG_MINI_APP_URL`       | Public HTTPS URL of the Mini App, e.g. `https://podcasts.example.com`. The bot sets it as its menu button  |
| `HTTP_ADDR`             | Address of the Mini App HTTP server. Optional, defaults to `:8080`                                        |
| `TG_MINI_APP_DEV_USER_ID` | Development only (ignored when `ENVIRONMENT=production`). Lets a normal browser open the Mini App as this Telegram user ID |
| `TG_MINI_APP_DEV_USERNAME` | Development only. The username of the dev user. Use `ADMIN_USERNAME` to get the admin page             |

## Running locally
- `cp .env.example .env` and fill in missing values
- `docker-compose up -d` to bring up [mediary](https://github.com/dir01/mediary) (and its Redis) and fake s3 ([localstack](https://github.com/localstack/localstack)).
*If you use Docker for Mac, it won't work, I recommend using [colima](https://github.com/abiosoft/colima)*
- `make run` will run the bot and the Mini App server

To work on the Mini App in a normal browser, set `TG_MINI_APP_DEV_USER_ID` (and `TG_MINI_APP_DEV_USERNAME`)
and open http://localhost:8080. To open it in Telegram, expose the server over HTTPS
(for example with `cloudflared tunnel --url http://localhost:8080`) and set `TG_MINI_APP_URL` to that URL.

## How the Mini App works
The Mini App is in the `tgminiapp` package: Go templates and [htmx](https://htmx.org), embedded in the binary.
Telegram gives the user identity to the app only as `initData` in JavaScript. Thus each full page load
gets the same shell page, and the shell loads the page content with htmx. Each htmx request sends
the `initData` in a header, and the server checks its signature with the bot token.

## Access
The user with `ADMIN_USERNAME` is the admin. The admin makes one-time invite links on the Admin page
of the Mini App. A friend opens the link, and the bot gives them access.


## Contribution and Support
If you want to contribute to this project or need help with running it, please contact me on Telegram: https://t.me/podcast_o_tron

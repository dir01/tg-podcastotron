# tg-podcastotron
A Telegram bot that allows you to create your own podcast feeds from magnet links.
Please note that it is intended strictly for legal content and is in no way intended to be used for piracy.

## How to use
- You send the bot a magnet link
- The bot downloads the torrent (you will be able to select which files to download)
- The bot converts the files to mp3 (optionally glues them together)
- The bot uploads the files to S3
- The bot creates a podcast feed for you and uploads it to S3
- The bot sends you the link to the podcast feed
- You can add the podcast feed to your podcast app of choice

## What is needed to run this bot
- [mediary](https://github.com/dir01/mediary) instance
- S3 bucket (minio won't work out of the box, but PRs are welcome)
- Redis instance (I've chosen to use Redis as default database solely because you can find a free Redis instance)
- Telegram bot token

## Configuration
Bot is configured via environment variables, here is a table:

| Variable                | Description                                                                                               |
| ----------------------- |:--------------------------------------------------------------------------------------------------------- |
| `MEDIARY_URL`           | Root endpoint of [mediary](https://github.com/dir01/mediary), my media downloader-encoder-uploader        |
| `REDIS_URL`             | Full `redis://username:password@host:port/db kind of URL. This redis will be used for storage             |
| `REDIS_URL_BG_JOBS`     | Full `redis://username:password@host:port/db kind of URL. This redis will be used for background jobs     |
| `BOT_TOKEN`             | Telegram bot token obtained from [@BotFather](https://t.me/BotFather)                                     |
| `ADMIN_USERNAME`        | Telegram username of a person who will be considered admin. This person can grant access to another users |
| `AWS_BUCKET_NAME`       | S3 bucket to store media files and actual podcast feeds                                                   |
| `AWS_REGION`            | AWS region for S3 bucket                                                                                  |
| `AWS_ACCESS_KEY_ID`     | AWS access key id which has access to configured bucket                                                   |
| `AWS_SECRET_ACCESS_KEY` | AWS secret access key for provided `AWS_ACCESS_KEY_ID`                                                    |
| `USER_PATH_SECRET`      | Just some secret string. We will use it to make user directories unguessable                              |

## Running locally
- `cp .env.example .env` and fill in missing values
- `docker-compose up -d` to bring up Redis, [mediary](https://github.com/dir01/mediary) and fake s3 ([localstack](https://github.com/localstack/localstack)).
*If you use Docker for Mac, it won't work, I recommend using [colima](https://github.com/abiosoft/colima)*
- `make run` will run the bot


## Contribution and Support
If you want to contribute to this project or need help with running it, please contact me on Telegram: https://t.me/podcast_o_tron

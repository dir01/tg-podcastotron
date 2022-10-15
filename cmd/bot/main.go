package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/joho/godotenv"
	"undercast-bot/auth"
	"undercast-bot/bot"
	"undercast-bot/mediary"
)

func main() {
	_ = godotenv.Load()
	botToken := os.Getenv("BOT_TOKEN")
	adminUsername := os.Getenv("ADMIN_USERNAME")
	mediaryURL := os.Getenv("MEDIARY_URL")

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	authService := auth.New(adminUsername)
	mediaryService := mediary.New(mediaryURL)
	ubot := bot.NewUndercastBot(botToken, authService, mediaryService)
	ubot.Start(ctx)
}

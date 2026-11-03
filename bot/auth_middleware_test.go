package bot

import (
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestIsReplyTo(t *testing.T) {
	cases := map[string]struct {
		update *models.Update
		want   bool
	}{
		"nil update":            {nil, false},
		"callback query":        {&models.Update{CallbackQuery: &models.CallbackQuery{}}, false},
		"message without reply": {&models.Update{Message: &models.Message{}}, false},
		"reply to other message": {
			&models.Update{Message: &models.Message{ReplyToMessage: &models.Message{ID: 2}}},
			false,
		},
		"reply to the message": {
			&models.Update{Message: &models.Message{ReplyToMessage: &models.Message{ID: 1}}},
			true,
		},
	}
	for name, tc := range cases {
		if got := isReplyTo(tc.update, 1); got != tc.want {
			t.Errorf("%s: isReplyTo() = %v, want %v", name, got, tc.want)
		}
	}
}

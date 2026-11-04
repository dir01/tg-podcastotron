package tgminiapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// telegramUser is the user object in the initData of a Mini App.
type telegramUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

var (
	errInitDataMissing = errors.New("init data is missing")
	errInitDataInvalid = errors.New("init data is not valid")
	errInitDataExpired = errors.New("init data expired")
)

// validateInitData checks the signature of the initData that Telegram gives to the
// Mini App, and returns the user. See
// https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app
func validateInitData(initData, botToken string, now time.Time, maxAge time.Duration) (*telegramUser, error) {
	if initData == "" {
		return nil, errInitDataMissing
	}
	values, err := url.ParseQuery(initData)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errInitDataInvalid, err)
	}
	hash := values.Get("hash")
	if hash == "" {
		return nil, fmt.Errorf("%w: no hash", errInitDataInvalid)
	}
	wantHash, err := hex.DecodeString(hash)
	if err != nil {
		return nil, fmt.Errorf("%w: hash is not hex", errInitDataInvalid)
	}

	secret := hmacSHA256([]byte("WebAppData"), []byte(botToken))
	// The documents do not agree if the "signature" field (Bot API 8.0) is part of
	// the data-check-string. Both forms need the bot token, so accept both.
	valid := false
	for _, skip := range [][]string{{"hash"}, {"hash", "signature"}} {
		got := hmacSHA256(secret, []byte(dataCheckString(values, skip...)))
		if hmac.Equal(got, wantHash) {
			valid = true
			break
		}
	}
	if !valid {
		return nil, fmt.Errorf("%w: wrong hash", errInitDataInvalid)
	}

	authDate, err := strconv.ParseInt(values.Get("auth_date"), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: auth_date", errInitDataInvalid)
	}
	if maxAge > 0 && now.Sub(time.Unix(authDate, 0)) > maxAge {
		return nil, errInitDataExpired
	}

	var user telegramUser
	if err := json.Unmarshal([]byte(values.Get("user")), &user); err != nil || user.ID == 0 {
		return nil, fmt.Errorf("%w: user", errInitDataInvalid)
	}
	return &user, nil
}

func dataCheckString(values url.Values, skip ...string) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		if !contains(skip, k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	lines := make([]string, len(keys))
	for i, k := range keys {
		lines[i] = k + "=" + values.Get(k)
	}
	return strings.Join(lines, "\n")
}

func hmacSHA256(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

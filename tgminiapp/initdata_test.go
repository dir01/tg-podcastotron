package tgminiapp

import (
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"testing"
	"time"
)

const testBotToken = "12345:test-token"

// signInitData makes initData like Telegram does. If withSignature is true, the
// data has a "signature" field that is part of the data-check-string.
func signInitData(t *testing.T, authDate time.Time, user string, withSignature bool) string {
	t.Helper()
	v := url.Values{}
	v.Set("auth_date", strconv.FormatInt(authDate.Unix(), 10))
	v.Set("query_id", "AAH")
	v.Set("user", user)
	if withSignature {
		v.Set("signature", "c2lnbmF0dXJl")
	}
	secret := hmacSHA256([]byte("WebAppData"), []byte(testBotToken))
	v.Set("hash", hex.EncodeToString(hmacSHA256(secret, []byte(dataCheckString(v, "hash")))))
	return v.Encode()
}

func TestValidateInitData(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	user := `{"id":42,"first_name":"Ann","username":"ann"}`

	for _, withSignature := range []bool{false, true} {
		got, err := validateInitData(signInitData(t, now.Add(-time.Hour), user, withSignature), testBotToken, now, 24*time.Hour)
		if err != nil {
			t.Fatalf("withSignature=%v: validateInitData() error = %v", withSignature, err)
		}
		if got.ID != 42 || got.Username != "ann" || got.FirstName != "Ann" {
			t.Errorf("withSignature=%v: user = %+v", withSignature, got)
		}
	}

	// The signature is also accepted when it is not part of the data-check-string.
	v, _ := url.ParseQuery(signInitData(t, now, user, false))
	v.Set("signature", "c2lnbmF0dXJl")
	if _, err := validateInitData(v.Encode(), testBotToken, now, time.Hour); err != nil {
		t.Errorf("signature outside of the check string: error = %v", err)
	}

	cases := map[string]struct {
		initData string
		token    string
		want     error
	}{
		"empty":       {"", testBotToken, errInitDataMissing},
		"wrong token": {signInitData(t, now, user, false), "other:token", errInitDataInvalid},
		"expired":     {signInitData(t, now.Add(-25*time.Hour), user, false), testBotToken, errInitDataExpired},
		"no user":     {signInitData(t, now, `{}`, false), testBotToken, errInitDataInvalid},
		"no hash":     {"auth_date=1&user=%7B%22id%22%3A1%7D", testBotToken, errInitDataInvalid},
	}
	for name, tc := range cases {
		if _, err := validateInitData(tc.initData, tc.token, now, 24*time.Hour); !errors.Is(err, tc.want) {
			t.Errorf("%s: error = %v, want %v", name, err, tc.want)
		}
	}

	// A changed field breaks the hash.
	v, _ = url.ParseQuery(signInitData(t, now, user, false))
	v.Set("user", `{"id":1,"username":"admin"}`)
	if _, err := validateInitData(v.Encode(), testBotToken, now, time.Hour); !errors.Is(err, errInitDataInvalid) {
		t.Errorf("changed user: error = %v, want errInitDataInvalid", err)
	}
}

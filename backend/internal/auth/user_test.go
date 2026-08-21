package auth

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUserJSON_OmitsPasswordHash(t *testing.T) {
	user := User{
		Username:     "admin",
		PasswordHash: "$2a$12$super-secret-password-hash",
	}

	encoded, err := json.Marshal(user)
	if err != nil {
		t.Fatalf("marshalling User: %v", err)
	}

	jsonBody := string(encoded)
	for _, forbidden := range []string{"PasswordHash", "password_hash", user.PasswordHash} {
		if strings.Contains(jsonBody, forbidden) {
			t.Errorf("marshalled User contains sensitive value %q: %s", forbidden, jsonBody)
		}
	}
}

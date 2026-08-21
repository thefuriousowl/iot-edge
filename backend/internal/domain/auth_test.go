package domain

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/go-playground/validator/v10"
)

func TestLoginResponseJSON_UsesAPIFieldNames(t *testing.T) {
	response := LoginResponse{
		AccessToken: "access-token",
		TokenType:   "Bearer",
		ExpiresIn:   900,
	}

	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshalling LoginResponse: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("unmarshalling LoginResponse JSON: %v", err)
	}
	want := map[string]any{
		"access_token": "access-token",
		"token_type":   "Bearer",
		"expires_in":   float64(900),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LoginResponse JSON = %#v, want %#v", got, want)
	}
}

func TestAuthRequestsJSON_DecodesAPIFieldNames(t *testing.T) {
	tests := []struct {
		name string
		body string
		got  any
		want any
	}{
		{
			name: "login",
			body: `{"username":"admin","password":"SecureP@ss123"}`,
			got:  &LoginRequest{},
			want: &LoginRequest{Username: "admin", Password: "SecureP@ss123"},
		},
		{
			name: "setup",
			body: `{"username":"admin","password":"SecureP@ss123","confirm_password":"SecureP@ss123"}`,
			got:  &SetupRequest{},
			want: &SetupRequest{Username: "admin", Password: "SecureP@ss123", ConfirmPassword: "SecureP@ss123"},
		},
		{
			name: "change password",
			body: `{"current_password":"OldP@ss123","new_password":"NewP@ss456","confirm_password":"NewP@ss456"}`,
			got:  &ChangePasswordRequest{},
			want: &ChangePasswordRequest{CurrentPassword: "OldP@ss123", NewPassword: "NewP@ss456", ConfirmPassword: "NewP@ss456"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := json.Unmarshal([]byte(tt.body), tt.got); err != nil {
				t.Fatalf("unmarshalling request: %v", err)
			}
			if !reflect.DeepEqual(tt.got, tt.want) {
				t.Errorf("decoded request = %#v, want %#v", tt.got, tt.want)
			}
		})
	}
}

func TestAuthRequestsValidation(t *testing.T) {
	validate := validator.New()
	tests := []struct {
		name    string
		request any
		wantErr bool
	}{
		{
			name:    "valid login",
			request: LoginRequest{Username: "admin", Password: "password"},
		},
		{
			name:    "login username too short",
			request: LoginRequest{Username: "ad", Password: "password"},
			wantErr: true,
		},
		{
			name:    "login password required",
			request: LoginRequest{Username: "admin"},
			wantErr: true,
		},
		{
			name:    "valid setup",
			request: SetupRequest{Username: "admin", Password: "password", ConfirmPassword: "password"},
		},
		{
			name:    "setup password too short",
			request: SetupRequest{Username: "admin", Password: "short", ConfirmPassword: "short"},
			wantErr: true,
		},
		{
			name:    "setup confirmation does not match",
			request: SetupRequest{Username: "admin", Password: "password", ConfirmPassword: "different"},
			wantErr: true,
		},
		{
			name:    "valid password change",
			request: ChangePasswordRequest{CurrentPassword: "old-password", NewPassword: "new-password", ConfirmPassword: "new-password"},
		},
		{
			name:    "current password required",
			request: ChangePasswordRequest{NewPassword: "new-password", ConfirmPassword: "new-password"},
			wantErr: true,
		},
		{
			name:    "new password too short",
			request: ChangePasswordRequest{CurrentPassword: "old-password", NewPassword: "short", ConfirmPassword: "short"},
			wantErr: true,
		},
		{
			name:    "new password confirmation does not match",
			request: ChangePasswordRequest{CurrentPassword: "old-password", NewPassword: "new-password", ConfirmPassword: "different"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate.Struct(tt.request)
			if tt.wantErr && err == nil {
				t.Fatal("validation succeeded, want an error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validation failed: %v", err)
			}
		})
	}
}

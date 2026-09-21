package winruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var secretEntropy = []byte("iot-edge/windows-runtime-secrets/v1")

type Secrets struct {
	DatabaseURL              string `json:"database_url"`
	JWTSecret                string `json:"jwt_secret"`
	PublisherMasterKeyID     string `json:"publisher_master_key_id,omitempty"`
	PublisherMasterKeyBase64 string `json:"publisher_master_key_base64,omitempty"`
}

func (s Secrets) Validate() error {
	if s.DatabaseURL == "" || len(s.JWTSecret) < 32 {
		return errors.New("database URL and JWT secret are required")
	}
	if (s.PublisherMasterKeyID == "") != (s.PublisherMasterKeyBase64 == "") {
		return errors.New("publisher master key fields must be configured together")
	}
	return nil
}

func WriteSecrets(filename string, secrets Secrets) error {
	if err := secrets.Validate(); err != nil {
		return err
	}
	wire := struct {
		Database             string `json:"database_url"`
		TokenSigningMaterial string `json:"jwt_secret"`
		PublisherKeyID       string `json:"publisher_master_key_id,omitempty"`
		PublisherKeyMaterial string `json:"publisher_master_key_base64,omitempty"`
	}{secrets.DatabaseURL, secrets.JWTSecret, secrets.PublisherMasterKeyID, secrets.PublisherMasterKeyBase64}
	// #nosec G117 -- this short-lived JSON buffer is immediately DPAPI-encrypted, cleared, and never logged or persisted as plaintext.
	encoded, err := json.Marshal(wire)
	if err != nil {
		return err
	}
	protected, err := ProtectMachine(encoded, secretEntropy)
	clear(encoded)
	if err != nil {
		return fmt.Errorf("protect runtime secrets: %w", err)
	}
	defer clear(protected)
	if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
		return err
	}
	temporary := filename + ".tmp"
	if err := os.WriteFile(temporary, protected, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, filename); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func ReadSecrets(filename string) (Secrets, error) {
	root, err := os.OpenRoot(filepath.Dir(filename))
	if err != nil {
		return Secrets{}, err
	}
	defer root.Close()
	file, err := root.Open(filepath.Base(filename))
	if err != nil {
		return Secrets{}, err
	}
	defer file.Close()
	ciphertext, err := io.ReadAll(io.LimitReader(file, 1024*1024))
	if err != nil {
		return Secrets{}, err
	}
	plaintext, err := UnprotectMachine(ciphertext, secretEntropy)
	clear(ciphertext)
	if err != nil {
		return Secrets{}, fmt.Errorf("unprotect runtime secrets: %w", err)
	}
	defer clear(plaintext)
	var wire struct {
		Database             string `json:"database_url"`
		TokenSigningMaterial string `json:"jwt_secret"`
		PublisherKeyID       string `json:"publisher_master_key_id,omitempty"`
		PublisherKeyMaterial string `json:"publisher_master_key_base64,omitempty"`
	}
	if err := json.Unmarshal(plaintext, &wire); err != nil {
		return Secrets{}, errors.New("protected runtime secrets are invalid")
	}
	secrets := Secrets{DatabaseURL: wire.Database, JWTSecret: wire.TokenSigningMaterial, PublisherMasterKeyID: wire.PublisherKeyID, PublisherMasterKeyBase64: wire.PublisherKeyMaterial}
	if secrets.Validate() != nil {
		return Secrets{}, errors.New("protected runtime secrets are invalid")
	}
	return secrets, nil
}

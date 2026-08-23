package publisher

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
	"strings"
)

const maxSecretKeyIDLength = 64

var (
	ErrInvalidSecretCipherKey  = errors.New("invalid Data Publisher secret cipher key")
	ErrSecretCipherKeyMismatch = errors.New("Data Publisher secret cipher key is unavailable")
)

type AESGCMSecretCipher struct {
	keyID string
	aead  cipher.AEAD
}

func NewAESGCMSecretCipher(keyID string, key []byte) (*AESGCMSecretCipher, error) {
	keyID = strings.TrimSpace(keyID)
	if !validSecretKeyID(keyID) || len(key) != 32 {
		return nil, ErrInvalidSecretCipherKey
	}
	keyCopy := append([]byte(nil), key...)
	defer zeroBytes(keyCopy)
	block, err := aes.NewCipher(keyCopy)
	if err != nil {
		return nil, ErrInvalidSecretCipherKey
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrInvalidSecretCipherKey
	}
	return &AESGCMSecretCipher{keyID: keyID, aead: aead}, nil
}

func validSecretKeyID(keyID string) bool {
	if keyID == "" || len(keyID) > maxSecretKeyIDLength || keyID != strings.TrimSpace(keyID) {
		return false
	}
	for _, character := range keyID {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func (secretCipher *AESGCMSecretCipher) Seal(ctx context.Context, additionalData, plaintext []byte) (string, []byte, error) {
	if secretCipher == nil || secretCipher.aead == nil || ctx == nil || len(plaintext) == 0 {
		return "", nil, ErrInvalidSecretMaterial
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	nonce := make([]byte, secretCipher.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", nil, err
	}
	ciphertext := secretCipher.aead.Seal(nonce, nonce, plaintext, additionalData)
	return secretCipher.keyID, ciphertext, nil
}

func (secretCipher *AESGCMSecretCipher) Open(ctx context.Context, keyID string, additionalData, ciphertext []byte) ([]byte, error) {
	if secretCipher == nil || secretCipher.aead == nil || ctx == nil {
		return nil, ErrSecretDecryption
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if keyID != secretCipher.keyID {
		return nil, ErrSecretCipherKeyMismatch
	}
	nonceSize := secretCipher.aead.NonceSize()
	if len(ciphertext) <= nonceSize {
		return nil, ErrSecretDecryption
	}
	plaintext, err := secretCipher.aead.Open(nil, ciphertext[:nonceSize], ciphertext[nonceSize:], additionalData)
	if err != nil {
		return nil, ErrSecretDecryption
	}
	return plaintext, nil
}

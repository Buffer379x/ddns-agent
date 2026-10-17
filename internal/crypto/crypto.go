package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Encryptor struct {
	gcm    cipher.AEAD
	keyHex string
}

func NewEncryptor(keyHex string) (*Encryptor, error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid encryption key hex: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key must be 32 bytes (64 hex chars), got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("creating AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}
	return &Encryptor{gcm: gcm, keyHex: keyHex}, nil
}

func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, e.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generating nonce: %w", err)
	}
	ciphertext := e.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return "v1:" + base64.StdEncoding.EncodeToString(ciphertext), nil
}

func (e *Encryptor) Decrypt(encoded string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(encoded, "v1:"))
	if err != nil {
		return "", fmt.Errorf("decoding base64: %w", err)
	}
	nonceSize := e.gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	plaintext, err := e.gcm.Open(nil, data[:nonceSize], data[nonceSize:], nil)
	if err != nil {
		return "", fmt.Errorf("decrypting: %w", err)
	}
	return string(plaintext), nil
}

func GenerateKey() string {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		panic("failed to generate encryption key: " + err.Error())
	}
	return hex.EncodeToString(key)
}

func LoadOrCreateKey(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		key := strings.TrimSpace(string(data))
		if _, err := NewEncryptor(key); err != nil {
			return "", fmt.Errorf("existing key file is invalid; restore it from backup: %w", err)
		}
		return key, nil
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("reading existing key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	key := GenerateKey()
	if _, err = f.WriteString(key); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	return key, nil
}

// OpenConfig accepts only authenticated ciphertext or an explicitly recognized legacy JSON object.
func (e *Encryptor) OpenConfig(value string) (string, error) {
	if strings.HasPrefix(strings.TrimSpace(value), "{") && json.Valid([]byte(value)) {
		return value, nil
	}
	return e.Decrypt(value)
}
func (e *Encryptor) Fingerprint() string {
	sum := sha256.Sum256([]byte(e.keyHex))
	return hex.EncodeToString(sum[:8])
}

// RecoveryKey is for local restricted backup files only. It is never returned by the API.
func (e *Encryptor) RecoveryKey() string { return e.keyHex }

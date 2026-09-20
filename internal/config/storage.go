package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"runtime"
)

func getMachineID() string {
	hostname, _ := os.Hostname()
	username := os.Getenv("USER")
	if username == "" {
		username = os.Getenv("USERNAME")
	}
	return fmt.Sprintf("%s-%s-%s", hostname, username, runtime.GOOS)
}

func deriveKey() []byte {
	machineID := getMachineID()
	hash := sha256.Sum256([]byte(machineID))
	return hash[:]
}

func EncryptAPIKey(apiKey string) (string, error) {
	if apiKey == "" {
		return "", nil
	}

	key := deriveKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(apiKey), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func DecryptAPIKey(encryptedKey string) (string, error) {
	if encryptedKey == "" {
		return "", nil
	}

	key := deriveKey()
	ciphertext, err := base64.StdEncoding.DecodeString(encryptedKey)
	if err != nil {
		return "", fmt.Errorf("failed to decode ciphertext: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	if len(ciphertext) < gcm.NonceSize() {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt: %w", err)
	}

	return string(plaintext), nil
}

// SetAPIKey stores the key in memory only. Saving here would publish a config
// the caller is still partway through applying.
func (c *Config) SetAPIKey(provider, apiKey string) error {
	encrypted, err := EncryptAPIKey(apiKey)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.AIProvider.Providers == nil {
		c.AIProvider.Providers = make(map[string]ProviderSettings)
	}

	settings := c.AIProvider.Providers[provider]
	settings.APIKey = encrypted
	c.AIProvider.Providers[provider] = settings
	return nil
}

func (c *Config) GetAPIKey(provider string) (string, error) {
	c.mu.RLock()
	encrypted := c.AIProvider.Providers[provider].APIKey
	c.mu.RUnlock()

	return DecryptAPIKey(encrypted)
}

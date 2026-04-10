package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
)

func Encrypt(plaintext, hexKey string) (string, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return "", fmt.Errorf("invalid hex key: %w", err)
	}
	if len(key) != 32 {
		return "", fmt.Errorf("key must be 32 bytes (64 hex chars), got %d bytes", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := aesGCM.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func Decrypt(encoded, hexKey string) (string, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return "", fmt.Errorf("invalid hex key: %w", err)
	}
	if len(key) != 32 {
		return "", fmt.Errorf("key must be 32 bytes (64 hex chars), got %d bytes", len(key))
	}

	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("invalid base64: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := aesGCM.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := aesGCM.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

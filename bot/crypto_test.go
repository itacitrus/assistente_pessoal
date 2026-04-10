package main

import (
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	plaintext := "ya29.a0AfH6SMBx-refresh-token-here"

	encrypted, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	if encrypted == plaintext {
		t.Fatal("encrypted should differ from plaintext")
	}

	decrypted, err := Decrypt(encrypted, key)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if decrypted != plaintext {
		t.Fatalf("expected %q, got %q", plaintext, decrypted)
	}
}

func TestDecryptWithWrongKey(t *testing.T) {
	key1 := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	key2 := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

	encrypted, err := Encrypt("secret", key1)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	_, err = Decrypt(encrypted, key2)
	if err == nil {
		t.Fatal("expected error decrypting with wrong key")
	}
}

func TestEncryptInvalidKeyLength(t *testing.T) {
	_, err := Encrypt("secret", "short-key")
	if err == nil {
		t.Fatal("expected error for invalid key length")
	}
}

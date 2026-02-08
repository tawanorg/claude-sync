package crypto

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strings"

	"filippo.io/age"
	"github.com/btcsuite/btcd/btcutil/bech32"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/curve25519"
)

type Encryptor struct {
	identity  *age.X25519Identity
	recipient *age.X25519Recipient
}

func NewEncryptor(keyPath string) (*Encryptor, error) {
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read age key: %w", err)
	}

	// Parse the identity (private key)
	identity, err := age.ParseX25519Identity(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, fmt.Errorf("failed to parse age identity: %w", err)
	}

	return &Encryptor{
		identity:  identity,
		recipient: identity.Recipient(),
	}, nil
}

func (e *Encryptor) Encrypt(plaintext []byte) ([]byte, error) {
	var buf bytes.Buffer

	w, err := age.Encrypt(&buf, e.recipient)
	if err != nil {
		return nil, fmt.Errorf("failed to create encryption writer: %w", err)
	}

	if _, err := w.Write(plaintext); err != nil {
		return nil, fmt.Errorf("failed to write encrypted data: %w", err)
	}

	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("failed to finalize encryption: %w", err)
	}

	return buf.Bytes(), nil
}

func (e *Encryptor) Decrypt(ciphertext []byte) ([]byte, error) {
	r, err := age.Decrypt(bytes.NewReader(ciphertext), e.identity)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt: %w", err)
	}

	plaintext, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read decrypted data: %w", err)
	}

	return plaintext, nil
}

func (e *Encryptor) PublicKey() string {
	return e.recipient.String()
}

func GenerateKey(keyPath string) error {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		return fmt.Errorf("failed to generate age key: %w", err)
	}

	if err := os.WriteFile(keyPath, []byte(identity.String()+"\n"), 0600); err != nil {
		return fmt.Errorf("failed to write age key: %w", err)
	}

	return nil
}

// GenerateKeyFromPassphrase derives a deterministic encryption key from a passphrase.
// The same passphrase will always generate the same key, allowing sync across devices
// without copying key files.
func GenerateKeyFromPassphrase(keyPath, passphrase string) error {
	// Use a fixed salt derived from "claude-sync" - this is intentional
	// so the same passphrase produces the same key on any device
	salt := sha256.Sum256([]byte("claude-sync-v1"))

	// Derive 32 bytes using Argon2id (memory-hard, resistant to GPU attacks)
	// Parameters: 64MB memory, 3 iterations, 4 threads
	key := argon2.IDKey([]byte(passphrase), salt[:], 3, 64*1024, 4, 32)

	// Clamp the scalar for X25519 (per RFC 7748)
	key[0] &= 248
	key[31] &= 127
	key[31] |= 64

	// Compute the public key
	var privateKey, publicKey [32]byte
	copy(privateKey[:], key)
	curve25519.ScalarBaseMult(&publicKey, &privateKey)

	// Encode as age identity string (Bech32 with AGE-SECRET-KEY- prefix)
	identityStr := encodeAgeIdentity(privateKey[:])

	if err := os.WriteFile(keyPath, []byte(identityStr+"\n"), 0600); err != nil {
		return fmt.Errorf("failed to write age key: %w", err)
	}

	return nil
}

// encodeAgeIdentity encodes a 32-byte scalar as an age identity string
func encodeAgeIdentity(scalar []byte) string {
	// age uses Bech32 encoding with HRP "age-secret-key-"
	// The bech32 library works with lowercase, then we convert to uppercase
	hrp := "age-secret-key-"

	// Convert 8-bit bytes to 5-bit groups using the bech32 library
	converted, err := bech32.ConvertBits(scalar, 8, 5, true)
	if err != nil {
		// This should never fail for valid input
		return ""
	}

	// Encode using bech32
	encoded, err := bech32.Encode(hrp, converted)
	if err != nil {
		return ""
	}

	// Age uses uppercase for secret keys
	return strings.ToUpper(encoded)
}

// ValidatePassphraseStrength checks if a passphrase is strong enough
func ValidatePassphraseStrength(passphrase string) error {
	if len(passphrase) < 8 {
		return fmt.Errorf("passphrase must be at least 8 characters")
	}
	if len(passphrase) < 12 {
		// Warn but allow
		return nil
	}
	return nil
}

func KeyExists(keyPath string) bool {
	_, err := os.Stat(keyPath)
	return err == nil
}

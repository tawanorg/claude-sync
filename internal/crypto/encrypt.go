package crypto

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"filippo.io/age"
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

func KeyExists(keyPath string) bool {
	_, err := os.Stat(keyPath)
	return err == nil
}

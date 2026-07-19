// Package projectenvsecrets encodes project-environment secrets for storage.
package projectenvsecrets

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

const reservedKey = "__sealed__"

var (
	// ErrKeyUnavailable means a sealed record cannot be decrypted safely.
	ErrKeyUnavailable = errors.New("project environment secrets: encryption key unavailable")
	// ErrReservedKey prevents users from producing an ambiguous envelope.
	ErrReservedKey = errors.New("project environment secrets: __sealed__ is reserved")
)

type sealedEnvelope struct {
	Version int    `json:"v"`
	Data    string `json:"data"`
}

// Codec dual-reads legacy plaintext maps and sealed envelopes. A nil Box
// deliberately preserves legacy plaintext mode for installations that have
// not configured a project-environment encryption key.
type Codec struct {
	box *secretbox.Box
}

func New(box *secretbox.Box) Codec {
	return Codec{box: box}
}

func (c Codec) Enabled() bool {
	return c.box != nil
}

func ValidateSecretKeys(secrets map[string]string) error {
	if _, ok := secrets[reservedKey]; ok {
		return ErrReservedKey
	}
	return nil
}

// IsSealed reports whether raw uses the reserved sealed-envelope shape.
// Callers that need to use the contents must still call Open to authenticate
// the ciphertext; this helper is only for choosing a migration operation.
func IsSealed(raw []byte) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 1 {
		return false
	}
	_, ok := fields[reservedKey]
	return ok
}

// Seal returns the versioned envelope whenever encryption is enabled.
func (c Codec) Seal(secrets map[string]string) ([]byte, error) {
	if err := ValidateSecretKeys(secrets); err != nil {
		return nil, err
	}
	plaintext, err := json.Marshal(secrets)
	if err != nil {
		return nil, fmt.Errorf("encode project environment secrets: %w", err)
	}
	if c.box == nil {
		return plaintext, nil
	}
	sealed, err := c.box.Seal(plaintext)
	if err != nil {
		return nil, fmt.Errorf("seal project environment secrets: %w", err)
	}
	return json.Marshal(map[string]sealedEnvelope{
		reservedKey: {Version: 1, Data: base64.StdEncoding.EncodeToString(sealed)},
	})
}

// Open validates and decrypts a stored secret document. Invalid sealed data
// is always an error: callers must not convert it into an empty secret map.
func (c Codec) Open(raw []byte) (map[string]string, error) {
	if len(raw) == 0 {
		return map[string]string{}, nil
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, errors.New("project environment secrets: invalid stored JSON object")
	}
	sealedRaw, isSealed := fields[reservedKey]
	if !isSealed {
		var legacy map[string]string
		if err := json.Unmarshal(raw, &legacy); err != nil || legacy == nil {
			return nil, errors.New("project environment secrets: invalid legacy plaintext map")
		}
		return legacy, nil
	}
	if len(fields) != 1 {
		return nil, errors.New("project environment secrets: sealed envelope must not contain sibling keys")
	}
	if c.box == nil {
		return nil, ErrKeyUnavailable
	}

	var envelope sealedEnvelope
	if err := json.Unmarshal(sealedRaw, &envelope); err != nil || envelope.Version != 1 || envelope.Data == "" {
		return nil, errors.New("project environment secrets: invalid sealed envelope")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(envelope.Data)
	if err != nil {
		return nil, fmt.Errorf("project environment secrets: invalid sealed ciphertext: %w", err)
	}
	plaintext, err := c.box.Open(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("project environment secrets: open sealed ciphertext: %w", err)
	}
	var secrets map[string]string
	if err := json.Unmarshal(plaintext, &secrets); err != nil || secrets == nil {
		return nil, errors.New("project environment secrets: invalid decrypted map")
	}
	if err := ValidateSecretKeys(secrets); err != nil {
		return nil, err
	}
	return secrets, nil
}

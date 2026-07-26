// Package providercredentials seals provider bearer tokens for database
// storage. Unlike the legacy project-environment codec, it has no plaintext
// compatibility mode: a missing master key always fails closed.
package providercredentials

import (
	"errors"
	"fmt"

	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

var ErrKeyUnavailable = errors.New("provider credentials: encryption key unavailable")

type Codec struct {
	box *secretbox.Box
}

func New(box *secretbox.Box) Codec {
	return Codec{box: box}
}

func (c Codec) Enabled() bool {
	return c.box != nil
}

func (c Codec) Seal(token string) ([]byte, error) {
	if c.box == nil {
		return nil, ErrKeyUnavailable
	}
	sealed, err := c.box.Seal([]byte(token))
	if err != nil {
		return nil, fmt.Errorf("seal provider credential: %w", err)
	}
	return sealed, nil
}

func (c Codec) Open(sealed []byte) (string, error) {
	if c.box == nil {
		return "", ErrKeyUnavailable
	}
	plaintext, err := c.box.Open(sealed)
	if err != nil {
		return "", fmt.Errorf("open provider credential: %w", err)
	}
	return string(plaintext), nil
}

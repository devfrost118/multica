package providercredentials

import (
	"bytes"
	"testing"

	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

func TestCodecRefusesPlaintextAndRoundTripsOnlySealedTokens(t *testing.T) {
	const token = "factory-secret-must-never-be-plaintext"

	if _, err := New(nil).Seal(token); err != ErrKeyUnavailable {
		t.Fatalf("Seal without key error = %v, want ErrKeyUnavailable", err)
	}

	box, err := secretbox.New(bytes.Repeat([]byte{7}, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	codec := New(box)
	sealed, err := codec.Seal(token)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if bytes.Contains(sealed, []byte(token)) {
		t.Fatal("sealed token contains plaintext")
	}
	opened, err := codec.Open(sealed)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if opened != token {
		t.Fatalf("Open() = %q", opened)
	}
}

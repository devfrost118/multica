package projectenvsecrets

import (
	"bytes"
	"errors"
	"testing"

	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

func TestCodecSealsAndOpensEnvelope(t *testing.T) {
	box, err := secretbox.New(bytes.Repeat([]byte{1}, secretbox.KeySize))
	if err != nil {
		t.Fatalf("new box: %v", err)
	}
	codec := New(box)

	stored, err := codec.Seal(map[string]string{"TOKEN": "top-secret"})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Contains(stored, []byte("top-secret")) {
		t.Fatalf("sealed envelope contains plaintext: %s", stored)
	}

	opened, err := codec.Open(stored)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got := opened["TOKEN"]; got != "top-secret" {
		t.Fatalf("opened TOKEN = %q, want top-secret", got)
	}
}

func TestCodecReadsLegacyPlaintextWithoutKey(t *testing.T) {
	opened, err := New(nil).Open([]byte(`{"TOKEN":"legacy"}`))
	if err != nil {
		t.Fatalf("open legacy plaintext: %v", err)
	}
	if got := opened["TOKEN"]; got != "legacy" {
		t.Fatalf("opened TOKEN = %q, want legacy", got)
	}
}

func TestCodecFailsClosedForSealedEnvelopeWithoutKeyOrAfterTampering(t *testing.T) {
	box, err := secretbox.New(bytes.Repeat([]byte{2}, secretbox.KeySize))
	if err != nil {
		t.Fatalf("new box: %v", err)
	}
	stored, err := New(box).Seal(map[string]string{"TOKEN": "top-secret"})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	if _, err := New(nil).Open(stored); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("open sealed data without key error = %v, want ErrKeyUnavailable", err)
	}

	tampered := append([]byte(nil), stored...)
	tampered[len(tampered)-3] ^= 1
	if _, err := New(box).Open(tampered); err == nil {
		t.Fatal("open tampered sealed data: expected error")
	}
}

func TestCodecRejectsReservedEnvelopeKey(t *testing.T) {
	if err := ValidateSecretKeys(map[string]string{"__sealed__": "user-value"}); !errors.Is(err, ErrReservedKey) {
		t.Fatalf("ValidateSecretKeys error = %v, want ErrReservedKey", err)
	}
}

func TestCodecIdentifiesSealedEnvelope(t *testing.T) {
	box, err := secretbox.New(bytes.Repeat([]byte{4}, secretbox.KeySize))
	if err != nil {
		t.Fatalf("new box: %v", err)
	}
	sealed, err := New(box).Seal(map[string]string{"TOKEN": "secret"})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if !IsSealed(sealed) {
		t.Fatal("IsSealed(envelope) = false, want true")
	}
	if IsSealed([]byte(`{"TOKEN":"legacy"}`)) {
		t.Fatal("IsSealed(legacy) = true, want false")
	}
}

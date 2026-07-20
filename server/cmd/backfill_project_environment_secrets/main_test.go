package main

import (
	"bytes"
	"testing"

	"github.com/multica-ai/multica/server/internal/projectenvsecrets"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

func TestReplacementForRecordRotationSkipsAlreadyRotatedEnvelope(t *testing.T) {
	oldBox, err := secretbox.New(bytes.Repeat([]byte{5}, secretbox.KeySize))
	if err != nil {
		t.Fatalf("new old box: %v", err)
	}
	currentBox, err := secretbox.New(bytes.Repeat([]byte{6}, secretbox.KeySize))
	if err != nil {
		t.Fatalf("new current box: %v", err)
	}
	oldCodec := projectenvsecrets.New(oldBox)
	currentCodec := projectenvsecrets.New(currentBox)
	raw, err := oldCodec.Seal(map[string]string{"TOKEN": "secret"})
	if err != nil {
		t.Fatalf("seal old envelope: %v", err)
	}

	replacement, update, err := replacementForRecord(raw, config{rotate: true}, oldCodec, currentCodec)
	if err != nil {
		t.Fatalf("first rotate: %v", err)
	}
	if !update {
		t.Fatal("first rotate should update old envelope")
	}
	if _, err := currentCodec.Open(replacement); err != nil {
		t.Fatalf("open rotated envelope with current key: %v", err)
	}

	_, update, err = replacementForRecord(replacement, config{rotate: true}, oldCodec, currentCodec)
	if err != nil {
		t.Fatalf("retry rotate: %v", err)
	}
	if update {
		t.Fatal("retry rotate should skip already-rotated envelope")
	}
}

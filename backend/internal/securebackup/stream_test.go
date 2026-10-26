package securebackup

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"testing"
	"time"
)

func TestStreamRoundTripAndTamper(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	plain := bytes.Repeat([]byte("synthetic private backup content"), 70000)
	var sealed bytes.Buffer
	before, e := Encrypt(context.Background(), &sealed, bytes.NewReader(plain), key, time.Unix(1700000000, 0))
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(sealed.Bytes(), []byte("synthetic private backup content")) {
		t.Fatal("plaintext exposed")
	}
	var result bytes.Buffer
	after, e := Decrypt(context.Background(), &result, bytes.NewReader(sealed.Bytes()), key)
	if e != nil || !bytes.Equal(result.Bytes(), plain) || before.PlainBytes != after.PlainBytes || !before.CreatedAt.Equal(after.CreatedAt) {
		t.Fatal("roundtrip", e)
	}
	for _, offset := range []int{0, 8, 16, 48, 52, sealed.Len() - 1} {
		altered := append([]byte(nil), sealed.Bytes()...)
		altered[offset] ^= 1
		if _, e = Decrypt(context.Background(), io.Discard, bytes.NewReader(altered), key); e == nil {
			t.Fatal("tamper", offset)
		}
	}
	for _, length := range []int{0, 47, 48, 100, sealed.Len() - 20, sealed.Len() - 1} {
		if _, e = Decrypt(context.Background(), io.Discard, bytes.NewReader(sealed.Bytes()[:length]), key); e == nil {
			t.Fatal("truncation", length)
		}
	}
	if _, e = Decrypt(context.Background(), io.Discard, bytes.NewReader(append(append([]byte(nil), sealed.Bytes()...), 1)), key); e == nil {
		t.Fatal("trailing bytes")
	}
	if _, e = Decrypt(context.Background(), io.Discard, bytes.NewReader(sealed.Bytes()), bytes.Repeat([]byte{8}, 32)); e == nil {
		t.Fatal("wrong key")
	}
	swapped := append([]byte(nil), sealed.Bytes()...)
	size := 4 + ChunkSize + 16
	a := append([]byte(nil), swapped[headerSize:headerSize+size]...)
	copy(swapped[headerSize:headerSize+size], swapped[headerSize+size:headerSize+2*size])
	copy(swapped[headerSize+size:headerSize+2*size], a)
	if _, e = Decrypt(context.Background(), io.Discard, bytes.NewReader(swapped), key); e == nil {
		t.Fatal("reorder")
	}
}
func TestStreamEmptyLimitsAndCancellation(t *testing.T) {
	key := bytes.Repeat([]byte{9}, 32)
	var sealed bytes.Buffer
	if _, e := Encrypt(context.Background(), &sealed, bytes.NewReader(nil), key, time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e := Decrypt(context.Background(), io.Discard, bytes.NewReader(sealed.Bytes()), key); e != nil {
		t.Fatal(e)
	}
	binary.BigEndian.PutUint32(sealed.Bytes()[48:52], ChunkSize+1)
	if _, e := Decrypt(context.Background(), io.Discard, bytes.NewReader(sealed.Bytes()), key); e == nil {
		t.Fatal("oversize")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := Encrypt(ctx, io.Discard, bytes.NewReader([]byte("data")), key, time.Now()); e == nil {
		t.Fatal("cancel")
	}
	if _, e := Encrypt(context.Background(), io.Discard, bytes.NewReader(nil), []byte("short"), time.Now()); e == nil {
		t.Fatal("invalid key")
	}
}

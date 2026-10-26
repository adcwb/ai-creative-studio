// Package securebackup defines a versioned authenticated streaming envelope.
// Keys are supplied explicitly by the operator; no credentials are discovered.
package securebackup

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"time"
)

const ChunkSize = 1 << 20
const MaxPlainBytes int64 = 104 << 30
const headerSize = 48

var magic = []byte("STBKP001")
var ErrEnvelope = errors.New("encrypted backup invalid, unavailable or unauthenticated")

type Info struct {
	CreatedAt  time.Time `json:"created_at"`
	PlainBytes int64     `json:"plain_bytes"`
}

func aeadFor(key, header []byte) (cipher.AEAD, error) {
	if len(key) != 32 || len(header) != headerSize {
		return nil, ErrEnvelope
	}
	// A random per-envelope 256-bit salt derives independent AES keys. Counters
	// are unique within that key; the header is also authenticated on every frame.
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("studio-backup-envelope-v1\x00"))
	mac.Write(header)
	derived := mac.Sum(nil)
	defer clear(derived)
	block, err := aes.NewCipher(derived)
	if err != nil {
		return nil, ErrEnvelope
	}
	return cipher.NewGCM(block)
}
func frameAAD(header []byte, counter uint64, size uint32) []byte {
	aad := make([]byte, headerSize+12)
	copy(aad, header)
	binary.BigEndian.PutUint64(aad[headerSize:], counter)
	binary.BigEndian.PutUint32(aad[headerSize+8:], size)
	return aad
}
func nonce(counter uint64) []byte {
	n := make([]byte, 12)
	binary.BigEndian.PutUint64(n[4:], counter)
	return n
}
func writeAll(w io.Writer, b []byte) error {
	n, e := w.Write(b)
	if e != nil {
		return e
	}
	if n != len(b) {
		return io.ErrShortWrite
	}
	return nil
}

func Encrypt(ctx context.Context, dst io.Writer, src io.Reader, key []byte, created time.Time) (Info, error) {
	info := Info{CreatedAt: created.UTC()}
	if created.IsZero() || created.Unix() < 0 {
		return info, ErrEnvelope
	}
	header := make([]byte, headerSize)
	copy(header, magic)
	binary.BigEndian.PutUint64(header[8:16], uint64(created.Unix()))
	if _, e := rand.Read(header[16:]); e != nil {
		return info, ErrEnvelope
	}
	aead, e := aeadFor(key, header)
	if e != nil {
		return info, e
	}
	if e = writeAll(dst, header); e != nil {
		return info, e
	}
	plain := make([]byte, ChunkSize)
	defer clear(plain)
	for counter := uint64(0); ; counter++ {
		if e = ctx.Err(); e != nil {
			return info, e
		}
		n, re := io.ReadFull(src, plain)
		if re != nil && re != io.EOF && re != io.ErrUnexpectedEOF {
			return info, re
		}
		if info.PlainBytes > MaxPlainBytes-int64(n) {
			return info, ErrEnvelope
		}
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(n))
		sealed := aead.Seal(nil, nonce(counter), plain[:n], frameAAD(header, counter, uint32(n)))
		if e = writeAll(dst, length[:]); e != nil {
			return info, e
		}
		if e = writeAll(dst, sealed); e != nil {
			return info, e
		}
		info.PlainBytes += int64(n)
		// A separately authenticated zero-length final frame is mandatory, including
		// after a short final data frame. Truncation is never a successful EOF.
		if n == 0 {
			return info, nil
		}
	}
}
func Decrypt(ctx context.Context, dst io.Writer, src io.Reader, key []byte) (Info, error) {
	var info Info
	header := make([]byte, headerSize)
	if _, e := io.ReadFull(src, header); e != nil || string(header[:8]) != string(magic) {
		return info, ErrEnvelope
	}
	seconds := binary.BigEndian.Uint64(header[8:16])
	if seconds > 253402300799 {
		return info, ErrEnvelope
	}
	info.CreatedAt = time.Unix(int64(seconds), 0).UTC()
	aead, e := aeadFor(key, header)
	if e != nil {
		return info, e
	}
	for counter := uint64(0); ; counter++ {
		if e = ctx.Err(); e != nil {
			return info, e
		}
		var length [4]byte
		if _, e = io.ReadFull(src, length[:]); e != nil {
			return info, ErrEnvelope
		}
		size := binary.BigEndian.Uint32(length[:])
		if size > ChunkSize || info.PlainBytes > MaxPlainBytes-int64(size) {
			return info, ErrEnvelope
		}
		sealed := make([]byte, int(size)+aead.Overhead())
		if _, e = io.ReadFull(src, sealed); e != nil {
			return info, ErrEnvelope
		}
		plain, err := aead.Open(sealed[:0], nonce(counter), sealed, frameAAD(header, counter, size))
		if err != nil {
			return info, ErrEnvelope
		}
		if size == 0 {
			var extra [1]byte
			n, err := io.ReadFull(src, extra[:])
			if n != 0 || err != io.EOF {
				return info, ErrEnvelope
			}
			return info, nil
		}
		e = writeAll(dst, plain)
		clear(plain)
		if e != nil {
			return info, e
		}
		info.PlainBytes += int64(size)
	}
}

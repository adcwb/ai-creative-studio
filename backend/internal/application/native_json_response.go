package application

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
)

type nativeJSONMedia struct {
	file     *os.File
	metadata []byte
	parts    map[string][2]int64
}

func (s *nativeJSONMedia) close() {
	if s != nil && s.file != nil {
		name := s.file.Name()
		_ = s.file.Close()
		_ = os.Remove(name)
	}
}
func (s *nativeJSONMedia) copy(ctx context.Context, marker string, out io.Writer) error {
	p, ok := s.parts[marker]
	if !ok || p[1] <= 0 {
		return providerFailure("invalid_provider_response")
	}
	_, e := io.CopyBuffer(out, contextBlobReader{ctx, io.NewSectionReader(s.file, p[0], p[1])}, make([]byte, 64<<10))
	return e
}
func (s *nativeJSONMedia) image(ctx context.Context, marker, mime string) (GenerationResult, error) {
	p, ok := s.parts[marker]
	if !ok || p[1] <= 0 {
		return GenerationResult{}, providerFailure("invalid_provider_response")
	}
	section := io.NewSectionReader(s.file, p[0], p[1])
	return nativeImageReader(contextReadSeeker{contextBlobReader{ctx, section}, section}, mime)
}

type nativeJSONParser struct {
	ctx         context.Context
	r           *bufio.Reader
	out         bytes.Buffer
	media       *nativeJSONMedia
	encoding    string
	selectField func([]string) bool
}

func newNativeJSONParser(ctx context.Context, r io.Reader, encoding string, selectField func([]string) bool) *nativeJSONParser {
	return &nativeJSONParser{ctx: ctx, r: bufio.NewReaderSize(contextBlobReader{ctx, r}, 64<<10), encoding: encoding, selectField: selectField}
}
func (p *nativeJSONParser) space() error {
	for {
		b, e := p.r.Peek(1)
		if e != nil {
			return e
		}
		if !bytes.ContainsRune([]byte(" \n\r\t"), rune(b[0])) {
			return nil
		}
		_, _ = p.r.ReadByte()
	}
}
func (p *nativeJSONParser) put(b []byte) error {
	if len(b) > 1<<20-p.out.Len() {
		return providerFailure("invalid_provider_response")
	}
	_, e := p.out.Write(b)
	return e
}
func (p *nativeJSONParser) smallString() (string, error) {
	var raw bytes.Buffer
	c, e := p.r.ReadByte()
	if e != nil || c != '"' {
		return "", providerFailure("invalid_provider_response")
	}
	raw.WriteByte(c)
	escaped := false
	for {
		c, e = p.r.ReadByte()
		if e != nil {
			return "", e
		}
		raw.WriteByte(c)
		if raw.Len() > 64<<10 {
			return "", providerFailure("invalid_provider_response")
		}
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == '"' {
			break
		}
	}
	var value string
	if json.Unmarshal(raw.Bytes(), &value) != nil {
		return "", providerFailure("invalid_provider_response")
	}
	return value, nil
}
func (p *nativeJSONParser) value(path []string) error {
	if len(path) > 32 {
		return providerFailure("invalid_provider_response")
	}
	if e := p.ctx.Err(); e != nil {
		return e
	}
	if e := p.space(); e != nil {
		return e
	}
	b, _ := p.r.Peek(1)
	switch b[0] {
	case '{':
		_, _ = p.r.ReadByte()
		if e := p.put([]byte{'{'}); e != nil {
			return e
		}
		seen := map[string]bool{}
		first := true
		for {
			if e := p.space(); e != nil {
				return e
			}
			c, _ := p.r.Peek(1)
			if c[0] == '}' {
				_, _ = p.r.ReadByte()
				return p.put([]byte{'}'})
			}
			if !first {
				c, e := p.r.ReadByte()
				if e != nil || c != ',' {
					return providerFailure("invalid_provider_response")
				}
				if e = p.put([]byte{','}); e != nil {
					return e
				}
				if e = p.space(); e != nil {
					return e
				}
			}
			first = false
			key, e := p.smallString()
			if e != nil {
				return e
			}
			fold := strings.ToLower(key)
			if seen[fold] {
				return providerFailure("invalid_provider_response")
			}
			seen[fold] = true
			raw, _ := json.Marshal(key)
			if e = p.put(raw); e != nil {
				return e
			}
			if e = p.space(); e != nil {
				return e
			}
			c0, e := p.r.ReadByte()
			if e != nil || c0 != ':' {
				return providerFailure("invalid_provider_response")
			}
			if e = p.put([]byte{':'}); e != nil {
				return e
			}
			next := append(append([]string(nil), path...), fold)
			if e = p.value(next); e != nil {
				return e
			}
		}
	case '[':
		_, _ = p.r.ReadByte()
		if e := p.put([]byte{'['}); e != nil {
			return e
		}
		index := 0
		for {
			if e := p.space(); e != nil {
				return e
			}
			c, _ := p.r.Peek(1)
			if c[0] == ']' {
				_, _ = p.r.ReadByte()
				return p.put([]byte{']'})
			}
			if index > 0 {
				c, e := p.r.ReadByte()
				if e != nil || c != ',' {
					return providerFailure("invalid_provider_response")
				}
				if e = p.put([]byte{','}); e != nil {
					return e
				}
			}
			next := append(append([]string(nil), path...), strconv.Itoa(index))
			if e := p.value(next); e != nil {
				return e
			}
			index++
		}
	case '"':
		if p.selectField(path) {
			_, _ = p.r.ReadByte()
			if p.media.file == nil {
				f, e := os.CreateTemp("", "studio-native-json-*")
				if e != nil {
					return e
				}
				p.media.file = f
			}
			offset, e := p.media.file.Seek(0, io.SeekCurrent)
			if e != nil {
				return e
			}
			text := &nativeJSONStringReader{ctx: p.ctx, r: p.r}
			var decoder io.Reader = base64.NewDecoder(base64.StdEncoding.Strict(), text)
			if p.encoding == "hex" {
				decoder = hex.NewDecoder(text)
			}
			size, e := io.CopyBuffer(p.media.file, decoder, make([]byte, 64<<10))
			if e != nil {
				return e
			}
			if !text.done {
				return providerFailure("invalid_provider_response")
			}
			marker := "@media:" + strconv.Itoa(len(p.media.parts))
			p.media.parts[marker] = [2]int64{offset, size}
			raw, _ := json.Marshal(marker)
			return p.put(raw)
		}
		value, e := p.smallString()
		if e != nil {
			return e
		}
		raw, _ := json.Marshal(value)
		return p.put(raw)
	default:
		var token []byte
		for {
			c, e := p.r.Peek(1)
			if e == io.EOF {
				break
			}
			if e != nil {
				return e
			}
			if bytes.ContainsRune([]byte(" \n\r\t,]}"), rune(c[0])) {
				break
			}
			token = append(token, c[0])
			_, _ = p.r.ReadByte()
			if len(token) > 1024 {
				return providerFailure("invalid_provider_response")
			}
		}
		if !json.Valid(token) {
			return providerFailure("invalid_provider_response")
		}
		return p.put(token)
	}
}
func (p *nativeJSONParser) next(strictEOF bool) (result *nativeJSONMedia, resultErr error) {
	p.out.Reset()
	p.media = &nativeJSONMedia{parts: map[string][2]int64{}}
	defer func() {
		if resultErr != nil {
			p.media.close()
		}
	}()
	if e := p.space(); e != nil {
		return nil, e
	}
	if e := p.value(nil); e != nil {
		if e == io.EOF {
			e = io.ErrUnexpectedEOF
		}
		return nil, e
	}
	if strictEOF {
		if e := p.space(); e != io.EOF {
			if e != nil {
				return nil, e
			}
			return nil, providerFailure("invalid_provider_response")
		}
	}
	p.media.metadata = append([]byte(nil), p.out.Bytes()...)
	return p.media, nil
}

// Only selected base64/hex strings use this reader. JSON escapes are decoded
// before binary decoding; raw controls, invalid escapes and premature EOF fail.
type nativeJSONStringReader struct {
	ctx  context.Context
	r    *bufio.Reader
	done bool
}

const jsonStringSpecial = "\"\\\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f"

func (s *nativeJSONStringReader) Read(out []byte) (int, error) {
	if s.done {
		return 0, io.EOF
	}
	n := 0
	for n < len(out) {
		if e := s.ctx.Err(); e != nil {
			return n, e
		}
		if s.r.Buffered() == 0 {
			if _, e := s.r.Peek(1); e != nil {
				return n, io.ErrUnexpectedEOF
			}
		}
		// Decoders often request only 1 KiB. Scan only bytes this call can
		// consume, rather than rescanning the full 64 KiB buffer per read.
		raw, _ := s.r.Peek(min(s.r.Buffered(), len(out)-n))
		span := bytes.IndexAny(raw, jsonStringSpecial)
		if span < 0 {
			span = len(raw)
		}
		if span > 0 {
			count := min(span, len(out)-n)
			copy(out[n:], raw[:count])
			_, _ = s.r.Discard(count)
			n += count
			continue
		}
		c, e := s.r.ReadByte()
		if e != nil {
			return n, e
		}
		if c == '"' {
			s.done = true
			if n > 0 {
				return n, nil
			}
			return 0, io.EOF
		}
		if c != '\\' {
			return n, providerFailure("invalid_provider_response")
		}
		c, e = s.r.ReadByte()
		if e != nil {
			return n, io.ErrUnexpectedEOF
		}
		switch c {
		case '"', '\\', '/':
		case 'n':
			c = '\n'
		case 'r':
			c = '\r'
		case 't':
			c = '\t'
		case 'b':
			c = '\b'
		case 'f':
			c = '\f'
		case 'u':
			var digits [4]byte
			if _, e = io.ReadFull(s.r, digits[:]); e != nil {
				return n, e
			}
			v, e := strconv.ParseUint(string(digits[:]), 16, 16)
			if e != nil || v > 127 {
				return n, providerFailure("invalid_provider_response")
			}
			c = byte(v)
		default:
			return n, providerFailure("invalid_provider_response")
		}
		out[n] = c
		n++
	}
	return n, nil
}
func mediaPath(path []string, want ...string) bool {
	if len(path) != len(want) {
		return false
	}
	for i, k := range want {
		if k != "*" && path[i] != k {
			return false
		}
	}
	return true
}
func nativeResponseError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return providerFailure("invalid_provider_response")
}

func (s *nativeJSONMedia) copyAudio(ctx context.Context, marker string, out io.Writer, format string) error {
	p, ok := s.parts[marker]
	if !ok {
		return providerFailure("invalid_provider_response")
	}
	var head [12]byte
	n, _ := s.file.ReadAt(head[:], p[0])
	if format == "wav" {
		if p[1] < 44 || n < 12 || string(head[:4]) != "RIFF" || string(head[8:12]) != "WAVE" {
			return providerFailure("invalid_provider_response")
		}
	} else if p[1] < 4 || n < 4 || !(string(head[:3]) == "ID3" || (head[0] == 255 && head[1]&0xe0 == 0xe0)) {
		return providerFailure("invalid_provider_response")
	}
	return s.copy(ctx, marker, out)
}

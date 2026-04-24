// Package aescbc holds the AES-CBC + PKCS#7 helpers shared between the device
// (encrypt) and the admin service (decrypt). Wire format used in this project:
//
//	wire = IV(16) || AES-CBC-PKCS7(plaintext)
//
// Callers typically also wrap `wire` in base64 for JSON transport.
package aescbc

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
)

// BlockSize is the AES block size (always 16 bytes).
const BlockSize = aes.BlockSize

// NormalizeKey returns a 16/24/32-byte key. If b's length is not one of those,
// SHA-256(b) is returned (32 bytes -> AES-256). This keeps key provisioning
// forgiving (string of any length still produces a valid AES key).
func NormalizeKey(b []byte) []byte {
	if len(b) == 16 || len(b) == 24 || len(b) == 32 {
		out := make([]byte, len(b))
		copy(out, b)
		return out
	}
	s := sha256.Sum256(b)
	return s[:]
}

// PadPKCS7 appends PKCS#7 padding so len(out) is a multiple of block.
// When len(p) is already aligned, a full block of padding is appended (RFC 5652).
func PadPKCS7(p []byte, block int) []byte {
	n := block - (len(p) % block)
	if n == 0 {
		n = block
	}
	out := make([]byte, len(p)+n)
	copy(out, p)
	for i := len(p); i < len(out); i++ {
		out[i] = byte(n)
	}
	return out
}

// UnpadPKCS7 removes PKCS#7 padding. Errors on malformed padding.
func UnpadPKCS7(p []byte, block int) ([]byte, error) {
	if len(p) == 0 || len(p)%block != 0 {
		return nil, errors.New("aescbc: bad pkcs7 length")
	}
	n := int(p[len(p)-1])
	if n < 1 || n > block {
		return nil, errors.New("aescbc: bad pkcs7 last byte")
	}
	for i := len(p) - n; i < len(p); i++ {
		if p[i] != byte(n) {
			return nil, errors.New("aescbc: bad pkcs7 padding")
		}
	}
	return p[:len(p)-n], nil
}

// Encrypt returns IV(16) || AES-CBC-PKCS7(plaintext). The IV is generated
// freshly per call from crypto/rand.
func Encrypt(key, plaintext []byte) ([]byte, error) {
	k := NormalizeKey(key)
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	iv := make([]byte, BlockSize)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, err
	}
	padded := PadPKCS7(plaintext, BlockSize)
	mode := cipher.NewCBCEncrypter(block, iv)
	ct := make([]byte, len(padded))
	mode.CryptBlocks(ct, padded)
	return append(iv, ct...), nil
}

// Decrypt parses IV(16) || ciphertext and returns the unpadded plaintext.
func Decrypt(key, wire []byte) ([]byte, error) {
	if len(wire) < BlockSize {
		return nil, fmt.Errorf("aescbc: short wire (%d < %d)", len(wire), BlockSize)
	}
	if (len(wire)-BlockSize)%BlockSize != 0 {
		return nil, errors.New("aescbc: ciphertext not block aligned")
	}
	iv, ct := wire[:BlockSize], wire[BlockSize:]
	k := NormalizeKey(key)
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	mode := cipher.NewCBCDecrypter(block, iv)
	plain := make([]byte, len(ct))
	mode.CryptBlocks(plain, ct)
	return UnpadPKCS7(plain, BlockSize)
}

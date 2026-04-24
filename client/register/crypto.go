package register

import (
	"encoding/base64"

	"xsocks5/common/crypto/aescbc"
)

// BuildRegisterPlayloadB64 implements the spec for JSON B's "playload" field:
//
//	playload = base64( IV(16) || AES-CBC-PKCS7( base64(JSON A) ) )
//
// Per spec, the *plaintext* fed to AES is the base64 encoding of the JSON A
// bytes, not the raw JSON. Exported so tests in other packages (e.g. the
// admin handler) can produce fixtures with the same algorithm.
func BuildRegisterPlayloadB64(aesKey, jsonA []byte) (string, error) {
	innerB64 := base64.StdEncoding.EncodeToString(jsonA)
	wire, err := aescbc.Encrypt(aesKey, []byte(innerB64))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(wire), nil
}

// buildRegisterPlayloadB64 keeps the lowercase name for the in-package
// caller; delegates to the exported version.
func buildRegisterPlayloadB64(aesKey, jsonA []byte) (string, error) {
	return BuildRegisterPlayloadB64(aesKey, jsonA)
}

// decodeEncryptedPayloadForTest is the inverse of buildRegisterPlayloadB64.
func decodeEncryptedPayloadForTest(aesKey []byte, b64Wire string) (string, error) {
	wire, err := base64.StdEncoding.DecodeString(b64Wire)
	if err != nil {
		return "", err
	}
	plain, err := aescbc.Decrypt(aesKey, wire)
	if err != nil {
		return "", err
	}
	out, err := base64.StdEncoding.DecodeString(string(plain))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

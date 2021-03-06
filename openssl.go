package openssl

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

// OpenSSL is a helper to generate OpenSSL compatible encryption
// with autmatic IV derivation and storage. As long as the key is known all
// data can also get decrypted using OpenSSL CLI.
// Code from http://dequeue.blogspot.de/2014/11/decrypting-something-encrypted-with.html
type OpenSSL struct {
	openSSLSaltHeader string
}

type openSSLCreds struct {
	key []byte
	iv  []byte
}

// New instanciates and initializes a new OpenSSL encrypter
func New() *OpenSSL {
	return &OpenSSL{
		openSSLSaltHeader: "Salted__", // OpenSSL salt is always this string + 8 bytes of actual salt
	}
}

// DecryptString decrypts a string that was encrypted using OpenSSL and AES-256-CBC
func (o *OpenSSL) DecryptString(passphrase, encryptedBase64String string, keystrength int) ([]byte, error) {
	data, err := base64.URLEncoding.DecodeString(encryptedBase64String)
	if err != nil {
		return nil, err
	}
	
	if len(data) < aes.BlockSize {
		return nil, fmt.Errorf("Cyphertext must be at least %d bytes decoded", aes.BlockSize)
	}

	saltHeader := data[:aes.BlockSize]
	salt := []byte{}
	if string(saltHeader[:8]) == o.openSSLSaltHeader {
		salt = saltHeader[8:]
		data = data[aes.BlockSize:]
	}
	creds, err := o.extractOpenSSLCreds2([]byte(passphrase), salt, keystrength)
	if err != nil {
		return nil, err
	}
	return o.decrypt(creds.key, creds.iv, data)
}

func (o *OpenSSL) decrypt(key, iv, data []byte) ([]byte, error) {
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("Data length must be a multiple of aes.BlockSize(%d)", aes.BlockSize)
	}
	c, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	cbc := cipher.NewCBCDecrypter(c, iv)
	cbc.CryptBlocks(data, data)
	out, err := o.pkcs7Unpad(data, aes.BlockSize)
	if out == nil {
		return nil, err
	}
	return out, nil
}

// EncryptString encrypts a string in a manner compatible to OpenSSL encryption
// functions using AES-256-CBC as encryption algorithm
func (o *OpenSSL) EncryptString(passphrase, plaintextString string) ([]byte, error) {
	salt := make([]byte, 8) // Generate an 8 byte salt
	_, err := io.ReadFull(rand.Reader, salt)
	if err != nil {
		return nil, err
	}

	data := make([]byte, len(plaintextString)+aes.BlockSize)
	copy(data[0:], o.openSSLSaltHeader)
	copy(data[8:], salt)
	copy(data[aes.BlockSize:], plaintextString)

	creds, err := o.extractOpenSSLCreds([]byte(passphrase), salt)
	if err != nil {
		return nil, err
	}

	enc, err := o.encrypt(creds.key, creds.iv, data)
	if err != nil {
		return nil, err
	}

	return []byte(base64.StdEncoding.EncodeToString(enc)), nil
}

func (o *OpenSSL) encrypt(key, iv, data []byte) ([]byte, error) {
	padded, err := o.pkcs7Pad(data, aes.BlockSize)
	if err != nil {
		return nil, err
	}

	c, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	cbc := cipher.NewCBCEncrypter(c, iv)
	cbc.CryptBlocks(padded[aes.BlockSize:], padded[aes.BlockSize:])

	return padded, nil
}

// openSSLEvpBytesToKey follows the OpenSSL (undocumented?) convention for extracting the key and IV from passphrase.
// It uses the EVP_BytesToKey() method which is basically:
// D_i = HASH^count(D_(i-1) || password || salt) where || denotes concatentaion, until there are sufficient bytes available
// 48 bytes since we're expecting to handle AES-256, 32bytes for a key and 16bytes for the IV
func (o *OpenSSL) extractOpenSSLCreds(password, salt []byte) (openSSLCreds, error) {
	m := make([]byte, 48)
	prev := []byte{}
	for i := 0; i < 3; i++ {
		prev = o.hash(prev, password, salt)
		copy(m[i*16:], prev)
	}
	return openSSLCreds{key: m[:32], iv: m[32:]}, nil
}

func (s *OpenSSL) extractOpenSSLCreds2(password, salt []byte, bitstrength int) (openSSLCreds, error) {
	keyLen := bitstrength / 8
	ivLen := aes.BlockSize

	byteCount := keyLen+ivLen

	out := make([]byte, byteCount)
	o := out[:16]
	var p []byte

	for {
		preHash := append(p, password...)
		preHash = append(preHash, salt...)
		hash := md5.Sum(preHash)
		copy(o, hash[:])
		p = o

		if len(o) == cap(o) {
			break
		}
		o = o[16:32]
	}
	return openSSLCreds{key: out[:keyLen], iv: out[keyLen:]}, nil
}

func (o *OpenSSL) hash(prev, password, salt []byte) []byte {
	a := make([]byte, len(prev)+len(password)+len(salt))
	copy(a, prev)
	copy(a[len(prev):], password)
	copy(a[len(prev)+len(password):], salt)
	return o.md5sum(a)
}

func (o *OpenSSL) md5sum(data []byte) []byte {
	h := md5.New()
	h.Write(data)
	return h.Sum(nil)
}

// pkcs7Pad appends padding.
func (o *OpenSSL) pkcs7Pad(data []byte, blocklen int) ([]byte, error) {
	if blocklen <= 0 {
		return nil, fmt.Errorf("Block length must be greater than 0")
	}
	padlen := 1
	for ((len(data) + padlen) % blocklen) != 0 {
		padlen = padlen + 1
	}

	pad := bytes.Repeat([]byte{byte(padlen)}, padlen)
	return append(data, pad...), nil
}

// pkcs7Unpad returns slice of the original data without padding.
func (o *OpenSSL) pkcs7Unpad(data []byte, blocklen int) ([]byte, error) {
	if blocklen <= 0 {
		return nil, fmt.Errorf("Block length must be greater than 0")
	}
	if len(data)%blocklen != 0 || len(data) == 0 {
		return nil, fmt.Errorf("Data length must be a multiple of block length and not 0")
	}
	padlen := int(data[len(data)-1])
	if padlen > blocklen || padlen == 0 {
		return nil, fmt.Errorf("Invalid padding")
	}
	pad := data[len(data)-padlen:]
	for i := 0; i < padlen; i++ {
		if pad[i] != byte(padlen) {
			return nil, fmt.Errorf("Invalid padding")
		}
	}
	return data[:len(data)-padlen], nil
}

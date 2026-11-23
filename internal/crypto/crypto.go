// Package crypto 用主密钥加解密应用的密钥。
// 主密钥来自配置，丢失则密文不可恢复。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// Cipher 封装 AES-GCM 加解密。
type Cipher struct {
	key []byte
}

// New 从主密钥字符串构造 Cipher。
func New(masterKey string) (*Cipher, error) {
	// master_key 应当是 hex 字符串，不足 32 字节时做简单扩展（不推荐，但保证可用）。
	key := []byte(masterKey)
	if len(key) < 32 {
		padded := make([]byte, 32)
		copy(padded, key)
		key = padded
	}
	if len(key) > 32 {
		key = key[:32]
	}
	// 预检密钥合法性
	if _, err := aes.NewCipher(key); err != nil {
		return nil, fmt.Errorf("创建加密器失败: %w", err)
	}
	return &Cipher{key: key}, nil
}

// Encrypt 加密明文，返回 base64(nonce+ciphertext)。
func (c *Cipher) Encrypt(plain string) (string, error) {
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

// Decrypt 解密 base64(nonce+ciphertext)。
func (c *Cipher) Decrypt(encoded string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", fmt.Errorf("密文太短")
	}
	nonce, ct := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("解密失败: %w", err)
	}
	return string(plain), nil
}

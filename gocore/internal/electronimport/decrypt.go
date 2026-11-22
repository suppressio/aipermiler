// Package electronimport copies the app's settings and history from Electron's
// store file into the core's store, once, at the service's first start (#296).
//
// Electron's file is written by electron-store, i.e. the `conf` package, with an
// encryption key (store/index.ts). The importer only reads it: it never writes,
// moves or deletes the file.
package electronimport

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha512"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf8"
)

const (
	// FileName is Electron's store file, in its userData folder.
	FileName = "aipermiler-config.json"
	// userDataName is the folder of Electron's userData: the app's productName.
	userDataName = "AIpermiler"
	// AppKey is the encryption key the app passes to electron-store (store/index.ts).
	// It is a development placeholder shipped in the app (#251), not a secret.
	AppKey = "dev-only-placeholder-change-before-release"

	ivSize       = 16
	pbkdf2Rounds = 10000
	keySize      = 32
)

// ErrUnsupportedFormat means the file is not in the format conf writes today
// (16-byte IV, ':', ciphertext). The older one, from `createDecipher` with a
// key derived without salt, is deliberately not supported: it is reported, not
// guessed.
var ErrUnsupportedFormat = errors.New("unsupported electron-store file format")

// ErrDecrypt means the file has the right format but could not be decrypted:
// wrong key, truncated or damaged.
var ErrDecrypt = errors.New("cannot decrypt the electron-store file")

// DevDirEnv is the development override of Electron's userData folder, an
// absolute path (as AIPERMILER_CONFIG_DIR is for the core's own folder).
const DevDirEnv = "AIPERMILER_ELECTRON_DIR"

// DefaultDir is Electron's userData folder: <user config dir>/AIpermiler, that
// is ~/.config/AIpermiler (or $XDG_CONFIG_HOME), %APPDATA%\AIpermiler and
// ~/Library/Application Support/AIpermiler, or the absolute DevDirEnv.
func DefaultDir() (string, error) {
	if override := os.Getenv(DevDirEnv); override != "" {
		if !filepath.IsAbs(override) {
			return "", fmt.Errorf("%s must be an absolute path", DevDirEnv)
		}
		return override, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config directory: %w", err)
	}
	return filepath.Join(base, userDataName), nil
}

// Decrypt reads a file written by conf with an encryptionKey: the IV (16
// bytes), ':', then AES-256-CBC ciphertext with PKCS#7 padding. The key is
// PBKDF2-SHA512(encryptionKey, salt, 10000, 32) where the salt is the IV as
// Node decodes it into a string, UTF-8 with U+FFFD for invalid bytes
// (`initializationVector.toString()` in conf), not the raw IV.
func Decrypt(data []byte, encryptionKey string) ([]byte, error) {
	if len(data) <= ivSize {
		return nil, fmt.Errorf("%w: file too short", ErrDecrypt)
	}
	if data[ivSize] != ':' {
		return nil, fmt.Errorf("%w: no IV separator, an older conf format", ErrUnsupportedFormat)
	}
	iv, ciphertext := data[:ivSize], data[ivSize+1:]
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("%w: ciphertext of %d bytes is not whole blocks", ErrDecrypt, len(ciphertext))
	}
	key, err := pbkdf2.Key(sha512.New, encryptionKey, []byte(nodeString(iv)), pbkdf2Rounds, keySize)
	if err != nil {
		return nil, fmt.Errorf("derive key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ciphertext)
	pad := int(plain[len(plain)-1])
	if pad == 0 || pad > aes.BlockSize {
		return nil, fmt.Errorf("%w: bad padding", ErrDecrypt)
	}
	for _, b := range plain[len(plain)-pad:] {
		if int(b) != pad {
			return nil, fmt.Errorf("%w: bad padding", ErrDecrypt)
		}
	}
	return plain[:len(plain)-pad], nil
}

// nodeString decodes bytes as Buffer.toString() does: UTF-8 following the
// WHATWG decoder, where an invalid sequence becomes one U+FFFD for its maximal
// invalid prefix. Go's own conversion gives one U+FFFD per byte, a different
// string for a truncated multi-byte sequence, hence a different salt.
func nodeString(b []byte) string {
	out := make([]rune, 0, len(b))
	var (
		code         rune
		needed       int
		lower, upper byte = 0x80, 0xBF
	)
	for i := 0; i < len(b); {
		c := b[i]
		if needed == 0 {
			i++
			switch {
			case c < 0x80:
				out = append(out, rune(c))
			case c >= 0xC2 && c <= 0xDF:
				needed, code = 1, rune(c&0x1F)
			case c >= 0xE0 && c <= 0xEF:
				switch c {
				case 0xE0:
					lower = 0xA0
				case 0xED:
					upper = 0x9F
				}
				needed, code = 2, rune(c&0x0F)
			case c >= 0xF0 && c <= 0xF4:
				switch c {
				case 0xF0:
					lower = 0x90
				case 0xF4:
					upper = 0x8F
				}
				needed, code = 3, rune(c&0x07)
			default:
				out = append(out, utf8.RuneError)
			}
			continue
		}
		if c < lower || c > upper {
			// The sequence ends here: one U+FFFD, and this byte is read again.
			needed, lower, upper = 0, 0x80, 0xBF
			out = append(out, utf8.RuneError)
			continue
		}
		lower, upper = 0x80, 0xBF
		code = code<<6 | rune(c&0x3F)
		needed--
		i++
		if needed == 0 {
			out = append(out, code)
		}
	}
	if needed != 0 {
		out = append(out, utf8.RuneError)
	}
	return string(out)
}

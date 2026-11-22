package electronimport_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/suppressio/aipermiler/gocore/internal/electronimport"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Every IV below was chosen by testdata/generate.js and encrypted by the real
// `conf` package: the salt is the IV as Node decodes it, invalid bytes included.
func TestDecryptRealConfFiles(t *testing.T) {
	t.Parallel()
	want := fixture(t, "plaintext.json")
	files, err := filepath.Glob(filepath.Join("testdata", "iv-*.bin"))
	if err != nil || len(files) < 10 {
		t.Fatalf("fixtures: %v, %d files", err, len(files))
	}
	for _, file := range files {
		t.Run(strings.TrimSuffix(filepath.Base(file), ".bin"), func(t *testing.T) {
			t.Parallel()
			got, err := electronimport.Decrypt(fixture(t, filepath.Base(file)), electronimport.AppKey)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("plaintext differs:\n%s", got)
			}
		})
	}
}

func TestDecryptWrongKey(t *testing.T) {
	t.Parallel()
	_, err := electronimport.Decrypt(fixture(t, "iv-ascii.bin"), "another key")
	if !errors.Is(err, electronimport.ErrDecrypt) {
		t.Fatalf("err = %v, want ErrDecrypt", err)
	}
}

func TestDecryptOlderFormatIsUnsupported(t *testing.T) {
	t.Parallel()
	// No ':' at byte 16: the createDecipher format of older conf versions.
	old := bytes.Repeat([]byte{0x41}, 48)
	_, err := electronimport.Decrypt(old, electronimport.AppKey)
	if !errors.Is(err, electronimport.ErrUnsupportedFormat) {
		t.Fatalf("err = %v, want ErrUnsupportedFormat", err)
	}
}

func TestDecryptRejectsTruncatedData(t *testing.T) {
	t.Parallel()
	good := fixture(t, "iv-ascii.bin")
	for name, data := range map[string][]byte{
		"empty":            {},
		"shorter than iv":  good[:10],
		"no ciphertext":    good[:17],
		"partial block":    good[:len(good)-1],
		"plain json (new)": []byte(`{"accounts":[]}`),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := electronimport.Decrypt(data, electronimport.AppKey); err == nil {
				t.Fatal("Decrypt succeeded")
			}
		})
	}
}

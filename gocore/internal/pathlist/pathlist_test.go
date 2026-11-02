package pathlist_test

import (
	"strings"
	"testing"

	"github.com/suppressio/aipermiler/gocore/internal/pathlist"
)

func TestSplit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		goos, value string
		want        []string
	}{
		{"linux", "/usr/bin:/opt/codex", []string{"/usr/bin", "/opt/codex"}},
		{"linux", "", nil},
		{"windows", `C:\Windows;"D:\Tools\Codex";E:\bin`, []string{`C:\Windows`, `D:\Tools\Codex`, `E:\bin`}},
		{"windows", `"D:\a;b\Codex";C:\x`, []string{`D:\a;b\Codex`, `C:\x`}},
	}
	for _, tt := range tests {
		t.Run(tt.goos+" "+tt.value, func(t *testing.T) {
			t.Parallel()
			got := pathlist.Split(tt.value, tt.goos)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") || len(got) != len(tt.want) {
				t.Errorf("Split(%q) = %q; want %q", tt.value, got, tt.want)
			}
		})
	}
}

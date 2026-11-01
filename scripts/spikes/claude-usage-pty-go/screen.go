package main

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// screen is a minimal VT renderer: enough cursor movement and erase handling to turn the
// TUI output (Ink on POSIX, ConPTY repaints on Windows) into the text a user would see.
type screen struct {
	g       [][]rune
	r, c    int
	sr, sc  int
	rows    int
	cols    int
	pending []byte
}

func newScreen(rows, cols int) *screen {
	s := &screen{rows: rows, cols: cols}
	s.clear()
	return s
}

func (s *screen) clear() {
	s.g = make([][]rune, s.rows)
	for i := range s.g {
		s.g[i] = blank(s.cols)
	}
}

func blank(n int) []rune {
	l := make([]rune, n)
	for i := range l {
		l[i] = ' '
	}
	return l
}

func (s *screen) clamp() {
	s.r = max(0, min(s.r, s.rows-1))
	s.c = max(0, min(s.c, s.cols-1))
}

func (s *screen) lf() {
	if s.r == s.rows-1 {
		s.g = append(s.g[1:], blank(s.cols))
	} else {
		s.r++
	}
}

func (s *screen) write(b []byte) {
	b = append(s.pending, b...)
	s.pending = nil
	i := 0
	for i < len(b) {
		ch := b[i]
		switch {
		case ch == 0x1b:
			n, ok := s.esc(b[i:])
			if !ok {
				s.pending = append([]byte(nil), b[i:]...)
				return
			}
			i += n
			continue
		case ch == '\r':
			s.c = 0
		case ch == '\n':
			s.lf()
		case ch == '\b':
			if s.c > 0 {
				s.c--
			}
		case ch == '\t':
			s.c = min(s.cols-1, (s.c/8+1)*8)
		case ch < 0x20 || ch == 0x7f:
		default:
			r, n := utf8.DecodeRune(b[i:])
			if r == utf8.RuneError && n == 1 && !utf8.FullRune(b[i:]) {
				s.pending = append([]byte(nil), b[i:]...)
				return
			}
			if s.c >= s.cols {
				s.c = 0
				s.lf()
			}
			s.g[s.r][s.c] = r
			s.c++
			i += n
			continue
		}
		i++
	}
}

// esc handles one escape sequence; ok=false means it is incomplete.
func (s *screen) esc(b []byte) (int, bool) {
	if len(b) < 2 {
		return 0, false
	}
	switch b[1] {
	case '[':
		j := 2
		for j < len(b) && (b[j] < 0x40 || b[j] > 0x7e) {
			j++
		}
		if j >= len(b) {
			return 0, false
		}
		s.csi(string(b[2:j]), b[j])
		return j + 1, true
	case ']', 'P', '_', '^':
		for j := 2; j < len(b); j++ {
			if b[j] == 0x07 {
				return j + 1, true
			}
			if b[j] == 0x1b && j+1 < len(b) && b[j+1] == '\\' {
				return j + 2, true
			}
		}
		return 0, false
	case '(', ')', '*', '+':
		if len(b) < 3 {
			return 0, false
		}
		return 3, true
	case '7':
		s.sr, s.sc = s.r, s.c
	case '8':
		s.r, s.c = s.sr, s.sc
	case 'M':
		if s.r > 0 {
			s.r--
		}
	case 'c':
		s.clear()
		s.r, s.c = 0, 0
	}
	return 2, true
}

func (s *screen) csi(params string, final byte) {
	priv := strings.HasPrefix(params, "?") || strings.HasPrefix(params, ">") || strings.HasPrefix(params, "<") || strings.HasPrefix(params, "=")
	p := strings.TrimLeft(params, "?><=")
	var a []int
	for _, f := range strings.Split(p, ";") {
		n, _ := strconv.Atoi(f)
		a = append(a, n)
	}
	arg := func(i, def int) int {
		if i < len(a) && a[i] != 0 {
			return a[i]
		}
		return def
	}
	if priv {
		if final == 'h' || final == 'l' {
			if arg(0, 0) == 1049 || arg(0, 0) == 47 || arg(0, 0) == 1047 {
				s.clear()
			}
		}
		return
	}
	switch final {
	case 'H', 'f':
		s.r, s.c = arg(0, 1)-1, arg(1, 1)-1
	case 'A':
		s.r -= arg(0, 1)
	case 'B':
		s.r += arg(0, 1)
	case 'C':
		s.c += arg(0, 1)
	case 'D':
		s.c -= arg(0, 1)
	case 'E':
		s.r += arg(0, 1)
		s.c = 0
	case 'F':
		s.r -= arg(0, 1)
		s.c = 0
	case 'G', '`':
		s.c = arg(0, 1) - 1
	case 'd':
		s.r = arg(0, 1) - 1
	case 'J':
		switch arg(0, 0) {
		case 0:
			s.eraseLine(s.r, s.c, s.cols)
			for r := s.r + 1; r < s.rows; r++ {
				s.g[r] = blank(s.cols)
			}
		case 1:
			for r := 0; r < s.r; r++ {
				s.g[r] = blank(s.cols)
			}
			s.eraseLine(s.r, 0, s.c+1)
		default:
			s.clear()
		}
	case 'K':
		switch arg(0, 0) {
		case 0:
			s.eraseLine(s.r, s.c, s.cols)
		case 1:
			s.eraseLine(s.r, 0, s.c+1)
		default:
			s.eraseLine(s.r, 0, s.cols)
		}
	case 'X':
		s.eraseLine(s.r, s.c, s.c+arg(0, 1))
	case 'S':
		for range arg(0, 1) {
			s.g = append(s.g[1:], blank(s.cols))
		}
	case 'T':
		for range arg(0, 1) {
			s.g = append([][]rune{blank(s.cols)}, s.g[:s.rows-1]...)
		}
	}
	s.clamp()
}

func (s *screen) eraseLine(r, from, to int) {
	for c := max(0, from); c < min(to, s.cols); c++ {
		s.g[r][c] = ' '
	}
}

func (s *screen) text() string {
	var b strings.Builder
	for _, l := range s.g {
		b.WriteString(strings.TrimRight(string(l), " "))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

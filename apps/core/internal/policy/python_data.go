package policy

import "strings"

// This is deliberately NOT a Python safety analyser. Only the exact grammar
// below receives data-slot exemptions; every other program keeps full signals.
type pyToken struct {
	text string
	data bool
}

func pyTokens(src string) ([]pyToken, bool) {
	var ts []pyToken
	for i := 0; i < len(src); {
		c := src[i]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			i++
			continue
		}
		if c == '#' {
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote := string(c)
			if strings.HasPrefix(src[i:], strings.Repeat(quote, 3)) {
				quote = strings.Repeat(quote, 3)
			}
			i += len(quote)
			var b strings.Builder
			closed := false
			for i < len(src) {
				if strings.HasPrefix(src[i:], quote) {
					i += len(quote)
					closed = true
					break
				}
				if src[i] == '\\' {
					i++
					if i >= len(src) {
						return nil, false
					}
					switch src[i] {
					case 'n':
						b.WriteByte('\n')
					case 't':
						b.WriteByte('\t')
					case 'r':
						b.WriteByte('\r')
					case '\\', '\'', '"':
						b.WriteByte(src[i])
					default:
						return nil, false
					}
					i++
					continue
				}
				b.WriteByte(src[i])
				i++
			}
			if !closed {
				return nil, false
			}
			ts = append(ts, pyToken{b.String(), true})
			continue
		}
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' {
			j := i + 1
			for j < len(src) && (src[j] >= 'a' && src[j] <= 'z' || src[j] >= 'A' && src[j] <= 'Z' || src[j] == '_' || src[j] >= '0' && src[j] <= '9') {
				j++
			}
			ts = append(ts, pyToken{src[i:j], false})
			i = j
			continue
		}
		if strings.ContainsRune("().,:=;", rune(c)) {
			ts = append(ts, pyToken{string(c), false})
			i++
			continue
		}
		return nil, false
	}
	return ts, true
}
func staticPythonWrites(src string) ([]string, bool) {
	ts, ok := pyTokens(src)
	if !ok {
		return nil, false
	}
	i := 0
	consume := func(pattern string) bool {
		words := strings.Fields(pattern)
		if len(ts)-i < len(words) {
			return false
		}
		for j, s := range words {
			if ts[i+j].data || ts[i+j].text != s {
				return false
			}
		}
		i += len(words)
		return true
	}
	str := func() (string, bool) {
		if i >= len(ts) || !ts[i].data {
			return "", false
		}
		v := ts[i].text
		i++
		return v, true
	}
	if !consume("from pathlib import Path") {
		if !consume("import pathlib") {
			return nil, false
		}
	}
	if i < len(ts) && ts[i].text == ";" {
		i++
	}
	paths := []string{}
	vars := map[string]string{}
	rep := false
	for i < len(ts) {
		if consume(";") {
			continue
		}
		if consume("def rep ( p , old , new ) : p = Path ( p ) ; p . write_text ( p . read_text ( ) . replace ( old , new ) )") {
			rep = true
			continue
		}
		if rep && consume("rep (") {
			p, ok := str()
			if !ok || !consume(",") {
				return nil, false
			}
			if _, ok = str(); !ok || !consume(",") {
				return nil, false
			}
			if _, ok = str(); !ok || !consume(")") {
				return nil, false
			}
			paths = append(paths, p)
			continue
		}
		// Path('fixed').write_text('static payload')
		before := i
		if consume("Path (") || consume("pathlib . Path (") {
			p, ok := str()
			if !ok || !consume(") . write_text (") {
				return nil, false
			}
			if _, ok = str(); !ok || !consume(")") {
				return nil, false
			}
			paths = append(paths, p)
			continue
		}
		i = before
		if i >= len(ts) || ts[i].data {
			return nil, false
		}
		name := ts[i].text
		i++
		if consume("= Path (") || consume("= pathlib . Path (") {
			p, ok := str()
			if !ok || !consume(")") {
				return nil, false
			}
			vars[name] = p
			continue
		}
		p, exists := vars[name]
		if !exists || !consume(". write_text (") {
			return nil, false
		}
		if i < len(ts) && ts[i].data {
			_, _ = str()
		} else {
			if !consume(name + " . read_text ( ) . replace (") {
				return nil, false
			}
			if _, ok = str(); !ok || !consume(",") {
				return nil, false
			}
			if _, ok = str(); !ok || !consume(")") {
				return nil, false
			}
		}
		if !consume(")") {
			return nil, false
		}
		paths = append(paths, p)
	}
	if len(paths) == 0 {
		return nil, false
	}
	for _, p := range paths {
		if p == "" || strings.ContainsAny(p, "$`\x00\n") {
			return nil, false
		}
	}
	return paths, true
}

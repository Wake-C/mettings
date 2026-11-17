package meeting

import (
	"math"
	"regexp"
	"strings"
)

func Correct(text, rules string) string {
	for _, line := range strings.Split(rules, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		wrong, right := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if wrong == "" || right == "" {
			continue
		}
		ascii := true
		for _, r := range wrong {
			if r > 127 {
				ascii = false
				break
			}
		}
		if !ascii {
			text = strings.ReplaceAll(text, wrong, right)
			continue
		}
		var out strings.Builder
		offset := 0
		for {
			index := strings.Index(text[offset:], wrong)
			if index < 0 {
				out.WriteString(text[offset:])
				break
			}
			index += offset
			end := index + len(wrong)
			boundary := func(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' }
			out.WriteString(text[offset:index])
			if (index == 0 || !boundary(text[index-1])) && (end == len(text) || !boundary(text[end])) {
				out.WriteString(right)
			} else {
				out.WriteString(wrong)
			}
			offset = end
		}
		text = out.String()
	}
	return text
}

var speakerMarker = regexp.MustCompile(`(\*\*\[(?:\d{4}-\d{2}-\d{2} )?[0-9:]+\]\s*)([^*\n]+)(\*\*[:：])`)

func MapSpeakers(text string, names map[string]string) string {
	return speakerMarker.ReplaceAllStringFunc(text, func(marker string) string {
		m := speakerMarker.FindStringSubmatch(marker)
		if name := names[strings.TrimSpace(m[2])]; name != "" {
			return m[1] + name + m[3]
		}
		return marker
	})
}
func Similarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, aa, bb float64
	for i := range a {
		dot += a[i] * b[i]
		aa += a[i] * a[i]
		bb += b[i] * b[i]
	}
	if aa == 0 || bb == 0 {
		return 0
	}
	return dot / math.Sqrt(aa*bb)
}

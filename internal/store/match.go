package store

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Search matching for List.
//
// File names from macOS disks are often NFD (한 = ᄒ+ᅡ+ᆫ) while typed
// queries are NFC, and a query read while the Korean IME is still composing
// ends in a half-built syllable: typing 컴퓨터 passes through 컴ㅍ and 컴픁,
// where the ㅌ meant for 터 sits as the final consonant of 픁. Both sides are
// therefore NFC-normalised, lower-cased and spelled out as compatibility jamo
// with compound vowels and finals split into the keys that type them
// (ㅘ → ㅗㅏ, ㄺ → ㄹㄱ). A plain substring test on that spelling then
// matches every intermediate state: 컴픁 is ㅋㅓㅁㅍㅠㅌ, a prefix of 컴퓨터's
// ㅋㅓㅁㅍㅠㅌㅓ. Each syllable spells its initial consonant (ㅇ for a silent
// one), so a final can only stand where the next syllable's initial is in
// the text when it is the query's last letter; whole syllables never match
// across a syllable boundary.

const (
	hangulBase  = 0xAC00
	hangulLast  = 0xD7A3
	medialCount = 21
	finalCount  = 28
)

var (
	initials = []rune("ㄱㄲㄴㄷㄸㄹㅁㅂㅃㅅㅆㅇㅈㅉㅊㅋㅌㅍㅎ")
	medials  = []rune("ㅏㅐㅑㅒㅓㅔㅕㅖㅗㅘㅙㅚㅛㅜㅝㅞㅟㅠㅡㅢㅣ")
	// finals[0] is "no final".
	finals = []rune("\x00ㄱㄲㄳㄴㄵㄶㄷㄹㄺㄻㄼㄽㄾㄿㅀㅁㅂㅄㅅㅆㅇㅈㅊㅋㅌㅍㅎ")

	// splitJamo spells a compound vowel or final consonant as the keys
	// typed for it on a 2-set keyboard.
	splitJamo = map[rune]string{
		'ㅘ': "ㅗㅏ", 'ㅙ': "ㅗㅐ", 'ㅚ': "ㅗㅣ", 'ㅝ': "ㅜㅓ", 'ㅞ': "ㅜㅔ", 'ㅟ': "ㅜㅣ", 'ㅢ': "ㅡㅣ",
		'ㄳ': "ㄱㅅ", 'ㄵ': "ㄴㅈ", 'ㄶ': "ㄴㅎ", 'ㄺ': "ㄹㄱ", 'ㄻ': "ㄹㅁ", 'ㄼ': "ㄹㅂ",
		'ㄽ': "ㄹㅅ", 'ㄾ': "ㄹㅌ", 'ㄿ': "ㄹㅍ", 'ㅀ': "ㄹㅎ", 'ㅄ': "ㅂㅅ",
	}
)

// searchKey is the spelling the matcher compares: NFC, lower case, Hangul as
// split compatibility jamo, everything else unchanged.
func searchKey(s string) string {
	s = strings.ToLower(norm.NFC.String(s))
	var b strings.Builder
	b.Grow(len(s) * 2)
	put := func(j rune) {
		if parts, ok := splitJamo[j]; ok {
			b.WriteString(parts)
			return
		}
		b.WriteRune(j)
	}
	for _, r := range s {
		switch {
		case r >= hangulBase && r <= hangulLast:
			n := int(r - hangulBase)
			put(initials[n/(medialCount*finalCount)])
			put(medials[n%(medialCount*finalCount)/finalCount])
			if f := n % finalCount; f > 0 {
				put(finals[f])
			}
		case r >= 0x1100 && r <= 0x1112: // conjoining initial left alone by NFC
			put(initials[r-0x1100])
		case r >= 0x1161 && r <= 0x1175: // conjoining vowel
			put(medials[r-0x1161])
		case r >= 0x11A8 && r <= 0x11C2: // conjoining final
			put(finals[r-0x11A8+1])
		default:
			put(r)
		}
	}
	return b.String()
}

// matcher tests texts against one query.
type matcher struct{ key string }

func newMatcher(query string) matcher {
	return matcher{key: searchKey(strings.TrimSpace(query))}
}

func (m matcher) empty() bool { return m.key == "" }

func (m matcher) match(text string) bool {
	return strings.Contains(searchKey(text), m.key)
}

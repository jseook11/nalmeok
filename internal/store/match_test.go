package store

import (
	"path/filepath"
	"testing"

	"golang.org/x/text/unicode/norm"
)

func TestSearchKeySpellsJamo(t *testing.T) {
	cases := map[string]string{
		"컴퓨터":   "ㅋㅓㅁㅍㅠㅌㅓ",
		"컴픁":    "ㅋㅓㅁㅍㅠㅌ",
		"와":     "ㅇㅗㅏ",
		"닭":     "ㄷㅏㄹㄱ",
		"없":     "ㅇㅓㅂㅅ",
		"ㅘ":     "ㅗㅏ",
		"Ch07강": "ch07ㄱㅏㅇ",
		"했":     "ㅎㅐㅆ",
		"힣":     "ㅎㅣㅎ",
		"가":     "ㄱㅏ",
	}
	for in, want := range cases {
		if got := searchKey(in); got != want {
			t.Errorf("searchKey(%q) = %q, want %q", in, got, want)
		}
	}
	// NFD (as macOS file names) spells the same as NFC.
	for _, s := range []string{"컴퓨터 구조", "선형대수 2주차.m4a", "Café 강의"} {
		if searchKey(norm.NFD.String(s)) != searchKey(s) {
			t.Errorf("NFD %q differs from NFC", s)
		}
	}
}

func TestMatchWhileComposing(t *testing.T) {
	name := "컴퓨터 구조 7강"
	// Every state the 2-set IME shows while typing 컴퓨터.
	for _, q := range []string{"ㅋ", "커", "컴", "컴ㅍ", "컴퓨", "컴픁", "컴퓨터", "퓨터", "구조", "ㄱ", "7강", "7가", "7ㄱ"} {
		if !newMatcher(q).match(name) {
			t.Errorf("%q should match %q", q, name)
		}
	}
	// And against the NFD spelling a macOS disk gives.
	for _, q := range []string{"컴퓨", "컴픁", "구조"} {
		if !newMatcher(q).match(norm.NFD.String(name)) {
			t.Errorf("%q should match NFD %q", q, name)
		}
	}
	// Compound vowels and finals mid-composition: 와 passes through 오,
	// 닭 through 달.
	if !newMatcher("오").match("과제 와플") || !newMatcher("달").match("닭갈비") {
		t.Error("partial compound should match")
	}
	for _, q := range []string{"컴퓨타", "쿰", "터컴", "알고"} {
		if newMatcher(q).match(name) {
			t.Errorf("%q should not match %q", q, name)
		}
	}
	// Whole syllables do not match across a syllable boundary: 나 is not in
	// 안아 even though ㄴ and ㅏ are neighbours in the spelling of 안.
	if newMatcher("나").match("안아") {
		t.Error("나 matched 안아")
	}
	if !newMatcher("ALGO").match("algorithm 07") {
		t.Error("case-insensitive Latin")
	}
}

func TestListSearchesNFDNamesAndSources(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "items.json"))
	if err != nil {
		t.Fatal(err)
	}
	nfd := norm.NFD.String("컴퓨터구조")
	s.Add(Item{ID: "a", Name: nfd + ".m4a", Source: "/x/" + nfd + ".m4a"})
	s.Add(Item{ID: "b", Name: "선형대수", Source: "/x/" + norm.NFD.String("강의") + "/선형대수"})
	s.Add(Item{ID: "c", Name: "algorithm"})

	ids := func(q string) []string {
		var out []string
		for _, it := range s.List(q) {
			out = append(out, it.ID)
		}
		return out
	}
	for q, want := range map[string]string{"컴퓨": "a", "컴픁": "a", "강의": "b", "선형": "b", "ALG": "c"} {
		got := ids(q)
		if len(got) != 1 || got[0] != want {
			t.Errorf("List(%q) = %v, want [%s]", q, got, want)
		}
	}
	if got := ids("  "); len(got) != 3 {
		t.Errorf("blank query = %v", got)
	}
}

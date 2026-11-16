package diff

import (
	"strings"
	"unicode"
)

// NormalizeText collapses every run of whitespace to one space and trims the
// ends. Extraction differs in line breaks and padding between tools and
// versions without the text changing, and CER must not count that.
func NormalizeText(s string) string {
	return strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
}

// CER is the character error rate of candidate against reference: the rune
// Levenshtein distance divided by the reference length. 0 means identical; a
// value above 1 is possible when the candidate is much longer. An empty
// reference gives 0 for an empty candidate and 1 otherwise. Both inputs are
// normalised first. Call it per page: the cost is O(len(ref) × len(cand)) time
// with two rows of memory, fine for a page, not for a whole book.
func CER(reference, candidate string) float64 {
	r := []rune(NormalizeText(reference))
	c := []rune(NormalizeText(candidate))
	if len(r) == 0 {
		if len(c) == 0 {
			return 0
		}
		return 1
	}
	prev := make([]int, len(c)+1)
	cur := make([]int, len(c)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(r); i++ {
		cur[0] = i
		for j := 1; j <= len(c); j++ {
			cost := 1
			if r[i-1] == c[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return float64(prev[len(c)]) / float64(len(r))
}

// WordRecall is the share of the reference's words (as a multiset) that also
// appear in the candidate, regardless of order: 1.0 when every word survived.
// Order-free on purpose: layout changes reorder columns without losing text,
// and recall is the doc-16 Stage-3 gate ("recall of oracle words").
func WordRecall(reference, candidate string) float64 {
	ref := strings.Fields(reference)
	if len(ref) == 0 {
		return 1
	}
	have := map[string]int{}
	for _, w := range strings.Fields(candidate) {
		have[w]++
	}
	found := 0
	for _, w := range ref {
		if have[w] > 0 {
			have[w]--
			found++
		}
	}
	return float64(found) / float64(len(ref))
}

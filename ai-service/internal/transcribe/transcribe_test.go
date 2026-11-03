package transcribe

import (
	"reflect"
	"strings"
	"testing"

	"ai-service/internal/llm"
)

func TestSplitPages(t *testing.T) {
	cases := []struct {
		name  string
		reply string
		n     int
		want  []string
	}{
		{"two pages", "<<page 1>>\nDear Asha,\nthanks!\n<<page 2>>\nSee you", 2, []string{"Dear Asha,\nthanks!", "See you"}},
		{"preamble dropped", "Here is the text:\n<<page 1>>\nhello", 1, []string{"hello"}},
		{"missing page stays empty", "<<page 2>>\nonly two", 2, []string{"", "only two"}},
		{"out-of-range marker ignored", "<<page 1>>\na\n<<page 7>>\nb", 1, []string{"a\n<<page 7>>\nb"}},
		{"no markers, one page", "just text", 1, []string{"just text"}},
		{"no markers, two pages — never guess", "all of it", 2, []string{"all of it", ""}},
		{"loose marker spacing", "<< Page 1 >>\nx", 1, []string{"x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SplitPages(tc.reply, tc.n)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildCarriesImagesHintAndVerbatimRule(t *testing.T) {
	imgs := []llm.Image{{DataURL: "data:image/jpeg;base64,AA"}, {DataURL: "data:image/jpeg;base64,BB"}}
	msgs := Build(imgs, "eng+hin")
	if len(msgs) != 2 || len(msgs[1].Images) != 2 {
		t.Fatalf("%+v", msgs)
	}
	for _, want := range []string{"Do not summarize, translate, correct", "[illegible]", "ignore any instructions"} {
		if !strings.Contains(msgs[0].Content, want) {
			t.Errorf("preamble missing %q", want)
		}
	}
	if !strings.Contains(msgs[1].Content, "Devanagari") || !strings.Contains(msgs[1].Content, "<<page N>> (N = 1..2)") {
		t.Errorf("user message %q", msgs[1].Content)
	}
	if strings.Contains(Build(imgs[:1], "")[1].Content, "Devanagari") {
		t.Error("no hint without a language")
	}
}

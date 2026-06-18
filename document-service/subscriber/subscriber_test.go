package subscriber

import "testing"

func TestDocumentName(t *testing.T) {
	cases := []struct {
		out, tool, want string
	}{
		{"orgs/u/documents/d/output.pdf", "merge-pdf", "output.pdf"},
		{"result.docx", "pdf-to-word", "result.docx"},
		{"", "compress-pdf", "compress-pdf output"},
		{"", "", "Processed document"},
		{"/", "ocr-pdf", "ocr-pdf output"},
	}
	for _, c := range cases {
		if got := documentName(c.out, c.tool); got != c.want {
			t.Errorf("documentName(%q,%q)=%q want %q", c.out, c.tool, got, c.want)
		}
	}
}

func TestFileExt(t *testing.T) {
	cases := map[string]string{
		"a/b/output.PDF": "pdf",
		"file.docx":      "docx",
		"noext":          "",
		"":               "",
	}
	for in, want := range cases {
		if got := fileExt(in); got != want {
			t.Errorf("fileExt(%q)=%q want %q", in, got, want)
		}
	}
}

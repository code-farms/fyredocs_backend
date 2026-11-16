package corpus

import (
	"bytes"
	"context"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
)

// Generator produces the file for one item inside g.OutDir and returns its path
// relative to g.OutDir. It must be deterministic in content for a given item
// (seeded from the item ID) so regenerating gives an equivalent corpus.
type Generator func(ctx context.Context, g *Gen, it Item) (string, error)

// Gen carries what generators share: where to write, where the repo is (for
// scripts/k6/fixtures), and a scratch directory for intermediate files.
type Gen struct {
	OutDir  string
	RepoDir string
	Scratch string
}

// Generators is the registry the manifest's "generator" names resolve against.
func Generators() map[string]Generator {
	return map[string]Generator{
		"textpdf":   genTextPDF,
		"image":     genImage,
		"imagepdf":  genImagePDF,
		"encrypted": genEncrypted,
		"malformed": genMalformed,
		"html":      genHTML,
		"scan":      genScan,
		"lo":        genLibreOffice,
		"k6fixture": genK6Fixture,
	}
}

// rng returns a generator-local random source seeded from the item ID, so a
// file's content depends on its identity and nothing else.
func rng(it Item) *rand.Rand {
	h := fnv.New64a()
	h.Write([]byte(it.ID))
	s := h.Sum64()
	return rand.New(rand.NewPCG(s, s^0x9e3779b97f4a7c15))
}

// param returns Params[key] or def.
func param(it Item, key, def string) string {
	if v, ok := it.Series.Params[key]; ok && v != "" {
		return v
	}
	return def
}

// intParam returns Params[key] as an int, or def when missing or invalid.
func intParam(it Item, key string, def int) int {
	if n, err := strconv.Atoi(param(it, key, "")); err == nil {
		return n
	}
	return def
}

// outPath returns (absolute path, relative path) for an item's file with ext,
// grouped by category, creating the directory.
func (g *Gen) outPath(it Item, ext string) (string, string, error) {
	rel := filepath.Join(it.Series.Category, it.ID+"."+ext)
	abs := filepath.Join(g.OutDir, rel)
	return abs, rel, os.MkdirAll(filepath.Dir(abs), 0o755)
}

// words is the vocabulary for generated text: plain English plus a few
// Devanagari words so Latin-only fonts show up as missing glyphs.
var words = []string{
	"invoice", "quarterly", "report", "payment", "customer", "delivery", "schedule",
	"summary", "analysis", "contract", "section", "table", "figure", "total", "amount",
	"document", "review", "approval", "revision", "appendix", "budget", "forecast",
	"meeting", "minutes", "action", "owner", "status", "pending", "complete", "risk",
}

// sentence builds a deterministic sentence of n words.
func sentence(r *rand.Rand, n int) string {
	var b bytes.Buffer
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(words[r.IntN(len(words))])
	}
	return b.String()
}

// errUnsupported wraps a parameter error with the item for a clear gen report.
func errUnsupported(it Item, what string) error {
	return fmt.Errorf("%s: unsupported %s", it.ID, what)
}

package display

import (
	"os"
	"path/filepath"
	"testing"
)

// Glyph mode is opt-in, so a missing table (the default install) and a blank
// path (glyphsPath() with no home dir) both have to be the normal case, not
// an error — mirrors report.Load's contract for the same reason.
func TestGlyphsFromFileMissingOrBlankIsEmptyNotError(t *testing.T) {
	g, err := GlyphsFromFile(filepath.Join(t.TempDir(), "glyphs.txt"))
	if g != nil || err != nil {
		t.Errorf("missing file: got (%v, %v), want (nil, nil)", g, err)
	}
	if g, err := GlyphsFromFile(""); g != nil || err != nil {
		t.Errorf("blank path: got (%v, %v), want (nil, nil)", g, err)
	}
}

func TestGlyphsFromFileParsesCommentsAndBlankLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "glyphs.txt")
	body := "# a comment\n\nload-bearing:†\n  substrate : ‡  \n\nmalformed\n:x\ny:\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := GlyphsFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if g["load-bearing"] != "†" {
		t.Errorf("basic pair not parsed: %v", g)
	}
	if g["substrate"] != "‡" {
		t.Errorf("surrounding space should be trimmed: %v", g)
	}
	if len(g) != 2 {
		t.Errorf("malformed/empty-side lines must be dropped, got %v", g)
	}
}

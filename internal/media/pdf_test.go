package media

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// plainPDF writes a minimal uncompressed PDF with n pages.
func plainPDF(n int) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	kids := ""
	for i := 0; i < n; i++ {
		kids += fmt.Sprintf("%d 0 R ", 3+i)
	}
	fmt.Fprintf(&b, "2 0 obj\n<< /Type /Pages /Kids [%s] /Count %d >>\nendobj\n", kids, n)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "%d 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>\nendobj\n", 3+i)
	}
	b.WriteString("trailer\n<< /Root 1 0 R >>\n%%EOF\n")
	return b.Bytes()
}

// objStmPDF writes a PDF 1.5 file whose page tree lives in a compressed
// object stream, as Word, Keynote and pdfTeX produce.
func objStmPDF(n int) []byte {
	objs := []string{fmt.Sprintf("<< /Count %d /Kids [] /Type /Pages >>", n)}
	for i := 0; i < n; i++ {
		objs = append(objs, "<</Type/Page/Parent 2 0 R>>")
	}
	var header, body bytes.Buffer
	for i, o := range objs {
		fmt.Fprintf(&header, "%d %d ", 2+i, body.Len())
		body.WriteString(o + " ")
	}
	raw := append(header.Bytes(), body.Bytes()...)
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	zw.Write(raw)
	zw.Close()

	var b bytes.Buffer
	b.WriteString("%PDF-1.5\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	fmt.Fprintf(&b, "9 0 obj\n<< /Type /ObjStm /N %d /First %d /Filter /FlateDecode /Length %d >>\nstream\n", len(objs), header.Len(), z.Len())
	b.Write(z.Bytes())
	b.WriteString("\nendstream\nendobj\n%%EOF\n")
	return b.Bytes()
}

func TestPDFPages(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		name string
		data []byte
		want int
	}{
		{"plain.pdf", plainPDF(3), 3},
		{"one.pdf", plainPDF(1), 1},
		{"objstm.pdf", objStmPDF(12), 12},
		{"leaves.pdf", []byte("%PDF-1.4\n3 0 obj\n<</Type/Page>>\nendobj\n4 0 obj\n<</Type /Page>>\nendobj\n"), 2},
		{"notpdf.pdf", []byte("hello /Type /Pages /Count 9"), 0},
		{"empty.pdf", nil, 0},
	}
	for _, c := range cases {
		if got := PDFPages(write(c.name, c.data)); got != c.want {
			t.Errorf("%s: PDFPages = %d, want %d", c.name, got, c.want)
		}
	}
	if got := PDFPages(filepath.Join(dir, "missing.pdf")); got != 0 {
		t.Errorf("missing: %d", got)
	}
}

func TestFolderPDFs(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "10장.pdf", "2장.PDF", "._2장.pdf", ".hidden.pdf", "a.m4a", "note.txt")
	os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	touch(t, filepath.Join(dir, "sub"), "deep.pdf")
	os.Mkdir(filepath.Join(dir, "folder.pdf"), 0o755)

	got := FolderPDFs(dir)
	want := []string{filepath.Join(dir, "2장.PDF"), filepath.Join(dir, "10장.pdf")}
	if !slices.Equal(got, want) {
		t.Errorf("FolderPDFs = %q, want %q", got, want)
	}
	if got := FolderPDFs(filepath.Join(dir, "a.m4a")); got != nil {
		t.Errorf("file: %q", got)
	}
}

// A folder with several PDFs scans fine and lists them all; choosing among
// them is the window's job.
func TestScanFolderManyPDFs(t *testing.T) {
	fx := fixtures(t)
	dir := t.TempDir()
	link(t, filepath.Join(fx, "x.wav"), filepath.Join(dir, "1강.wav"))
	touch(t, dir, "b.pdf", "a.PDF", "c.pdf")
	in, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(dir, "a.PDF"), filepath.Join(dir, "b.pdf"), filepath.Join(dir, "c.pdf")}
	if !slices.Equal(in.PDFs, want) {
		t.Errorf("PDFs = %q, want %q", in.PDFs, want)
	}
}

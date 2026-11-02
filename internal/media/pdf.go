package media

import (
	"bytes"
	"compress/zlib"
	"io"
	"os"
	"regexp"
	"strconv"
)

// pdfPagesMaxBytes bounds how much of a PDF PDFPages reads. A larger file
// has an unknown page count.
const pdfPagesMaxBytes = 128 << 20

// pdfObjStmMaxBytes bounds one decompressed object stream.
const pdfObjStmMaxBytes = 32 << 20

var (
	pdfObjRe     = regexp.MustCompile(`\d+\s+\d+\s+obj\b`)
	pdfPagesRe   = regexp.MustCompile(`/Type\s*/Pages\b`)
	pdfPageRe    = regexp.MustCompile(`/Type\s*/Page\b`)
	pdfCountRe   = regexp.MustCompile(`/Count\s+(\d+)`)
	pdfObjStmRe  = regexp.MustCompile(`/Type\s*/ObjStm\b`)
	pdfFlateRe   = regexp.MustCompile(`/Filter\s*/FlateDecode\b`)
	pdfFirstRe   = regexp.MustCompile(`/First\s+(\d+)`)
	pdfStreamRe  = regexp.MustCompile(`stream\r?\n`)
	pdfEndStream = []byte("endstream")
	pdfEndObj    = []byte("endobj")
)

// PDFPages returns the page count of a PDF, or 0 when it cannot tell. It is
// a cheap look at the page tree, not a PDF parser: the largest /Count of a
// /Type /Pages dictionary wins (the root holds the total), falling back to
// the number of /Type /Page leaves. Objects packed into Flate-compressed
// object streams (PDF 1.5+) are unpacked for the same look. Encrypted or
// unusual files give 0, and the window then shows no count.
func PDFPages(path string) int {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > pdfPagesMaxBytes {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.HasPrefix(bytes.TrimLeft(data[:min(len(data), 1024)], "\x00\r\n\t "), []byte("%PDF")) {
		return 0
	}
	count, leaves := 0, 0
	look := func(obj []byte) {
		switch {
		case pdfPagesRe.Match(obj):
			if m := pdfCountRe.FindSubmatch(obj); m != nil {
				if n, err := strconv.Atoi(string(m[1])); err == nil && n > count {
					count = n
				}
			}
		case pdfPageRe.Match(obj):
			leaves++
		}
	}
	for _, obj := range pdfObjects(data) {
		head, body, isStream := splitStream(obj)
		if isStream && pdfObjStmRe.Match(head) && pdfFlateRe.Match(head) {
			for _, inner := range objStmObjects(head, body) {
				look(inner)
			}
			continue
		}
		look(head)
	}
	if count > 0 {
		return count
	}
	return leaves
}

// pdfObjects cuts the file into "N G obj ... endobj" pieces.
func pdfObjects(data []byte) [][]byte {
	starts := pdfObjRe.FindAllIndex(data, -1)
	objs := make([][]byte, 0, len(starts))
	for i, s := range starts {
		end := len(data)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		obj := data[s[1]:end]
		if j := bytes.Index(obj, pdfEndObj); j >= 0 {
			obj = obj[:j]
		}
		objs = append(objs, obj)
	}
	return objs
}

// splitStream separates an object's dictionary from its stream data.
func splitStream(obj []byte) (head, body []byte, isStream bool) {
	loc := pdfStreamRe.FindIndex(obj)
	if loc == nil {
		return obj, nil, false
	}
	body = obj[loc[1]:]
	if j := bytes.LastIndex(body, pdfEndStream); j >= 0 {
		body = body[:j]
	}
	return obj[:loc[0]], body, true
}

// objStmObjects inflates an object stream and returns its objects. The
// stream starts with N pairs "number offset"; offsets count from /First.
func objStmObjects(head, body []byte) [][]byte {
	m := pdfFirstRe.FindSubmatch(head)
	if m == nil {
		return nil
	}
	first, err := strconv.Atoi(string(m[1]))
	if err != nil {
		return nil
	}
	zr, err := zlib.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil
	}
	defer zr.Close()
	data, _ := io.ReadAll(io.LimitReader(zr, pdfObjStmMaxBytes))
	if first <= 0 || first > len(data) {
		return nil
	}
	fields := bytes.Fields(data[:first])
	var offsets []int
	for i := 1; i < len(fields); i += 2 {
		off, err := strconv.Atoi(string(fields[i]))
		if err != nil || off < 0 || off > len(data)-first {
			return nil
		}
		offsets = append(offsets, first+off)
	}
	objs := make([][]byte, 0, len(offsets))
	for i, off := range offsets {
		end := len(data)
		if i+1 < len(offsets) && offsets[i+1] >= off {
			end = offsets[i+1]
		}
		objs = append(objs, data[off:end])
	}
	return objs
}

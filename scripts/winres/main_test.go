package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"os"
	"strings"
	"testing"
)

// rsrcEntry is one leaf of a parsed resource tree.
type rsrcEntry struct {
	typ, id, lang uint32
	data          []byte
}

// parseRsrc walks a .rsrc section as Windows would, after the linker has
// placed it at rva.
func parseRsrc(t *testing.T, sect []byte, rva uint32) []rsrcEntry {
	t.Helper()
	le := binary.LittleEndian
	var out []rsrcEntry
	var walk func(at uint32, depth int, path []uint32)
	walk = func(at uint32, depth int, path []uint32) {
		n := int(le.Uint16(sect[at+12:])) + int(le.Uint16(sect[at+14:]))
		prev := int64(-1)
		for i := 0; i < n; i++ {
			e := at + 16 + uint32(8*i)
			name, off := le.Uint32(sect[e:]), le.Uint32(sect[e+4:])
			if name&0x80000000 != 0 {
				t.Fatalf("unexpected named entry")
			}
			if int64(name) <= prev {
				t.Fatalf("entries not sorted at depth %d", depth)
			}
			prev = int64(name)
			p := append(append([]uint32{}, path...), name)
			if off&0x80000000 != 0 {
				if depth == 2 {
					t.Fatalf("subdirectory below language level")
				}
				walk(off&^0x80000000, depth+1, p)
				continue
			}
			if depth != 2 {
				t.Fatalf("data entry at depth %d", depth)
			}
			dataRVA, size := le.Uint32(sect[off:]), le.Uint32(sect[off+4:])
			start := dataRVA - rva
			if start%8 != 0 {
				t.Errorf("data at %#x not 8-byte aligned", start)
			}
			out = append(out, rsrcEntry{p[0], p[1], p[2], sect[start : start+size]})
		}
	}
	walk(0, 0, nil)
	return out
}

func TestObjectRoundTrip(t *testing.T) {
	ico, err := os.ReadFile("../../assets/nalmeok.ico")
	if err != nil {
		t.Fatal(err)
	}
	imgs, err := parseICO(ico)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range arches {
		obj, err := buildObject(ico, manifestXML, a)
		if err != nil {
			t.Fatal(err)
		}
		f, err := pe.NewFile(bytes.NewReader(obj))
		if err != nil {
			t.Fatalf("%s: debug/pe: %v", a.goarch, err)
		}
		if f.Machine != a.machine || len(f.Sections) != 1 || f.Sections[0].Name != ".rsrc" {
			t.Fatalf("%s: bad header %+v", a.goarch, f.FileHeader)
		}
		s := f.Sections[0]
		sect, err := s.Data()
		if err != nil {
			t.Fatal(err)
		}
		// Apply relocations the way the Go linker does: field += section RVA.
		const rva = 0x5000
		sect = append([]byte{}, sect...)
		for _, r := range s.Relocs {
			if r.Type != a.relType || r.SymbolTableIndex != 0 {
				t.Fatalf("%s: bad relocation %+v", a.goarch, r)
			}
			v := binary.LittleEndian.Uint32(sect[r.VirtualAddress:])
			binary.LittleEndian.PutUint32(sect[r.VirtualAddress:], v+rva)
		}
		entries := parseRsrc(t, sect, rva)
		if len(s.Relocs) != len(entries) {
			t.Errorf("%s: %d relocs for %d entries", a.goarch, len(s.Relocs), len(entries))
		}

		icons := map[uint32][]byte{}
		var group, manifest []byte
		for _, e := range entries {
			if e.lang != langEnUS {
				t.Errorf("lang %#x", e.lang)
			}
			switch {
			case e.typ == rtIcon:
				icons[e.id] = e.data
			case e.typ == rtGroupIcon && e.id == groupIconID:
				group = e.data
			case e.typ == rtManifest && e.id == manifestID:
				manifest = e.data
			default:
				t.Errorf("unexpected resource %d/%d", e.typ, e.id)
			}
		}
		if !strings.Contains(string(manifest), `level="asInvoker"`) {
			t.Errorf("%s: manifest missing", a.goarch)
		}
		if len(group) < 6 || int(binary.LittleEndian.Uint16(group[4:])) != len(imgs) {
			t.Fatalf("%s: group icon has wrong count", a.goarch)
		}
		for i, img := range imgs {
			g := group[6+14*i:]
			id := uint32(binary.LittleEndian.Uint16(g[12:]))
			if g[0] != img.width || binary.LittleEndian.Uint32(g[8:]) != uint32(len(img.data)) {
				t.Errorf("%s: group entry %d mismatch", a.goarch, i)
			}
			if !bytes.Equal(icons[id], img.data) {
				t.Errorf("%s: RT_ICON %d differs from ico image %d", a.goarch, id, i)
			}
		}
	}
}

// The committed .syso files must match what the generator makes now, so a
// changed icon or manifest is not forgotten.
func TestCommittedSysoUpToDate(t *testing.T) {
	ico, err := os.ReadFile("../../assets/nalmeok.ico")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range arches {
		want, err := buildObject(ico, manifestXML, a)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile("../../cmd/nalmeok/rsrc_windows_" + a.goarch + ".syso")
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("cmd/nalmeok/rsrc_windows_%s.syso is stale; run go run ./scripts/winres", a.goarch)
		}
	}
}

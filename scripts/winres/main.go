// Command winres writes the Windows resource objects (.syso) that give
// Nalmeok.exe its icon and application manifest. It uses only the standard
// library so the build needs no extra tool.
//
// Run from the repository root whenever assets/nalmeok.ico or the manifest
// changes, and commit the results:
//
//	go run ./scripts/winres
//
// The Go linker picks up cmd/nalmeok/rsrc_windows_<arch>.syso automatically
// when GOOS=windows; other targets ignore it because of the file name suffix.
package main

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
)

//go:embed nalmeok.exe.manifest
var manifestXML []byte

// Resource types and IDs.
const (
	rtIcon      = 3
	rtGroupIcon = 14
	rtManifest  = 24

	// Wails loads the window icon from icon group 3 (winc.AppIconID). It is
	// the only icon group, so Explorer and the taskbar use it too.
	groupIconID = 3
	manifestID  = 1 // CREATEPROCESS_MANIFEST_RESOURCE_ID
	langEnUS    = 0x0409
)

// COFF constants.
const (
	machineAMD64 = 0x8664
	machineARM64 = 0xAA64

	relAMD64Addr32NB = 0x0003 // IMAGE_REL_AMD64_ADDR32NB
	relARM64Addr32NB = 0x0002 // IMAGE_REL_ARM64_ADDR32NB

	scnInitData = 0x00000040 // IMAGE_SCN_CNT_INITIALIZED_DATA
	scnMemRead  = 0x40000000 // IMAGE_SCN_MEM_READ
	symStatic   = 3          // IMAGE_SYM_CLASS_STATIC
)

type arch struct {
	goarch  string
	machine uint16
	relType uint16
}

var arches = []arch{
	{"amd64", machineAMD64, relAMD64Addr32NB},
	{"arm64", machineARM64, relARM64Addr32NB},
}

func main() {
	icoPath := flag.String("ico", "assets/nalmeok.ico", "multi-size .ico file")
	outDir := flag.String("out", "cmd/nalmeok", "directory for rsrc_windows_<arch>.syso")
	flag.Parse()

	ico, err := os.ReadFile(*icoPath)
	if err != nil {
		log.Fatal(err)
	}
	for _, a := range arches {
		obj, err := buildObject(ico, manifestXML, a)
		if err != nil {
			log.Fatal(err)
		}
		out := filepath.Join(*outDir, "rsrc_windows_"+a.goarch+".syso")
		if err := os.WriteFile(out, obj, 0o644); err != nil {
			log.Fatal(err)
		}
		fmt.Println("wrote", out, len(obj), "bytes")
	}
}

// iconImage is one image inside an .ico file.
type iconImage struct {
	width, height, colors uint8
	planes, bitCount      uint16
	data                  []byte
}

func parseICO(b []byte) ([]iconImage, error) {
	if len(b) < 6 || binary.LittleEndian.Uint16(b[0:]) != 0 || binary.LittleEndian.Uint16(b[2:]) != 1 {
		return nil, errors.New("not an .ico file")
	}
	n := int(binary.LittleEndian.Uint16(b[4:]))
	if n == 0 || len(b) < 6+16*n {
		return nil, errors.New("ico: bad image count")
	}
	imgs := make([]iconImage, n)
	for i := range imgs {
		e := b[6+16*i:]
		size := binary.LittleEndian.Uint32(e[8:])
		off := binary.LittleEndian.Uint32(e[12:])
		if uint64(off)+uint64(size) > uint64(len(b)) {
			return nil, fmt.Errorf("ico: image %d out of range", i)
		}
		img := iconImage{
			width: e[0], height: e[1], colors: e[2],
			planes:   binary.LittleEndian.Uint16(e[4:]),
			bitCount: binary.LittleEndian.Uint16(e[6:]),
			data:     b[off : off+size],
		}
		if img.planes == 0 {
			img.planes = 1
		}
		imgs[i] = img
	}
	return imgs, nil
}

// groupIconDir builds a GRPICONDIR whose entries point at RT_ICON IDs 1..n.
func groupIconDir(imgs []iconImage) []byte {
	var b bytes.Buffer
	w := func(v any) { binary.Write(&b, binary.LittleEndian, v) }
	w(uint16(0))
	w(uint16(1))
	w(uint16(len(imgs)))
	for i, img := range imgs {
		w([4]uint8{img.width, img.height, img.colors, 0})
		w(img.planes)
		w(img.bitCount)
		w(uint32(len(img.data)))
		w(uint16(i + 1))
	}
	return b.Bytes()
}

type resource struct {
	typ, id uint32
	data    []byte
}

func align(n, a int) int { return (n + a - 1) &^ (a - 1) }

// buildRsrc lays out a .rsrc section: directory tables (type → ID →
// language), then data entries, then the data itself. It returns the section
// bytes and the offsets of the data-entry RVA fields, which need relocations.
// Those fields hold the data's offset within the section; the linker adds the
// section's RVA.
func buildRsrc(res []resource) (sect []byte, relocs []uint32) {
	sort.Slice(res, func(i, j int) bool {
		if res[i].typ != res[j].typ {
			return res[i].typ < res[j].typ
		}
		return res[i].id < res[j].id
	})
	var types []uint32
	byType := map[uint32][]resource{}
	for _, r := range res {
		if len(byType[r.typ]) == 0 {
			types = append(types, r.typ)
		}
		byType[r.typ] = append(byType[r.typ], r)
	}

	dirSize := func(n int) int { return 16 + 8*n }
	// Offsets of every table, computed before writing.
	off := dirSize(len(types))
	typeDir := map[uint32]int{}
	for _, t := range types {
		typeDir[t] = off
		off += dirSize(len(byType[t]))
	}
	langDir := make([]int, len(res))
	for i := range res {
		langDir[i] = off
		off += dirSize(1)
	}
	dataEntry := make([]int, len(res))
	for i := range res {
		dataEntry[i] = off
		off += 16
	}
	dataOff := make([]int, len(res))
	for i, r := range res {
		off = align(off, 8)
		dataOff[i] = off
		off += len(r.data)
	}
	sect = make([]byte, align(off, 8))

	le := binary.LittleEndian
	writeDir := func(at, n int) int {
		le.PutUint16(sect[at+14:], uint16(n)) // NumberOfIdEntries
		return at + 16
	}
	const subdir = 0x80000000

	e := writeDir(0, len(types))
	idx := 0
	for _, t := range types {
		le.PutUint32(sect[e:], t)
		le.PutUint32(sect[e+4:], uint32(typeDir[t])|subdir)
		e += 8
		te := writeDir(typeDir[t], len(byType[t]))
		for _, r := range byType[t] {
			le.PutUint32(sect[te:], r.id)
			le.PutUint32(sect[te+4:], uint32(langDir[idx])|subdir)
			te += 8
			le2 := writeDir(langDir[idx], 1)
			le.PutUint32(sect[le2:], langEnUS)
			le.PutUint32(sect[le2+4:], uint32(dataEntry[idx]))
			de := dataEntry[idx]
			le.PutUint32(sect[de:], uint32(dataOff[idx])) // OffsetToData (RVA)
			le.PutUint32(sect[de+4:], uint32(len(r.data)))
			relocs = append(relocs, uint32(de))
			copy(sect[dataOff[idx]:], r.data)
			idx++
		}
	}
	return sect, relocs
}

// buildObject returns a COFF object with a single .rsrc section.
func buildObject(ico, manifest []byte, a arch) ([]byte, error) {
	imgs, err := parseICO(ico)
	if err != nil {
		return nil, err
	}
	var res []resource
	for i, img := range imgs {
		res = append(res, resource{rtIcon, uint32(i + 1), img.data})
	}
	res = append(res,
		resource{rtGroupIcon, groupIconID, groupIconDir(imgs)},
		resource{rtManifest, manifestID, manifest},
	)
	sect, relocs := buildRsrc(res)

	const fileHdr, sectHdr = 20, 40
	rawPtr := fileHdr + sectHdr
	relPtr := rawPtr + len(sect)
	symPtr := relPtr + 10*len(relocs)

	var b bytes.Buffer
	w := func(v any) { binary.Write(&b, binary.LittleEndian, v) }
	// IMAGE_FILE_HEADER
	w(a.machine)
	w(uint16(1)) // NumberOfSections
	w(uint32(0)) // TimeDateStamp (fixed for reproducible output)
	w(uint32(symPtr))
	w(uint32(1)) // NumberOfSymbols
	w(uint16(0)) // SizeOfOptionalHeader
	w(uint16(0)) // Characteristics
	// IMAGE_SECTION_HEADER
	w([8]byte{'.', 'r', 's', 'r', 'c'})
	w(uint32(0)) // VirtualSize
	w(uint32(0)) // VirtualAddress
	w(uint32(len(sect)))
	w(uint32(rawPtr))
	w(uint32(relPtr))
	w(uint32(0)) // PointerToLinenumbers
	w(uint16(len(relocs)))
	w(uint16(0)) // NumberOfLinenumbers
	w(uint32(scnInitData | scnMemRead))
	b.Write(sect)
	// Relocations against symbol 0 (the section itself).
	for _, r := range relocs {
		w(r)
		w(uint32(0))
		w(a.relType)
	}
	// Symbol table: one static symbol for .rsrc.
	w([8]byte{'.', 'r', 's', 'r', 'c'})
	w(uint32(0)) // Value
	w(int16(1))  // SectionNumber
	w(uint16(0)) // Type
	w(uint8(symStatic))
	w(uint8(0)) // NumberOfAuxSymbols
	// Empty string table (its size field counts itself).
	w(uint32(4))
	return b.Bytes(), nil
}

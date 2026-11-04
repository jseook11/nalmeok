package media

import (
	"bufio"
	"encoding/binary"
	"io"
	"math/bits"
	"os"

	"github.com/jseook11/nalmeok/internal/i18n"
)

var errFLAC = i18n.Error("media.flacBad")

// flacInfo is what STREAMINFO says about a FLAC stream, plus where the
// frames start.
type flacInfo struct {
	streamInfo [34]byte
	maxBlock   int
	rate       int
	channels   int
	bps        int
	total      int64 // samples per channel; 0 = unknown
	audioOff   int64
	size       int64
}

// parseFLAC reads the "fLaC" marker (after an optional ID3v2 tag) and the
// metadata blocks up to the first frame.
func parseFLAC(f *os.File) (*flacInfo, error) {
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	fi := &flacInfo{size: st.Size()}
	var head [10]byte
	n, _ := f.ReadAt(head[:], 0)
	pos := id3Size(head[:n])
	var magic [4]byte
	if _, err := f.ReadAt(magic[:], pos); err != nil || string(magic[:]) != "fLaC" {
		return nil, errFLAC
	}
	pos += 4
	for first := true; ; first = false {
		var bh [4]byte
		if _, err := f.ReadAt(bh[:], pos); err != nil {
			return nil, errFLAC
		}
		last, kind := bh[0]&0x80 != 0, bh[0]&0x7F
		length := int64(bh[1])<<16 | int64(bh[2])<<8 | int64(bh[3])
		pos += 4
		if pos+length > fi.size || kind == 127 || first != (kind == 0) {
			return nil, errFLAC // STREAMINFO must come first, and only there
		}
		if kind == 0 {
			if length != 34 {
				return nil, errFLAC
			}
			if _, err := f.ReadAt(fi.streamInfo[:], pos); err != nil {
				return nil, errFLAC
			}
		}
		pos += length
		if last {
			break
		}
	}
	si := fi.streamInfo[:]
	fi.maxBlock = int(binary.BigEndian.Uint16(si[2:]))
	fi.rate = int(si[10])<<12 | int(si[11])<<4 | int(si[12])>>4
	fi.channels = int(si[12]>>1&7) + 1
	fi.bps = int(si[12]&1)<<4 | int(si[13]>>4) + 1
	fi.total = int64(si[13]&0x0F)<<32 | int64(binary.BigEndian.Uint32(si[14:]))
	fi.audioOff = pos
	if fi.rate == 0 || fi.maxBlock == 0 || pos >= fi.size {
		return nil, errFLAC
	}
	return fi, nil
}

// flacHeader is a parsed frame header.
type flacHeader struct {
	length   int    // header bytes, CRC-8 included
	samples  int64  // block size
	variable bool   // blocking strategy: num counts samples, not frames
	num      uint64 // frame or first-sample number
}

var flacBlockRates = [12]int{0, 88200, 176400, 192000, 8000, 16000, 22050, 24000, 32000, 44100, 48000, 96000}
var flacDepths = [8]int{0, 8, 12, 0, 16, 20, 24, 32}

// header parses and checks a frame header at the start of b. Audio data can
// contain the sync code, so every field must agree with STREAMINFO, the
// CRC-8 must match, and the frame must continue prev (nil for the first).
func (fi *flacInfo) header(b []byte, prev *flacHeader) (flacHeader, bool) {
	var h flacHeader
	if len(b) < 6 || b[0] != 0xFF || b[1]&0xFE != 0xF8 || b[3]&1 != 0 {
		return h, false
	}
	h.variable = b[1]&1 != 0
	bsCode, rateCode := b[2]>>4, b[2]&0x0F
	chCode, depthCode := b[3]>>4, b[3]>>1&7
	if bsCode == 0 || rateCode == 15 || chCode > 10 || depthCode == 3 {
		return h, false
	}
	if prev != nil && h.variable != prev.variable {
		return h, false
	}
	num, n, ok := flacUTF8(b[4:], h.variable)
	if !ok {
		return h, false
	}
	h.num = num
	i := 4 + n
	// block size and sample rate may sit in 8 or 16 bits after the number
	extra := 0
	switch bsCode {
	case 6:
		extra = 1
	case 7:
		extra = 2
	}
	switch rateCode {
	case 12:
		extra++
	case 13, 14:
		extra += 2
	}
	if len(b) < i+extra+1 {
		return h, false
	}
	switch {
	case bsCode == 1:
		h.samples = 192
	case bsCode <= 5:
		h.samples = 576 << (bsCode - 2)
	case bsCode == 6:
		h.samples, i = int64(b[i])+1, i+1
	case bsCode == 7:
		h.samples, i = int64(binary.BigEndian.Uint16(b[i:]))+1, i+2
	default:
		h.samples = 256 << (bsCode - 8)
	}
	rate := fi.rate
	switch {
	case rateCode == 12:
		rate, i = int(b[i])*1000, i+1
	case rateCode == 13:
		rate, i = int(binary.BigEndian.Uint16(b[i:])), i+2
	case rateCode == 14:
		rate, i = int(binary.BigEndian.Uint16(b[i:]))*10, i+2
	case rateCode != 0:
		rate = flacBlockRates[rateCode]
	}
	channels := int(chCode) + 1
	if chCode >= 8 { // left/side, right/side, mid/side stereo
		channels = 2
	}
	depth := flacDepths[depthCode]
	if rate != fi.rate || channels != fi.channels || (depth != 0 && depth != fi.bps) ||
		h.samples > int64(fi.maxBlock) || crc8(b[:i]) != b[i] {
		return h, false
	}
	h.length = i + 1
	if prev != nil {
		want := prev.num + 1
		if h.variable {
			want = prev.num + uint64(prev.samples)
		}
		if h.num != want {
			return h, false
		}
	}
	return h, true
}

// flacUTF8 decodes the UTF-8-style coded frame (up to 31 bits, 6 bytes) or
// sample number (up to 36 bits, 7 bytes).
func flacUTF8(b []byte, variable bool) (uint64, int, bool) {
	if len(b) == 0 {
		return 0, 0, false
	}
	c := b[0]
	// leading ones give the length: 0 is one byte, 1 a stray continuation
	ones := bits.LeadingZeros8(^c)
	switch {
	case ones == 0:
		return uint64(c), 1, true
	case ones == 1 || ones > 7 || (ones == 7 && !variable) || len(b) < ones:
		return 0, 0, false
	}
	v := uint64(c) & (0x7F >> ones)
	for _, x := range b[1:ones] {
		if x&0xC0 != 0x80 {
			return 0, 0, false
		}
		v = v<<6 | uint64(x&0x3F)
	}
	return v, ones, true
}

var crc8Table, crc16Table = flacCRCTables()

func flacCRCTables() (t8 [256]byte, t16 [256]uint16) {
	for i := range 256 {
		c8, c16 := byte(i), uint16(i)<<8
		for range 8 {
			c8 = c8<<1 ^ byte(0x07*int(c8>>7))
			c16 = c16<<1 ^ uint16(0x8005*int(c16>>15))
		}
		t8[i], t16[i] = c8, c16
	}
	return
}

func crc8(b []byte) byte {
	var c byte
	for _, x := range b {
		c = crc8Table[c^x]
	}
	return c
}

// flacFrame is one frame's place in the file.
type flacFrame struct{ off, size, samples int64 }

// walkFLAC calls fn for every frame in order. Frames carry no length, so a
// frame ends where its CRC-16 footer checks out (the CRC over the frame with
// its footer is 0) and a valid next header, or the end of the file, follows.
// Anything else in the frame region is an error.
func walkFLAC(f *os.File, fi *flacInfo, fn func(flacFrame) error) error {
	br := bufio.NewReaderSize(io.NewSectionReader(f, fi.audioOff, fi.size-fi.audioOff), 1<<16)
	b, _ := br.Peek(16)
	h, ok := fi.header(b, nil)
	if !ok {
		return errFLAC
	}
	frame := flacFrame{off: fi.audioOff, samples: h.samples}
	var crc uint16
	for {
		c, err := br.ReadByte()
		if err != nil {
			return errFLAC // the last frame is cut short
		}
		crc = crc<<8 ^ crc16Table[byte(crc>>8)^c]
		frame.size++
		if crc != 0 || frame.size < int64(h.length)+3 {
			continue
		}
		b, _ := br.Peek(16)
		if len(b) == 0 {
			return fn(frame)
		}
		next, ok := fi.header(b, &h)
		if !ok {
			continue // a zero CRC inside the frame, not its end
		}
		if err := fn(frame); err != nil {
			return err
		}
		h, crc = next, 0
		frame = flacFrame{off: frame.off + frame.size, samples: h.samples}
	}
}

func flacDuration(path string) (float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	fi, err := parseFLAC(f)
	if err != nil {
		return 0, err
	}
	total := fi.total
	if total == 0 { // streaming encoders leave it unknown: count the frames
		err = walkFLAC(f, fi, func(fr flacFrame) error { total += fr.samples; return nil })
		if err != nil {
			return 0, err
		}
	}
	return float64(total) / float64(fi.rate), nil
}

// pieceHeader is "fLaC" and a lone STREAMINFO for a piece of samples
// samples. The MD5 covers the whole stream, so it is zeroed (= unknown).
func (fi *flacInfo) pieceHeader(samples int64) []byte {
	si := fi.streamInfo
	si[13] = si[13]&0xF0 | byte(samples>>32)&0x0F
	binary.BigEndian.PutUint32(si[14:], uint32(samples))
	clear(si[18:])
	return append([]byte("fLaC\x80\x00\x00\x22"), si[:]...)
}

func prepareFLAC(path, dir string, maxSeconds float64) ([]Piece, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := parseFLAC(f)
	if err != nil {
		return nil, err
	}
	whole := func(total int64) ([]Piece, error) {
		p := Piece{Path: piecePath(dir, path, 1, ".flac"), MIME: "audio/flac", Seconds: float64(total) / float64(fi.rate)}
		if err := linkOrCopy(path, p.Path); err != nil {
			return nil, err
		}
		return []Piece{p}, nil
	}
	if fi.total > 0 && (maxSeconds <= 0 || float64(fi.total) <= maxSeconds*float64(fi.rate)) {
		return whole(fi.total)
	}
	// Group frames into spans of at most limit samples (at least one frame).
	limit := int64(maxSeconds * float64(fi.rate))
	if maxSeconds <= 0 {
		limit = 1<<63 - 1
	}
	var spans []flacFrame
	var total int64
	err = walkFLAC(f, fi, func(fr flacFrame) error {
		total += fr.samples
		if n := len(spans); n > 0 && spans[n-1].samples+fr.samples <= limit {
			spans[n-1].size += fr.size
			spans[n-1].samples += fr.samples
			return nil
		}
		spans = append(spans, fr)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(spans) == 1 { // unknown total that turned out to fit
		return whole(total)
	}
	pieces := make([]Piece, 0, len(spans))
	for _, s := range spans {
		p := Piece{
			Path:    piecePath(dir, path, len(pieces)+1, ".flac"),
			MIME:    "audio/flac",
			Seconds: float64(s.samples) / float64(fi.rate),
		}
		pw, err := createPiece(p.Path)
		if err != nil {
			return nil, err
		}
		bw := bufio.NewWriterSize(pw, 256<<10)
		_, err = bw.Write(fi.pieceHeader(s.samples))
		if err == nil {
			_, err = io.Copy(bw, io.NewSectionReader(f, s.off, s.size))
		}
		if err == nil {
			err = bw.Flush()
		}
		if err != nil {
			pw.abort()
			return nil, err
		}
		if err := pw.commit(); err != nil {
			return nil, err
		}
		pieces = append(pieces, p)
	}
	return pieces, nil
}

package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/jseook11/nalmeok/internal/i18n"
)

// ErrNotAAC is returned for MP4 files whose first audio track is not AAC.
var ErrNotAAC = i18n.Error("media.mp4NotAAC")

// errMP4 wraps structural problems of an MP4 file. detail is catalog text.
func errMP4(detail string) error {
	return i18n.Error("media.mp4Unsupported", "detail", detail)
}

// maxMoovSize guards against absurd moov sizes in corrupt files.
const maxMoovSize = 512 << 20

var adtsSampleRates = []int{96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350}

type mp4Track struct {
	timescale uint32
	edited    float64 // presentation length from the edit list (0 if none)
	// sampleEntry is the source mp4a box (with esds), copied verbatim into pieces.
	sampleEntry []byte
	// mp3 marks MPEG audio (layer III) samples: they are whole MP3 frames,
	// so pieces are written as plain MP3 instead of M4A.
	mp3      bool
	profile  int // ADTS profile = AAC object type - 1
	srIndex  int
	channels int
	sizes    []uint32
	offsets  []int64
	deltas   []uint32
}

func (t *mp4Track) ticks() uint64 {
	var total uint64
	for _, d := range t.deltas {
		total += uint64(d)
	}
	return total
}

// playSeconds is the presented length: the edit list drops encoder priming
// and padding, which is what afinfo and players report.
func (t *mp4Track) playSeconds() float64 {
	if t.edited > 0 {
		return t.edited
	}
	return t.seconds()
}

// seconds is the length of all coded samples (what the ADTS pieces carry).
func (t *mp4Track) seconds() float64 {
	if t.timescale == 0 {
		return 0
	}
	return float64(t.ticks()) / float64(t.timescale)
}

type box struct {
	typ  string
	body []byte
}

// childBoxes splits b into boxes.
func childBoxes(b []byte) ([]box, error) {
	var out []box
	for len(b) > 0 {
		if len(b) < 8 {
			return out, errMP4(i18n.T("mp4.boxSize"))
		}
		size := uint64(binary.BigEndian.Uint32(b))
		typ := string(b[4:8])
		head := uint64(8)
		switch size {
		case 0:
			size = uint64(len(b))
		case 1:
			if len(b) < 16 {
				return out, errMP4(i18n.T("mp4.boxSize"))
			}
			size = binary.BigEndian.Uint64(b[8:])
			head = 16
		}
		if size < head || size > uint64(len(b)) {
			return out, errMP4(i18n.T("mp4.boxSizeOf", "box", strconv.Quote(typ)))
		}
		out = append(out, box{typ: typ, body: b[head:size]})
		b = b[size:]
	}
	return out, nil
}

func findBox(b []byte, path ...string) ([]byte, bool) {
	for _, name := range path {
		boxes, _ := childBoxes(b)
		found := false
		for _, bx := range boxes {
			if bx.typ == name {
				b, found = bx.body, true
				break
			}
		}
		if !found {
			return nil, false
		}
	}
	return b, true
}

// readMoov walks top-level boxes with seeks and returns the moov body.
// moov may be at the start or at the end of the file.
func readMoov(f *os.File) ([]byte, []moofBox, error) {
	st, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	fileSize := st.Size()
	var head [16]byte
	var moov []byte
	var moofs []moofBox
	moofBytes := 0
	for pos := int64(0); pos+8 <= fileSize; {
		if _, err := f.ReadAt(head[:8], pos); err != nil {
			return nil, nil, err
		}
		size := int64(binary.BigEndian.Uint32(head[:4]))
		typ := string(head[4:8])
		hlen := int64(8)
		switch size {
		case 0:
			size = fileSize - pos
		case 1:
			if _, err := f.ReadAt(head[8:16], pos+8); err != nil {
				return nil, nil, err
			}
			size = int64(binary.BigEndian.Uint64(head[8:16]))
			hlen = 16
		}
		if size < hlen || pos+size > fileSize {
			if moov != nil {
				break // trailing garbage after a complete moov
			}
			return nil, nil, errMP4(i18n.T("mp4.boxSizeOf", "box", strconv.Quote(typ)))
		}
		switch typ {
		case "moov":
			if size-hlen > maxMoovSize {
				return nil, nil, errMP4(i18n.T("mp4.moovTooBig"))
			}
			moov = make([]byte, size-hlen)
			if _, err := f.ReadAt(moov, pos+hlen); err != nil {
				return nil, nil, err
			}
		case "moof":
			moofBytes += int(size)
			if moofBytes > maxMoovSize {
				return nil, nil, errMP4(i18n.T("mp4.moovTooBig"))
			}
			body := make([]byte, size-hlen)
			if _, err := f.ReadAt(body, pos+hlen); err != nil {
				return nil, nil, err
			}
			moofs = append(moofs, moofBox{pos: pos, body: body})
		}
		pos += size
	}
	if moov == nil {
		return nil, nil, errMP4(i18n.T("mp4.missing", "box", "moov"))
	}
	return moov, moofs, nil
}

func parseMP4(path string) (*mp4Track, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	moov, moofs, err := readMoov(f)
	if err != nil {
		return nil, err
	}
	if _, ok := findBox(moov, "mvex"); !ok {
		moofs = nil // moof only extends a moov that declares mvex (OBS hybrid MP4 keeps both)
	}
	traks, err := childBoxes(moov)
	if err != nil {
		return nil, err
	}
	for _, trak := range traks {
		if trak.typ != "trak" {
			continue
		}
		hdlr, ok := findBox(trak.body, "mdia", "hdlr")
		if !ok || len(hdlr) < 12 || string(hdlr[8:12]) != "soun" {
			continue
		}
		tr, err := parseAudioTrak(trak.body, len(moofs) > 0)
		if err != nil {
			return nil, err
		}
		if err := tr.addFragments(moov, trak.body, moofs); err != nil {
			return nil, err
		}
		tr.edited = editedSeconds(moov, trak.body)
		if tr.edited == 0 {
			if n := smpbSamples(moov); n > 0 {
				tr.edited = float64(n) / float64(tr.timescale)
			}
		}
		return tr, nil
	}
	return nil, errMP4(i18n.T("mp4.noAudioTrack"))
}

// smpbSamples reads the valid sample count from iTunes gapless metadata
// (iTunSMPB: " 0 priming padding samples ..."), which afconvert's m4af
// writes instead of an edit list. Returns 0 if absent.
func smpbSamples(moov []byte) uint64 {
	i := bytes.Index(moov, []byte("iTunSMPB"))
	if i < 0 {
		return 0
	}
	rest := moov[i:]
	j := bytes.Index(rest, []byte("data"))
	if j < 4 {
		return 0
	}
	size := int(binary.BigEndian.Uint32(rest[j-4:]))
	if size < 16 || j-4+size > len(rest) {
		return 0
	}
	fields := strings.Fields(string(rest[j+12 : j-4+size]))
	if len(fields) < 4 {
		return 0
	}
	n, err := strconv.ParseUint(fields[3], 16, 64)
	if err != nil {
		return 0
	}
	return n
}

// editedSeconds sums the non-empty edit list segments, in seconds.
func editedSeconds(moov, trak []byte) float64 {
	mvhd, ok := findBox(moov, "mvhd")
	if !ok || len(mvhd) < 20 {
		return 0
	}
	var movieScale uint32
	if mvhd[0] == 1 {
		if len(mvhd) < 24 {
			return 0
		}
		movieScale = binary.BigEndian.Uint32(mvhd[20:])
	} else {
		movieScale = binary.BigEndian.Uint32(mvhd[12:])
	}
	elst, ok := findBox(trak, "edts", "elst")
	if !ok || len(elst) < 8 || movieScale == 0 {
		return 0
	}
	count := int(binary.BigEndian.Uint32(elst[4:]))
	entry := 12
	if elst[0] == 1 {
		entry = 20
	}
	if count <= 0 || 8+count*entry > len(elst) {
		return 0
	}
	var total uint64
	for i := range count {
		e := elst[8+i*entry:]
		var segment uint64
		var mediaTime int64
		if elst[0] == 1 {
			segment = binary.BigEndian.Uint64(e)
			mediaTime = int64(binary.BigEndian.Uint64(e[8:]))
		} else {
			segment = uint64(binary.BigEndian.Uint32(e))
			mediaTime = int64(int32(binary.BigEndian.Uint32(e[4:])))
		}
		if mediaTime >= 0 {
			total += segment
		}
	}
	return float64(total) / float64(movieScale)
}

func parseAudioTrak(trak []byte, fragmented bool) (*mp4Track, error) {
	t := &mp4Track{}
	mdhd, ok := findBox(trak, "mdia", "mdhd")
	if !ok || len(mdhd) < 4 {
		return nil, errMP4(i18n.T("mp4.missing", "box", "mdhd"))
	}
	if mdhd[0] == 1 {
		if len(mdhd) < 24 {
			return nil, errMP4(i18n.T("mp4.bad", "box", "mdhd"))
		}
		t.timescale = binary.BigEndian.Uint32(mdhd[20:])
	} else {
		if len(mdhd) < 16 {
			return nil, errMP4(i18n.T("mp4.bad", "box", "mdhd"))
		}
		t.timescale = binary.BigEndian.Uint32(mdhd[12:])
	}
	if t.timescale == 0 {
		return nil, errMP4("timescale 0")
	}
	stbl, ok := findBox(trak, "mdia", "minf", "stbl")
	if !ok {
		return nil, errMP4(i18n.T("mp4.missing", "box", "stbl"))
	}
	stsd, ok := findBox(stbl, "stsd")
	if !ok || len(stsd) < 8 {
		return nil, errMP4(i18n.T("mp4.missing", "box", "stsd"))
	}
	entries, err := childBoxes(stsd[8:])
	if err != nil || len(entries) == 0 {
		return nil, errMP4(i18n.T("mp4.bad", "box", "stsd"))
	}
	switch entries[0].typ {
	case "mp4a":
		if err := t.parseMP4A(entries[0].body); err != nil {
			return nil, err
		}
	case ".mp3": // QuickTime's MP3 sample entry
		t.mp3 = true
	default:
		return nil, fmt.Errorf("%w (%s)", ErrNotAAC, entries[0].typ)
	}
	t.sampleEntry = mp4Box("mp4a", entries[0].body)
	if err := t.parseSampleTable(stbl, fragmented); err != nil {
		return nil, err
	}
	return t, nil
}

// parseMP4A reads the AudioSampleEntry and its esds.
func (t *mp4Track) parseMP4A(b []byte) error {
	if len(b) < 28 {
		return errMP4(i18n.T("mp4.bad", "box", "mp4a"))
	}
	skip := 28
	switch binary.BigEndian.Uint16(b[8:]) { // sound description version
	case 1:
		skip += 16
	case 2:
		skip += 36
	}
	if len(b) < skip {
		return errMP4(i18n.T("mp4.bad", "box", "mp4a"))
	}
	esds := findESDS(b[skip:], 0)
	if esds == nil {
		return errMP4(i18n.T("mp4.missing", "box", "esds"))
	}
	objectType, asc, err := decoderSpecificInfo(esds)
	if err != nil {
		return err
	}
	if objectType == 0x69 || objectType == 0x6B { // MPEG-2 / MPEG-1 audio
		t.mp3 = true
		return nil
	}
	return t.parseASC(asc)
}

func findESDS(b []byte, depth int) []byte {
	boxes, _ := childBoxes(b)
	for _, bx := range boxes {
		if bx.typ == "esds" {
			return bx.body
		}
	}
	if depth < 2 {
		for _, bx := range boxes {
			if bx.typ == "wave" {
				if e := findESDS(bx.body, depth+1); e != nil {
					return e
				}
			}
		}
	}
	return nil
}

// readDescriptor returns (tag, body, rest) of an MPEG-4 descriptor.
func readDescriptor(b []byte) (byte, []byte, []byte, error) {
	if len(b) < 2 {
		return 0, nil, nil, errMP4(i18n.T("mp4.bad", "box", "esds"))
	}
	tag := b[0]
	n, i := 0, 1
	for ; i < 5 && i < len(b); i++ {
		n = n<<7 | int(b[i]&0x7f)
		if b[i]&0x80 == 0 {
			i++
			break
		}
	}
	if i+n > len(b) {
		return 0, nil, nil, errMP4(i18n.T("mp4.badLength", "box", "esds"))
	}
	return tag, b[i : i+n], b[i+n:], nil
}

// decoderSpecificInfo returns the DecoderConfig object type and, for AAC,
// its AudioSpecificConfig.
func decoderSpecificInfo(esds []byte) (byte, []byte, error) {
	if len(esds) < 4 {
		return 0, nil, errMP4(i18n.T("mp4.bad", "box", "esds"))
	}
	tag, es, _, err := readDescriptor(esds[4:])
	if err != nil {
		return 0, nil, err
	}
	if tag != 0x03 || len(es) < 3 {
		return 0, nil, errMP4(i18n.T("mp4.missing", "box", "ES_Descriptor"))
	}
	flags := es[2]
	es = es[3:]
	if flags&0x80 != 0 {
		if len(es) < 2 {
			return 0, nil, errMP4(i18n.T("mp4.bad", "box", "esds"))
		}
		es = es[2:]
	}
	if flags&0x40 != 0 {
		if len(es) < 1 || len(es) < 1+int(es[0]) {
			return 0, nil, errMP4(i18n.T("mp4.bad", "box", "esds"))
		}
		es = es[1+int(es[0]):]
	}
	if flags&0x20 != 0 {
		if len(es) < 2 {
			return 0, nil, errMP4(i18n.T("mp4.bad", "box", "esds"))
		}
		es = es[2:]
	}
	for len(es) > 0 {
		tag, body, rest, err := readDescriptor(es)
		if err != nil {
			return 0, nil, err
		}
		es = rest
		if tag != 0x04 {
			continue
		}
		if len(body) < 13 {
			return 0, nil, errMP4(i18n.T("mp4.bad", "box", "DecoderConfig"))
		}
		switch body[0] {
		case 0x40, 0x66, 0x67, 0x68: // MPEG-4 audio, MPEG-2 AAC main/LC/SSR
		case 0x69, 0x6B: // MPEG-2 / MPEG-1 audio: no AudioSpecificConfig
			return body[0], nil, nil
		default:
			return 0, nil, fmt.Errorf("%w (objectType 0x%02x)", ErrNotAAC, body[0])
		}
		sub := body[13:]
		for len(sub) > 0 {
			tag, dsi, rest, err := readDescriptor(sub)
			if err != nil {
				return 0, nil, err
			}
			if tag == 0x05 {
				return body[0], dsi, nil
			}
			sub = rest
		}
		return 0, nil, errMP4(i18n.T("mp4.missing", "box", "AudioSpecificConfig"))
	}
	return 0, nil, errMP4(i18n.T("mp4.missing", "box", "DecoderConfig"))
}

type bitReader struct {
	b   []byte
	pos int
}

func (r *bitReader) read(n int) (int, bool) {
	v := 0
	for range n {
		if r.pos >= len(r.b)*8 {
			return 0, false
		}
		bit := (r.b[r.pos/8] >> (7 - r.pos%8)) & 1
		v = v<<1 | int(bit)
		r.pos++
	}
	return v, true
}

// parseASC reads AudioSpecificConfig into ADTS header fields.
func (t *mp4Track) parseASC(asc []byte) error {
	r := &bitReader{b: asc}
	objectType := func() (int, bool) {
		v, ok := r.read(5)
		if ok && v == 31 {
			ext, ok2 := r.read(6)
			return 32 + ext, ok2
		}
		return v, ok
	}
	srIndex := func() (int, bool) {
		v, ok := r.read(4)
		if ok && v == 15 {
			freq, ok2 := r.read(24)
			for i, rate := range adtsSampleRates {
				if rate == freq {
					return i, ok2
				}
			}
			return -1, ok2
		}
		return v, ok
	}
	aot, ok1 := objectType()
	sri, ok2 := srIndex()
	ch, ok3 := r.read(4)
	if !ok1 || !ok2 || !ok3 {
		return errMP4(i18n.T("mp4.bad", "box", "AudioSpecificConfig"))
	}
	if aot == 5 || aot == 29 { // HE-AAC explicit signaling: core follows
		if _, ok := srIndex(); !ok {
			return errMP4(i18n.T("mp4.bad", "box", "AudioSpecificConfig"))
		}
		var ok bool
		if aot, ok = objectType(); !ok {
			return errMP4(i18n.T("mp4.bad", "box", "AudioSpecificConfig"))
		}
	}
	if aot < 1 || aot > 4 {
		return fmt.Errorf("%w (object type %d)", ErrNotAAC, aot)
	}
	if sri < 0 || sri >= len(adtsSampleRates) {
		return errMP4(i18n.T("mp4.sampleRate"))
	}
	if ch < 1 || ch > 7 {
		return errMP4(i18n.T("mp4.channels", "count", ch))
	}
	t.profile, t.srIndex, t.channels = aot-1, sri, ch
	return nil
}

func u32s(b []byte, off, count int, out func(i int, v uint32)) bool {
	if count < 0 || off+count*4 > len(b) {
		return false
	}
	for i := range count {
		out(i, binary.BigEndian.Uint32(b[off+i*4:]))
	}
	return true
}

func (t *mp4Track) parseSampleTable(stbl []byte, fragmented bool) error {
	// stsz
	stsz, ok := findBox(stbl, "stsz")
	if !ok || len(stsz) < 12 {
		return errMP4(i18n.T("mp4.missing", "box", "stsz"))
	}
	fixed := binary.BigEndian.Uint32(stsz[4:])
	n := int(binary.BigEndian.Uint32(stsz[8:]))
	if n == 0 && fragmented {
		return nil // samples come from moof boxes
	}
	if n <= 0 {
		return errMP4(i18n.T("mp4.noSamples"))
	}
	if n > 1<<26 {
		return errMP4(i18n.T("mp4.sampleCount"))
	}
	t.sizes = make([]uint32, n)
	if fixed != 0 {
		for i := range t.sizes {
			t.sizes[i] = fixed
		}
	} else if !u32s(stsz, 12, n, func(i int, v uint32) { t.sizes[i] = v }) {
		return errMP4(i18n.T("mp4.bad", "box", "stsz"))
	}

	// stco / co64
	var chunks []int64
	if stco, ok := findBox(stbl, "stco"); ok && len(stco) >= 8 {
		c := int(binary.BigEndian.Uint32(stco[4:]))
		chunks = make([]int64, max(c, 0))
		if !u32s(stco, 8, c, func(i int, v uint32) { chunks[i] = int64(v) }) {
			return errMP4(i18n.T("mp4.bad", "box", "stco"))
		}
	} else if co64, ok := findBox(stbl, "co64"); ok && len(co64) >= 8 {
		c := int(binary.BigEndian.Uint32(co64[4:]))
		if c < 0 || 8+c*8 > len(co64) {
			return errMP4(i18n.T("mp4.bad", "box", "co64"))
		}
		chunks = make([]int64, c)
		for i := range chunks {
			chunks[i] = int64(binary.BigEndian.Uint64(co64[8+i*8:]))
		}
	} else {
		return errMP4(i18n.T("mp4.missing", "box", "stco"))
	}

	// stsc
	stsc, ok := findBox(stbl, "stsc")
	if !ok || len(stsc) < 8 {
		return errMP4(i18n.T("mp4.missing", "box", "stsc"))
	}
	sc := int(binary.BigEndian.Uint32(stsc[4:]))
	if sc <= 0 || 8+sc*12 > len(stsc) {
		return errMP4(i18n.T("mp4.bad", "box", "stsc"))
	}
	t.offsets = make([]int64, n)
	s := 0
	for e := 0; e < sc && s < n; e++ {
		first := int(binary.BigEndian.Uint32(stsc[8+e*12:])) - 1
		per := int(binary.BigEndian.Uint32(stsc[12+e*12:]))
		last := len(chunks)
		if e+1 < sc {
			last = int(binary.BigEndian.Uint32(stsc[8+(e+1)*12:])) - 1
		}
		if first < 0 || last > len(chunks) || first > last {
			return errMP4(i18n.T("mp4.bad", "box", "stsc"))
		}
		for c := first; c < last && s < n; c++ {
			off := chunks[c]
			for k := 0; k < per && s < n; k++ {
				t.offsets[s] = off
				off += int64(t.sizes[s])
				s++
			}
		}
	}
	if s != n {
		return errMP4(i18n.T("mp4.sampleTable"))
	}

	// stts
	stts, ok := findBox(stbl, "stts")
	if !ok || len(stts) < 8 {
		return errMP4(i18n.T("mp4.missing", "box", "stts"))
	}
	ec := int(binary.BigEndian.Uint32(stts[4:]))
	if ec < 0 || 8+ec*8 > len(stts) {
		return errMP4(i18n.T("mp4.bad", "box", "stts"))
	}
	t.deltas = make([]uint32, 0, n)
	var last uint32 = 1024
	for e := 0; e < ec && len(t.deltas) < n; e++ {
		count := int(binary.BigEndian.Uint32(stts[8+e*8:]))
		last = binary.BigEndian.Uint32(stts[12+e*8:])
		for k := 0; k < count && len(t.deltas) < n; k++ {
			t.deltas = append(t.deltas, last)
		}
	}
	for len(t.deltas) < n {
		t.deltas = append(t.deltas, last)
	}
	return nil
}

// windowReader serves t's samples from a sliding buffer so mostly
// sequential samples cost few syscalls.
type windowReader struct {
	f     io.ReaderAt
	t     *mp4Track
	buf   []byte
	start int64
	n     int
}

func (w *windowReader) sample(i int) ([]byte, error) {
	off, size := w.t.offsets[i], int(w.t.sizes[i])
	if off >= w.start && off+int64(size) <= w.start+int64(w.n) {
		j := int(off - w.start)
		return w.buf[j : j+size], nil
	}
	if size > len(w.buf) {
		w.buf = make([]byte, size)
	}
	// Refill only the samples stored back to back from here: in a video file
	// the bytes after an audio chunk are video, which a full refill would read.
	want := size
	for j := i + 1; j < len(w.t.sizes) && w.t.offsets[j] == off+int64(want) && want+int(w.t.sizes[j]) <= len(w.buf); j++ {
		want += int(w.t.sizes[j])
	}
	n, err := w.f.ReadAt(w.buf[:want], off)
	if n < size {
		if err == nil || errors.Is(err, io.EOF) {
			err = errMP4(i18n.T("mp4.sampleOutside"))
		}
		return nil, err
	}
	w.start, w.n = off, n
	return w.buf[:size], nil
}

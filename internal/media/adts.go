package media

import (
	"bufio"
	"fmt"
	"io"
	"os"

	"github.com/jseook11/nalmeok/internal/i18n"
)

var errADTS = i18n.Error("media.adtsBad")

// Raw ADTS AAC (.aac) is remuxed into M4A like the MP4 inputs (see m4a.go:
// Gemini may block the same audio sent as audio/aac). Every ADTS frame
// becomes one MP4 sample: its payload without the ADTS header.

// adtsHeader holds the ADTS header fields this package needs.
type adtsHeader struct {
	profile, srIndex, channels int // fixed header: must not change
	length                     int // whole frame, header included
	headLen                    int // 7, or 9 with CRC
	blocks                     int // raw data blocks in the frame minus 1
}

// parseADTSHeader reads a header from b (at least 7 bytes).
func parseADTSHeader(b []byte) (adtsHeader, bool) {
	if b[0] != 0xFF || b[1]&0xF6 != 0xF0 { // sync 0xFFF, layer 00
		return adtsHeader{}, false
	}
	h := adtsHeader{
		profile:  int(b[2] >> 6),
		srIndex:  int(b[2]>>2) & 0xF,
		channels: int(b[2]&1)<<2 | int(b[3]>>6),
		length:   int(b[3]&3)<<11 | int(b[4])<<3 | int(b[5]>>5),
		headLen:  7,
		blocks:   int(b[6] & 3),
	}
	if b[1]&1 == 0 { // protection_absent = 0: a CRC follows
		h.headLen = 9
	}
	ok := h.srIndex < len(adtsSampleRates) && h.length > h.headLen
	return h, ok
}

func (h adtsHeader) sameStream(ref adtsHeader) bool {
	return h.profile == ref.profile && h.srIndex == ref.srIndex && h.channels == ref.channels
}

// adtsFrameAt reports whether a frame of ref's stream starts at pos in f.
func adtsFrameAt(f io.ReaderAt, pos int64, ref adtsHeader) (adtsHeader, bool) {
	var b [7]byte
	if _, err := f.ReadAt(b[:], pos); err != nil {
		return adtsHeader{}, false
	}
	h, ok := parseADTSHeader(b[:])
	return h, ok && h.sameStream(ref)
}

func parseADTS(path string) (*mp4Track, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := st.Size()
	var head [10]byte
	n, _ := f.ReadAt(head[:], 0)
	pos := id3Size(head[:n])

	t := &mp4Track{}
	var ref adtsHeader
	var payload, maxPayload int64
	br := bufio.NewReaderSize(io.NewSectionReader(f, pos, size-pos), 1<<16)
	for {
		b, _ := br.Peek(7)
		if len(b) < 7 {
			break
		}
		h, ok := parseADTSHeader(b)
		if ok && len(t.sizes) > 0 {
			ok = h.sameStream(ref)
		}
		if !ok || pos+int64(h.length) > size {
			// Junk (an ID3v1 "TAG", a cut-off last frame) may follow the
			// last frame, but a stream that resumes after it was broken.
			if len(t.sizes) > 0 && adtsResumes(f, pos+1, size, ref) {
				return nil, fmt.Errorf("%w (offset %d)", errADTS, pos)
			}
			break
		}
		if len(t.sizes) == 0 {
			ref = h
			if h.channels == 0 { // layout lives in a PCE we would have to parse
				return nil, fmt.Errorf("%w (ADTS channel_configuration 0)", ErrUnsupported)
			}
		}
		// Several raw data blocks share one header without their positions
		// (with protection_absent=1), so they cannot be split into samples
		// without decoding. Encoders practically never write them.
		if h.blocks != 0 {
			return nil, fmt.Errorf("%w (ADTS %d raw data blocks per frame)", ErrUnsupported, h.blocks+1)
		}
		if len(t.sizes) >= 1<<26 {
			return nil, errADTS
		}
		p := int64(h.length - h.headLen)
		t.sizes = append(t.sizes, uint32(p))
		t.offsets = append(t.offsets, pos+int64(h.headLen))
		t.deltas = append(t.deltas, 1024)
		payload += p
		maxPayload = max(maxPayload, p)
		br.Discard(h.length)
		pos += int64(h.length)
	}
	if len(t.sizes) == 0 {
		return nil, errADTS
	}
	t.profile, t.srIndex, t.channels = ref.profile, ref.srIndex, ref.channels
	t.timescale = uint32(adtsSampleRates[ref.srIndex])
	bitrate := uint32(float64(payload*8) / t.seconds())
	t.sampleEntry = t.mp4aEntry(uint32(min(maxPayload, 0xFFFFFF)), bitrate)
	return t, nil
}

// adtsResumes reports whether two consecutive frames of ref's stream start
// anywhere in [from, size). Two in a row rule out a stray 0xFFF in a tag.
func adtsResumes(f *os.File, from, size int64, ref adtsHeader) bool {
	br := bufio.NewReaderSize(io.NewSectionReader(f, from, size-from), 1<<16)
	for pos := from; ; pos++ {
		b, err := br.ReadByte()
		if err != nil {
			return false
		}
		if b != 0xFF {
			continue
		}
		h, ok := adtsFrameAt(f, pos, ref)
		if !ok || pos+int64(h.length) > size {
			continue
		}
		next := pos + int64(h.length)
		if next == size {
			return true
		}
		if _, ok := adtsFrameAt(f, next, ref); ok {
			return true
		}
	}
}

// mp4aEntry builds the AudioSampleEntry with an esds carrying the
// AudioSpecificConfig the ADTS header implies.
func (t *mp4Track) mp4aEntry(bufferSize, bitrate uint32) []byte {
	descriptor := func(tag byte, body ...[]byte) []byte {
		n := 0
		for _, b := range body {
			n += len(b)
		}
		out := []byte{tag, byte(n)} // every descriptor here is < 128 bytes
		for _, b := range body {
			out = append(out, b...)
		}
		return out
	}
	aot := t.profile + 1
	asc := []byte{byte(aot<<3 | t.srIndex>>1), byte(t.srIndex&1<<7 | t.channels<<3)}
	config := be{0x40, 0x15} // MPEG-4 audio; audio stream, upstream 0, reserved 1
	config = append(config, byte(bufferSize>>16), byte(bufferSize>>8), byte(bufferSize))
	config = config.u32(bitrate).u32(bitrate)
	es := descriptor(0x03,
		be(nil).u16(0), []byte{0}, // ES_ID, flags
		descriptor(0x04, config, descriptor(0x05, asc)),
		descriptor(0x06, []byte{0x02}),
	)

	channels := uint16(t.channels)
	if t.channels == 7 { // configuration 7 is 7.1
		channels = 8
	}
	var rate uint32
	if t.timescale <= 0xFFFF { // 16.16 field; 64 kHz and up do not fit, decoders use the ASC
		rate = t.timescale << 16
	}
	entry := append(make(be, 6), 0, 1) // reserved, data_reference_index 1
	entry = append(entry, make([]byte, 8)...)
	entry = entry.u16(channels).u16(16).u32(0).u32(rate)
	return mp4Box("mp4a", entry, fullBox("esds", 0, 0, es))
}

// prepareADTS always remuxes, even a short file: ADTS uploads can be blocked.
func prepareADTS(path, dir string, maxSeconds float64) ([]Piece, error) {
	t, err := parseADTS(path)
	if err != nil {
		return nil, err
	}
	return remuxPieces(t, path, dir, maxSeconds)
}

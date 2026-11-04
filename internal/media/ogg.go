package media

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jseook11/nalmeok/internal/i18n"
)

var (
	errOgg      = i18n.Error("media.oggBad")
	errOggCodec = i18n.Error("media.oggCodec")
)

// Ogg page header flags.
const (
	oggContinued = 0x01 // the first packet continues one from the previous page
	oggBOS       = 0x02
	oggEOS       = 0x04
)

// oggMaxPage is the largest possible page: 27-byte header, 255 lacing
// values of 255 bytes each.
const oggMaxPage = 27 + 255 + 255*255

// oggCRCTable is the Ogg CRC-32: polynomial 0x04C11DB7, not reflected,
// initial value 0, no final xor.
var oggCRCTable = func() (t [256]uint32) {
	for i := range t {
		r := uint32(i) << 24
		for range 8 {
			if r&0x80000000 != 0 {
				r = r<<1 ^ 0x04C11DB7
			} else {
				r <<= 1
			}
		}
		t[i] = r
	}
	return t
}()

// oggCRC continues crc over b.
func oggCRC(crc uint32, b []byte) uint32 {
	for _, c := range b {
		crc = crc<<8 ^ oggCRCTable[byte(crc>>24)^c]
	}
	return crc
}

// oggPage is one whole page; raw holds its bytes, header included.
type oggPage struct {
	raw []byte
}

func (p oggPage) flags() byte    { return p.raw[5] }
func (p oggPage) granule() int64 { return int64(binary.LittleEndian.Uint64(p.raw[6:])) }
func (p oggPage) serial() uint32 { return binary.LittleEndian.Uint32(p.raw[14:]) }
func (p oggPage) lacing() []byte { return p.raw[27 : 27+int(p.raw[26])] }
func (p oggPage) body() []byte   { return p.raw[27+int(p.raw[26]):] }

// packetEnds counts the packets that finish on this page (lacing < 255).
func (p oggPage) packetEnds() int {
	n := 0
	for _, l := range p.lacing() {
		if l < 255 {
			n++
		}
	}
	return n
}

// oggPageSize returns the size of the page starting at b, or 0 if b does not
// start with a page header or is too short to tell.
func oggPageSize(b []byte) int {
	if len(b) < 27 || string(b[:4]) != "OggS" || b[4] != 0 || len(b) < 27+int(b[26]) {
		return 0
	}
	n := 27 + int(b[26])
	for _, l := range b[27:n] {
		n += int(l)
	}
	return n
}

// parseOggPage checks the page at the start of b (header and CRC) and
// returns it without copying.
func parseOggPage(b []byte) (oggPage, bool) {
	n := oggPageSize(b)
	if n == 0 || n > len(b) {
		return oggPage{}, false
	}
	raw := b[:n]
	// the CRC is computed with its own field zeroed
	crc := oggCRC(oggCRC(oggCRC(0, raw[:22]), make([]byte, 4)), raw[26:])
	return oggPage{raw: raw}, crc == binary.LittleEndian.Uint32(raw[22:])
}

// readOggPage reads the next page. It returns io.EOF at a clean end,
// io.ErrUnexpectedEOF for a page cut off by the end of the file, and errOgg
// for anything that is not a valid page.
func readOggPage(br *bufio.Reader) (oggPage, error) {
	head, _ := br.Peek(27)
	switch {
	case len(head) == 0:
		return oggPage{}, io.EOF
	case len(head) < 27:
		return oggPage{}, io.ErrUnexpectedEOF
	case string(head[:4]) != "OggS" || head[4] != 0:
		return oggPage{}, errOgg
	}
	head, _ = br.Peek(27 + int(head[26]))
	n := oggPageSize(head)
	b, _ := br.Peek(n)
	if n == 0 || len(b) < n {
		return oggPage{}, io.ErrUnexpectedEOF
	}
	p, ok := parseOggPage(b)
	if !ok {
		return oggPage{}, errOgg
	}
	p.raw = bytes.Clone(p.raw) // Peek's bytes die on the next read
	br.Discard(n)
	return p, nil
}

// oggStream describes the single logical stream of a file.
type oggStream struct {
	serial      uint32
	rate        int64 // granule positions per second
	preskip     int64 // Opus samples the decoder drops at the start
	headerSize  int64 // bytes of the header pages
	headerPages int
}

// readOggHeader reads the codec header pages from br. The Opus and Vorbis
// specs both require the last header packet to end its page, so the audio
// starts on a fresh page and the header pages can be copied verbatim.
func readOggHeader(br *bufio.Reader) (*oggStream, error) {
	first, err := readOggPage(br)
	if err != nil || first.flags()&oggBOS == 0 {
		return nil, errOgg
	}
	pkt := first.body()
	for i, l := range first.lacing() {
		if l < 255 {
			pkt = pkt[:255*i+int(l)]
			break
		}
	}
	s := &oggStream{serial: first.serial(), headerSize: int64(len(first.raw)), headerPages: 1}
	need := 0
	switch {
	case len(pkt) >= 19 && string(pkt[:8]) == "OpusHead":
		s.rate, s.preskip, need = 48000, int64(binary.LittleEndian.Uint16(pkt[10:])), 2 // OpusHead, OpusTags
	case len(pkt) >= 30 && string(pkt[:7]) == "\x01vorbis":
		s.rate, need = int64(binary.LittleEndian.Uint32(pkt[12:])), 3 // identification, comment, setup
	default:
		return nil, errOggCodec
	}
	if s.rate <= 0 {
		return nil, errOgg
	}
	for p, ends := first, first.packetEnds(); ; {
		if ends > need || ends == need && p.lacing()[len(p.lacing())-1] == 255 {
			return nil, errOgg // audio shares a page with the headers
		}
		if ends == need {
			return s, nil
		}
		if p, err = readOggPage(br); err != nil {
			return nil, errOgg
		}
		if p.serial() != s.serial || p.flags()&oggBOS != 0 {
			return nil, errOggCodec // multiplexed streams
		}
		ends += p.packetEnds()
		s.headerSize += int64(len(p.raw))
		s.headerPages++
	}
}

// lastOggGranule finds the last valid page with a granule position by
// scanning backward from the end; a cut-off tail page is skipped.
func lastOggGranule(f *os.File, size int64, s *oggStream) (int64, error) {
	n := min(size, 4*oggMaxPage)
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, size-n); err != nil {
		return 0, errOgg
	}
	for i := len(buf); ; {
		if i = bytes.LastIndex(buf[:i], []byte("OggS")); i < 0 {
			return 0, errOgg
		}
		p, ok := parseOggPage(buf[i:])
		if !ok || p.granule() == -1 {
			continue
		}
		if p.serial() != s.serial {
			return 0, errOggCodec // chained or multiplexed streams
		}
		return p.granule(), nil
	}
}

// openOgg opens path, reads its header pages and its last granule position.
// The returned reader is positioned at the first audio page.
func openOgg(path string) (*os.File, *bufio.Reader, *oggStream, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, nil, 0, err
	}
	fail := func(err error) (*os.File, *bufio.Reader, *oggStream, int64, error) {
		f.Close()
		return nil, nil, nil, 0, fmt.Errorf("%w: %s", err, filepath.Base(path))
	}
	st, err := f.Stat()
	if err != nil {
		return fail(err)
	}
	br := bufio.NewReaderSize(f, 2*oggMaxPage)
	s, err := readOggHeader(br)
	if err != nil {
		return fail(err)
	}
	last, err := lastOggGranule(f, st.Size(), s)
	if err != nil {
		return fail(err)
	}
	return f, br, s, last, nil
}

func (s *oggStream) seconds(samples int64) float64 {
	return float64(max(samples, 0)) / float64(s.rate)
}

func oggDuration(path string) (float64, error) {
	f, _, s, last, err := openOgg(path)
	if err != nil {
		return 0, err
	}
	f.Close()
	return s.seconds(last - s.preskip), nil
}

func prepareOgg(path, dir string, maxSeconds float64) ([]Piece, error) {
	f, br, s, last, err := openOgg(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	total := s.seconds(last - s.preskip)
	if maxSeconds <= 0 || total <= maxSeconds {
		// .opus is the same Ogg file; the piece uses .ogg like audio/ogg
		p := Piece{Path: piecePath(dir, path, 1, ".ogg"), MIME: "audio/ogg", Seconds: total}
		if err := linkOrCopy(path, p.Path); err != nil {
			return nil, err
		}
		return []Piece{p}, nil
	}

	c := &oggCutter{s: s, f: f, path: path, dir: dir, limit: int64(maxSeconds * float64(s.rate))}
	// The original granules already count the pre-skip, so the first piece
	// keeps them as they are: its start is preskip in granule terms.
	c.start, c.end = s.preskip, s.preskip
	var unit []oggPage
	for {
		p, err := readOggPage(br)
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			break // a cut-off last page is dropped, as lastOggGranule does
		}
		if err == nil && (p.serial() != s.serial || p.flags()&oggBOS != 0) {
			err = errOggCodec // chained or multiplexed streams
		}
		if err != nil {
			c.abort()
			return nil, fmt.Errorf("%w: %s", err, filepath.Base(path))
		}
		// A cut may only fall before a page that starts a fresh packet.
		if p.flags()&oggContinued == 0 && len(unit) > 0 {
			if err := c.add(unit); err != nil {
				c.abort()
				return nil, err
			}
			unit = nil
		}
		unit = append(unit, p)
	}
	if len(unit) > 0 {
		if err := c.add(unit); err != nil {
			c.abort()
			return nil, err
		}
	}
	if err := c.finish(); err != nil {
		return nil, err
	}
	return c.pieces, nil
}

// oggCutter collects runs of audio pages that may not be split (a packet
// spanning pages ties them together) into pieces. The run last added is
// held back so the piece's final page can get the EOS flag.
type oggCutter struct {
	s          *oggStream
	f          *os.File
	path, dir  string
	limit      int64 // granule positions per piece
	start, end int64 // granule at the start and end of the open piece
	pieces     []Piece
	pw         *pieceWriter
	bw         *bufio.Writer
	seq        uint32
	pending    []oggPage
}

func (c *oggCutter) add(unit []oggPage) error {
	end := c.end
	for _, p := range unit {
		if g := p.granule(); g != -1 {
			end = g
		}
	}
	if c.pw != nil && end-c.start > c.limit {
		if err := c.finish(); err != nil {
			return err
		}
		c.start = c.end
	}
	if c.pw == nil {
		if err := c.open(); err != nil {
			return err
		}
	}
	if err := c.write(c.pending, false); err != nil {
		return err
	}
	c.pending, c.end = unit, end
	return nil
}

// open starts a piece with a verbatim copy of the header pages.
func (c *oggCutter) open() error {
	p := Piece{Path: piecePath(c.dir, c.path, len(c.pieces)+1, ".ogg"), MIME: "audio/ogg"}
	pw, err := createPiece(p.Path)
	if err != nil {
		return err
	}
	c.pw, c.bw, c.seq = pw, bufio.NewWriterSize(pw, 256<<10), uint32(c.s.headerPages)
	c.pieces = append(c.pieces, p)
	_, err = io.Copy(c.bw, io.NewSectionReader(c.f, 0, c.s.headerSize))
	return err
}

// write appends pages with continuous sequence numbers and granules
// rebased so the piece plays from its own start; eos marks the last page.
func (c *oggCutter) write(pages []oggPage, eos bool) error {
	for i, p := range pages {
		b := p.raw
		b[5] &^= oggEOS
		if eos && i == len(pages)-1 {
			b[5] |= oggEOS
		}
		if g := p.granule(); g != -1 {
			binary.LittleEndian.PutUint64(b[6:], uint64(g-c.start+c.s.preskip))
		}
		binary.LittleEndian.PutUint32(b[18:], c.seq)
		binary.LittleEndian.PutUint32(b[22:], 0)
		binary.LittleEndian.PutUint32(b[22:], oggCRC(0, b))
		c.seq++
		if _, err := c.bw.Write(b); err != nil {
			return err
		}
	}
	return nil
}

func (c *oggCutter) finish() error {
	if c.pw == nil {
		return nil
	}
	err := c.write(c.pending, true)
	if err == nil {
		err = c.bw.Flush()
	}
	if err != nil {
		c.abort()
		return err
	}
	if err := c.pw.commit(); err != nil {
		c.pw = nil
		return err
	}
	c.pieces[len(c.pieces)-1].Seconds = c.s.seconds(c.end - c.start)
	c.pw, c.pending = nil, nil
	return nil
}

func (c *oggCutter) abort() {
	if c.pw != nil {
		c.pw.abort()
		c.pw = nil
	}
}

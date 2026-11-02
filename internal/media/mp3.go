package media

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jseook11/nalmeok/internal/i18n"
)

var errMP3 = i18n.Error("media.mp3NoFrame")

var mp3Bitrates = [5][16]int{
	{0, 32, 64, 96, 128, 160, 192, 224, 256, 288, 320, 352, 384, 416, 448, 0}, // V1 L1
	{0, 32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384, 0},    // V1 L2
	{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0},     // V1 L3
	{0, 32, 48, 56, 64, 80, 96, 112, 128, 144, 160, 176, 192, 224, 256, 0},    // V2 L1
	{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0},         // V2 L2/L3
}

var mp3Rates = [4][3]int{
	{11025, 12000, 8000},  // MPEG 2.5
	{},                    // reserved
	{22050, 24000, 16000}, // MPEG 2
	{44100, 48000, 32000}, // MPEG 1
}

type mp3Header struct {
	version    int // 3 = MPEG1, 2 = MPEG2, 0 = MPEG2.5
	layer      int // 1, 2, 3
	sampleRate int
	length     int
	samples    int
	mono       bool
}

func parseMP3Header(b []byte) (mp3Header, bool) {
	if len(b) < 4 {
		return mp3Header{}, false
	}
	h := binary.BigEndian.Uint32(b)
	if h>>21 != 0x7FF {
		return mp3Header{}, false
	}
	version := int(h>>19) & 3
	layer := 4 - int(h>>17)&3
	brIndex := int(h>>12) & 0xF
	srIndex := int(h>>10) & 3
	if version == 1 || layer == 4 || brIndex == 0 || brIndex == 15 || srIndex == 3 {
		return mp3Header{}, false
	}
	pad := int(h>>9) & 1
	table := 4
	if version == 3 {
		table = layer - 1
	} else if layer == 1 {
		table = 3
	}
	bitrate := mp3Bitrates[table][brIndex] * 1000
	rate := mp3Rates[version][srIndex]
	out := mp3Header{version: version, layer: layer, sampleRate: rate, mono: (h>>6)&3 == 3}
	switch {
	case layer == 1:
		out.samples = 384
		out.length = (12*bitrate/rate + pad) * 4
	case layer == 2:
		out.samples = 1152
		out.length = 144*bitrate/rate + pad
	case version == 3:
		out.samples = 1152
		out.length = 144*bitrate/rate + pad
	default:
		out.samples = 576
		out.length = 72*bitrate/rate + pad
	}
	if out.length < 4 {
		return mp3Header{}, false
	}
	return out, true
}

func (h mp3Header) compatible(o mp3Header) bool {
	return h.version == o.version && h.layer == o.layer && h.sampleRate == o.sampleRate
}

// infoFrames returns the total frame count from a Xing/Info or VBRI header in
// frame, and whether frame is such a metadata frame.
func infoFrames(h mp3Header, frame []byte) (int, bool) {
	side := 32
	switch {
	case h.version == 3 && h.mono:
		side = 17
	case h.version != 3 && h.mono:
		side = 9
	case h.version != 3:
		side = 17
	}
	if h.layer == 3 && len(frame) >= 4+side+12 {
		x := frame[4+side:]
		if tag := string(x[:4]); tag == "Xing" || tag == "Info" {
			flags := binary.BigEndian.Uint32(x[4:])
			if flags&1 != 0 {
				return int(binary.BigEndian.Uint32(x[8:])), true
			}
			return 0, true
		}
	}
	if len(frame) >= 36+18 && string(frame[36:40]) == "VBRI" {
		return int(binary.BigEndian.Uint32(frame[36+14:])), true
	}
	return 0, false
}

// id3Size returns the size of an ID3v2 tag at the start of b, or 0.
func id3Size(b []byte) int64 {
	if len(b) < 10 || string(b[:3]) != "ID3" {
		return 0
	}
	size := int64(b[6]&0x7f)<<21 | int64(b[7]&0x7f)<<14 | int64(b[8]&0x7f)<<7 | int64(b[9]&0x7f)
	size += 10
	if b[5]&0x10 != 0 {
		size += 10
	}
	return size
}

// walkMP3 calls fn for every audio frame. info marks a leading Xing/Info/VBRI
// frame, which carries no audio. frame is only valid during the call.
func walkMP3(f *os.File, fn func(h mp3Header, frame []byte, info bool) error) error {
	var head [10]byte
	n, _ := f.ReadAt(head[:], 0)
	start := id3Size(head[:n])
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return err
	}
	br := bufio.NewReaderSize(f, 1<<16)
	var ref *mp3Header
	for {
		b, _ := br.Peek(4)
		if len(b) < 4 {
			break
		}
		h, ok := parseMP3Header(b)
		if ok && ref != nil {
			ok = h.compatible(*ref)
		}
		if ok && ref == nil {
			// lock on only if the next frame header also looks right
			if nb, _ := br.Peek(h.length + 4); len(nb) >= h.length+4 {
				next, ok2 := parseMP3Header(nb[h.length:])
				ok = ok2 && next.compatible(h)
			}
		}
		if !ok {
			if _, err := br.Discard(1); err != nil {
				break
			}
			continue
		}
		frame, _ := br.Peek(h.length)
		if len(frame) < h.length {
			break // truncated last frame
		}
		info := false
		if ref == nil {
			ref = &h
			_, info = infoFrames(h, frame)
		}
		if err := fn(h, frame, info); err != nil {
			return err
		}
		br.Discard(h.length)
	}
	if ref == nil {
		return errMP3
	}
	return nil
}

func mp3Duration(path string) (float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	// Fast path: Xing/Info/VBRI frame count in the first frame.
	var head [10]byte
	n, _ := f.ReadAt(head[:], 0)
	buf := make([]byte, 8192)
	m, _ := f.ReadAt(buf, id3Size(head[:n]))
	buf = buf[:m]
	for i := 0; i+4 <= len(buf); i++ {
		h, ok := parseMP3Header(buf[i:])
		if !ok || i+h.length+4 > len(buf) {
			continue
		}
		next, ok := parseMP3Header(buf[i+h.length:])
		if !ok || !next.compatible(h) {
			continue
		}
		if frames, isInfo := infoFrames(h, buf[i:i+h.length]); isInfo && frames > 0 {
			return float64(frames) * float64(h.samples) / float64(h.sampleRate), nil
		}
		break
	}

	var seconds float64
	err = walkMP3(f, func(h mp3Header, _ []byte, info bool) error {
		if !info {
			seconds += float64(h.samples) / float64(h.sampleRate)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return seconds, nil
}

func prepareMP3(path, dir string, maxSeconds float64) ([]Piece, error) {
	seconds, err := mp3Duration(path)
	if err != nil {
		return nil, err
	}
	if maxSeconds <= 0 || seconds <= maxSeconds {
		p := Piece{Path: piecePath(dir, path, 1, ".mp3"), MIME: "audio/mp3", Seconds: seconds}
		if err := linkOrCopy(path, p.Path); err != nil {
			return nil, err
		}
		return []Piece{p}, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var pieces []Piece
	var pw *pieceWriter
	var bw *bufio.Writer
	var samples, rate int
	finish := func() error {
		if pw == nil {
			return nil
		}
		if err := bw.Flush(); err != nil {
			pw.abort()
			return err
		}
		if err := pw.commit(); err != nil {
			return err
		}
		pieces[len(pieces)-1].Seconds = float64(samples) / float64(rate)
		pw = nil
		return nil
	}
	err = walkMP3(f, func(h mp3Header, frame []byte, info bool) error {
		if info {
			return nil // the Xing/VBRI count would be wrong for a piece
		}
		limit := int(maxSeconds * float64(h.sampleRate))
		if pw != nil && samples+h.samples > limit {
			if err := finish(); err != nil {
				return err
			}
		}
		if pw == nil {
			p := Piece{Path: piecePath(dir, path, len(pieces)+1, ".mp3"), MIME: "audio/mp3"}
			w, err := createPiece(p.Path)
			if err != nil {
				return err
			}
			pw, bw, samples, rate = w, bufio.NewWriterSize(w, 256<<10), 0, h.sampleRate
			pieces = append(pieces, p)
		}
		samples += h.samples
		_, err := bw.Write(frame)
		return err
	})
	if err != nil {
		if pw != nil {
			pw.abort()
		}
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	if err := finish(); err != nil {
		return nil, err
	}
	return pieces, nil
}

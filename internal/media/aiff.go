package media

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/jseook11/nalmeok/internal/i18n"
)

var errAIFF = i18n.Error("media.aiffBad")

// aiffPCM lists the AIFC compression types that unpack to plain PCM samples:
// the payload can be sliced at frame boundaries without decoding it. "NONE",
// "twos", "fl32"/"fl64" and "in24"/"in32" are big-endian, "sowt" and
// "FL32"/"FL64" are little-endian; we never look at the sample bytes. "raw "
// is 8-bit unsigned. Everything else (ulaw, alaw, ima4, ...) needs a codec.
var aiffPCM = map[string]bool{
	"NONE": true, "twos": true, "sowt": true, "raw ": true,
	"fl32": true, "FL32": true, "fl64": true, "FL64": true,
	"in24": true, "in32": true,
}

type aiffInfo struct {
	formType   string // "AIFF" or "AIFC"
	comm       []byte // COMM chunk body, verbatim
	sampleRate float64
	frames     int64
	frameSize  int // block align in bytes
	dataOff    int64
	dataSize   int64
}

func (a *aiffInfo) seconds() float64 {
	return float64(a.frames) / a.sampleRate
}

// float80 decodes an 80-bit IEEE extended float (the AIFF sample rate).
func float80(b []byte) float64 {
	sign := 1.0
	if b[0]&0x80 != 0 {
		sign = -1
	}
	exp := int(binary.BigEndian.Uint16(b[:2]) & 0x7fff)
	mant := binary.BigEndian.Uint64(b[2:])
	switch {
	case exp == 0 && mant == 0:
		return 0
	case exp == 0x7fff: // infinity/NaN: not a usable sample rate
		return math.NaN()
	}
	return sign * math.Ldexp(float64(mant), exp-16383-63)
}

func parseAIFF(path string) (*aiffInfo, error) {
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
	var head [12]byte
	if _, err := f.ReadAt(head[:], 0); err != nil {
		return nil, errAIFF
	}
	form := string(head[8:12])
	if string(head[:4]) != "FORM" || (form != "AIFF" && form != "AIFC") {
		return nil, errAIFF
	}
	a := &aiffInfo{formType: form}
	for pos := int64(12); pos+8 <= size; {
		var ch [8]byte
		if _, err := f.ReadAt(ch[:], pos); err != nil {
			break
		}
		id := string(ch[:4])
		n := int64(binary.BigEndian.Uint32(ch[4:]))
		body := pos + 8
		switch id {
		case "COMM":
			if n < 18 || n > 1<<16 || body+n > size {
				return nil, errAIFF
			}
			a.comm = make([]byte, n)
			if _, err := f.ReadAt(a.comm, body); err != nil {
				return nil, errAIFF
			}
			channels := int(binary.BigEndian.Uint16(a.comm[0:]))
			bits := int(binary.BigEndian.Uint16(a.comm[6:]))
			a.frames = int64(binary.BigEndian.Uint32(a.comm[2:]))
			a.sampleRate = float80(a.comm[8:18])
			bytesPerSample := (bits + 7) / 8
			if form == "AIFC" {
				if n < 22 {
					return nil, errAIFF
				}
				comp := string(a.comm[18:22])
				if !aiffPCM[comp] {
					return nil, fmt.Errorf("%w: %s", ErrUnsupported, comp)
				}
				switch comp {
				case "fl32", "FL32":
					bytesPerSample = 4
				case "fl64", "FL64":
					bytesPerSample = 8
				}
			}
			a.frameSize = channels * bytesPerSample
		case "SSND":
			if a.comm == nil || n < 8 {
				return nil, errAIFF
			}
			var ssnd [8]byte
			if _, err := f.ReadAt(ssnd[:], body); err != nil {
				return nil, errAIFF
			}
			offset := int64(binary.BigEndian.Uint32(ssnd[:4]))
			a.dataOff = body + 8 + offset
			if a.dataOff > size {
				return nil, errAIFF
			}
			// A streaming writer may leave 0xFFFFFFFF; otherwise the chunk
			// bounds the data. Either way the file end clamps it.
			avail := size - a.dataOff
			if n != 0xFFFFFFFF {
				declared := n - 8 - offset
				if declared < 0 {
					return nil, errAIFF
				}
				avail = min(avail, declared)
			}
			if avail < 0 {
				avail = 0
			}
			a.dataSize = min(a.frames*int64(a.frameSize), avail)
		}
		if a.dataOff != 0 {
			break
		}
		pos = body + n + n&1
	}
	if a.comm == nil || a.dataOff == 0 || a.frameSize <= 0 || !(a.sampleRate > 0) || math.IsInf(a.sampleRate, 0) {
		return nil, errAIFF
	}
	a.frames = a.dataSize / int64(a.frameSize)
	return a, nil
}

// writeHeader writes a FORM header, the source COMM chunk with its frame
// count patched, and an SSND header with no offset.
func (a *aiffInfo) writeHeader(out io.Writer, frames, dataSize int64) error {
	comm := make([]byte, len(a.comm))
	copy(comm, a.comm)
	binary.BigEndian.PutUint32(comm[2:], uint32(frames))
	commPad := len(comm) & 1
	ssnd := 8 + dataSize // offset + blockSize + samples
	form := 4 + 8 + int64(len(comm)) + int64(commPad) + 8 + ssnd + dataSize&1
	hdr := make([]byte, 0, 12+8+len(comm)+24)
	hdr = append(hdr, "FORM"...)
	hdr = binary.BigEndian.AppendUint32(hdr, uint32(form))
	hdr = append(hdr, a.formType...)
	hdr = append(hdr, "COMM"...)
	hdr = binary.BigEndian.AppendUint32(hdr, uint32(len(comm)))
	hdr = append(hdr, comm...)
	if commPad == 1 {
		hdr = append(hdr, 0)
	}
	hdr = append(hdr, "SSND"...)
	hdr = binary.BigEndian.AppendUint32(hdr, uint32(ssnd))
	hdr = binary.BigEndian.AppendUint32(hdr, 0) // offset
	hdr = binary.BigEndian.AppendUint32(hdr, 0) // blockSize
	_, err := out.Write(hdr)
	return err
}

func aiffDuration(path string) (float64, error) {
	a, err := parseAIFF(path)
	if err != nil {
		return 0, err
	}
	return a.seconds(), nil
}

func prepareAIFF(path, dir string, maxSeconds float64) ([]Piece, error) {
	a, err := parseAIFF(path)
	if err != nil {
		return nil, err
	}
	total := a.seconds()
	if maxSeconds <= 0 || total <= maxSeconds {
		p := Piece{Path: piecePath(dir, path, 1, ".aiff"), MIME: "audio/aiff", Seconds: total}
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
	// FORM/SSND sizes are 32-bit; leave room for the header chunks.
	maxFrames := (int64(math.MaxUint32) - 1<<17) / int64(a.frameSize)
	frames := min(int64(maxSeconds*float64(a.sampleRate)), maxFrames)
	frames = max(frames, 1)
	step := frames * int64(a.frameSize)
	var pieces []Piece
	for off := int64(0); off < a.dataSize; off += step {
		n := min(step, a.dataSize-off)
		frames := n / int64(a.frameSize)
		p := Piece{
			Path:    piecePath(dir, path, len(pieces)+1, ".aiff"),
			MIME:    "audio/aiff",
			Seconds: float64(frames) / a.sampleRate,
		}
		pw, err := createPiece(p.Path)
		if err != nil {
			return nil, err
		}
		bw := bufio.NewWriterSize(pw, 256<<10)
		err = a.writeHeader(bw, frames, n)
		if err == nil {
			_, err = io.Copy(bw, io.NewSectionReader(f, a.dataOff+off, n))
		}
		if err == nil && n&1 == 1 {
			err = bw.WriteByte(0)
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

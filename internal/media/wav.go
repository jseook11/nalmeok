package media

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"os"
)

var errWAV = errors.New("WAV 형식을 읽지 못했습니다")

type wavInfo struct {
	fmtChunk   []byte
	sampleRate int
	blockAlign int
	dataOff    int64
	dataSize   int64
}

func (w *wavInfo) seconds() float64 {
	frames := w.dataSize / int64(w.blockAlign)
	return float64(frames) / float64(w.sampleRate)
}

func parseWAV(path string) (*wavInfo, error) {
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
		return nil, errWAV
	}
	if string(head[:4]) != "RIFF" || string(head[8:12]) != "WAVE" {
		return nil, errWAV
	}
	w := &wavInfo{}
	for pos := int64(12); pos+8 <= size; {
		var ch [8]byte
		if _, err := f.ReadAt(ch[:], pos); err != nil {
			break
		}
		id := string(ch[:4])
		n := int64(binary.LittleEndian.Uint32(ch[4:]))
		body := pos + 8
		switch id {
		case "fmt ":
			if n < 16 || n > 1<<16 || body+n > size {
				return nil, errWAV
			}
			w.fmtChunk = make([]byte, n)
			if _, err := f.ReadAt(w.fmtChunk, body); err != nil {
				return nil, errWAV
			}
			w.sampleRate = int(binary.LittleEndian.Uint32(w.fmtChunk[4:]))
			w.blockAlign = int(binary.LittleEndian.Uint16(w.fmtChunk[12:]))
		case "data":
			if w.fmtChunk == nil {
				return nil, errWAV
			}
			w.dataOff = body
			// streaming writers leave 0 or 0xFFFFFFFF; clamp to the file
			w.dataSize = min(n, size-body)
			if n == 0 {
				w.dataSize = size - body
			}
		}
		if w.dataOff != 0 {
			break
		}
		pos = body + n + n&1
	}
	if w.fmtChunk == nil || w.dataOff == 0 || w.sampleRate <= 0 || w.blockAlign <= 0 {
		return nil, errWAV
	}
	w.dataSize -= w.dataSize % int64(w.blockAlign)
	return w, nil
}

// writeWAVHeader writes RIFF/WAVE, the original fmt chunk, and a data header.
func (w *wavInfo) writeHeader(out io.Writer, dataSize int64) error {
	fmtPad := len(w.fmtChunk) & 1
	riff := 4 + 8 + len(w.fmtChunk) + fmtPad + 8 + int(dataSize) + int(dataSize&1)
	hdr := make([]byte, 0, 64)
	hdr = append(hdr, "RIFF"...)
	hdr = binary.LittleEndian.AppendUint32(hdr, uint32(riff))
	hdr = append(hdr, "WAVEfmt "...)
	hdr = binary.LittleEndian.AppendUint32(hdr, uint32(len(w.fmtChunk)))
	hdr = append(hdr, w.fmtChunk...)
	if fmtPad == 1 {
		hdr = append(hdr, 0)
	}
	hdr = append(hdr, "data"...)
	hdr = binary.LittleEndian.AppendUint32(hdr, uint32(dataSize))
	_, err := out.Write(hdr)
	return err
}

func prepareWAV(path, dir string, maxSeconds float64) ([]Piece, error) {
	w, err := parseWAV(path)
	if err != nil {
		return nil, err
	}
	total := w.seconds()
	if maxSeconds <= 0 || total <= maxSeconds {
		p := Piece{Path: piecePath(dir, path, 1, ".wav"), MIME: "audio/wav", Seconds: total}
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
	frames := max(int64(maxSeconds*float64(w.sampleRate)), 1)
	step := frames * int64(w.blockAlign)
	var pieces []Piece
	for off := int64(0); off < w.dataSize; off += step {
		n := min(step, w.dataSize-off)
		p := Piece{
			Path:    piecePath(dir, path, len(pieces)+1, ".wav"),
			MIME:    "audio/wav",
			Seconds: float64(n/int64(w.blockAlign)) / float64(w.sampleRate),
		}
		pw, err := createPiece(p.Path)
		if err != nil {
			return nil, err
		}
		bw := bufio.NewWriterSize(pw, 256<<10)
		err = w.writeHeader(bw, n)
		if err == nil {
			_, err = io.Copy(bw, io.NewSectionReader(f, w.dataOff+off, n))
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

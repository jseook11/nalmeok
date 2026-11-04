package media

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

// M4A pieces: Gemini blocks some requests (promptFeedback.blockReason=OTHER)
// when the same audio is sent as ADTS (audio/aac), but accepts it as M4A.
// So AAC inputs are always remuxed — never re-encoded — into a minimal
// audio-only, faststart MP4: ftyp, moov, mdat. Even an unsplit file is
// remuxed so a video track is dropped and odd containers are normalized.

// mp4Box builds a box from its type and concatenated body parts.
func mp4Box(typ string, body ...[]byte) []byte {
	n := 8
	for _, b := range body {
		n += len(b)
	}
	out := make([]byte, 0, n)
	out = binary.BigEndian.AppendUint32(out, uint32(n))
	out = append(out, typ...)
	for _, b := range body {
		out = append(out, b...)
	}
	return out
}

// fullBox prefixes the body with version and flags.
func fullBox(typ string, version byte, flags uint32, body ...[]byte) []byte {
	vf := binary.BigEndian.AppendUint32(nil, uint32(version)<<24|flags&0xFFFFFF)
	return mp4Box(typ, append([][]byte{vf}, body...)...)
}

type be []byte

func (b be) u16(v uint16) be { return binary.BigEndian.AppendUint16(b, v) }
func (b be) u32(v uint32) be { return binary.BigEndian.AppendUint32(b, v) }
func (b be) u64(v uint64) be { return binary.BigEndian.AppendUint64(b, v) }

var unityMatrix = be(nil).u32(0x10000).u32(0).u32(0).u32(0).u32(0x10000).u32(0).u32(0).u32(0).u32(0x40000000)

// m4aMoov builds the moov for samples [from, to) stored back to back at
// dataOffset.
func (t *mp4Track) m4aMoov(from, to int, ticks uint64, dataOffset uint64) []byte {
	long := ticks > math.MaxUint32
	var version byte
	dur := func(b be) be {
		if long {
			return b.u64(ticks)
		}
		return b.u32(uint32(ticks))
	}
	times := func(b be) be { // creation, modification
		if long {
			return b.u64(0).u64(0)
		}
		return b.u32(0).u32(0)
	}
	if long {
		version = 1
	}

	mvhd := times(nil).u32(t.timescale)
	mvhd = dur(mvhd).u32(0x10000).u16(0x100).u16(0).u32(0).u32(0)
	mvhd = append(mvhd, unityMatrix...)
	mvhd = append(mvhd, make([]byte, 24)...)
	mvhd = mvhd.u32(2) // next track ID

	tkhd := times(nil).u32(1).u32(0)
	tkhd = dur(tkhd).u32(0).u32(0).u16(0).u16(0).u16(0x100).u16(0)
	tkhd = append(tkhd, unityMatrix...)
	tkhd = tkhd.u32(0).u32(0)

	mdhd := times(nil).u32(t.timescale)
	mdhd = dur(mdhd).u16(0x55C4).u16(0) // language "und"

	hdlr := be(nil).u32(0)
	hdlr = append(hdlr, "soun"...)
	hdlr = append(hdlr, make([]byte, 12)...)
	hdlr = append(hdlr, "SoundHandler\x00"...)

	// stts: run-length deltas
	var runs be
	nruns := uint32(0)
	for i := from; i < to; {
		j := i
		for j < to && t.deltas[j] == t.deltas[i] {
			j++
		}
		runs = runs.u32(uint32(j - i)).u32(t.deltas[i])
		nruns++
		i = j
	}
	count := uint32(to - from)
	stsz := be(nil).u32(0).u32(count)
	for _, s := range t.sizes[from:to] {
		stsz = stsz.u32(s)
	}
	chunk := fullBox("stco", 0, 0, be(nil).u32(1).u32(uint32(dataOffset)))
	if dataOffset > math.MaxUint32 {
		chunk = fullBox("co64", 0, 0, be(nil).u32(1).u64(dataOffset))
	}

	stbl := mp4Box("stbl",
		fullBox("stsd", 0, 0, be(nil).u32(1), t.sampleEntry),
		fullBox("stts", 0, 0, be(nil).u32(nruns), runs),
		fullBox("stsc", 0, 0, be(nil).u32(1).u32(1).u32(count).u32(1)),
		fullBox("stsz", 0, 0, stsz),
		chunk,
	)
	minf := mp4Box("minf",
		fullBox("smhd", 0, 0, be(nil).u32(0)),
		mp4Box("dinf", fullBox("dref", 0, 0, be(nil).u32(1), fullBox("url ", 0, 1))),
		stbl,
	)
	return mp4Box("moov",
		fullBox("mvhd", version, 0, mvhd),
		mp4Box("trak",
			fullBox("tkhd", version, 3, tkhd), // enabled, in movie
			mp4Box("mdia", fullBox("mdhd", version, 0, mdhd), fullBox("hdlr", 0, 0, hdlr), minf),
		),
	)
}

var m4aFtyp = mp4Box("ftyp", []byte("M4A \x00\x00\x00\x00M4A isommp42"))

func (t *mp4Track) writeM4A(path string, reader *windowReader, from, to int, ticks uint64) error {
	var payload uint64
	for _, s := range t.sizes[from:to] {
		payload += uint64(s)
	}
	mdatHead := be(nil).u32(uint32(8 + payload)).u32(0)
	copy(mdatHead[4:], "mdat")
	if payload+8 > math.MaxUint32 {
		mdatHead = be(nil).u32(1)
		mdatHead = append(mdatHead, "mdat"...)
		mdatHead = mdatHead.u64(16 + payload)
	}
	// moov size does not depend on the offset value unless it crosses 4 GiB.
	moov := t.m4aMoov(from, to, ticks, 0)
	offset := uint64(len(m4aFtyp)+len(moov)) + uint64(len(mdatHead))
	moov = t.m4aMoov(from, to, ticks, offset)
	if uint64(len(m4aFtyp)+len(moov)+len(mdatHead)) != offset {
		offset = uint64(len(m4aFtyp)+len(moov)) + uint64(len(mdatHead))
		moov = t.m4aMoov(from, to, ticks, offset)
	}

	pw, err := createPiece(path)
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(pw, 256<<10)
	for _, part := range [][]byte{m4aFtyp, moov, mdatHead} {
		bw.Write(part)
	}
	for i := from; i < to; i++ {
		data, err := reader.sample(i)
		if err != nil {
			pw.abort()
			return err
		}
		bw.Write(data)
	}
	if err := bw.Flush(); err != nil {
		pw.abort()
		return err
	}
	return pw.commit()
}

func prepareMP4(path, dir string, maxSeconds float64) ([]Piece, error) {
	t, err := parseMP4(path)
	if err != nil {
		return nil, err
	}
	return remuxPieces(t, path, dir, maxSeconds)
}

// remuxPieces writes t's samples, read from path, as M4A pieces of at most
// maxSeconds each.
func remuxPieces(t *mp4Track, path, dir string, maxSeconds float64) ([]Piece, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	reader := &windowReader{f: f, t: t, buf: make([]byte, 1<<20)}
	limit := uint64(math.MaxInt64)
	if maxSeconds > 0 {
		limit = uint64(maxSeconds * float64(t.timescale))
	}

	var pieces []Piece
	for i := 0; i < len(t.sizes); {
		from := i
		var ticks uint64
		for ; i < len(t.sizes); i++ {
			if ticks > 0 && ticks+uint64(t.deltas[i]) > limit {
				break
			}
			ticks += uint64(t.deltas[i])
		}
		piece := Piece{
			Path:    piecePath(dir, path, len(pieces)+1, ".m4a"),
			MIME:    "audio/m4a",
			Seconds: float64(ticks) / float64(t.timescale),
		}
		if err := t.writeM4A(piece.Path, reader, from, i, ticks); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		pieces = append(pieces, piece)
	}
	return pieces, nil
}

package media

import (
	"encoding/binary"

	"github.com/jseook11/nalmeok/internal/i18n"
)

// Fragmented MP4 (moov+mvex, then moof/mdat pairs) as written by browser
// recorders, OBS and DASH/CMAF tools: the audio track's samples are listed
// in each moof's traf/trun instead of moov's sample table. They are appended
// to the same sizes/offsets/deltas tables, so remuxing is unchanged.

type moofBox struct {
	pos  int64 // file offset of the moof box start
	body []byte
}

func (t *mp4Track) addFragments(moov, trak []byte, moofs []moofBox) error {
	if len(moofs) == 0 {
		return nil
	}
	tkhd, ok := findBox(trak, "tkhd")
	if !ok || len(tkhd) < 20 {
		return errMP4(i18n.T("mp4.missing", "box", "tkhd"))
	}
	id := binary.BigEndian.Uint32(tkhd[12:])
	if tkhd[0] == 1 {
		id = binary.BigEndian.Uint32(tkhd[20:])
	}
	var defDur, defSize uint32
	if mvex, ok := findBox(moov, "mvex"); ok {
		boxes, _ := childBoxes(mvex)
		for _, b := range boxes {
			if b.typ == "trex" && len(b.body) >= 24 && binary.BigEndian.Uint32(b.body[4:]) == id {
				defDur, defSize = binary.BigEndian.Uint32(b.body[12:]), binary.BigEndian.Uint32(b.body[16:])
			}
		}
	}
	bad := func(box string) error { return errMP4(i18n.T("mp4.bad", "box", box)) }
	for _, m := range moofs {
		trafs, err := childBoxes(m.body)
		if err != nil {
			return err
		}
		next := m.pos // implicit base of a traf: the moof, then the end of the previous traf's data
		for _, traf := range trafs {
			if traf.typ != "traf" {
				continue
			}
			tfhd, ok := findBox(traf.body, "tfhd")
			if !ok || len(tfhd) < 8 {
				return bad("tfhd")
			}
			flags := binary.BigEndian.Uint32(tfhd) & 0xFFFFFF
			base, dur, size := next, defDur, defSize
			if flags&0x20000 != 0 {
				base = m.pos
			}
			r := tfhd[8:]
			take := func(n int) []byte {
				if len(r) < n {
					return nil
				}
				v := r[:n]
				r = r[n:]
				return v
			}
			if flags&0x1 != 0 {
				v := take(8)
				if v == nil {
					return bad("tfhd")
				}
				base = int64(binary.BigEndian.Uint64(v))
			}
			if flags&0x2 != 0 && take(4) == nil {
				return bad("tfhd")
			}
			if flags&0x8 != 0 {
				if v := take(4); v != nil {
					dur = binary.BigEndian.Uint32(v)
				}
			}
			if flags&0x10 != 0 {
				if v := take(4); v != nil {
					size = binary.BigEndian.Uint32(v)
				}
			}
			mine := binary.BigEndian.Uint32(tfhd[4:]) == id
			off := base
			boxes, _ := childBoxes(traf.body)
			for _, b := range boxes {
				if b.typ != "trun" {
					continue
				}
				if len(b.body) < 8 {
					return bad("trun")
				}
				tf := binary.BigEndian.Uint32(b.body) & 0xFFFFFF
				n := int(binary.BigEndian.Uint32(b.body[4:]))
				p := 8
				if tf&0x1 != 0 {
					if len(b.body) < p+4 {
						return bad("trun")
					}
					off = base + int64(int32(binary.BigEndian.Uint32(b.body[p:])))
					p += 4
				}
				if tf&0x4 != 0 {
					p += 4
				}
				per := 4 * popcount(tf&0xF00)
				if n < 0 || n > 1<<26 || len(b.body) < p+n*per {
					return bad("trun")
				}
				for range n {
					d, s := dur, size
					if tf&0x100 != 0 {
						d = binary.BigEndian.Uint32(b.body[p:])
						p += 4
					}
					if tf&0x200 != 0 {
						s = binary.BigEndian.Uint32(b.body[p:])
						p += 4
					}
					p += 4 * popcount(tf&0xC00)
					if mine {
						t.sizes = append(t.sizes, s)
						t.offsets = append(t.offsets, off)
						t.deltas = append(t.deltas, d)
					}
					off += int64(s)
				}
			}
			next = off
		}
	}
	if len(t.sizes) == 0 {
		return errMP4(i18n.T("mp4.noSamples"))
	}
	if len(t.sizes) > 1<<26 {
		return errMP4(i18n.T("mp4.sampleCount"))
	}
	return nil
}

func popcount(v uint32) int {
	n := 0
	for ; v != 0; v &= v - 1 {
		n++
	}
	return n
}

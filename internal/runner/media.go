package runner

import (
	"github.com/jseook11/nalmeok/internal/media"
	"github.com/jseook11/nalmeok/internal/plan"
)

// DefaultMedia adapts the media and plan packages to the Media interface.
type DefaultMedia struct{}

// Scan reads a dropped file or folder.
func (DefaultMedia) Scan(path string) (Input, error) {
	in, err := media.Scan(path)
	if err != nil {
		return Input{}, err
	}
	tracks := make([]Track, 0, len(in.Tracks))
	for _, t := range in.Tracks {
		tracks = append(tracks, Track{Path: t.Path, Format: t.Format, Seconds: t.Seconds})
	}
	return Input{Source: in.Source, Kind: in.Kind, Name: in.Name, Tracks: tracks, PDF: in.PDF}, nil
}

// Prepare writes upload-ready pieces of one track into dir.
func (DefaultMedia) Prepare(t Track, dir string, maxSeconds float64) ([]Piece, error) {
	pieces, err := media.Prepare(media.Track{Path: t.Path, Format: t.Format, Seconds: t.Seconds}, dir, maxSeconds)
	if err != nil {
		return nil, err
	}
	out := make([]Piece, 0, len(pieces))
	for _, p := range pieces {
		out = append(out, Piece{Path: p.Path, MIME: p.MIME, Seconds: p.Seconds})
	}
	return out, nil
}

// Units groups the pieces of each source file into requests of at most
// maxSeconds, never mixing two files in one request.
func (DefaultMedia) Units(tracks [][]Piece, maxSeconds float64) []UnitPlan {
	in := make([][]media.Piece, 0, len(tracks))
	for _, pieces := range tracks {
		track := make([]media.Piece, 0, len(pieces))
		for _, p := range pieces {
			track = append(track, media.Piece{Path: p.Path, MIME: p.MIME, Seconds: p.Seconds})
		}
		in = append(in, track)
	}
	plans := plan.Units(in, maxSeconds)
	out := make([]UnitPlan, 0, len(plans))
	for _, u := range plans {
		ps := make([]Piece, 0, len(u.Pieces))
		for _, p := range u.Pieces {
			ps = append(ps, Piece{Path: p.Path, MIME: p.MIME, Seconds: p.Seconds})
		}
		out = append(out, UnitPlan{Index: u.Index, Pieces: ps, Seconds: u.Seconds})
	}
	return out
}

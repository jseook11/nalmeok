package media

import "github.com/jseook11/nalmeok/internal/i18n"

var errFLAC = i18n.Error("media.flacBad")

// TODO(FLAC): stub, replaced by the real implementation.

func flacDuration(path string) (float64, error)                         { return 0, ErrUnsupported }
func prepareFLAC(path, dir string, maxSeconds float64) ([]Piece, error) { return nil, ErrUnsupported }

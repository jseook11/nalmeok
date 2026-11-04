package media

import "github.com/jseook11/nalmeok/internal/i18n"

var errAIFF = i18n.Error("media.aiffBad")

// TODO(AIFF): stub, replaced by the real implementation.

func aiffDuration(path string) (float64, error)                         { return 0, ErrUnsupported }
func prepareAIFF(path, dir string, maxSeconds float64) ([]Piece, error) { return nil, ErrUnsupported }

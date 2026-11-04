package media

import "github.com/jseook11/nalmeok/internal/i18n"

var errADTS = i18n.Error("media.adtsBad")

// TODO(ADTS): stub, replaced by the real implementation.

func parseADTS(path string) (*mp4Track, error)                          { return nil, ErrUnsupported }
func prepareADTS(path, dir string, maxSeconds float64) ([]Piece, error) { return nil, ErrUnsupported }

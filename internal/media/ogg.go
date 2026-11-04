package media

import "github.com/jseook11/nalmeok/internal/i18n"

var (
	errOgg      = i18n.Error("media.oggBad")
	errOggCodec = i18n.Error("media.oggCodec")
)

// TODO(Ogg): stub, replaced by the real implementation.

func oggDuration(path string) (float64, error)                         { return 0, ErrUnsupported }
func prepareOgg(path, dir string, maxSeconds float64) ([]Piece, error) { return nil, ErrUnsupported }

package claude

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

func TestValidatePayloadRejectsSpecialFiles(t *testing.T) {
	t.Parallel()
	for _, mode := range []fs.FileMode{fs.ModeNamedPipe, fs.ModeSocket, fs.ModeDevice} {
		t.Run(mode.String(), func(t *testing.T) {
			t.Parallel()
			source := fstest.MapFS{"special": {Mode: mode}}
			require.ErrorIs(t, validatePayload(source), ErrUnsupportedPayload)
		})
	}
}

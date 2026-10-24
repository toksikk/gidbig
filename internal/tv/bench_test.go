package tv

import (
	"bufio"
	"compress/gzip"
	"io"
	"os"
	"testing"
	"time"
)

// BenchmarkParse parses a real feed from EPG_FILE (plain XML or gzip). It is
// skipped in CI, where the variable is unset.
func BenchmarkParse(b *testing.B) {
	path := os.Getenv("EPG_FILE")
	if path == "" {
		b.Skip("EPG_FILE not set")
	}
	for b.Loop() {
		f, err := os.Open(path)
		if err != nil {
			b.Fatal(err)
		}
		br := bufio.NewReader(f)
		var r io.Reader = br
		if magic, _ := br.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
			zr, err := gzip.NewReader(br)
			if err != nil {
				b.Fatal(err)
			}
			r = zr
		}
		ix, err := parse(r, defaultChannels, time.Now())
		_ = f.Close()
		if err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(ix.programmeCount()), "programmes")
	}
}

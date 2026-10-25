package handlers

import (
	"bytes"
	"context"
	"errors"
	"image"
	"net/http"
	"time"
)

const (
	// MaxJSONBodyBytes caps JSON request bodies.
	MaxJSONBodyBytes int64 = 1 << 20
	// Upload caps include 1 MB of multipart overhead.
	maxReceiptUploadBytes = maxReceiptBytes + 1<<20
	maxPDFUploadBytes     = maxPDFBytes + 1<<20
	// maxReceiptPixels is 50 MP: covers modern phone cameras while bounding
	// decode memory (up to ~200 MB for RGBA).
	maxReceiptPixels = 50_000_000
)

var (
	pdfParseTimeout = 30 * time.Second

	errImageTooLarge    = errors.New("image dimensions too large")
	errUnsupportedImage = errors.New("unsupported or unreadable image")
	errPDFTimeout       = errors.New("PDF took too long to parse")

	extractPDFTextFn = extractPDFText
	// pdfSem bounds concurrent pdfcpu extractions, since they cannot be cancelled.
	pdfSem = make(chan struct{}, 2)
)

func isBodyTooLarge(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}

// checkImageDimensions reads only the image header and rejects huge images.
func checkImageDimensions(imgBytes []byte) error {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(imgBytes))
	if err != nil {
		return errUnsupportedImage
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxReceiptPixels {
		return errImageTooLarge
	}
	return nil
}

// extractPDFTextWithTimeout runs extraction with a deadline. pdfcpu can't be
// cancelled, so a timed-out goroutine keeps running; the semaphore (released
// only when extraction really finishes) limits how many can pile up.
func extractPDFTextWithTimeout(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, pdfParseTimeout)
	defer cancel()

	select {
	case pdfSem <- struct{}{}:
	case <-ctx.Done():
		return "", errPDFTimeout
	}
	type result struct {
		text string
		err  error
	}
	ch := make(chan result, 1)
	fn := extractPDFTextFn
	go func() {
		defer func() { <-pdfSem }()
		t, err := fn(path)
		ch <- result{t, err}
	}()
	select {
	case res := <-ch:
		return res.text, res.err
	case <-ctx.Done():
		return "", errPDFTimeout
	}
}

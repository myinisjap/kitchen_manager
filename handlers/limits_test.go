package handlers

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"kitchen_manager/internal/testutil"
)

func TestReadJSONRejectsOversizedBody(t *testing.T) {
	db := testutil.NewDB(t)
	mux := http.NewServeMux()
	RegisterShopping(mux, db, NewHub())

	big := `{"name":"` + strings.Repeat("a", 2<<20) + `"}`
	req := httptest.NewRequest("POST", "/api/shopping/", strings.NewReader(big))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 400 && rec.Code != 413 {
		t.Fatalf("got %d", rec.Code)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM shopping_list`).Scan(&n)
	if n != 0 {
		t.Fatalf("rows inserted: %d", n)
	}

	req = httptest.NewRequest("POST", "/api/shopping/", strings.NewReader(`{"name":"milk"}`))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code/100 != 2 {
		t.Fatalf("control got %d: %s", rec.Code, rec.Body)
	}
}

func hugePNG(t *testing.T) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	binary.BigEndian.PutUint32(b[16:], 100000)
	binary.BigEndian.PutUint32(b[20:], 100000)
	binary.BigEndian.PutUint32(b[29:], crc32.ChecksumIEEE(b[12:29]))
	return b
}

func multipartBody(t *testing.T, ctype string, data []byte) (*bytes.Buffer, string) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{`form-data; name="file"; filename="f"`}
	h["Content-Type"] = []string{ctype}
	pw, _ := mw.CreatePart(h)
	pw.Write(data)
	mw.Close()
	return &body, mw.FormDataContentType()
}

func TestReceiptRejectsHugeImageDimensions(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	img := hugePNG(t)
	if err := checkImageDimensions(img); err != errImageTooLarge {
		t.Fatalf("got %v", err)
	}
	var ok bytes.Buffer
	png.Encode(&ok, image.NewGray(image.Rect(0, 0, 1, 1)))
	if err := checkImageDimensions(ok.Bytes()); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	RegisterReceipt(mux, testutil.NewDB(t))
	body, ct := multipartBody(t, "image/png", img)
	req := httptest.NewRequest("POST", "/api/receipt/parse", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 413 || !strings.Contains(rec.Body.String(), "dimensions") {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestPDFParseTimeout(t *testing.T) {
	oldT, oldF := pdfParseTimeout, extractPDFTextFn
	release := make(chan struct{})
	pdfParseTimeout = 50 * time.Millisecond
	extractPDFTextFn = func(string) (string, error) { <-release; return "", nil }
	t.Cleanup(func() {
		close(release)
		time.Sleep(50 * time.Millisecond) // let goroutine exit before restoring
		pdfParseTimeout, extractPDFTextFn = oldT, oldF
	})

	mux := http.NewServeMux()
	RegisterRecipeImport(mux)
	body, ct := multipartBody(t, "application/pdf", []byte("%PDF-1.4"))
	req := httptest.NewRequest("POST", "/api/recipes/import/pdf", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	start := time.Now()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "too long") {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("too slow")
	}
}

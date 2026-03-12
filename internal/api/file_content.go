package api

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	pdflib "github.com/ledongthuc/pdf"
	"golang.org/x/net/html"
)

const maxExtractedTextBytes = 2 * 1024 * 1024 // 2 MB

// extractFileContent detects the MIME type of data, extracts plain text, and
// returns the text, the detected MIME type, and any error.
//
// metadata may contain "filename" which is used as a fallback to distinguish
// text/markdown from text/plain (http.DetectContentType cannot tell them apart).
// metadata may also contain "mime_type" which is used as a fallback when sniffing
// returns application/octet-stream.
func extractFileContent(data []byte, metadata map[string]string) (string, string, error) {
	if len(data) == 0 {
		return "", "", fmt.Errorf("empty file")
	}

	// Sniff MIME — stdlib reads at most 512 bytes internally.
	sniffed := http.DetectContentType(data)

	// Normalise: strip parameters like "; charset=utf-8".
	mime := sniffed
	if idx := strings.Index(mime, ";"); idx != -1 {
		mime = strings.TrimSpace(mime[:idx])
	}

	// Fallback chain: sniff → metadata["mime_type"] → filename extension.
	if strings.HasPrefix(mime, "application/octet-stream") {
		if provided, ok := metadata["mime_type"]; ok && provided != "" && provided != "application/octet-stream" {
			mime = provided
		}
	}

	// Filename extension override for markdown (http.DetectContentType cannot
	// distinguish markdown from plain text).
	if filename, ok := metadata["filename"]; ok {
		ext := strings.ToLower(filepath.Ext(filename))
		if ext == ".md" || ext == ".markdown" {
			mime = "text/markdown"
		}
	}

	switch mime {
	case "application/pdf":
		text, err := extractTextFromPDF(data)
		if err != nil {
			return "", mime, fmt.Errorf("extracting text from PDF: %w", err)
		}
		return text, mime, nil

	case "text/plain", "text/markdown":
		return string(data), mime, nil

	case "text/html":
		return stripHTML(string(data)), mime, nil

	default:
		return "", mime, fmt.Errorf("unsupported file type: %s", mime)
	}
}

// extractTextFromPDF reads all page text from a PDF byte slice.
// It recovers from panics produced by malformed PDFs.
func extractTextFromPDF(data []byte) (text string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("pdf extraction panic: %v", r)
		}
	}()

	r := bytes.NewReader(data)
	reader, err := pdflib.NewReader(r, int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("opening pdf: %w", err)
	}

	plainText, err := reader.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("reading pdf text: %w", err)
	}

	buf, err := io.ReadAll(io.LimitReader(plainText, maxExtractedTextBytes))
	if err != nil {
		return "", fmt.Errorf("reading pdf text buffer: %w", err)
	}

	return strings.TrimSpace(string(buf)), nil
}

// stripHTML walks the HTML token stream and collects visible text nodes,
// skipping content inside <script>, <style>, and <noscript> tags.
func stripHTML(input string) string {
	tokenizer := html.NewTokenizer(strings.NewReader(input))
	var buf strings.Builder
	skipDepth := 0
	for {
		tt := tokenizer.Next()
		switch tt {
		case html.ErrorToken:
			return strings.TrimSpace(buf.String())
		case html.StartTagToken:
			t := tokenizer.Token()
			if t.Data == "script" || t.Data == "style" || t.Data == "noscript" {
				skipDepth++
			}
		case html.EndTagToken:
			t := tokenizer.Token()
			if t.Data == "script" || t.Data == "style" || t.Data == "noscript" {
				if skipDepth > 0 {
					skipDepth--
				}
			}
		case html.TextToken:
			if skipDepth == 0 {
				text := strings.TrimSpace(tokenizer.Token().Data)
				if text != "" {
					buf.WriteString(text)
					buf.WriteString(" ")
				}
			}
		}
	}
}

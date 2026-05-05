package book

import (
	"crypto/sha256"
	"encoding/hex"
)

func IDFromURL(bookURL string) string {
	sum := sha256.Sum256([]byte(bookURL))
	return "book_" + hex.EncodeToString(sum[:])[:24]
}

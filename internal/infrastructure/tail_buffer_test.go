package infrastructure

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestTailBuffer_50MiBOutput(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "transcript.log")

	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	tail := NewTailBuffer(1024 * 1024) // 1 MiB
	writer := io.MultiWriter(file, tail)

	// Stream 50 MiB in 64 KiB chunks
	chunk := make([]byte, 64*1024)
	for i := range chunk {
		chunk[i] = byte('A' + (i % 26))
	}
	// Make the very last 10 bytes unique marker
	lastMarker := []byte("END_MARKER")

	totalBytes := 50 * 1024 * 1024
	written := 0
	for written < totalBytes {
		toWrite := len(chunk)
		if totalBytes-written < toWrite {
			toWrite = totalBytes - written
		}
		if written+toWrite == totalBytes {
			copy(chunk[toWrite-len(lastMarker):toWrite], lastMarker)
		}
		n, err := writer.Write(chunk[:toWrite])
		if err != nil {
			t.Fatal(err)
		}
		written += n
	}

	_ = file.Sync()
	stat, err := os.Stat(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Size() != int64(totalBytes) {
		t.Errorf("file size got %d, want %d", stat.Size(), totalBytes)
	}

	tailBytes := tail.Bytes()
	if len(tailBytes) != 1024*1024 {
		t.Errorf("tail buffer length got %d, want %d", len(tailBytes), 1024*1024)
	}
	if !bytes.HasSuffix(tailBytes, lastMarker) {
		t.Errorf("tail buffer does not end with expected marker")
	}
}

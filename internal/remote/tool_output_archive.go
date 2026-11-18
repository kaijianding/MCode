package remote

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"

	"mcode/internal/fsutil"
	"mcode/internal/remote/wire"
	"mcode/internal/store"
)

const (
	toolOutputArchiveMaxBytes = 8 * 1024 * 1024
	toolOutputReadMaxBytes    = 512 * 1024
)

func toolOutputPath(dataDir, threadID, messageID string) string {
	return filepath.Join(dataDir, "tool-outputs", threadID, messageID+".txt")
}

func trimPartialRune(data []byte) []byte {
	for i := len(data) - 1; i >= 0 && i >= len(data)-(utf8.UTFMax-1); i-- {
		if utf8.RuneStart(data[i]) {
			if !utf8.FullRune(data[i:]) {
				return data[:i]
			}
			break
		}
	}
	return data
}

func archiveToolOutput(dataDir, threadID, messageID, text string) (int64, error) {
	if len([]rune(text)) <= toolOutputStreamMaxRunes {
		return 0, nil
	}
	if dataDir == "" {
		return 0, errors.New("data directory is empty")
	}
	data := []byte(text)
	if len(data) > toolOutputArchiveMaxBytes {
		data = data[:toolOutputArchiveMaxBytes]
		data = trimPartialRune(data)
	}
	path := toolOutputPath(dataDir, threadID, messageID)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return 0, err
	}
	if err := fsutil.WriteFileAtomic(path, data, 0600); err != nil {
		return 0, err
	}
	return int64(len(data)), nil
}

func (b *MemoryBackend) ThreadToolOutput(ctx context.Context, threadID, messageID string, offset, length int64) (wire.ThreadsToolOutputResult, error) {
	if !isValidUUID(threadID) || !isValidUUID(messageID) || offset < 0 || length < 1 {
		return wire.ThreadsToolOutputResult{}, store.ErrNotFound
	}
	if length > toolOutputReadMaxBytes {
		length = toolOutputReadMaxBytes
	}
	b.mu.Lock()
	dataDir := b.dataDir
	_, threadExists := b.threads[threadID]
	b.mu.Unlock()
	if !threadExists || dataDir == "" {
		return wire.ThreadsToolOutputResult{}, store.ErrNotFound
	}
	path := toolOutputPath(dataDir, threadID, messageID)
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return wire.ThreadsToolOutputResult{}, store.ErrNotFound
	}
	if err != nil {
		return wire.ThreadsToolOutputResult{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return wire.ThreadsToolOutputResult{}, err
	}
	total := info.Size()
	if total > toolOutputArchiveMaxBytes {
		return wire.ThreadsToolOutputResult{}, errors.New("tool output archive exceeds limit")
	}
	if offset > total {
		offset = total
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return wire.ThreadsToolOutputResult{}, err
	}
	data := make([]byte, min(length, toolOutputReadMaxBytes)+(utf8.UTFMax-1))
	n, err := f.Read(data)
	if err != nil && !errors.Is(err, io.EOF) {
		return wire.ThreadsToolOutputResult{}, err
	}
	data = data[:n]
	actualOffset := offset
	for len(data) > 0 && !utf8.RuneStart(data[0]) {
		data = data[1:]
		actualOffset++
	}
	raw := data
	limit := int(min(length, toolOutputReadMaxBytes))
	if len(data) > limit {
		data = data[:limit]
	}
	data = trimPartialRune(data)
	if len(data) == 0 && actualOffset < total {
		// 请求长度小于单个多字节字符宽度时裁剪会变空，改为返回一个完整 rune，确保偏移量前进。
		if !utf8.FullRune(raw) && actualOffset+int64(len(raw)) < total {
			var runeBuf [utf8.UTFMax]byte
			if rn, rerr := f.ReadAt(runeBuf[:], actualOffset); rn > 0 && (rerr == nil || errors.Is(rerr, io.EOF)) {
				raw = runeBuf[:rn]
			}
		}
		_, size := utf8.DecodeRune(raw)
		if size > len(raw) {
			size = len(raw)
		}
		data = raw[:size]
	}
	return wire.ThreadsToolOutputResult{Text: string(data), Offset: actualOffset, TotalBytes: total, EOF: actualOffset+int64(len(data)) >= total}, nil
}

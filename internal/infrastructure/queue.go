package infrastructure

import (
	"context"
	"fmt"
	"os"

	"agyq/internal/application"
	"agyq/internal/domain"
)

// FileQueueSource loads and parses a markdown queue from the filesystem (F-001).
type FileQueueSource struct{}

// NewFileQueueSource constructs a FileQueueSource.
func NewFileQueueSource() *FileQueueSource {
	return &FileQueueSource{}
}

// Load reads path from the filesystem, parses and validates its prompts per docs/07.
func (s *FileQueueSource) Load(ctx context.Context, path string) ([]domain.Prompt, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read queue file %q: %w", path, err)
	}
	return domain.ValidateQueueBytes(data)
}

// Ensure interface satisfaction
var _ application.QueueSource = (*FileQueueSource)(nil)

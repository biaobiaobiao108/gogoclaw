package evaluation

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func (r BehaviorReport) WriteJSON(writer io.Writer) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(r); err != nil {
		return fmt.Errorf("evaluation: encode behavior report: %w", err)
	}
	return nil
}

func (r BehaviorReport) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("evaluation: create report directory: %w", err)
	}
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("evaluation: create report: %w", err)
	}
	defer file.Close()
	return r.WriteJSON(file)
}

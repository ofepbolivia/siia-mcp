package contract

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

// update enables golden regeneration via `go test -update`.
var update = flag.Bool("update", false, "regenerate golden schema files")

// toolGolden is the on-disk shape of one frozen tool schema.
type toolGolden struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	InputSchema *Schema `json:"inputSchema"`
}

// GenerateToolsGolden writes the frozen tool schemas as JSON files under dir,
// one file per tool. It is used by cmd/contractgen -update and by the golden
// test. Output is deterministic (encoding/json sorts map keys).
func GenerateToolsGolden(dir string) error {
	for _, t := range Tools() {
		g := toolGolden{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema}
		data, err := json.MarshalIndent(g, "", "  ")
		if err != nil {
			return err
		}
		data = append(data, '\n')
		path := filepath.Join(dir, t.Name+".json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// MustEqualToolGolden compares all current tool schemas against the golden
// files in dir. It returns the first mismatch or nil when everything matches.
func MustEqualToolGolden(dir string) error {
	for _, t := range Tools() {
		want, err := os.ReadFile(filepath.Join(dir, t.Name+".json"))
		if err != nil {
			return fmt.Errorf("missing golden for %s: %w", t.Name, err)
		}
		want = trimTrailing(want)
		got, err := json.MarshalIndent(toolGolden{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema}, "", "  ")
		if err != nil {
			return err
		}
		if string(want) != string(got) {
			return fmt.Errorf("golden mismatch for %s", t.Name)
		}
	}
	return nil
}

func trimTrailing(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ') {
		b = b[:len(b)-1]
	}
	return b
}

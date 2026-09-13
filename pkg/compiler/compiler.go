package compiler

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CompileReport contains statistics of the compilation.
type CompileReport struct {
	TotalCompiled int
	FailedFiles   []string
}

// CompileProtos compiles all .proto files in protoDir using protoc and protoc-gen-go.
func CompileProtos(protoDir string, filter string) (*CompileReport, error) {
	protocPath, err := exec.LookPath("protoc")
	if err != nil {
		return nil, fmt.Errorf("protoc compiler not found in PATH: %w", err)
	}

	// Find all .proto files
	var protoFiles []string
	filter = strings.ToLower(strings.TrimSpace(filter))

	err = filepath.WalkDir(protoDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".proto") {
			relPath, relErr := filepath.Rel(protoDir, path)
			if relErr == nil {
				if filter == "" || strings.Contains(strings.ToLower(relPath), filter) {
					protoFiles = append(protoFiles, relPath)
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed scanning proto directory: %w", err)
	}

	report := &CompileReport{}

	for _, rel := range protoFiles {
		args := []string{
			"--proto_path=" + protoDir,
			"--go_out=" + protoDir,
			"--go_opt=paths=source_relative",
			rel,
		}

		cmd := exec.Command(protocPath, args...)
		cmd.Dir = protoDir
		cmd.Env = os.Environ()

		output, err := cmd.CombinedOutput()
		if err != nil {
			report.FailedFiles = append(report.FailedFiles, rel)
			fmt.Printf("❌ Failed compiling %s: %v\n%s\n", rel, err, string(output))
		} else {
			report.TotalCompiled++
		}
	}

	return report, nil
}

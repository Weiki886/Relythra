package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Weiki886/relythra/internal/diagnosiscase"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: relythra-casecheck <case-directory>")
		os.Exit(2)
	}
	if err := checkDirectory(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func checkDirectory(directory string) error {
	root := os.DirFS(directory)
	var cases []diagnosiscase.Case
	if err := fs.WalkDir(root, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(name) != ".json" {
			return nil
		}
		caseFile, err := diagnosiscase.Read(root, name, diagnosiscase.Evaluator, nil)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		cases = append(cases, caseFile)
		return nil
	}); err != nil {
		return fmt.Errorf("scan case directory: %w", err)
	}
	if len(cases) == 0 {
		return fmt.Errorf("no JSON diagnosis cases found in %s", directory)
	}
	if err := diagnosiscase.ValidateCaseSet(cases); err != nil {
		return err
	}
	fmt.Printf("Validated %d diagnosis case(s).\n", len(cases))
	return nil
}

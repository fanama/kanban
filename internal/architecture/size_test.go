// Package architecture holds structural guardrails for the Go codebase: the
// readability caps every file and function must respect.
package architecture

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	// maxFileLines is the readability cap for one Go file.
	maxFileLines = 400
	// maxFuncLines is the readability cap for one function body.
	maxFuncLines = 40
)

// TestSizeCaps fails when a production or test Go file grows past the caps.
// The only exception is the served stylesheet, which is not Go source.
func TestSizeCaps(t *testing.T) {
	root := repoRoot(t)
	for _, violation := range collectViolations(t, root) {
		t.Errorf("%s", violation)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	root, err := filepath.Abs(filepath.Join(wd, "..", ".."))
	if err != nil {
		t.Fatalf("Resolving the repository root failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("Expected %s to hold go.mod: %v", root, err)
	}

	return root
}

func collectViolations(t *testing.T, root string) []string {
	t.Helper()
	var violations []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if skipDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		violations = append(violations, checkFile(rel, path)...)
		return nil
	})
	if err != nil {
		t.Fatalf("Walking %s failed: %v", root, err)
	}
	return violations
}

func skipDir(name string) bool {
	return name == ".git" || name == "bin" || name == "node_modules"
}

func checkFile(rel, path string) []string {
	var violations []string
	if lines := countLines(path); lines > maxFileLines {
		violations = append(violations, message(rel, 0, "file", lines))
	}
	violations = append(violations, checkFunctions(rel, path)...)
	return violations
}

func countLines(path string) int {
	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lines := 0
	for scanner.Scan() {
		lines++
	}
	return lines
}

func checkFunctions(rel, path string) []string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return []string{rel + ": parse failed: " + err.Error()}
	}

	var violations []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		length := fset.Position(fn.End()).Line - fset.Position(fn.Pos()).Line + 1
		if length > maxFuncLines {
			violations = append(violations, message(rel, fset.Position(fn.Pos()).Line, fn.Name.Name, length))
		}
	}
	return violations
}

func message(rel string, line int, name string, length int) string {
	if line == 0 {
		return rel + ": over " + strconv.Itoa(maxFileLines) + " lines (" + strconv.Itoa(length) + ")"
	}
	return rel + ":" + strconv.Itoa(line) + ": " + name + " over " + strconv.Itoa(maxFuncLines) + " lines (" + strconv.Itoa(length) + ")"
}

//go:build mage

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	golangciLintInstall = "go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.1"
	govulncheckInstall  = "go install golang.org/x/vuln/cmd/govulncheck@v1.3.0"
)

var Default = Check

// Tidy normalizes module dependencies.
func Tidy() error {
	return run("go", "mod", "tidy")
}

// TidyCheck verifies go.mod and go.sum are already tidy.
func TidyCheck() error {
	before, err := moduleFiles()
	if err != nil {
		return err
	}
	if err := Tidy(); err != nil {
		return err
	}
	after, err := moduleFiles()
	if err != nil {
		return err
	}
	if !sameFiles(before, after) {
		return errors.New("go.mod or go.sum changed after go mod tidy; review and stage the tidy result")
	}
	return nil
}

// Test runs the Go test suite when packages exist.
func Test() error {
	if ok, err := hasPackages("test"); !ok || err != nil {
		return err
	}
	return run("go", "test", "./...")
}

// Vet runs go vet when packages exist.
func Vet() error {
	if ok, err := hasPackages("vet"); !ok || err != nil {
		return err
	}
	return run("go", "vet", "./...")
}

// Lint runs golangci-lint when packages exist.
func Lint() error {
	if ok, err := hasPackages("lint"); !ok || err != nil {
		return err
	}
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		return missingTool("golangci-lint", golangciLintInstall)
	}
	return run("golangci-lint", "run", "./...")
}

// Vuln runs govulncheck when packages exist.
func Vuln() error {
	if ok, err := hasPackages("vuln"); !ok || err != nil {
		return err
	}
	if _, err := exec.LookPath("govulncheck"); err != nil {
		return missingTool("govulncheck", govulncheckInstall)
	}
	return run("govulncheck", "./...")
}

// Check runs all applicable verification gates.
func Check() error {
	steps := []struct {
		name string
		run  func() error
	}{
		{name: "tidy", run: TidyCheck},
		{name: "test", run: Test},
		{name: "vet", run: Vet},
		{name: "lint", run: Lint},
		{name: "vuln", run: Vuln},
	}

	for _, step := range steps {
		if err := step.run(); err != nil {
			return fmt.Errorf("%s failed: %w", step.name, err)
		}
	}
	return nil
}

func hasPackages(gate string) (bool, error) {
	command := exec.Command("go", "list", "./...")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return false, fmt.Errorf("go list ./... failed: %w: %s", err, message)
		}
		return false, fmt.Errorf("go list ./... failed: %w", err)
	}

	if strings.TrimSpace(string(out)) == "" {
		fmt.Printf("skip %s: no Go packages yet\n", gate)
		return false, nil
	}
	return true, nil
}

func missingTool(name string, install string) error {
	return fmt.Errorf("%s not found; install it with `%s`", name, install)
}

func moduleFiles() (map[string][]byte, error) {
	files := make(map[string][]byte, 2)
	for _, path := range []string{"go.mod", "go.sum"} {
		contents, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		files[path] = contents
	}
	return files, nil
}

func sameFiles(a map[string][]byte, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for path, left := range a {
		if !bytes.Equal(left, b[path]) {
			return false
		}
	}
	return true
}

func run(name string, args ...string) error {
	command := exec.Command(name, args...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Stdin = os.Stdin
	if err := command.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("%s exited with status %d", name, exitErr.ExitCode())
		}
		return err
	}
	return nil
}

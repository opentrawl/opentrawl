//go:build darwin

package photos

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFetchAppBuilderRequiresANewExplicitOutput(t *testing.T) {
	repoRoot := photoKitTestRepoRoot(t)
	builder := filepath.Join(repoRoot, "trawlers", "photos", "cmd", "photoscrawl-fetch", "build-app")

	output, exitCode := runPackagingCommand(t, builder)
	if exitCode != 2 || string(output) != "usage: build-app --output OUTPUT.app\n" {
		t.Fatalf("no-argument output = %q, exit = %d", output, exitCode)
	}

	output, exitCode = runPackagingCommand(t, builder, "--output", filepath.Join(t.TempDir(), "not-an-app"))
	if exitCode != 2 || string(output) != "usage: build-app --output OUTPUT.app\n" {
		t.Fatalf("invalid-output output = %q, exit = %d", output, exitCode)
	}

	existing := filepath.Join(t.TempDir(), "Photoscrawl Fetch.app")
	if err := os.MkdirAll(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(existing, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep exact existing output"), 0o600); err != nil {
		t.Fatal(err)
	}
	output, exitCode = runPackagingCommand(t, builder, "--output", existing)
	if exitCode != 1 || !strings.Contains(string(output), "output already exists") {
		t.Fatalf("existing-output output = %q, exit = %d", output, exitCode)
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "keep exact existing output" {
		t.Fatalf("existing output changed: data = %q err = %v", data, err)
	}
	t.Logf("boundary=fetch_app_builder raw_input_argv=%q raw_output=%q", []string{builder, "--output", existing}, output)
}

func TestFetchAppBuilderWorksOutsideRepository(t *testing.T) {
	if os.Getenv("TRAWL276_RUN_BUILD_APP") != "1" {
		t.Skip("set TRAWL276_RUN_BUILD_APP=1 for the signed packaging proof")
	}
	repoRoot := photoKitTestRepoRoot(t)
	builder := filepath.Join(repoRoot, "trawlers", "photos", "cmd", "photoscrawl-fetch", "build-app")
	outsideRepository := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "Photoscrawl Fetch.app")

	output, exitCode := runPackagingCommandInDir(t, outsideRepository, builder, "--output", outputPath)
	if exitCode != 0 {
		t.Fatalf("outside-repository build output = %q, exit = %d", output, exitCode)
	}
	identifier, err := os.ReadFile(filepath.Join(outputPath, "Contents", "Info.plist"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(identifier, []byte(photoKitFetchBundleID)) {
		t.Fatalf("built Info.plist does not contain %q", photoKitFetchBundleID)
	}
	if _, err := os.Stat(filepath.Join(outputPath, "Contents", "MacOS", photoKitFetchExecutable)); err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=outside_repository_builder raw_input_cwd=%q raw_input_argv=%q raw_output=%q", outsideRepository, []string{builder, "--output", outputPath}, output)
}

func TestStandaloneTrawlBuilderRequiresANewExplicitOutput(t *testing.T) {
	repoRoot := photoKitTestRepoRoot(t)
	builder := filepath.Join(repoRoot, "scripts", "build-signed-trawl")
	output, exitCode := runPackagingCommand(t, builder)
	if exitCode != 2 || string(output) != "usage: scripts/build-signed-trawl --output OUTPUT\n" {
		t.Fatalf("no-argument output = %q, exit = %d", output, exitCode)
	}

	existing := filepath.Join(t.TempDir(), "trawl")
	if err := os.WriteFile(existing, []byte("keep exact existing output"), 0o700); err != nil {
		t.Fatal(err)
	}
	output, exitCode = runPackagingCommand(t, builder, "--output", existing)
	if exitCode != 1 || !strings.Contains(string(output), "output already exists") {
		t.Fatalf("existing-output output = %q, exit = %d", output, exitCode)
	}
	data, err := os.ReadFile(existing)
	if err != nil || string(data) != "keep exact existing output" {
		t.Fatalf("existing output changed: data = %q err = %v", data, err)
	}
	t.Logf("boundary=standalone_trawl_builder raw_input_argv=%q raw_output=%q", []string{builder, "--output", existing}, output)
}

func TestHelperPackagingHasNoDeleteRegistrationOrFallbackStep(t *testing.T) {
	repoRoot := photoKitTestRepoRoot(t)
	builderPath := filepath.Join(repoRoot, "trawlers", "photos", "cmd", "photoscrawl-fetch", "build-app")
	builder, err := os.ReadFile(builderPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range [][]byte{[]byte("rm -rf \"$app\""), []byte("lsregister"), []byte("$HOME/Applications")} {
		if bytes.Contains(builder, forbidden) {
			t.Fatalf("helper builder contains forbidden operation %q", forbidden)
		}
	}

	devRunPath := filepath.Join(repoRoot, "app", "scripts", "dev-run")
	devRun, err := os.ReadFile(devRunPath)
	if err != nil {
		t.Fatal(err)
	}
	helperBuild := bytes.Index(devRun, []byte("Photoscrawl Fetch.app"))
	outerSign := bytes.LastIndex(devRun, []byte("--timestamp=none \"$app\""))
	if helperBuild < 0 || outerSign < 0 || helperBuild >= outerSign {
		t.Fatalf("helper build index = %d, outer sign index = %d", helperBuild, outerSign)
	}
}

func photoKitTestRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func runPackagingCommand(t *testing.T, name string, args ...string) ([]byte, int) {
	t.Helper()
	command := exec.Command(name, args...)
	return packagingCommandOutput(t, command)
}

func runPackagingCommandInDir(t *testing.T, directory, name string, args ...string) ([]byte, int) {
	t.Helper()
	command := exec.Command(name, args...)
	command.Dir = directory
	return packagingCommandOutput(t, command)
}

func packagingCommandOutput(t *testing.T, command *exec.Cmd) ([]byte, int) {
	t.Helper()
	output, err := command.CombinedOutput()
	if err == nil {
		return output, 0
	}
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		t.Fatalf("run %s: %v", command.Path, err)
	}
	return output, exitError.ExitCode()
}

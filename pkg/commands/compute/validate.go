package compute

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/fastly/cli/pkg/cmd"
	"github.com/fastly/cli/pkg/config"
	fsterr "github.com/fastly/cli/pkg/errors"
	"github.com/fastly/cli/pkg/manifest"
	"github.com/fastly/cli/pkg/text"
	"github.com/kennygrant/sanitize"
	"github.com/mholt/archiver/v3"
)

// NewValidateCommand returns a usable command registered under the parent.
func NewValidateCommand(parent cmd.Registerer, globals *config.Data, data manifest.Data) *ValidateCommand {
	var c ValidateCommand
	c.Globals = globals
	c.manifest = data
	c.CmdClause = parent.Command("validate", "Validate a Compute@Edge package")
	c.CmdClause.Flag("package", "Path to a package tar.gz").Short('p').StringVar(&c.path)
	return &c
}

// Exec implements the command interface.
func (c *ValidateCommand) Exec(_ io.Reader, out io.Writer) error {
	packagePath := c.path
	if packagePath == "" {
		projectName, source := c.manifest.Name()
		if source == manifest.SourceUndefined {
			return fsterr.RemediationError{
				Inner:       fmt.Errorf("failed to read project name: %w", fsterr.ErrReadingManifest),
				Remediation: "Run `fastly compute build` to produce a Compute@Edge package, alternatively use the --package flag to reference a package outside of the current project.",
			}
		}
		packagePath = filepath.Join("pkg", fmt.Sprintf("%s.tar.gz", sanitize.BaseName(projectName)))
	}

	p, err := filepath.Abs(packagePath)
	if err != nil {
		c.Globals.ErrLog.AddWithContext(err, map[string]any{
			"Path": c.path,
		})
		return fmt.Errorf("error reading file path: %w", err)
	}

	if err := validate(p, nil); err != nil {
		c.Globals.ErrLog.AddWithContext(err, map[string]any{
			"Path": c.path,
		})
		return fsterr.RemediationError{
			Inner:       fmt.Errorf("failed to validate package: %w", err),
			Remediation: "Run `fastly compute build` to produce a Compute@Edge package, alternatively use the --package flag to reference a package outside of the current project.",
		}
	}

	text.Success(out, "Validated package %s", p)
	return nil
}

// ValidateCommand validates a package archive.
type ValidateCommand struct {
	cmd.Base
	manifest manifest.Data
	path     string
}

// FileValidator validates a file.
type FileValidator func(archiver.File) error

// validate is a utility function to determine whether a package is valid.
// It attempts to unarchive and read a tar.gz file from a specific path,
// if successful, it then iterates through (streams) each file in the archive
// checking the filename against a list of required files. If one of the files
// doesn't exist it returns an error.
// validate also call fileValidator, if not nil, passing the file obtained from
// tar.Read().
//
// NOTE: This function is also called by the `deploy` command.
func validate(path string, fileValidator FileValidator) error {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("error reading package: %w", err)
	}
	defer file.Close() // #nosec G307

	tar := archiver.NewTarGz()
	err = tar.Open(file, 0)
	if err != nil {
		return fmt.Errorf("error unarchiving package: %w", err)
	}
	defer tar.Close()

	files := map[string]bool{
		"fastly.toml": false,
		"main.wasm":   false,
	}

	for {
		f, err := tar.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("error reading package: %w", err)
		}

		for k := range files {
			if k == f.Name() {
				files[k] = true
			}
		}

		if fileValidator != nil {
			if err = fileValidator(f); err != nil {
				f.Close()
				return err
			}
		}

		err = f.Close()
		if err != nil {
			return fmt.Errorf("error closing file: %w", err)
		}
	}

	for k, found := range files {
		if !found {
			return fmt.Errorf("error validating package: package must contain a %s file", k)
		}
	}

	return nil
}

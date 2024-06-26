package config

import (
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/kami-zh/go-capturer"
	"github.com/stretchr/testify/require"
)

func TestFindConfigYaml(t *testing.T) {
	dir, err := os.MkdirTemp("", "keepsake-test")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	// Loads a basic config
	err = os.WriteFile(path.Join(dir, "keepsake.yaml"), []byte("repository: 'foo'"), 0644)
	require.NoError(t, err)
	conf, _, err := FindConfig(dir)
	require.NoError(t, err)
	require.Equal(t, &Config{
		Repository: "foo",
	}, conf)
}

func TestFindConfigYml(t *testing.T) {
	dir, err := os.MkdirTemp("", "keepsake-test")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	// Loads a basic config
	err = os.WriteFile(path.Join(dir, "keepsake.yml"), []byte("repository: 'foo'"), 0644)
	require.NoError(t, err)
	conf, _, err := FindConfig(dir)
	require.NoError(t, err)
	require.Equal(t, &Config{
		Repository: "foo",
	}, conf)
}

func TestFindConfigDeprecatedFilename(t *testing.T) {
	dir, err := os.MkdirTemp("", "keepsake-test")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	// Loads a basic config
	err = os.WriteFile(path.Join(dir, "replicate.yaml"), []byte("repository: 'foo'"), 0644)
	require.NoError(t, err)
	var conf *Config
	stderr := capturer.CaptureStderr(func() {
		conf, _, err = FindConfig(dir)
	})
	require.Contains(t, stderr, "replicate.yaml is deprecated")
	require.NoError(t, err)
	require.Equal(t, &Config{
		Repository: "foo",
	}, conf)
}

func TestFindConfigYamlInWorkingDir(t *testing.T) {
	dir, err := os.MkdirTemp("", "keepsake-test")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	// Uses override directory if that is passed
	err = os.WriteFile(path.Join(dir, "keepsake.yaml"), []byte("repository: 'foo'"), 0644)
	require.NoError(t, err)
	conf, _, err := FindConfigInWorkingDir(dir)
	require.NoError(t, err)
	require.Equal(t, &Config{
		Repository: "foo",
	}, conf)

	// Throw error if override directory doesn't have keepsake.yaml
	emptyDir, err := os.MkdirTemp("", "keepsake-test")
	require.NoError(t, err)
	defer os.RemoveAll(emptyDir)
	_, _, err = FindConfigInWorkingDir(emptyDir)
	require.Error(t, err)
}

func TestFindConfigYmlInWorkingDir(t *testing.T) {
	dir, err := os.MkdirTemp("", "keepsake-test")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	// Uses override directory if that is passed
	err = os.WriteFile(path.Join(dir, "keepsake.yml"), []byte("repository: 'foo'"), 0644)
	require.NoError(t, err)
	conf, _, err := FindConfigInWorkingDir(dir)
	require.NoError(t, err)
	require.Equal(t, &Config{
		Repository: "foo",
	}, conf)

	// Throw error if override directory doesn't have keepsake.yaml
	emptyDir, err := os.MkdirTemp("", "keepsake-test")
	require.NoError(t, err)
	defer os.RemoveAll(emptyDir)
	_, _, err = FindConfigInWorkingDir(emptyDir)
	require.Error(t, err)
}

func TestParse(t *testing.T) {
	// Disallows unknown fields
	_, err := Parse([]byte("unknown: 'field'"), "")
	require.Error(t, err)

	// Load empty config
	conf, err := Parse([]byte(""), "/foo")
	require.NoError(t, err)
	require.Equal(t, &Config{}, conf)

	// Sets defaults in empty config
	conf, err = Parse([]byte("repository: s3://foobar"), "/foo")
	require.NoError(t, err)
	require.Equal(t, &Config{
		Repository: "s3://foobar",
	}, conf)

}

func TestStorageBackwardsCompatible(t *testing.T) {
	conf, err := Parse([]byte("storage: 's3://foobar'"), "")
	require.NoError(t, err)
	require.Equal(t, &Config{
		Repository: "s3://foobar",
	}, conf)
}

func TestDeprecatedRepositoryBackwardsCompatible(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "keepsake-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	err = os.MkdirAll(filepath.Join(tmpDir, ".replicate/storage"), 0755)
	require.NoError(t, err)

	conf, projectDir, err := FindConfig(tmpDir)
	require.NoError(t, err)
	require.Equal(t, &Config{
		Repository: "file://.replicate/storage",
	}, conf)
	require.Equal(t, tmpDir, projectDir)
}

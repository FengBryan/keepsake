package list

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/kami-zh/go-capturer"
	"github.com/stretchr/testify/require"

	"replicate.ai/cli/pkg/config"
	"replicate.ai/cli/pkg/param"
	"replicate.ai/cli/pkg/project"
	"replicate.ai/cli/pkg/storage"
)

func createTestData(t *testing.T, workingDir string, conf *config.Config) storage.Storage {
	store, err := storage.NewDiskStorage(path.Join(workingDir, ".replicate/storage"))
	require.NoError(t, err)

	require.NoError(t, err)
	var experiments = []*project.Experiment{{
		ID:      "1eeeeeeeee",
		Created: time.Now().UTC(),
		Params: map[string]*param.Value{
			"param-1": param.Int(100),
			"param-2": param.String("hello"),
		},
		Host:   "10.1.1.1",
		User:   "andreas",
		Config: conf,
	}, {
		ID:      "2eeeeeeeee",
		Created: time.Now().UTC().Add(-1 * time.Minute),
		Params: map[string]*param.Value{
			"param-1": param.Int(200),
			"param-2": param.String("hello"),
			"param-3": param.String("hi"),
		},
		Host:   "10.1.1.2",
		User:   "andreas",
		Config: conf,
	}, {
		ID:      "3eeeeeeeee",
		Created: time.Now().UTC().Add(-2 * time.Minute),
		Params: map[string]*param.Value{
			"param-1": param.Int(200),
			"param-2": param.String("hello"),
			"param-3": param.String("hi"),
		},
		Host:   "10.1.1.2",
		User:   "ben",
		Config: conf,
	}}
	for _, exp := range experiments {
		require.NoError(t, exp.Save(store))
	}

	var commits = []*project.Commit{{
		ID:           "1ccccccccc",
		Created:      time.Now().UTC().Add(-1 * time.Minute),
		ExperimentID: experiments[0].ID,
		Labels: map[string]*param.Value{
			"label-1": param.Float(0.1),
			"label-2": param.Int(2),
		},
		Step: 10,
	}, {
		ID:           "2ccccccccc",
		Created:      time.Now().UTC(),
		ExperimentID: experiments[0].ID,
		Labels: map[string]*param.Value{
			"label-1": param.Float(0.01),
			"label-2": param.Int(2),
		},
		Step: 20,
	}, {
		ID:           "3ccccccccc",
		Created:      time.Now().UTC(),
		ExperimentID: experiments[0].ID,
		Labels: map[string]*param.Value{
			"label-1": param.Float(0.02),
			"label-2": param.Int(2),
		},
		Step: 20,
	}, {
		ID:           "4ccccccccc",
		Created:      time.Now().UTC(),
		ExperimentID: experiments[1].ID,
		Labels: map[string]*param.Value{
			"label-3": param.Float(0.5),
		},
		Step: 5,
	}}
	for _, com := range commits {
		require.NoError(t, com.Save(store, workingDir))
	}

	require.NoError(t, project.CreateHeartbeat(store, experiments[0].ID, time.Now().UTC()))
	require.NoError(t, project.CreateHeartbeat(store, experiments[1].ID, time.Now().UTC().Add(-1*time.Minute)))

	return store
}

func TestOutputTableWithPrimaryMetricOnlyChangedParams(t *testing.T) {
	workingDir, err := ioutil.TempDir("", "replicate-test")
	require.NoError(t, err)
	defer os.RemoveAll(workingDir)

	conf := &config.Config{
		Metrics: []config.Metric{{
			Name:    "label-1",
			Goal:    config.GoalMinimize,
			Primary: true,
		}, {
			Name: "label-3",
			Goal: config.GoalMinimize,
		}},
	}

	store := createTestData(t, workingDir, conf)

	actual := capturer.CaptureStdout(func() {
		err = Experiments(store, FormatTable, false)
	})
	require.NoError(t, err)
	expected := `
experiment  started             status   host      user     param-1  latest   step  label-1  label-3  best     step  label-1  label-3
3eeeeee     2 minutes ago       stopped  10.1.1.2  ben      200
1eeeeee     about a second ago  running  10.1.1.1  andreas  100      3cccccc  20    0.02              2cccccc  20    0.01
2eeeeee     about a minute ago  stopped  10.1.1.2  andreas  200      4cccccc  5              0.5
`
	expected = expected[1:] // strip initial whitespace, added for readability
	actual = trimRightLines(actual)
	require.Equal(t, expected, actual)
}

func TestOutputTableWithPrimaryMetricAllParams(t *testing.T) {
	workingDir, err := ioutil.TempDir("", "replicate-test")
	require.NoError(t, err)
	defer os.RemoveAll(workingDir)

	conf := &config.Config{
		Metrics: []config.Metric{{
			Name:    "label-1",
			Goal:    config.GoalMinimize,
			Primary: true,
		}, {
			Name: "label-3",
			Goal: config.GoalMinimize,
		}},
	}

	store := createTestData(t, workingDir, conf)

	actual := capturer.CaptureStdout(func() {
		err = Experiments(store, FormatTable, true)
	})
	require.NoError(t, err)
	expected := `
experiment  started             status   host      user     param-1  param-2  param-3  latest   step  label-1  label-3  best     step  label-1  label-3
3eeeeee     2 minutes ago       stopped  10.1.1.2  ben      200      hello    hi
1eeeeee     about a second ago  running  10.1.1.1  andreas  100      hello             3cccccc  20    0.02              2cccccc  20    0.01
2eeeeee     about a minute ago  stopped  10.1.1.2  andreas  200      hello    hi       4cccccc  5              0.5
`
	expected = expected[1:] // strip initial whitespace, added for readability
	actual = trimRightLines(actual)
	require.Equal(t, expected, actual)
}

func trimRightLines(s string) string {
	lines := []string{}
	for _, line := range strings.Split(s, "\n") {
		lines = append(lines, strings.TrimRight(line, " "))
	}
	return strings.Join(lines, "\n")
}

func TestListJSON(t *testing.T) {
	workingDir, err := ioutil.TempDir("", "replicate-test")
	require.NoError(t, err)
	storageDir := path.Join(workingDir, ".replicate/storage")

	storage, err := storage.NewDiskStorage(storageDir)
	require.NoError(t, err)
	defer os.RemoveAll(storageDir)

	// Experiment no longer running
	exp := project.NewExperiment(map[string]*param.Value{
		"learning_rate": param.Float(0.001),
	})
	require.NoError(t, exp.Save(storage))
	require.NoError(t, err)
	require.NoError(t, project.CreateHeartbeat(storage, exp.ID, time.Now().UTC().Add(-24*time.Hour)))
	com := project.NewCommit(exp.ID, map[string]*param.Value{
		"accuracy": param.Float(0.987),
	})
	require.NoError(t, com.Save(storage, workingDir))

	// Experiment still running
	exp = project.NewExperiment(map[string]*param.Value{
		"learning_rate": param.Float(0.002),
	})
	require.NoError(t, exp.Save(storage))
	require.NoError(t, err)
	require.NoError(t, project.CreateHeartbeat(storage, exp.ID, time.Now().UTC()))
	com = project.NewCommit(exp.ID, map[string]*param.Value{
		"accuracy": param.Float(0.987),
	})
	require.NoError(t, com.Save(storage, workingDir))

	// replicate list
	actual := capturer.CaptureStdout(func() {
		err = Experiments(storage, FormatJSON, true)
	})
	require.NoError(t, err)

	experiments := make([]ListExperiment, 0)
	require.NoError(t, json.Unmarshal([]byte(actual), &experiments))
	require.Equal(t, 2, len(experiments))

	require.Equal(t, param.Float(0.001), experiments[0].Params["learning_rate"])
	require.Equal(t, 1, experiments[0].NumCommits)
	require.Equal(t, param.Float(0.987), experiments[0].LatestCommit.Labels["accuracy"])
	require.Equal(t, false, experiments[0].Running)

	require.Equal(t, param.Float(0.002), experiments[1].Params["learning_rate"])
	require.Equal(t, 1, experiments[1].NumCommits)
	require.Equal(t, param.Float(0.987), experiments[1].LatestCommit.Labels["accuracy"])
	require.Equal(t, true, experiments[1].Running)
}
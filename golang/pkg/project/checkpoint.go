package project

import (
	"sort"
	"time"

	"github.com/replicate/keepsake/golang/pkg/hash"
	"github.com/replicate/keepsake/golang/pkg/param"
)

type MetricGoal string

const (
	GoalMaximize MetricGoal = "maximize"
	GoalMinimize MetricGoal = "minimize"
)

type PrimaryMetric struct {
	Name string     `json:"name"`
	Goal MetricGoal `json:"goal"`
}

// Checkpoint is a snapshot of an experiment's filesystem
type Checkpoint struct {
	ID            string         `json:"id"`
	Created       time.Time      `json:"created"`
	Metrics       param.ValueMap `json:"metrics"`
	Step          int64          `json:"step"`
	Path          string         `json:"path"`
	PrimaryMetric *PrimaryMetric `json:"primary_metric"`
}

// NewCheckpoint creates a checkpoint with default values
func NewCheckpoint(metrics param.ValueMap) *Checkpoint {
	return &Checkpoint{
		ID:      hash.Random(),
		Created: time.Now().UTC(),
		Metrics: metrics,
	}
}

func (c *Checkpoint) SortedMetrics() []*NamedParam {
	ret := []*NamedParam{}
	for k, v := range c.Metrics {
		ret = append(ret, &NamedParam{Name: k, Value: v})
	}
	sort.Slice(ret, func(i, j int) bool {
		return ret[i].Name < ret[j].Name
	})
	return ret
}

func (c *Checkpoint) ShortID() string {
	return c.ID[:7]
}

func (c *Checkpoint) StorageTarPath() string {
	return "checkpoints/" + c.ID + ".tar.gz"
}

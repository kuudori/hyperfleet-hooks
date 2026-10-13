// Package prow reads the job metadata Prow injects into every decorated job.
package prow

import (
	"encoding/json"
	"fmt"
)

// JobSpecEnv is the environment variable Prow sets to the JSON-encoded job spec.
const JobSpecEnv = "JOB_SPEC"

// JobSpec is the subset of the Prow job spec this tool reads.
// It mirrors prowapi.JobSpec, keeping only the fields needed here.
type JobSpec struct {
	Refs *Refs  `json:"refs,omitempty"`
	Type string `json:"type,omitempty"`
	Job  string `json:"job,omitempty"`
}

// Refs describes the repository and pull requests a job runs against.
// It mirrors prowapi.Refs.
type Refs struct {
	Org     string `json:"org"`
	Repo    string `json:"repo"`
	BaseRef string `json:"base_ref,omitempty"`
	BaseSHA string `json:"base_sha,omitempty"`
	Pulls   []Pull `json:"pulls,omitempty"`
}

// Pull describes a single pull request in the job refs.
// It mirrors prowapi.Pull.
type Pull struct {
	Author string `json:"author"`
	SHA    string `json:"sha"`
	Title  string `json:"title,omitempty"`
	Number int    `json:"number"`
}

// ParseJobSpec decodes a JSON-encoded Prow job spec.
func ParseJobSpec(raw string) (*JobSpec, error) {
	if raw == "" {
		return nil, fmt.Errorf("%s is empty", JobSpecEnv)
	}

	var spec JobSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return nil, fmt.Errorf("invalid %s JSON: %w", JobSpecEnv, err)
	}

	return &spec, nil
}

// PullFromJobSpec returns the first pull request in a JSON-encoded Prow job spec.
// It fails when the spec is malformed, carries no pull requests (for example on a
// periodic or postsubmit job), or the pull request has no title.
func PullFromJobSpec(raw string) (*Pull, error) {
	spec, err := ParseJobSpec(raw)
	if err != nil {
		return nil, err
	}

	if spec.Refs == nil || len(spec.Refs.Pulls) == 0 {
		return nil, fmt.Errorf("%s has no pull requests in refs", JobSpecEnv)
	}

	pull := spec.Refs.Pulls[0]
	if pull.Title == "" {
		return nil, fmt.Errorf("%s pull request #%d has no title", JobSpecEnv, pull.Number)
	}

	return &pull, nil
}
